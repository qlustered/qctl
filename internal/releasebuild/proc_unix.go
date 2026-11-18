//go:build unix

package releasebuild

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in a new process group and makes cancellation
// kill the whole group.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
