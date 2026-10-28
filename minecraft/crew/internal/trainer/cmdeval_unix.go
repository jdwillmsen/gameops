//go:build unix

package trainer

import (
	"os/exec"
	"syscall"
)

// confine puts the evaluator in its own process group and makes cancellation
// kill the whole group. Killing only the command would leave a grandchild
// that inherited the stdout pipe alive, holding the fight open.
func confine(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
}

// killGroup kills everything the evaluator started, also after it exited.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
