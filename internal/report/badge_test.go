package report_test

import (
	"testing"

	"github.com/oplosy/disavery/internal/report"
)

func TestNewBadge(t *testing.T) {
	h := []report.HistoryEntry{
		{Scenario: "s6-restore-test", Result: report.Pass, StartedAt: at(0), DurationSeconds: 60},
		{Scenario: "s6-restore-test", Result: report.Failed, StartedAt: at(3600), DurationSeconds: 90},
		{Scenario: "s1-site-loss", Result: report.Pass, StartedAt: at(7200), DurationSeconds: 60},
		// Refused by preflight: nothing ran, so it does not replace the last run.
		{Scenario: "s6-restore-test", Result: report.Error, Refused: true, StartedAt: at(9000), DurationSeconds: 1},
	}
	got := report.NewBadge(h, "s6-restore-test", "last restore drill")
	want := report.Badge{SchemaVersion: 1, Label: "last restore drill", Message: "FAILED · 2026-10-07 13:01 UTC", Color: "red"}
	if got != want {
		t.Fatalf("got %+v", got)
	}
	if b := report.NewBadge(nil, "s6-restore-test", "x"); b.Message != "no run yet" || b.Color != "lightgrey" {
		t.Fatalf("empty history: %+v", b)
	}
}
