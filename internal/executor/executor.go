// Package executor runs a runbook's phases and cleanup through pluggable step
// runners and records what happened. It knows nothing about the lab; the
// runners do the work.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/verify"
)

// Step is a rendered step handed to a runner.
type Step struct {
	runbook.Step
	Phase    string
	Incident *time.Time
}

// Decision records a manual step's outcome.
type Decision struct {
	Prompt string    `json:"prompt"`
	Answer string    `json:"answer"`
	Auto   bool      `json:"auto"`
	At     time.Time `json:"at"`
}

// Output is what a runner produced.
type Output struct {
	Stdout   string
	Check    *verify.Result
	Decision *Decision
}

// Runner executes one kind of step. Output must be returned even on error
// when there is any (e.g. a failed check's result).
type Runner interface {
	Run(ctx context.Context, s Step, log io.Writer) (Output, error)
}

// Status of a step.
type Status string

// Step statuses.
const (
	OK      Status = "ok"
	Failed  Status = "failed"
	Skipped Status = "skipped"
)

// StepRecord is what happened to one step.
type StepRecord struct {
	ID       string         `json:"id"`
	Phase    string         `json:"phase"`
	Kind     string         `json:"kind"`
	Status   Status         `json:"status"`
	Attempts int            `json:"attempts,omitempty"`
	Start    time.Time      `json:"start"`
	End      time.Time      `json:"end"`
	Error    string         `json:"error,omitempty"`
	Check    *verify.Result `json:"check,omitempty"`
	Decision *Decision      `json:"decision,omitempty"`
}

// PhaseRecord is when a phase ran.
type PhaseRecord struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Outcome of the phases (cleanup does not change it).
type Outcome string

// Outcomes.
const (
	Completed Outcome = "completed" // every phase ran; some steps may have failed with on_failure: continue
	Aborted   Outcome = "aborted"   // a step failed with on_failure: abort
	Cancelled Outcome = "cancelled" // the context ended (Ctrl-C or global timeout)
)

// Result is the full record of a run.
type Result struct {
	Outcome    Outcome
	FailedStep string
	Incident   *time.Time
	Phases     []PhaseRecord
	Steps      []StepRecord
	Cleanup    []StepRecord
	Data       map[string]any
}

// Options configure Execute.
type Options struct {
	Data           map[string]any // template data: vars and built-ins
	Runners        map[string]Runner
	Logs           func(stepID string) (io.WriteCloser, error)
	Now            func() time.Time
	CleanupTimeout time.Duration
	Progress       func(StepRecord)
}

// Execute runs the phases in order and then the cleanup steps, which always
// run (with their own deadline) even if ctx was cancelled.
func Execute(ctx context.Context, rb *runbook.Runbook, opt Options) Result {
	e := &execution{opt: opt, data: maps.Clone(opt.Data)}
	if e.data == nil {
		e.data = map[string]any{}
	}
	res := Result{Outcome: Completed}

phases:
	for _, p := range rb.Phases {
		if ctx.Err() != nil {
			res.Outcome = Cancelled
			break
		}
		pr := PhaseRecord{Name: p.Name, Start: e.now()}
		if p.Name == "inject" {
			at := pr.Start
			res.Incident = &at
		}
		for _, s := range p.Steps {
			if ctx.Err() != nil {
				res.Outcome = Cancelled
				break
			}
			rec := e.step(ctx, s, p.Name, res.Incident)
			res.Steps = append(res.Steps, rec)
			if rec.Status != Failed {
				continue
			}
			if ctx.Err() != nil {
				res.Outcome = Cancelled
				break
			}
			if onFailure(s, p.Name) == "abort" {
				res.Outcome, res.FailedStep = Aborted, s.ID
				break
			}
		}
		pr.End = e.now()
		res.Phases = append(res.Phases, pr)
		if res.Outcome != Completed {
			break phases
		}
	}

	timeout := opt.CleanupTimeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	for _, s := range rb.Cleanup {
		res.Cleanup = append(res.Cleanup, e.step(cctx, s, "cleanup", res.Incident))
	}
	res.Data = e.data
	return res
}

// onFailure returns the effective on_failure: verification (and a drill's
// preflight) continues so every check reports, everything else aborts.
func onFailure(s runbook.Step, phase string) string {
	if s.OnFailure != "" {
		return s.OnFailure
	}
	if phase == "verify" || phase == "preflight" {
		return "continue"
	}
	return "abort"
}

type execution struct {
	opt  Options
	data map[string]any
}

func (e *execution) now() time.Time {
	if e.opt.Now != nil {
		return e.opt.Now()
	}
	return time.Now()
}

func (e *execution) step(ctx context.Context, s runbook.Step, phase string, incident *time.Time) StepRecord {
	rec := StepRecord{ID: s.ID, Phase: phase, Kind: s.Kind(), Start: e.now()}
	finish := func(st Status, err error) StepRecord {
		rec.Status, rec.End = st, e.now()
		if err != nil {
			rec.Error = err.Error()
		}
		if e.opt.Progress != nil {
			e.opt.Progress(rec)
		}
		return rec
	}

	if s.When != "" {
		run, err := evalWhen(s.When, e.data)
		if err != nil {
			return finish(Failed, err)
		}
		if !run {
			return finish(Skipped, nil)
		}
	}
	rs, err := s.Rendered(e.data)
	if err != nil {
		return finish(Failed, fmt.Errorf("render: %w", err))
	}
	runner, ok := e.opt.Runners[rs.Kind()]
	if !ok {
		return finish(Failed, fmt.Errorf("no runner for step kind %q", rs.Kind()))
	}
	log := io.WriteCloser(nopCloser{io.Discard})
	if e.opt.Logs != nil {
		if log, err = e.opt.Logs(s.ID); err != nil {
			return finish(Failed, fmt.Errorf("open step log: %w", err))
		}
	}
	defer log.Close()
	fmt.Fprintf(log, "# step %s (%s) in phase %s\n# start %s\n", s.ID, rec.Kind, phase, rec.Start.UTC().Format(time.RFC3339Nano))

	var out Output
	for attempt := 1; attempt <= s.Retries+1; attempt++ {
		rec.Attempts = attempt
		fmt.Fprintf(log, "# attempt %d\n", attempt)
		actx, cancel := ctx, context.CancelFunc(func() {})
		if s.Timeout > 0 {
			actx, cancel = context.WithTimeout(ctx, s.Timeout.D())
		}
		out, err = runner.Run(actx, Step{Step: rs, Phase: phase, Incident: incident}, log)
		if err != nil && ctx.Err() == nil && errors.Is(actx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", s.Timeout, err)
		}
		cancel()
		if err == nil || ctx.Err() != nil {
			break
		}
		fmt.Fprintf(log, "# attempt %d failed: %v\n", attempt, err)
	}
	rec.Check, rec.Decision = out.Check, out.Decision
	if err != nil {
		fmt.Fprintf(log, "# end %s failed: %v\n", e.now().UTC().Format(time.RFC3339Nano), err)
		return finish(Failed, err)
	}
	if s.Capture != "" {
		e.data[s.Capture] = parseCapture(out.Stdout)
	}
	fmt.Fprintf(log, "# end %s ok\n", e.now().UTC().Format(time.RFC3339Nano))
	return finish(OK, nil)
}

// evalWhen evaluates a condition over the string-valued template data.
func evalWhen(src string, data map[string]any) (bool, error) {
	expr, err := runbook.ParseExpr(src)
	if err != nil {
		return false, err
	}
	vars := map[string]string{}
	for k, v := range data {
		if s, ok := v.(string); ok {
			vars[k] = s
		}
	}
	return expr.Eval(vars)
}

// parseCapture turns captured stdout into template data: a JSON object
// becomes a map (so templates can use {{.point.set}}), anything else a
// trimmed string.
func parseCapture(stdout string) any {
	s := strings.TrimSpace(stdout)
	if strings.HasPrefix(s, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(s), &m) == nil {
			return m
		}
	}
	return s
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
