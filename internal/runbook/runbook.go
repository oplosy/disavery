// Package runbook parses and validates drill runbooks (runbooks/*.yaml).
package runbook

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/oplosy/disavery/internal/yamltime"
)

// PhaseNames are the only phase names, in their required order. They are
// fixed so RTO can be broken down into detect, decide, recover and verify.
var PhaseNames = []string{"inject", "detect", "decide", "recover", "verify"}

// Step kinds; a step has exactly one.
const (
	KindRun       = "run"
	KindSSH       = "ssh"
	KindWaitHTTP  = "wait_http"
	KindWaitAlert = "wait_alert"
	KindSleep     = "sleep"
	KindManual    = "manual"
	KindCheck     = "check"
)

// Runbook is one drill scenario.
type Runbook struct {
	ID          string            `yaml:"id"`
	Description string            `yaml:"description"`
	Tiers       []string          `yaml:"tiers"`
	Vars        map[string]string `yaml:"vars"`
	// Preflight checks must all pass before anything changes; a failure ends
	// the drill as ERROR with the lab untouched.
	Preflight []Step  `yaml:"preflight"`
	Phases    []Phase `yaml:"phases"`
	Cleanup   []Step  `yaml:"cleanup"`

	Path string `yaml:"-"`
}

// Phase is a named group of steps.
type Phase struct {
	Name  string `yaml:"name"`
	Steps []Step `yaml:"steps"`
}

// Step is one action. Common fields first, then exactly one kind field.
type Step struct {
	ID        string            `yaml:"id"`
	When      string            `yaml:"when"`
	Timeout   yamltime.Duration `yaml:"timeout"`
	Retries   int               `yaml:"retries"`
	OnFailure string            `yaml:"on_failure"`
	Capture   string            `yaml:"capture"`

	Run       string            `yaml:"run"`
	SSH       *SSH              `yaml:"ssh"`
	WaitHTTP  *WaitHTTP         `yaml:"wait_http"`
	WaitAlert string            `yaml:"wait_alert"`
	Sleep     yamltime.Duration `yaml:"sleep"`
	Manual    string            `yaml:"manual"`
	AutoAfter yamltime.Duration `yaml:"auto_after"`
	Check     string            `yaml:"check"`
	With      map[string]string `yaml:"with"`
}

// SSH runs a command on a lab node.
type SSH struct {
	Host string `yaml:"host"`
	Cmd  string `yaml:"cmd"`
}

// WaitHTTP polls a URL until it answers with Status.
type WaitHTTP struct {
	URL      string            `yaml:"url"`
	Status   int               `yaml:"status"`
	Interval yamltime.Duration `yaml:"interval"`
}

// Kind returns the step's kind, or "" unless exactly one kind field is set.
func (s Step) Kind() string {
	if k := s.kinds(); len(k) == 1 {
		return k[0]
	}
	return ""
}

func (s Step) kinds() []string {
	var k []string
	if s.Run != "" {
		k = append(k, KindRun)
	}
	if s.SSH != nil {
		k = append(k, KindSSH)
	}
	if s.WaitHTTP != nil {
		k = append(k, KindWaitHTTP)
	}
	if s.WaitAlert != "" {
		k = append(k, KindWaitAlert)
	}
	if s.Sleep > 0 {
		k = append(k, KindSleep)
	}
	if s.Manual != "" {
		k = append(k, KindManual)
	}
	if s.Check != "" {
		k = append(k, KindCheck)
	}
	return k
}

// HasPhase reports whether the runbook contains the named phase.
func (rb *Runbook) HasPhase(name string) bool {
	return slices.ContainsFunc(rb.Phases, func(p Phase) bool { return p.Name == name })
}

// Load parses one runbook file. Unknown keys are errors, so typos fail loudly.
func Load(path string) (*Runbook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var rb Runbook
	if err := dec.Decode(&rb); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	rb.Path = path
	return &rb, nil
}

// LoadDir parses every *.yaml file in dir, sorted by file name.
func LoadDir(dir string) ([]*Runbook, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	var (
		out  []*Runbook
		errs []error
	)
	for _, f := range files {
		rb, err := Load(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, rb)
	}
	return out, errors.Join(errs...)
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Find loads the runbook with the given id from dir.
func Find(dir, id string) (*Runbook, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid runbook id %q", id)
	}
	return Load(filepath.Join(dir, id+".yaml"))
}
