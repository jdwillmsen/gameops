//go:build !unix

package trainer

import "os/exec"

func confine(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
