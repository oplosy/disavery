package lab

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// SSH runs commands on lab nodes with the lab key.
type SSH struct {
	KeyFile string
	User    string
}

// Command builds an ssh invocation of command on host.
func (s SSH) Command(ctx context.Context, host, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "ssh",
		"-i", s.KeyFile,
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		s.User+"@"+host, command)
}

// Run executes command on host with optional stdin. A non-zero remote exit is
// returned as an *exec.ExitError wrapped with stderr.
func (s SSH) Run(ctx context.Context, host, command string, stdin io.Reader) (stdout string, err error) {
	cmd := s.Command(ctx, host, command)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("ssh %s %q: %w: %s", host, command, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
