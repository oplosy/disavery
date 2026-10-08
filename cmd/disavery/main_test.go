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
