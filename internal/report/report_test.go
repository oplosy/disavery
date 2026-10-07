package report_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/report"
	"github.com/oplosy/disavery/internal/verify"
)

var update = flag.Bool("update", false, "rewrite golden files")

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func at(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

func rec(phase, id, kind string, status executor.Status, start, end float64) executor.StepRecord {
	return executor.StepRecord{ID: id, Phase: phase, Kind: kind, Status: status, Attempts: 1, Start: at(start), End: at(end)}
}

// sample is a restore test that passed with one orphan object.
func sample() *report.Report {
	pitr := rec("verify", "pitr", "check", executor.OK, 150, 151.2)
	pitr.Check = &verify.Result{Name: "pitr", Status: verify.Pass, Summary: "restore matches 2026-10-07T11:40:00Z: all 110 writes acknowledged before it are present, none of the 119 sent after it"}
	objs := rec("verify", "objects", "check", executor.OK, 151.2, 152)
	objs.Check = &verify.Result{Name: "db-object-consistency", Status: verify.Pass, Summary: "all 3 documents have their attachment in vault; 1 orphan object(s)",
		Details: []string{"orphan objects: [documents/x]"}}
	return &report.Report{
		SchemaVersion: report.SchemaVersion,
		RunID:         "20261007T120000Z-s6-restore-test",
		Scenario:      "s6-restore-test",
		Description:   "Restore a random backup to a random point in time and verify it.",
		Env:           "drill",
		Result:        report.Pass,
		Seed:          42,
		StartedAt:     t0,
		FinishedAt:    at(170),
		Measurements:  []report.Measurement{{Name: "recovery_time", TargetSeconds: 900, ActualSeconds: 152.4, Met: true}},
		Preflight: []report.Check{
			{Name: "app-ready", Status: "pass", Detail: "https://docs.disavery.test/readyz answered 200"},
			{Name: "backup-age", Status: "pass", Detail: "newest backup per repository: repo1 12m ago, repo2 12m ago"},
		},
		Phases: []executor.PhaseRecord{{Name: "recover", Start: at(0.4), End: at(150)}, {Name: "verify", Start: at(150), End: at(152)}},
		Steps: []executor.StepRecord{
			rec("recover", "pick-point", "run", executor.OK, 0.4, 2.1),
			rec("recover", "provision", "run", executor.OK, 2.1, 20),
			rec("recover", "restore", "run", executor.OK, 20, 150),
			pitr, objs,
		},
		Cleanup:      []executor.StepRecord{rec("cleanup", "remove-node", "run", executor.OK, 152, 169)},
		Availability: &report.Availability{Probes: 340, Failed: 0},
	}
}

// failed is a site-loss drill that missed RTO and had a failing step and a decision.
func failed() *report.Report {
	r := sample()
	inc := at(1)
	r.RunID, r.Scenario, r.Tier, r.Description = "20261007T120000Z-s1-site-loss-warm-standby", "s1-site-loss", "warm-standby", ""
	r.Result, r.IncidentAt = report.Failed, &inc
	r.Notes = []string{"step promote failed; later phases did not run", "cleanup step remove-node failed; run `disavery env reset`"}
	r.Measurements = nil
	decide := rec("decide", "declare", "manual", executor.OK, 30, 60)
	decide.Decision = &executor.Decision{Prompt: "Declare disaster?", Answer: "continue", Auto: true, At: at(60)}
	promote := rec("recover", "promote", "ssh", executor.Failed, 60, 61)
	promote.Error = "ssh db-b \"pg_ctl promote\": exit status 1 | no such cluster"
	r.Phases = []executor.PhaseRecord{{Name: "inject", Start: at(1), End: at(2)}, {Name: "decide", Start: at(30), End: at(60)}, {Name: "recover", Start: at(60), End: at(61)}}
	r.Steps = []executor.StepRecord{rec("inject", "kill-site-a", "run", executor.OK, 1, 2), decide, promote}
	r.Cleanup[0].Status, r.Cleanup[0].Error = executor.Failed, "exit status 1"
	r.Availability = &report.Availability{Probes: 120, Failed: 120}
	return r
}

func TestMarkdownGolden(t *testing.T) {
	for name, r := range map[string]*report.Report{"pass": sample(), "failed": failed()} {
		t.Run(name, func(t *testing.T) {
			got, err := report.Markdown(r)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name+".golden.md")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if string(got) != string(want) {
				t.Fatalf("%s differs from rendering (run with -update to accept):\n%s", path, got)
			}
		})
	}
}

func TestWriteDirAndReadBack(t *testing.T) {
	dir := t.TempDir()
	if err := sample().WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	r, err := report.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil || r.Result != report.Pass || r.Steps[3].Check.Name != "pitr" || r.SchemaVersion != 1 {
		t.Fatalf("read back %+v %v", r, err)
	}
	if md, _ := os.ReadFile(filepath.Join(dir, "report.md")); !strings.HasPrefix(string(md), "# Drill report: s6-restore-test") {
		t.Fatalf("report.md: %q", md)
	}
}

func TestExitCodes(t *testing.T) {
	for r, want := range map[report.Result]int{report.Pass: 0, report.MissedTarget: 2, report.Failed: 3, report.Error: 4} {
		if r.ExitCode() != want {
			t.Fatalf("%s: %d", r, r.ExitCode())
		}
	}
}

func TestHistoryAndTrend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "history.jsonl")
	for i, actual := range []float64{120, 300, 180} {
		r := sample()
		r.StartedAt, r.FinishedAt = at(float64(i)*3600), at(float64(i)*3600+200)
		r.Measurements[0].ActualSeconds = actual
		if i == 1 {
			r.Result = report.MissedTarget
			r.Measurements[0].Met = false
		}
		if err := report.AppendHistory(path, r.HistoryEntry()); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := report.ReadHistory(path)
	if err != nil || len(entries) != 3 {
		t.Fatalf("history %v %v", entries, err)
	}
	rows := report.Trend(entries)
	if len(rows) != 1 || rows[0].Runs != 3 || rows[0].Passed != 2 || rows[0].Last != report.Pass {
		t.Fatalf("trend %+v", rows)
	}
	m := rows[0].Measures[0]
	if m.Median != 180*time.Second || m.Max != 300*time.Second || m.Met != 2 {
		t.Fatalf("stats %+v", m)
	}
	table := report.RenderTrend(rows)
	if !strings.Contains(table, "| s6-restore-test | — | 3 | 2 | PASS | 2026-10-07 14:00 | recovery_time: median 3m0s, max 5m0s, target 15m0s, met 2/3 |") {
		t.Fatalf("table:\n%s", table)
	}
}
