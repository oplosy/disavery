package main

import (
	"bytes"
	"context"
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
