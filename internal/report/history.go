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
	Refused         bool          `json:"refused,omitempty"`    // see Report.Refused
	DataBytes       int64         `json:"data_bytes,omitempty"` // see Report.DataBytes
	StartedAt       time.Time     `json:"started_at"`
	DurationSeconds float64       `json:"duration_seconds"`
	Measurements    []Measurement `json:"measurements,omitempty"`
}

// HistoryEntry summarises the report for the history file.
func (r *Report) HistoryEntry() HistoryEntry {
	return HistoryEntry{
		RunID: r.RunID, Scenario: r.Scenario, Tier: r.Tier, Result: r.Result, Refused: r.Refused, DataBytes: r.DataBytes,
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
	LastRefused    bool // the newest attempt was refused by its preflight
	Measures       []MeasureStats
}

// Trend groups history by scenario and tier. A refused drill is no run: it
// only becomes the last result when nothing ran after it, so a drill that
// never got past preflight stays visible while the retries of a drill
// programme do not.
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
		if !e.StartedAt.Before(row.LastAt) {
			row.Last, row.LastAt, row.LastRefused = e.Result, e.StartedAt, e.Refused
		}
		if e.Refused {
			continue
		}
		row.Runs++
		if e.Result == Pass {
			row.Passed++
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
		last := string(r.Last)
		if r.LastRefused {
			last += " (refused)"
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %s | %s | %s |\n", r.Scenario, tier, r.Runs, r.Passed, last,
			r.LastAt.UTC().Format("2006-01-02 15:04"), strings.Join(ms, "<br>"))
	}
	return b.String()
}

// TierSpec is what the BIA declares about a tier.
type TierSpec struct {
	Name                 string
	TargetRPO, TargetRTO time.Duration
	AlwaysOnNodes        int
	MonthlyUSD           float64
}

// TierRow compares one tier's measured recoveries with its targets and cost.
type TierRow struct {
	TierSpec
	Runs, Passed int
	RPO, RTO     *MeasureStats // nil without measurements
}

// CompareTiers summarises the runs of scenario (normally s1-site-loss) per
// tier: spec success criterion 5.
func CompareTiers(entries []HistoryEntry, scenario string, tiers []TierSpec) []TierRow {
	rows := make([]TierRow, 0, len(tiers))
	for _, t := range tiers {
		row := TierRow{TierSpec: t}
		var mine []HistoryEntry
		for _, e := range entries {
			if e.Scenario == scenario && e.Tier == t.Name {
				mine = append(mine, e)
			}
		}
		for _, tr := range Trend(mine) {
			row.Runs, row.Passed = tr.Runs, tr.Passed
			for i := range tr.Measures {
				switch tr.Measures[i].Name {
				case "rpo":
					row.RPO = &tr.Measures[i]
				case "rto":
					row.RTO = &tr.Measures[i]
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// RenderTierComparison renders the comparison as a Markdown table.
func RenderTierComparison(rows []TierRow) string {
	stat := func(m *MeasureStats) string {
		if m == nil {
			return "—"
		}
		return fmt.Sprintf("%s (max %s)", FormatDuration(m.Median), FormatDuration(m.Max))
	}
	var b strings.Builder
	b.WriteString("| Tier | Target RPO | Actual RPO, median | Target RTO | Actual RTO, median | Runs passed | Always-on DR nodes | Est. monthly cost |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d/%d | %d | $%.0f |\n", r.Name,
			FormatDuration(r.TargetRPO), stat(r.RPO), FormatDuration(r.TargetRTO), stat(r.RTO),
			r.Passed, r.Runs, r.AlwaysOnNodes, r.MonthlyUSD)
	}
	return b.String()
}
