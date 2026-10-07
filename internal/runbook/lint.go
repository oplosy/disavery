package runbook

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// BuiltinVars are set by the drill runner for every run and may be used in
// templates and conditions next to a runbook's own vars.
var BuiltinVars = []string{"scenario", "tier", "env", "run_id", "report_dir", "seed", "db_host"}

// Known lists the names a runbook may refer to.
type Known struct {
	Tiers  []string // from bia.yaml
	Checks []string // registered verifiers
}

var (
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	alertPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Lint validates a runbook and returns every problem found.
func Lint(rb *Runbook, k Known) error {
	l := &linter{known: k, ids: map[string]bool{}, vars: map[string]bool{}}
	if !idPattern.MatchString(rb.ID) {
		l.add("id %q must match %s", rb.ID, idPattern)
	}
	if rb.Path != "" && strings.TrimSuffix(filepath.Base(rb.Path), ".yaml") != rb.ID {
		l.add("id %q must equal the file name", rb.ID)
	}
	for _, t := range rb.Tiers {
		if !slices.Contains(k.Tiers, t) {
			l.add("unknown tier %q (bia.yaml defines %s)", t, strings.Join(k.Tiers, ", "))
		}
	}
	for _, v := range BuiltinVars {
		l.vars[v] = true
	}
	for name := range rb.Vars {
		if !namePattern.MatchString(name) || slices.Contains(BuiltinVars, name) {
			l.add("var %q: must match %s and not shadow a built-in variable", name, namePattern)
		}
		l.vars[name] = true
	}

	for _, s := range rb.Preflight {
		if s.Kind() != KindCheck {
			l.add("preflight step %q: must be a check", s.ID)
		}
		l.step(s, "preflight")
	}

	if len(rb.Phases) == 0 {
		l.add("at least one phase is required")
	}
	last := -1
	for _, p := range rb.Phases {
		switch idx := slices.Index(PhaseNames, p.Name); {
		case idx < 0:
			l.add("phase %q: must be one of %s", p.Name, strings.Join(PhaseNames, ", "))
		case idx <= last:
			l.add("phase %q: phases must appear at most once, in the order %s", p.Name, strings.Join(PhaseNames, ", "))
		default:
			last = idx
		}
		if len(p.Steps) == 0 {
			l.add("phase %q has no steps", p.Name)
		}
		for _, s := range p.Steps {
			l.step(s, p.Name)
		}
	}
	for _, s := range rb.Cleanup {
		l.step(s, "cleanup")
	}
	return errors.Join(l.errs...)
}

type linter struct {
	known Known
	ids   map[string]bool
	vars  map[string]bool
	errs  []error
}

func (l *linter) add(format string, a ...any) { l.errs = append(l.errs, fmt.Errorf(format, a...)) }

func (l *linter) step(s Step, where string) {
	at := fmt.Sprintf("%s step %q", where, s.ID)
	if !idPattern.MatchString(s.ID) {
		l.add("%s: id must match %s", at, idPattern)
	}
	if l.ids[s.ID] {
		l.add("%s: duplicate step id", at)
	}
	l.ids[s.ID] = true

	kinds := s.kinds()
	if len(kinds) != 1 {
		l.add("%s: needs exactly one of run, ssh, wait_http, wait_alert, sleep, manual, check (found %d)", at, len(kinds))
	}
	switch s.Kind() {
	case KindSSH:
		if s.SSH.Host == "" || s.SSH.Cmd == "" {
			l.add("%s: ssh needs host and cmd", at)
		}
	case KindWaitHTTP:
		if s.WaitHTTP.URL == "" {
			l.add("%s: wait_http needs url", at)
		}
		if s.WaitHTTP.Status != 0 && (s.WaitHTTP.Status < 100 || s.WaitHTTP.Status > 599) {
			l.add("%s: wait_http status %d is not an HTTP status", at, s.WaitHTTP.Status)
		}
	case KindWaitAlert:
		if !alertPattern.MatchString(s.WaitAlert) {
			l.add("%s: wait_alert %q is not an alert name", at, s.WaitAlert)
		}
	case KindManual:
		if s.AutoAfter <= 0 {
			l.add("%s: manual needs auto_after so unattended runs cannot hang", at)
		}
	case KindCheck:
		if !slices.Contains(l.known.Checks, s.Check) {
			l.add("%s: unknown check %q (known: %s)", at, s.Check, strings.Join(l.known.Checks, ", "))
		}
	}
	if s.AutoAfter > 0 && s.Manual == "" {
		l.add("%s: auto_after is only valid with manual", at)
	}
	if len(s.With) > 0 && s.Check == "" {
		l.add("%s: with is only valid with check", at)
	}
	if s.OnFailure != "" && s.OnFailure != "abort" && s.OnFailure != "continue" {
		l.add("%s: on_failure must be abort or continue", at)
	}
	if s.Retries < 0 || s.Retries > 10 {
		l.add("%s: retries must be between 0 and 10", at)
	}
	if s.When != "" {
		e, err := ParseExpr(s.When)
		if err != nil {
			l.add("%s: %v", at, err)
		} else {
			for _, id := range e.Idents() {
				if !l.vars[id] {
					l.add("%s: condition refers to unknown variable %q", at, id)
				}
			}
		}
	}
	for name, p := range s.templated() {
		if _, err := parseTemplate(*p); err != nil {
			l.add("%s: %s: %v", at, name, err)
		}
	}
	for k, v := range s.With {
		if _, err := parseTemplate(v); err != nil {
			l.add("%s: with.%s: %v", at, k, err)
		}
	}
	if s.Capture != "" {
		switch {
		case s.Kind() != KindRun && s.Kind() != KindSSH:
			l.add("%s: capture is only valid with run or ssh", at)
		case !namePattern.MatchString(s.Capture) || l.vars[s.Capture]:
			l.add("%s: capture %q must match %s and not reuse a variable name", at, s.Capture, namePattern)
		}
		l.vars[s.Capture] = true
	}
}
