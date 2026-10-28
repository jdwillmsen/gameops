//go:build unix

package trainer

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// confine puts the evaluator in its own process group. Cancellation sends the
// group SIGTERM so the evaluator can withdraw what it set up, and SIGKILL once
// grace has passed: killing only the command would leave a grandchild that
// inherited the stdout pipe alive, holding the fight open. The returned func
// must run once the command has been waited for, so the late SIGKILL never
// lands on a group that is already gone.
func confine(cmd *exec.Cmd, grace time.Duration) (done func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var (
		mu       sync.Mutex
		finished bool
		killer   *time.Timer
	)
	cmd.Cancel = func() error {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			return os.ErrProcessDone
		}
		err := signalGroup(cmd, syscall.SIGTERM)
		killer = time.AfterFunc(grace, func() {
			mu.Lock()
			defer mu.Unlock()
			if !finished {
				_ = signalGroup(cmd, syscall.SIGKILL)
			}
		})
		return err
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		finished = true
		if killer != nil {
			killer.Stop()
		}
	}
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// killGroup kills everything the evaluator started, also after it exited.
func killGroup(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGKILL) }
