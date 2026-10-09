package report

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// durationMeasure is the pseudo-measure for a drill's wall-clock duration.
const durationMeasure = "drill duration"

// ScalingRow is one measure of one scenario and tier across data sizes.
type ScalingRow struct {
	Scenario, Tier, Measure string
	Median                  map[int]time.Duration // by data size in GB
	Runs                    map[int]int
}

// Scaling summarises recovered runs by data size, rounded to the nearest GB
// (spec §14, RTO vs. data size): the median of every measure and of the
// drill's duration. Runs without a recorded size, refused runs and runs that
// did not recover (FAILED, ERROR) are left out. It returns the sizes found,
// ascending, and the rows.
func Scaling(entries []HistoryEntry) ([]int, []ScalingRow) {
	type key struct{ scenario, tier, measure string }
	values := map[key]map[int][]float64{}
	sizes := map[int]bool{}
	add := func(k key, size int, v float64) {
		if values[k] == nil {
			values[k] = map[int][]float64{}
		}
		values[k][size] = append(values[k][size], v)
	}
	for _, e := range entries {
		if e.DataBytes <= 0 || e.Refused || (e.Result != Pass && e.Result != MissedTarget) {
			continue
		}
		size := int(math.Round(float64(e.DataBytes) / (1 << 30)))
		sizes[size] = true
		add(key{e.Scenario, e.Tier, durationMeasure}, size, e.DurationSeconds)
		for _, m := range e.Measurements {
			add(key{e.Scenario, e.Tier, m.Name}, size, m.ActualSeconds)
		}
	}
	var rows []ScalingRow
	for k, bySize := range values {
		row := ScalingRow{Scenario: k.scenario, Tier: k.tier, Measure: k.measure,
			Median: map[int]time.Duration{}, Runs: map[int]int{}}
		for size, vs := range bySize {
			slices.Sort(vs)
			row.Median[size], row.Runs[size] = seconds(vs[len(vs)/2]), len(vs)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Scenario != b.Scenario {
			return a.Scenario < b.Scenario
		}
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		return a.Measure < b.Measure
	})
	out := make([]int, 0, len(sizes))
	for s := range sizes {
		out = append(out, s)
	}
	slices.Sort(out)
	return out, rows
}

// RenderScaling renders Scaling's result as a Markdown table: one column per
// data size, each cell the median and, in parentheses, the number of runs.
func RenderScaling(sizes []int, rows []ScalingRow) string {
	var b strings.Builder
	b.WriteString("| Scenario | Tier | Measure |")
	for _, s := range sizes {
		if s == 0 {
			b.WriteString(" < 0.5 GB |")
		} else {
			fmt.Fprintf(&b, " %d GB |", s)
		}
	}
	b.WriteString("\n|---|---|---|" + strings.Repeat("---|", len(sizes)) + "\n")
	for _, r := range rows {
		tier := r.Tier
		if tier == "" {
			tier = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s |", r.Scenario, tier, r.Measure)
		for _, s := range sizes {
			if n := r.Runs[s]; n > 0 {
				fmt.Fprintf(&b, " %s (%d) |", FormatDuration(r.Median[s]), n)
			} else {
				b.WriteString(" — |")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
