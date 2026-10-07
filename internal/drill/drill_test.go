package drill_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/bia"
	"github.com/oplosy/disavery/internal/drill"
	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/prober"
	"github.com/oplosy/disavery/internal/report"
	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/verify"
)

type fakeLab struct {
	preflight []report.Check
	safetyErr error
	ran       []string
	checks    map[string]verify.Status
}

func (f *fakeLab) Safety(context.Context, string) error           { return f.safetyErr }
func (f *fakeLab) Preflight(context.Context) []report.Check       { return f.preflight }
func (f *fakeLab) DBHost() (string, error)                        { return "db-a", nil }
func (f *fakeLab) Prober(context.Context) (*prober.Prober, error) { return nil, nil }
func (f *fakeLab) Runners(bool) map[string]executor.Runner {
	return map[string]executor.Runner{runbook.KindRun: f}
}
func (f *fakeLab) Run(_ context.Context, s executor.Step, _ io.Writer) (executor.Output, error) {
	f.ran = append(f.ran, s.Run)
	if s.Run == "fail" {
		return executor.Output{}, errors.New("boom")
	}
	return executor.Output{Stdout: `{"set":"F1"}`}, nil
}

type fakeCheck struct{ status verify.Status }

func (c fakeCheck) Run(context.Context, verify.Params) (verify.Result, error) {
	return verify.Result{Status: c.status, Summary: "fake " + string(c.status)}, nil
}

func (f *fakeLab) Checks(drill.RunContext) verify.Registry {
	reg := verify.Registry{}
	for name, st := range f.checks {
		reg[name] = fakeCheck{st}
	}
	return reg
}

const restoreTest = `id: restore
description: test
phases:
  - name: recover
    steps:
      - { id: pick, run: "pick {{.seed}}", capture: point }
      - { id: restore, run: "restore {{.point.set}} from {{.db_host}}" }
  - name: verify
    steps:
      - { id: integ, check: amcheck }
cleanup:
  - { id: tidy, run: "tidy" }
`

const siteLoss = `id: loss
tiers: [warm-standby]
phases:
  - name: inject
    steps:
      - { id: kill, run: "kill" }
  - name: recover
    steps:
      - { id: promote, run: "fail" }
`

func setup(t *testing.T, maxDuration string) (drill.Options, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"restore.yaml": restoreTest, "loss.yaml": siteLoss} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	biaPath := filepath.Join(dir, "bia.yaml")
	if err := os.WriteFile(biaPath, []byte(`tiers: {warm-standby: {rpo: 5s, rto: 2m}}
restore_test: {max_duration: `+maxDuration+`, pitr_tolerance: 1s}
preflight: {max_backup_age: 2h, max_archive_age: 90s, max_canary_silence: 10s}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := bia.Load(biaPath)
	if err != nil {
		t.Fatal(err)
	}
	reports := filepath.Join(dir, "reports")
	return drill.Options{
		RunbookID: "restore", Env: "drill", Timeout: time.Minute, Seed: 7,
		RunbookDir: dir, ReportsDir: reports, BIA: b, Out: io.Discard, Now: time.Now,
	}, reports
}

func pass() []report.Check { return []report.Check{{Name: "app-ready", Status: "pass"}} }

func TestRestoreTestPasses(t *testing.T) {
	o, reports := setup(t, "15m")
	l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"amcheck": verify.Pass}}
	r, dir, err := drill.Run(context.Background(), o, l)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != report.Pass || len(r.Measurements) != 1 || r.Measurements[0].Name != "recovery_time" || !r.Measurements[0].Met {
		t.Fatalf("report %+v", r)
	}
	if got := strings.Join(l.ran, "|"); got != "pick 7|restore F1 from db-a|tidy" {
		t.Fatalf("ran %s", got)
	}
	for _, f := range []string{"report.json", "report.md", "steps/pick.log", "steps/integ.log"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	h, err := report.ReadHistory(filepath.Join(reports, "history.jsonl"))
	if err != nil || len(h) != 1 || h[0].Result != report.Pass {
		t.Fatalf("history %+v %v", h, err)
	}
}

func TestOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*drill.Options, *fakeLab)
		max      string
		result   report.Result
		note     string
		executed bool
	}{
		{"missed target", nil, "1ns", report.MissedTarget, "recovery_time was", true},
		{"failed check", func(_ *drill.Options, l *fakeLab) { l.checks["amcheck"] = verify.Fail }, "15m", report.Failed, "failed steps: integ", true},
		{"preflight", func(_ *drill.Options, l *fakeLab) {
			l.preflight = append(l.preflight, report.Check{Name: "backups-fresh", Status: "fail"})
		}, "15m", report.Error, "preflight failed (backups-fresh)", false},
		{"aborted step", func(o *drill.Options, _ *fakeLab) { o.RunbookID, o.Tier, o.Yes = "loss", "warm-standby", true }, "15m", report.Failed, "step promote failed", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, _ := setup(t, tc.max)
			l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"amcheck": verify.Pass}}
			if tc.mutate != nil {
				tc.mutate(&o, l)
			}
			r, _, err := drill.Run(context.Background(), o, l)
			if err != nil {
				t.Fatal(err)
			}
			if r.Result != tc.result || len(r.Notes) == 0 || !strings.Contains(r.Notes[0], tc.note) || (len(l.ran) > 0) != tc.executed {
				t.Fatalf("result %s notes %v ran %v", r.Result, r.Notes, l.ran)
			}
		})
	}
}

func TestRefusesToStart(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*drill.Options, *fakeLab)
		want   string
	}{
		{"inject without --yes", func(o *drill.Options, _ *fakeLab) { o.RunbookID, o.Tier = "loss", "warm-standby" }, "pass --yes"},
		{"missing tier", func(o *drill.Options, _ *fakeLab) { o.RunbookID, o.Yes = "loss", true }, "needs --tier"},
		{"tier on untiered runbook", func(o *drill.Options, _ *fakeLab) { o.Tier = "warm-standby" }, "does not take a tier"},
		{"safety lock", func(_ *drill.Options, l *fakeLab) { l.safetyErr = errors.New("workspace is prod") }, "safety lock: workspace is prod"},
		{"unknown check", func(_ *drill.Options, l *fakeLab) { delete(l.checks, "amcheck") }, `unknown check "amcheck"`},
		{"unknown runbook", func(o *drill.Options, _ *fakeLab) { o.RunbookID = "nope" }, "nope.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, reports := setup(t, "15m")
			l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"amcheck": verify.Pass}}
			tc.mutate(&o, l)
			_, _, err := drill.Run(context.Background(), o, l)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if _, err := os.Stat(reports); !os.IsNotExist(err) || len(l.ran) > 0 {
				t.Fatal("a refused drill must not write reports or run steps")
			}
		})
	}
}

func TestCancelledRunIsError(t *testing.T) {
	o, _ := setup(t, "15m")
	o.Timeout = time.Nanosecond
	l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"amcheck": verify.Pass}}
	r, _, err := drill.Run(context.Background(), o, l)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != report.Error || !strings.Contains(r.Notes[0], "global timeout of 1ns reached") || l.ran[len(l.ran)-1] != "tidy" {
		t.Fatalf("result %s notes %v ran %v", r.Result, r.Notes, l.ran)
	}
}

const withPreflight = `id: guarded
preflight:
  - { id: expected-topology, check: topology }
phases:
  - name: recover
    steps:
      - { id: work, run: "work" }
`

func TestRunbookPreflightFailureIsError(t *testing.T) {
	o, _ := setup(t, "15m")
	if err := os.WriteFile(filepath.Join(o.RunbookDir, "guarded.yaml"), []byte(withPreflight), 0o644); err != nil {
		t.Fatal(err)
	}
	o.RunbookID = "guarded"
	l := &fakeLab{preflight: pass(), checks: map[string]verify.Status{"topology": verify.Fail}}
	r, _, err := drill.Run(context.Background(), o, l)
	if err != nil {
		t.Fatal(err)
	}
	last := r.Preflight[len(r.Preflight)-1]
	if r.Result != report.Error || last.Name != "expected-topology" || last.Status != "fail" || len(l.ran) != 0 {
		t.Fatalf("result %s preflight %+v ran %v", r.Result, r.Preflight, l.ran)
	}
}
