package runbook_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/runbook"
)

var known = runbook.Known{
	Tiers:  []string{"pilot-light", "warm-standby"},
	Checks: []string{"amcheck", "pitr"},
}

const validRunbook = `
id: sample
description: A sample drill.
tiers: [pilot-light, warm-standby]
vars: { delay: 30s }
phases:
  - name: inject
    steps:
      - { id: kill, run: "echo kill {{.tier}}" }
  - name: decide
    steps:
      - { id: declare, manual: "Declare disaster?", auto_after: 30s }
  - name: recover
    steps:
      - { id: provision, when: "tier == 'pilot-light'", run: "echo build", timeout: 5m, retries: 1 }
      - { id: promote, when: "tier == 'warm-standby'", ssh: { host: db-b, cmd: "pg_ctl promote" } }
      - { id: pick, run: "echo '{\"set\":\"x\"}'", capture: point }
  - name: verify
    steps:
      - { id: integ, check: amcheck, with: { host: "{{.point.set}}" } }
cleanup:
  - { id: tidy, run: "true" }
`

// writeRunbook writes content as <dir>/sample.yaml and loads it.
func writeRunbook(t *testing.T, content string) *runbook.Runbook {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rb, err := runbook.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return rb
}

func TestValidRunbook(t *testing.T) {
	rb := writeRunbook(t, validRunbook)
	if err := runbook.Lint(rb, known); err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !rb.HasPhase("inject") || rb.HasPhase("detect") {
		t.Fatal("HasPhase is wrong")
	}
	recover := rb.Phases[2].Steps
	if recover[0].Kind() != runbook.KindRun || recover[1].Kind() != runbook.KindSSH || recover[0].Timeout.D() != 5*time.Minute {
		t.Fatalf("unexpected steps: %+v", recover)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.yaml")
	if err := os.WriteFile(path, []byte("id: x\nphases: []\nretry: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runbook.Load(path); err == nil || !strings.Contains(err.Error(), "field retry not found") {
		t.Fatalf("want unknown field error, got %v", err)
	}
}

func TestLint(t *testing.T) {
	base := validRunbook
	tests := []struct {
		name, old, new, want string
	}{
		{"id differs from file", "id: sample", "id: other", `id "other" must equal the file name`},
		{"unknown tier", "tiers: [pilot-light, warm-standby]", "tiers: [cold]", `unknown tier "cold"`},
		{"unknown phase", "name: decide", "name: decision", `phase "decision": must be one of`},
		{"phase order", "  - name: inject\n", "  - name: verify\n", "phases must appear at most once, in the order"},
		{"two kinds", `{ id: tidy, run: "true" }`, `{ id: tidy, run: "true", sleep: 1s }`, "needs exactly one of"},
		{"no kind", `{ id: tidy, run: "true" }`, `{ id: tidy }`, "needs exactly one of"},
		{"duplicate id", "id: tidy", "id: kill", "duplicate step id"},
		{"bad step id", "id: tidy", "id: Tidy", "id must match"},
		{"manual without auto_after", ", auto_after: 30s", "", "manual needs auto_after"},
		{"unknown check", "check: amcheck", "check: fsck", `unknown check "fsck"`},
		{"with without check", `{ id: tidy, run: "true" }`, `{ id: tidy, run: "true", with: { a: b } }`, "with is only valid with check"},
		{"bad on_failure", `{ id: tidy, run: "true" }`, `{ id: tidy, run: "true", on_failure: ignore }`, "on_failure must be abort or continue"},
		{"too many retries", "retries: 1", "retries: 11", "retries must be between 0 and 10"},
		{"bad condition", "tier == 'pilot-light'", "tier = 'pilot-light'", "unexpected character"},
		{"incomplete condition", "tier == 'pilot-light'", "tier == pilot", "expected <variable> =="},
		{"unknown condition variable", "tier == 'pilot-light'", "site == 'a'", `unknown variable "site"`},
		{"bad template", `echo kill {{.tier}}`, `echo kill {{.tier}`, "run:"},
		{"capture on check", "check: amcheck, with", "check: amcheck, capture: x, with", "capture is only valid with run or ssh"},
		{"capture shadows var", "capture: point", "capture: tier", `capture "tier" must match`},
		{"wait_alert not yet", `{ id: tidy, run: "true" }`, `{ id: tidy, wait_alert: PostgresPrimaryDown }`, "wait_alert is not available yet"},
		{"ssh without host", `ssh: { host: db-b, cmd: "pg_ctl promote" }`, `ssh: { cmd: "pg_ctl promote" }`, "ssh needs host and cmd"},
		{"var shadows builtin", "vars: { delay: 30s }", "vars: { tier: x }", `var "tier"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(base, tc.old) {
				t.Fatalf("test bug: %q not in base runbook", tc.old)
			}
			rb := writeRunbook(t, strings.Replace(base, tc.old, tc.new, 1))
			err := runbook.Lint(rb, known)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestExpr(t *testing.T) {
	vars := map[string]string{"tier": "pilot-light", "site": "b"}
	tests := []struct {
		src  string
		want bool
	}{
		{"tier == 'pilot-light'", true},
		{"tier != 'pilot-light'", false},
		{"tier == 'warm-standby' || site == 'b'", true},
		{"tier == 'pilot-light' && site == 'a'", false},
		{"tier == 'x' && site == 'b' || tier == 'pilot-light'", true},
		{"  tier=='pilot-light'  ", true},
	}
	for _, tc := range tests {
		e, err := runbook.ParseExpr(tc.src)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		got, err := e.Eval(vars)
		if err != nil || got != tc.want {
			t.Fatalf("%q = %v, %v; want %v", tc.src, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "tier", "tier == pilot", "tier == 'a' &&", "tier == 'a", "(tier == 'a')", "tier === 'a'"} {
		if _, err := runbook.ParseExpr(bad); err == nil {
			t.Fatalf("ParseExpr(%q): want error", bad)
		}
	}
	e, _ := runbook.ParseExpr("zone == 'a'")
	if _, err := e.Eval(vars); err == nil {
		t.Fatal("want error for unknown variable")
	}
}

func TestRendered(t *testing.T) {
	s := runbook.Step{
		ID:   "x",
		SSH:  &runbook.SSH{Host: "db-{{.site}}", Cmd: "echo {{.point.set}}"},
		With: map[string]string{"target": "{{.point.target}}"},
	}
	data := map[string]any{"site": "b", "point": map[string]any{"set": "F1", "target": "T"}}
	r, err := s.Rendered(data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if r.SSH.Host != "db-b" || r.SSH.Cmd != "echo F1" || r.With["target"] != "T" {
		t.Fatalf("rendered %+v %+v", r.SSH, r.With)
	}
	if s.SSH.Host != "db-{{.site}}" || s.With["target"] != "{{.point.target}}" {
		t.Fatal("Rendered modified the original step")
	}
	if _, err := (runbook.Step{Run: "{{.missing}}"}).Rendered(data); err == nil {
		t.Fatal("want error for a missing key")
	}
}

func TestFindRejectsPathTraversal(t *testing.T) {
	if _, err := runbook.Find(t.TempDir(), "../etc/passwd"); err == nil || !strings.Contains(err.Error(), "invalid runbook id") {
		t.Fatalf("got %v", err)
	}
}
