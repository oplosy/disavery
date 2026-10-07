package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// HistoryEntry is one line of reports/history.jsonl.
type HistoryEntry struct {
	RunID           string        `json:"run_id"`
	Scenario        string        `json:"scenario"`
	Tier            string        `json:"tier,omitempty"`
	Result          Result        `json:"result"`
	StartedAt       time.Time     `json:"started_at"`
	DurationSeconds float64       `json:"duration_seconds"`
	Measurements    []Measurement `json:"measurements,omitempty"`
}

// HistoryEntry summarises the report for the history file.
func (r *Report) HistoryEntry() HistoryEntry {
	return HistoryEntry{
		RunID: r.RunID, Scenario: r.Scenario, Tier: r.Tier, Result: r.Result,
		StartedAt: r.StartedAt, DurationSeconds: r.Duration().Seconds(), Measurements: r.Measurements,
	}
}

// AppendHistory appends e to the history file, creating it if needed.
func AppendHistory(path string, e HistoryEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadHistory reads the history file; a missing file is empty history.
func ReadHistory(path string) ([]HistoryEntry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []HistoryEntry
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e HistoryEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

// MeasureStats summarises one measurement across runs.
type MeasureStats struct {
	Name                string
	Count               int
	Median, Max, Target time.Duration
	Met                 int
}

// TrendRow summarises all runs of one scenario and tier.
type TrendRow struct {
	Scenario, Tier string
	Runs, Passed   int
	Last           Result
	LastAt         time.Time
	Measures       []MeasureStats
}

// Trend groups history by scenario and tier.
func Trend(entries []HistoryEntry) []TrendRow {
	type key struct{ scenario, tier string }
	rows := map[key]*TrendRow{}
	values := map[key]map[string][]Measurement{}
	for _, e := range entries {
		k := key{e.Scenario, e.Tier}
		row := rows[k]
		if row == nil {
			row = &TrendRow{Scenario: e.Scenario, Tier: e.Tier}
			rows[k], values[k] = row, map[string][]Measurement{}
		}
		row.Runs++
		if e.Result == Pass {
			row.Passed++
		}
		if !e.StartedAt.Before(row.LastAt) {
			row.Last, row.LastAt = e.Result, e.StartedAt
		}
		for _, m := range e.Measurements {
			values[k][m.Name] = append(values[k][m.Name], m)
		}
	}
	var out []TrendRow
	for k, row := range rows {
		for name, ms := range values[k] {
			secs := make([]float64, len(ms))
			st := MeasureStats{Name: name, Count: len(ms), Target: seconds(ms[len(ms)-1].TargetSeconds)}
			for i, m := range ms {
				secs[i] = m.ActualSeconds
				if m.Met {
					st.Met++
				}
			}
			slices.Sort(secs)
			st.Median, st.Max = seconds(secs[len(secs)/2]), seconds(secs[len(secs)-1])
			row.Measures = append(row.Measures, st)
		}
		sort.Slice(row.Measures, func(i, j int) bool { return row.Measures[i].Name < row.Measures[j].Name })
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scenario != out[j].Scenario {
			return out[i].Scenario < out[j].Scenario
		}
		return out[i].Tier < out[j].Tier
	})
	return out
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// RenderTrend renders the trend as a Markdown table.
func RenderTrend(rows []TrendRow) string {
	var b strings.Builder
	b.WriteString("| Scenario | Tier | Runs | Passed | Last result | Last run (UTC) | Measures |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		var ms []string
		for _, m := range r.Measures {
			ms = append(ms, fmt.Sprintf("%s: median %s, max %s, target %s, met %d/%d",
				m.Name, FormatDuration(m.Median), FormatDuration(m.Max), FormatDuration(m.Target), m.Met, m.Count))
		}
		tier := r.Tier
		if tier == "" {
			tier = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s | %s |\n", r.Scenario, tier, r.Runs, r.Passed, r.Last,
			r.LastAt.UTC().Format("2006-01-02 15:04"), strings.Join(ms, "<br>"))
	}
	return b.String()
}
