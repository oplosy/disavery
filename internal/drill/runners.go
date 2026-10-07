package drill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/oplosy/disavery/internal/executor"
	"github.com/oplosy/disavery/internal/lab"
	"github.com/oplosy/disavery/internal/verify"
)

// runRunner runs `run` steps with bash in the repository root (the toolbox).
type runRunner struct {
	Dir string
}

func (r runRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	cmd := exec.CommandContext(ctx, "bash", "-o", "pipefail", "-c", s.Run)
	cmd.Dir = r.Dir
	return runCommand(cmd, log)
}

// sshRunner runs `ssh` steps on a lab node.
type sshRunner struct {
	SSH lab.SSH
}

func (r sshRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	return runCommand(r.SSH.Command(ctx, s.SSH.Host, s.SSH.Cmd), log)
}

// runCommand streams stdout and stderr into the step log and keeps stdout
// for capture. The command runs in its own process group so a timeout stops
// its children (e.g. terraform providers) too.
func runCommand(cmd *exec.Cmd, log io.Writer) (executor.Output, error) {
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(log, &out)
	cmd.Stderr = log
	setProcessGroup(cmd)
	err := cmd.Run()
	return executor.Output{Stdout: out.String()}, err
}

// waitHTTPRunner polls a URL until it answers with the expected status.
type waitHTTPRunner struct {
	Client func(ctx context.Context) (*http.Client, error)
}

func (r waitHTTPRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	client, err := r.Client(ctx)
	if err != nil {
		return executor.Output{}, err
	}
	want, interval := s.WaitHTTP.Status, s.WaitHTTP.Interval.D()
	if want == 0 {
		want = http.StatusOK
	}
	if interval == 0 {
		interval = time.Second
	}
	var last string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.WaitHTTP.URL, nil)
		if err != nil {
			return executor.Output{}, err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == want {
				fmt.Fprintf(log, "%s answered %d\n", s.WaitHTTP.URL, resp.StatusCode)
				return executor.Output{}, nil
			}
			last = fmt.Sprintf("status %d", resp.StatusCode)
		} else {
			last = err.Error()
		}
		fmt.Fprintf(log, "%s: %s\n", s.WaitHTTP.URL, last)
		select {
		case <-ctx.Done():
			return executor.Output{}, fmt.Errorf("%s never answered %d (last: %s): %w", s.WaitHTTP.URL, want, last, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// sleepRunner waits.
type sleepRunner struct{}

func (sleepRunner) Run(ctx context.Context, s executor.Step, _ io.Writer) (executor.Output, error) {
	select {
	case <-ctx.Done():
		return executor.Output{}, ctx.Err()
	case <-time.After(s.Sleep.D()):
		return executor.Output{}, nil
	}
}

// manualRunner asks an operator, or continues automatically after
// auto_after when unattended. Either way the decision is recorded.
type manualRunner struct {
	Lines <-chan string // operator input; nil when unattended
	Out   io.Writer
	Now   func() time.Time
}

func (m manualRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	d := &executor.Decision{Prompt: s.Manual}
	wait := s.AutoAfter.D()
	if m.Lines == nil {
		fmt.Fprintf(m.Out, "  decision %q: continuing automatically in %s\n", s.Manual, wait)
	} else {
		fmt.Fprintf(m.Out, "  %s [y/N] (continues automatically in %s) ", s.Manual, wait)
	}
	select {
	case <-ctx.Done():
		return executor.Output{}, ctx.Err()
	case <-time.After(wait):
		d.Answer, d.Auto = "continue", true
	case line := <-m.Lines:
		if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
			d.Answer = "yes"
		} else {
			d.Answer = "no"
		}
	}
	d.At = m.Now()
	fmt.Fprintf(log, "decision %q: %s (automatic: %v)\n", d.Prompt, d.Answer, d.Auto)
	if d.Answer == "no" {
		return executor.Output{Decision: d}, errors.New("operator declined")
	}
	return executor.Output{Decision: d}, nil
}

// readLines feeds stdin lines to manual steps from one goroutine, so a
// prompt that timed out does not swallow the answer to the next one.
func readLines(in io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			ch <- sc.Text()
		}
		// On EOF or a read error the channel stays open: later prompts then
		// continue automatically instead of reading an empty answer as "no".
		_ = sc.Err()
	}()
	return ch
}

// checkRunner runs a built-in verifier.
type checkRunner struct {
	Registry verify.Registry
	Start    time.Time
	Now      func() time.Time
}

func (c checkRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	chk, ok := c.Registry[s.Check]
	if !ok {
		return executor.Output{}, fmt.Errorf("unknown check %q", s.Check)
	}
	res, err := chk.Run(ctx, verify.Params{With: s.With, Start: c.Start, Incident: s.Incident, Now: c.Now()})
	if err != nil {
		return executor.Output{}, err
	}
	res.Name = s.Check
	fmt.Fprintf(log, "%s: %s\n", res.Status, res.Summary)
	for _, d := range res.Details {
		fmt.Fprintf(log, "  %s\n", d)
	}
	for k, v := range res.Metrics {
		fmt.Fprintf(log, "  metric %s = %g\n", k, v)
	}
	out := executor.Output{Check: &res}
	if res.Status == verify.Fail {
		return out, fmt.Errorf("check failed: %s", res.Summary)
	}
	return out, nil
}

// waitAlertRunner waits until Alertmanager reports the named alert as active.
type waitAlertRunner struct {
	URL    string // Alertmanager base URL
	Client *http.Client
}

func (r waitAlertRunner) Run(ctx context.Context, s executor.Step, log io.Writer) (executor.Output, error) {
	for {
		active, err := activeAlerts(ctx, r.Client, r.URL)
		switch {
		case err != nil:
			fmt.Fprintf(log, "alertmanager: %v\n", err)
		case slices.Contains(active, s.WaitAlert):
			fmt.Fprintf(log, "%s is firing\n", s.WaitAlert)
			return executor.Output{}, nil
		}
		select {
		case <-ctx.Done():
			return executor.Output{}, fmt.Errorf("%s did not fire: %w", s.WaitAlert, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// activeAlerts returns the names of active (not silenced or inhibited) alerts.
func activeAlerts(ctx context.Context, c *http.Client, base string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/v2/alerts?active=true&silenced=false&inhibited=false", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var alerts []struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(alerts))
	for _, a := range alerts {
		names = append(names, a.Labels["alertname"])
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
