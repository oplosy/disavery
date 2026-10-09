// Package drill runs one drill end to end: safety lock, preflight, the
// runbook with a prober alongside, classification and evidence (spec §8, §9).
package drill

import (
	"context"
	"encoding/json"

	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oplosy/disavery/internal/bia"
	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/prober"
	"github.com/oplosy/disavery/internal/report"
	"github.com/oplosy/disavery/internal/restorepoint"
	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/verify"
)

// retryDelay spaces out the attempts of a step with retries: S7's fencing
// steps retry until the restarted site-a nodes accept SSH, which takes a few
// seconds on a CI runner.
const retryDelay = 3 * time.Second

// RunContext is what per-run checks need to know about the run.
type RunContext struct {
	Targets bia.Targets
	Samples func() []prober.Sample
}

// Lab is everything a drill needs from the environment. LabEnv is the real
// implementation; tests use fakes.
type Lab interface {
	// Safety refuses to run unless the environment is the expected drill environment.
	Safety(ctx context.Context, env string) error
	// Preflight checks the environment is healthy before anything is touched.
	Preflight(ctx context.Context) []report.Check
	// DBHost is the production database node.
	DBHost() (string, error)
	// Prober returns the availability prober, or nil to run without one.
	Prober(ctx context.Context) (*prober.Prober, error)
	// Runners returns the step runners except "check".
	Runners(interactive bool) map[string]executor.Runner
	// Checks returns the verifiers for one run.
	Checks(rc RunContext) verify.Registry
}

// Options select and configure one drill.
type Options struct {
	RunbookID   string
	Tier        string
	Env         string
	Yes         bool // allow destructive (inject) phases
	CI          bool // unattended: allows destructive phases, never prompts
	Interactive bool // prompt for manual steps
	Timeout     time.Duration
	Seed        int64
	RunbookDir  string
	ReportsDir  string
	BIA         *bia.BIA
	Out         io.Writer
	Now         func() time.Time
}

// CheckNames lists the check names a lab registers, for runbook lint.
func CheckNames(l Lab) []string { return l.Checks(RunContext{}).Names() }

// Run executes a drill. It returns an error, without writing a report, only
// when the drill cannot start (unknown runbook, lint errors, wrong tier,
// safety lock); otherwise the report says what happened.
func Run(ctx context.Context, o Options, l Lab) (*report.Report, string, error) {
	rb, err := runbook.Find(o.RunbookDir, o.RunbookID)
	if err != nil {
		return nil, "", err
	}
	if err := runbook.Lint(rb, runbook.Known{Tiers: o.BIA.TierNames(), Checks: CheckNames(l)}); err != nil {
		return nil, "", fmt.Errorf("runbook %s is invalid:\n%w", rb.ID, err)
	}
	targets, err := resolveTier(rb, o)
	if err != nil {
		return nil, "", err
	}
	if rb.HasPhase("inject") && !o.Yes && !o.CI {
		return nil, "", fmt.Errorf("runbook %s injects a failure: pass --yes to confirm (CI runs are confirmed automatically)", rb.ID)
	}
	if err := l.Safety(ctx, o.Env); err != nil {
		return nil, "", fmt.Errorf("safety lock: %w", err)
	}
	dbHost, err := l.DBHost()
	if err != nil {
		return nil, "", err
	}

	start := o.Now()
	runID := start.UTC().Format("20060102T150405Z") + "-" + rb.ID
	if o.Tier != "" {
		runID += "-" + o.Tier
	}
	dir := filepath.Join(o.ReportsDir, runID)
	if err := os.MkdirAll(filepath.Join(dir, "steps"), 0o755); err != nil {
		return nil, "", err
	}
	r := &report.Report{
		SchemaVersion: report.SchemaVersion, RunID: runID, Scenario: rb.ID, Description: rb.Description,
		Tier: o.Tier, Env: o.Env, Seed: o.Seed, StartedAt: start,
		Measurements: []report.Measurement{}, Preflight: []report.Check{},
	}
	finish := func() (*report.Report, string, error) {
		r.FinishedAt = o.Now()
		if err := r.WriteDir(dir); err != nil {
			return r, dir, err
		}
		return r, dir, report.AppendHistory(filepath.Join(o.ReportsDir, "history.jsonl"), r.HistoryEntry())
	}

	probes := &sampleLog{}
	data := map[string]any{
		"scenario": rb.ID, "tier": o.Tier, "env": o.Env, "run_id": runID,
		"report_dir": dir, "seed": strconv.FormatInt(o.Seed, 10), "db_host": dbHost,
	}
	for k, v := range rb.Vars {
		data[k] = v
	}
	runners := maps.Clone(l.Runners(o.Interactive && !o.CI))
	runners[runbook.KindCheck] = checkRunner{
		Registry: l.Checks(RunContext{Targets: targets, Samples: probes.snapshot}),
		Start:    start, Now: o.Now,
	}
	logs := func(id string) (io.WriteCloser, error) { return os.Create(filepath.Join(dir, "steps", id+".log")) }

	fmt.Fprintf(o.Out, "drill %s (seed %d)\n\npreflight\n", runID, o.Seed)
	r.Preflight = append(l.Preflight(ctx), runbookPreflight(ctx, rb, data, runners, logs, o.Now)...)
	var failedPreflight []string
	for _, c := range r.Preflight {
		fmt.Fprintf(o.Out, "  %-16s %-4s %s\n", c.Name, c.Status, c.Detail)
		if c.Status != "pass" {
			failedPreflight = append(failedPreflight, c.Name)
		}
	}
	if len(failedPreflight) > 0 {
		r.Result = report.Error
		r.Notes = []string{"preflight failed (" + strings.Join(failedPreflight, ", ") + "); nothing was changed"}
		// scripts/drill-programme.sh retries on this line.
		fmt.Fprintf(o.Out, "\n%s\n", r.Notes[0])
		return finish()
	}

	stopProber, err := startProber(ctx, l, filepath.Join(dir, "prober.jsonl"), probes)
	if err != nil {
		return nil, "", err
	}

	runCtx, cancel := context.WithTimeoutCause(ctx, o.Timeout, fmt.Errorf("global timeout of %s reached", o.Timeout))
	defer cancel()
	fmt.Fprintf(o.Out, "\nsteps\n")
	res := executor.Execute(runCtx, rb, executor.Options{
		Data:       data,
		Runners:    runners,
		Logs:       logs,
		Now:        o.Now,
		RetryDelay: retryDelay,
		Progress: func(s executor.StepRecord) {
			line := fmt.Sprintf("  %-8s %-24s %-7s %s", s.Phase, s.ID, s.Status, report.FormatDuration(s.End.Sub(s.Start)))
			if s.Check != nil {
				line += "  " + s.Check.Summary
			}
			if s.Error != "" {
				line += "  " + s.Error
			}
			fmt.Fprintln(o.Out, line)
		},
	})
	stopProber()

	r.IncidentAt, r.Phases, r.Steps, r.Cleanup, r.Data = res.Incident, res.Phases, res.Steps, res.Cleanup, res.Data
	if samples := probes.snapshot(); len(samples) > 0 {
		total, failed := prober.Availability(samples)
		r.Availability = &report.Availability{Probes: total, Failed: failed}
	}
	ms := measurements(rb, res, o.BIA)
	for _, m := range ms {
		r.Measurements = append(r.Measurements, report.NewMeasurement(m))
	}
	var cause error
	if res.Outcome == executor.Cancelled {
		cause = context.Cause(runCtx)
	}
	r.Result, r.Notes = classify(res, ms, cause)
	fmt.Fprintf(o.Out, "\nresult %s\n", r.Result)
	for _, n := range r.Notes {
		fmt.Fprintf(o.Out, "  %s\n", n)
	}
	return finish()
}

func resolveTier(rb *runbook.Runbook, o Options) (bia.Targets, error) {
	if len(rb.Tiers) == 0 {
		if o.Tier != "" {
			return bia.Targets{}, fmt.Errorf("runbook %s does not take a tier", rb.ID)
		}
		t, _ := o.BIA.ScenarioTargets(rb.ID)
		return t, nil
	}
	if o.Tier == "" {
		return bia.Targets{}, fmt.Errorf("runbook %s needs --tier (one of %s)", rb.ID, strings.Join(rb.Tiers, ", "))
	}
	for _, t := range rb.Tiers {
		if t == o.Tier {
			return o.BIA.Targets(t)
		}
	}
	return bia.Targets{}, fmt.Errorf("runbook %s does not support tier %q (supports %s)", rb.ID, o.Tier, strings.Join(rb.Tiers, ", "))
}

// measurements collects what the checks measured. A runbook without tiers
// and without scenario targets is a restore test: its recovery time runs from
// the start of recover to the end of verify and is judged against
// restore_test.max_duration.
func measurements(rb *runbook.Runbook, res executor.Result, b *bia.BIA) []verify.Measurement {
	var out []verify.Measurement
	for _, s := range res.Steps {
		if s.Check != nil {
			out = append(out, s.Check.Measurements...)
		}
	}
	_, scenario := b.ScenarioTargets(rb.ID)
	if len(rb.Tiers) == 0 && !scenario && res.Outcome == executor.Completed {
		var start, end time.Time
		for _, p := range res.Phases {
			switch p.Name {
			case "recover":
				start = p.Start
			case "verify":
				end = p.End
			}
		}
		if !start.IsZero() && !end.IsZero() {
			out = append(out, verify.Measurement{Name: "recovery_time", Actual: end.Sub(start), Target: b.RestoreTest.MaxDuration.D()})
		}
	}
	return out
}

// classify turns the run into a verdict (spec §8): a cancelled run is ERROR,
// an aborted run or any failed step is FAILED, a missed target is
// MISSED_TARGET. Cleanup failures do not change the verdict but are noted.
func classify(res executor.Result, ms []verify.Measurement, cause error) (report.Result, []string) {
	var notes []string
	for _, c := range res.Cleanup {
		if c.Status == executor.Failed {
			notes = append(notes, fmt.Sprintf("cleanup step %s failed (%s); run `disavery env reset`", c.ID, c.Error))
		}
	}
	switch res.Outcome {
	case executor.Cancelled:
		reason := "cancelled"
		if cause != nil {
			reason = cause.Error()
		}
		return report.Error, append([]string{"aborted: " + reason + "; cleanup ran"}, notes...)
	case executor.Aborted:
		return report.Failed, append([]string{fmt.Sprintf("step %s failed; the remaining steps did not run", res.FailedStep)}, notes...)
	}
	var failed []string
	for _, s := range res.Steps {
		if s.Status == executor.Failed {
			failed = append(failed, s.ID)
		}
	}
	if len(failed) > 0 {
		return report.Failed, append([]string{"failed steps: " + strings.Join(failed, ", ")}, notes...)
	}
	for _, m := range ms {
		if !m.Met() {
			return report.MissedTarget, append([]string{fmt.Sprintf("%s was %s, target %s", m.Name,
				report.FormatDuration(m.Actual), report.FormatDuration(m.Target))}, notes...)
		}
	}
	return report.Pass, notes
}

// DisruptiveWindows returns when earlier drills changed production: every run
// of a tiered runbook (S1, S7) or of one with scenario targets (S2–S5), from
// its start to its end. The random restore test (S6) draws its targets from
// normal operation outside them: inside, production may legitimately lack a
// table or serve from another site, and a restore of that moment proves
// nothing about the backups.
func DisruptiveWindows(history []report.HistoryEntry, b *bia.BIA) []restorepoint.Interval {
	var out []restorepoint.Interval
	for _, e := range history {
		if _, scenario := b.ScenarioTargets(e.Scenario); e.Tier == "" && !scenario {
			continue
		}
		end := e.StartedAt.Add(time.Duration(e.DurationSeconds * float64(time.Second)))
		out = append(out, restorepoint.Interval{From: e.StartedAt, To: end})
	}
	return out
}

// sampleLog collects prober samples for the checks.
type sampleLog struct {
	mu      sync.Mutex
	samples []prober.Sample
}

func (s *sampleLog) add(x prober.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, x)
}

func (s *sampleLog) snapshot() []prober.Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]prober.Sample(nil), s.samples...)
}

// startProber probes in the background, keeping samples in memory and in
// path. The returned function stops it and waits.
func startProber(ctx context.Context, l Lab, path string, log *sampleLog) (func(), error) {
	p, err := l.Prober(ctx)
	if err != nil || p == nil {
		return func() {}, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := json.NewEncoder(f)
		p.Run(pctx, func(s prober.Sample) {
			log.add(s)
			_ = enc.Encode(s)
		})
	}()
	return func() {
		cancel()
		<-done
		f.Close()
	}, nil
}

// runbookPreflight runs the runbook's own preflight checks (e.g. the expected
// topology or a healthy replica) through the executor, without cleanup.
func runbookPreflight(ctx context.Context, rb *runbook.Runbook, data map[string]any, runners map[string]executor.Runner,
	logs func(string) (io.WriteCloser, error), now func() time.Time) []report.Check {
	if len(rb.Preflight) == 0 {
		return nil
	}
	pre := &runbook.Runbook{Phases: []runbook.Phase{{Name: "preflight", Steps: rb.Preflight}}}
	res := executor.Execute(ctx, pre, executor.Options{Data: data, Runners: runners, Logs: logs, Now: now})
	out := make([]report.Check, 0, len(res.Steps))
	for _, s := range res.Steps {
		c := report.Check{Name: s.ID, Status: "pass"}
		switch {
		case s.Status == executor.Skipped:
			c.Detail = "skipped (condition false)"
		case s.Check != nil:
			c.Detail = s.Check.Summary
			if s.Status != executor.OK {
				c.Status = "fail"
			}
		default:
			c.Status, c.Detail = "fail", s.Error
		}
		out = append(out, c)
	}
	return out
}
