//go:build !unix

package trainer

import (
	"os/exec"
	"time"
)

func confine(*exec.Cmd, time.Duration) (done func()) { return func() {} }

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
