package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, "usage: disavery"},
		{[]string{"explode"}, `unknown command "explode"`},
		{[]string{"drill", "run", "--root", "../.."}, "missing runbook id"},
		{[]string{"drill", "run", "nope", "extra", "--root", "../.."}, "unexpected arguments"},
		{[]string{"report"}, "missing report directory"},
		{[]string{"attachments", "rewind", "--store", "obj-a"}, "--store and --to are required"},
		{[]string{"attachments", "rewind", "--store", "obj-a", "--to", "yesterday"}, `cannot parse time "yesterday"`},
		{[]string{"vault", "undelete"}, "--since"},
	}
	for _, tc := range tests {
		code, _, errOut := runCLI(tc.args...)
		if code != 4 || !strings.Contains(errOut, tc.want) {
			t.Fatalf("%v: exit %d, stderr %q", tc.args, code, errOut)
		}
	}
	if code, out, _ := runCLI("help"); code != 0 || !strings.Contains(out, "Exit codes") {
		t.Fatalf("help: %d %q", code, out)
	}
}

func TestRunbookLintRepository(t *testing.T) {
	code, out, errOut := runCLI("runbook", "lint", "--root", "../..")
	if code != 0 || !strings.Contains(out, "ok   ") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
}

func TestEnvSet(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "infra", "terraform", "envs", "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI("env", "set", "--root", root, "site_b_enabled=true", "standby_site=b")
	if code != 0 || !strings.Contains(out, "active_site=a standby_site=b site_a_enabled=true site_b_enabled=true") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := runCLI("env", "set", "--root", root, "active_site=b"); code != 4 || !strings.Contains(errOut, "must differ") {
		t.Fatalf("invalid topology accepted: %d %s", code, errOut)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"500MB": 500 << 20, "1gb": 1 << 30, "64 KB": 64 << 10, "10B": 10} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Fatalf("%q: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "MB", "-5MB", "5TB", "five MB"} {
		if _, err := parseSize(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

// TestReportScalingExcludesRestoreTests: S6 restores a random backup set, not
// the current database, so by default it has no place in a table by size.
func TestReportScalingExcludesRestoreTests(t *testing.T) {
	history := filepath.Join(t.TempDir(), "history.jsonl")
	lines := `{"scenario":"s6-restore-test","result":"PASS","data_bytes":1073741824,"started_at":"2026-10-09T12:00:00Z","duration_seconds":60}
{"scenario":"s1-site-loss","tier":"pilot-light","result":"PASS","data_bytes":1073741824,"started_at":"2026-10-09T12:00:00Z","duration_seconds":200}
`
	if err := os.WriteFile(history, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI("report", "scaling", "--history", history)
	if code != 0 || strings.Contains(out, "s6-restore-test") || !strings.Contains(out, "| s1-site-loss | pilot-light | drill duration | 3m20s (1) |") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if code, out, _ := runCLI("report", "scaling", "--history", history, "--exclude", ""); code != 0 || !strings.Contains(out, "s6-restore-test") {
		t.Fatalf("--exclude '': exit %d\n%s", code, out)
	}
}
