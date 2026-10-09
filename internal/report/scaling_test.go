package report_test

import (
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/report"
)

// TestScaling: recovered runs are grouped by data size (nearest GB) and their
// measures summarised as medians; runs without a size, refused or failed runs
// do not count.
func TestScaling(t *testing.T) {
	const gb = 1 << 30
	run := func(size float64, rto, duration float64, result report.Result) report.HistoryEntry {
		return report.HistoryEntry{
			Scenario: "s1-site-loss", Tier: "pilot-light", Result: result, StartedAt: at(0),
			DataBytes: int64(size * gb), DurationSeconds: duration,
			Measurements: []report.Measurement{{Name: "rto", TargetSeconds: 900, ActualSeconds: rto}},
		}
	}
	entries := []report.HistoryEntry{
		run(1.04, 120, 300, report.Pass),
		run(0.98, 140, 310, report.Pass),
		run(1.01, 130, 320, report.MissedTarget),
		run(10.2, 600, 900, report.Pass),
		run(9.9, 9999, 9999, report.Failed),                            // did not recover
		{Scenario: "s1-site-loss", Tier: "pilot-light", Refused: true}, // never started
		run(0, 50, 60, report.Pass),                                    // no size recorded
	}
	table := report.RenderScaling(report.Scaling(entries))
	for _, want := range []string{
		"| Scenario | Tier | Measure | 1 GB | 10 GB |",
		"| s1-site-loss | pilot-light | drill duration | 5m10s (3) | 15m0s (1) |",
		"| s1-site-loss | pilot-light | rto | 2m10s (3) | 10m0s (1) |",
	} {
		if !strings.Contains(table, want) {
			t.Fatalf("missing %q in\n%s", want, table)
		}
	}
	if strings.Contains(table, "2h") || strings.Contains(table, "50s") {
		t.Fatalf("a failed or sizeless run was counted:\n%s", table)
	}
}
