//go:build unix

package trainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func writeScript(t *testing.T, body string) (script, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "grandchild.pid")
	script = filepath.Join(dir, "eval.sh")
	body = strings.ReplaceAll(body, "PIDFILE", pidFile)
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, pidFile
}

func shortWaitDelay(t *testing.T) {
	t.Helper()
	old := waitDelay
	waitDelay = 200 * time.Millisecond
	t.Cleanup(func() { waitDelay = old })
}

// gone waits for the grandchild whose pid the script recorded to disappear.
func gone(t *testing.T, pidFile string) {
	t.Helper()
	var pid int
	for i := 0; i < 50; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the script never recorded its grandchild")
	}
	for i := 0; i < 100; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d still running after the fight", pid)
}

var cmdScenario = Scenario{Name: "pack", Kind: tactics.ScenarioMob, Family: tactics.FamilyZombie, Seed: 1, Side: "a"}

func TestCmdEvaluatorDoesNotWaitOnAGrandchildHoldingThePipe(t *testing.T) {
	shortWaitDelay(t)
	script, pidFile := writeScript(t, `sleep 60 &
echo $! > PIDFILE
echo '{"profile":"p1","outcome":"win"}'
`)
	start := time.Now()
	_, err := CmdEvaluator{Command: script}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if err == nil || !strings.Contains(err.Error(), "holding its output") {
		t.Fatalf("evaluator with a lingering grandchild = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("fight took %v with a 200ms wait delay", d)
	}
	gone(t, pidFile)
}

func TestCmdEvaluatorTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	shortWaitDelay(t)
	script, pidFile := writeScript(t, `sleep 60 &
echo $! > PIDFILE
wait
`)
	start := time.Now()
	_, err := CmdEvaluator{Command: script, Timeout: 300 * time.Millisecond}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out evaluator = %v, want the deadline error", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("timeout took %v", d)
	}
	gone(t, pidFile)
}

func TestCmdEvaluatorCapsOutputWhileReadingIt(t *testing.T) {
	shortWaitDelay(t)
	script, pidFile := writeScript(t, `sleep 60 &
echo $! > PIDFILE
exec yes '{"profile":"p1","outcome":"win"}'
`)
	start := time.Now()
	cards, err := CmdEvaluator{Command: script, Timeout: time.Minute}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if !errors.Is(err, errOutputTooLarge) || len(cards) != 0 {
		t.Fatalf("flooding evaluator = %d cards, %v", len(cards), err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("flood took %v to stop", d)
	}
	gone(t, pidFile)
}
