package arena

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commandRate is the rate lab/arena.sh and lab/courses.sh assume the server
// drains console commands at. The server buffers faster writes, but the
// director polls between batches and must not run ahead of the logged results.
const commandRate = 15

// settle is how long between log reads while waiting for a command's line.
const settle = 50 * time.Millisecond

// logWait caps the wait for a command that logs nothing, so a silent command
// costs one second instead of stalling the run.
const logWait = time.Second

// noiseWait caps the extra wait for a reply behind a run of fill successes:
// the server drains console commands in order, so a reply can sit behind the
// director's own fills.
const noiseWait = 10 * time.Second

type shellConsole struct {
	sendArgs []string
	logsArgs []string

	run       func(name string, args []string) ([]byte, error)
	now       func() time.Time
	sleep     func(time.Duration)
	last      time.Time
	served    int
	ready     bool
	waitLimit time.Duration
}

// NewKubectlConsole drives the cluster lab's server pod. It uses the same
// commands as lab/arena.sh: kubectl exec pod -- send-command, and the pod's
// log for the result.
func NewKubectlConsole(namespace, pod string) Console {
	return newShell(
		[]string{"kubectl", "-n", namespace, "exec", pod, "--", "send-command"},
		[]string{"kubectl", "-n", namespace, "logs", pod},
	)
}

// NewDockerConsole drives a compose lab. It matches lab/arena.sh's compose
// invocation, including -T so exec does not claim a TTY.
func NewDockerConsole(composeFile, service string) Console {
	return newShell(
		[]string{"docker", "compose", "-f", composeFile, "exec", "-T", service, "send-command"},
		[]string{"docker", "compose", "-f", composeFile, "logs", "--no-log-prefix", service},
	)
}

func newShell(sendArgs, logsArgs []string) *shellConsole {
	return &shellConsole{
		sendArgs: sendArgs,
		logsArgs: logsArgs,
		run: func(name string, args []string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		now:   time.Now,
		sleep: time.Sleep,
	}
}

// Exec sends one command, waits for the server to log the reply, and returns
// only the log lines produced since the previous call. Server logs are read
// whole because a restarted container truncates them; that resets the cursor.
func (c *shellConsole) Exec(cmd string) (string, error) {
	if !c.ready {
		// Skip whatever the server logged before this run, so a first testfor
		// cannot read a stale result from an earlier one.
		out, err := c.run(c.logsArgs[0], c.logsArgs[1:])
		if err != nil {
			return "", fmt.Errorf("read server log: %w", err)
		}
		c.served = len(splitLog(string(out)))
		c.ready = true
	}
	if !c.last.IsZero() {
		if wait := time.Second/commandRate - c.now().Sub(c.last); wait > 0 {
			c.sleep(wait)
		}
	}
	args := append(append([]string{}, c.sendArgs[1:]...), cmd)
	if out, err := c.run(c.sendArgs[0], args); err != nil {
		return string(out), fmt.Errorf("send %q: %w", cmd, err)
	}
	c.last = c.now()
	limit := c.waitLimit
	if limit <= 0 {
		limit = logWait
	}
	deadline := c.now().Add(limit)
	// A fill's own reply is a fill success, so only other commands wait behind
	// the noise.
	noiseDeadline := deadline
	if !strings.HasPrefix(cmd, "fill ") {
		noiseDeadline = c.now().Add(noiseWait)
	}
	for {
		c.sleep(settle)
		out, err := c.run(c.logsArgs[0], c.logsArgs[1:])
		if err != nil {
			return "", fmt.Errorf("read server log: %w", err)
		}
		lines := splitLog(string(out))
		if c.served > len(lines) {
			c.served = 0
		}
		if len(lines) > c.served {
			fresh := lines[c.served:]
			if !allFillNoise(fresh) || !c.now().Before(noiseDeadline) {
				c.served = len(lines)
				return strings.Join(fresh, "\n"), nil
			}
			continue
		}
		if !c.now().Before(deadline) {
			return "", nil
		}
	}
}

// allFillNoise says every line is the success reply of a fill. The unit kit's
// restock fills hundreds of cells a minute, and those lines would otherwise
// end the wait before the reply to the command that was sent is logged, so a
// testfor would be read as a reply-less batch of fills.
func allFillNoise(lines []string) bool {
	for _, l := range lines {
		if !strings.Contains(l, " blocks filled") {
			return false
		}
	}
	return true
}

func splitLog(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
