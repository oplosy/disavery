package executor_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/runbook"
	"github.com/oplosy/disavery/internal/yamltime"
)

// fakeRunner runs "run" steps: the command text decides what happens.
//   - "fail"        always fails
//   - "flaky:N"     fails until attempt N
//   - "block"       waits for the context
//   - "emit:<out>"  succeeds and prints <out>
//   - "cancel"      cancels the drill context, then fails
//
// Every executed command is recorded in order.
type fakeRunner struct {
	mu       sync.Mutex
	ran      []string
	attempts map[string]int
	cancel   context.CancelFunc
}

func (f *fakeRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	f.mu.Lock()
	f.ran = append(f.ran, s.Run)
	if f.attempts == nil {
		f.attempts = map[string]int{}
	}
	f.attempts[s.ID]++
	n := f.attempts[s.ID]
	f.mu.Unlock()
	_, _ = io.WriteString(log, "output of "+s.Run+"\n")
	switch {
	case s.Run == "fail":
		return executor.Output{}, errors.New("boom")
	case strings.HasPrefix(s.Run, "flaky:"):
		if want := s.Run[len("flaky:"):]; n < int(want[0]-'0') {
			return executor.Output{}, errors.New("not yet")
		}
	case s.Run == "block":
		<-ctx.Done()
		return executor.Output{}, ctx.Err()
	case strings.HasPrefix(s.Run, "emit:"):
		return executor.Output{Stdout: s.Run[len("emit:"):]}, nil
	case s.Run == "cancel":
		f.cancel()
		return executor.Output{}, context.Canceled
	}
	return executor.Output{}, nil
}

func step(id, run string) runbook.Step { return runbook.Step{ID: id, Run: run} }

func run(t *testing.T, ctx context.Context, rb *runbook.Runbook, f *fakeRunner, data map[string]any) executor.Result {
	t.Helper()
	return executor.Execute(ctx, rb, executor.Options{
		Data:    data,
		Runners: map[string]executor.Runner{runbook.KindRun: f},
	})
}

func statuses(recs []executor.StepRecord) string {
	var b strings.Builder
	for _, r := range recs {
		b.WriteString(r.ID + "=" + string(r.Status) + " ")
	}
	return strings.TrimSpace(b.String())
}

func TestOrderingWhenAndCapture(t *testing.T) {
	rb := &runbook.Runbook{
		Phases: []runbook.Phase{
			{Name: "inject", Steps: []runbook.Step{step("kill", "kill {{.tier}}")}},
			{Name: "recover", Steps: []runbook.Step{
				{ID: "build", Run: "build", When: "tier == 'pilot-light'"},
				{ID: "promote", Run: "promote", When: "tier == 'warm-standby'"},
				{ID: "pick", Run: `emit:{"set":"F1"}`, Capture: "point"},
				step("restore", "restore {{.point.set}}"),
			}},
		},
		Cleanup: []runbook.Step{step("tidy", "tidy")},
	}
	f := &fakeRunner{}
	res := run(t, context.Background(), rb, f, map[string]any{"tier": "warm-standby"})

	if res.Outcome != executor.Completed || res.Incident == nil {
		t.Fatalf("outcome %s incident %v", res.Outcome, res.Incident)
	}
	want := []string{"kill warm-standby", "promote", `emit:{"set":"F1"}`, "restore F1", "tidy"}
	if !slices.Equal(f.ran, want) {
		t.Fatalf("ran %q, want %q", f.ran, want)
	}
	if got := statuses(res.Steps); got != "kill=ok build=skipped promote=ok pick=ok restore=ok" {
		t.Fatalf("statuses %s", got)
	}
	if len(res.Phases) != 2 || res.Phases[0].Name != "inject" || !res.Incident.Equal(res.Phases[0].Start) {
		t.Fatalf("phases %+v", res.Phases)
	}
}

func TestRetries(t *testing.T) {
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "recover", Steps: []runbook.Step{
		{ID: "a", Run: "flaky:3", Retries: 2},
		{ID: "b", Run: "flaky:3", Retries: 1},
		step("c", "never"),
	}}}}
	f := &fakeRunner{}
	res := run(t, context.Background(), rb, f, nil)
	if res.Outcome != executor.Aborted || res.FailedStep != "b" || res.Steps[0].Attempts != 3 || res.Steps[1].Attempts != 2 {
		t.Fatalf("result %+v", res)
	}
	if slices.Contains(f.ran, "never") {
		t.Fatal("a step after an aborting failure ran")
	}
}

// TestRetryDelay: attempts are spaced out, so a retry can outlast a node that
// is still booting (S7 fences site a right after starting it); cancelling the
// drill ends the wait at once.
func TestRetryDelay(t *testing.T) {
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "recover", Steps: []runbook.Step{
		{ID: "a", Run: "flaky:3", Retries: 2},
	}}}}
	start := time.Now()
	res := executor.Execute(context.Background(), rb, executor.Options{
		Runners:    map[string]executor.Runner{runbook.KindRun: &fakeRunner{}},
		RetryDelay: 50 * time.Millisecond,
	})
	if res.Outcome != executor.Completed || res.Steps[0].Attempts != 3 {
		t.Fatalf("result %+v", res)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("three attempts took %s, want two pauses of 50ms", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start = time.Now()
	rb.Phases[0].Steps[0] = runbook.Step{ID: "b", Run: "fail", Retries: 3}
	res = executor.Execute(ctx, rb, executor.Options{
		Runners:    map[string]executor.Runner{runbook.KindRun: &fakeRunner{}},
		RetryDelay: time.Hour,
	})
	if res.Outcome != executor.Cancelled || res.Steps[0].Attempts != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled wait: %+v after %s", res, time.Since(start))
	}
}

func TestTimeoutAbortAndCleanup(t *testing.T) {
	rb := &runbook.Runbook{
		Phases: []runbook.Phase{
			{Name: "recover", Steps: []runbook.Step{{ID: "slow", Run: "block", Timeout: yamltime.Duration(20 * time.Millisecond)}}},
			{Name: "verify", Steps: []runbook.Step{step("never", "never")}},
		},
		Cleanup: []runbook.Step{step("c1", "fail"), step("c2", "tidy")},
	}
	f := &fakeRunner{}
	res := run(t, context.Background(), rb, f, nil)
	if res.Outcome != executor.Aborted || !strings.Contains(res.Steps[0].Error, "timed out after 20ms") {
		t.Fatalf("result %+v", res.Steps)
	}
	if len(res.Phases) != 1 || slices.Contains(f.ran, "never") {
		t.Fatalf("later phase ran: %+v", res.Phases)
	}
	if got := statuses(res.Cleanup); got != "c1=failed c2=ok" {
		t.Fatalf("cleanup must run every step: %s", got)
	}
}

func TestVerifyContinuesByDefault(t *testing.T) {
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "verify", Steps: []runbook.Step{
		step("v1", "fail"), step("v2", "ok"), {ID: "v3", Run: "fail", OnFailure: "abort"}, step("v4", "never"),
	}}}}
	res := run(t, context.Background(), rb, &fakeRunner{}, nil)
	if got := statuses(res.Steps); got != "v1=failed v2=ok v3=failed" || res.Outcome != executor.Aborted {
		t.Fatalf("%s %s", got, res.Outcome)
	}
}

func TestCancellationStillCleansUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rb := &runbook.Runbook{
		Phases:  []runbook.Phase{{Name: "recover", Steps: []runbook.Step{step("a", "cancel"), step("b", "never")}}},
		Cleanup: []runbook.Step{step("tidy", "tidy")},
	}
	f := &fakeRunner{cancel: cancel}
	res := run(t, ctx, rb, f, nil)
	if res.Outcome != executor.Cancelled || !slices.Equal(f.ran, []string{"cancel", "tidy"}) || res.Cleanup[0].Status != executor.OK {
		t.Fatalf("outcome %s ran %v cleanup %+v", res.Outcome, f.ran, res.Cleanup)
	}
}

func TestBadConditionAndTemplateFailTheStep(t *testing.T) {
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "verify", Steps: []runbook.Step{
		{ID: "a", Run: "x", When: "site == 'a'"},
		step("b", "{{.missing}}"),
		{ID: "c", Check: "nope"},
	}}}}
	res := run(t, context.Background(), rb, &fakeRunner{}, nil)
	if got := statuses(res.Steps); got != "a=failed b=failed c=failed" {
		t.Fatalf("%s", got)
	}
	if !strings.Contains(res.Steps[0].Error, `unknown variable "site"`) || !strings.Contains(res.Steps[2].Error, `no runner for step kind "check"`) {
		t.Fatalf("errors %+v", res.Steps)
	}
}

func TestStepLogs(t *testing.T) {
	dir := t.TempDir()
	rb := &runbook.Runbook{Phases: []runbook.Phase{{Name: "recover", Steps: []runbook.Step{{ID: "a", Run: "flaky:2", Retries: 1}}}}}
	executor.Execute(context.Background(), rb, executor.Options{
		Runners: map[string]executor.Runner{runbook.KindRun: &fakeRunner{}},
		Logs:    func(id string) (io.WriteCloser, error) { return os.Create(filepath.Join(dir, id+".log")) },
	})
	b, err := os.ReadFile(filepath.Join(dir, "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# step a (run) in phase recover", "# attempt 1", "# attempt 1 failed: not yet", "# attempt 2", "output of flaky:2", "ok\n"} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("log lacks %q:\n%s", want, b)
		}
	}
}
