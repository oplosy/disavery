//go:build !unix

package drill

import "os/exec"

// setProcessGroup is a no-op outside Unix; the CLI runs in the Linux toolbox.
func setProcessGroup(*exec.Cmd) {}
