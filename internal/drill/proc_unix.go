//go:build unix

package drill

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup makes cancellation signal the whole process group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
}
