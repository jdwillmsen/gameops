//go:build unix

package trainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestCmdEvaluatorTermsTheGroupSoItCanCleanUp(t *testing.T) {
	shortWaitDelay(t)
	marks := t.TempDir()
	script, pidFile := writeScript(t, strings.ReplaceAll(`trap 'echo term > MARKS/term; echo "$TRAINER_DEADLINE" > MARKS/deadline; echo cleaned > MARKS/cleanup; exit 0' TERM
sleep 60 &
echo $! > PIDFILE
echo "$TRAINER_DEADLINE" > MARKS/deadline-at-start
wait
`, "MARKS", marks))
	start := time.Now()
	_, err := CmdEvaluator{Command: script, Timeout: time.Second}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out evaluator = %v, want the deadline error", err)
	}
	for _, f := range []string{"term", "cleanup"} {
		if _, err := os.Stat(filepath.Join(marks, f)); err != nil {
			t.Errorf("the evaluator never saw SIGTERM and ran its cleanup: missing %s", f)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(marks, "deadline-at-start"))
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("TRAINER_DEADLINE = %q: %v", raw, err)
	}
	if want := start.Add(time.Second); deadline.After(want.Add(time.Second)) || deadline.Before(want.Add(-2*time.Second)) {
		t.Errorf("TRAINER_DEADLINE = %v, want about %v", deadline, want)
	}
	gone(t, pidFile)
}

func TestCmdEvaluatorKillsAGroupThatIgnoresSIGTERM(t *testing.T) {
	shortWaitDelay(t)
	script, pidFile := writeScript(t, `trap '' TERM
sh -c 'trap "" TERM; sleep 60' &
echo $! > PIDFILE
while :; do sleep 1; done
`)
	start := time.Now()
	_, err := CmdEvaluator{Command: script, Timeout: 300 * time.Millisecond}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if err == nil {
		t.Fatal("an evaluator that ignores SIGTERM was not stopped")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("hostile evaluator held the fight for %v", d)
	}
	gone(t, pidFile)
}

func TestCmdEvaluatorWithoutADeadlineSetsNone(t *testing.T) {
	marks := t.TempDir()
	script, _ := writeScript(t, strings.ReplaceAll(`echo "${TRAINER_DEADLINE-unset}" > MARKS/deadline
echo '{"profile":"p1","outcome":"win"}'
`, "MARKS", marks))
	if _, err := (CmdEvaluator{Command: script}).Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(marks, "deadline")); strings.TrimSpace(string(raw)) != "unset" {
		t.Errorf("TRAINER_DEADLINE = %q with no timeout", raw)
	}
}

// recordSignals wraps sendSignal and returns the signals sent so far.
func recordSignals(t *testing.T) func() []syscall.Signal {
	t.Helper()
	var (
		mu   sync.Mutex
		sent []syscall.Signal
	)
	old := sendSignal
	sendSignal = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		sent = append(sent, sig)
		mu.Unlock()
		return old(pid, sig)
	}
	t.Cleanup(func() { sendSignal = old })
	return func() []syscall.Signal {
		mu.Lock()
		defer mu.Unlock()
		return append([]syscall.Signal(nil), sent...)
	}
}

func TestCmdEvaluatorSignalsNothingAfterANormalExit(t *testing.T) {
	sent := recordSignals(t)
	script, _ := writeScript(t, `echo '{"profile":"p1","outcome":"win"}'
`)
	if _, err := (CmdEvaluator{Command: script, Timeout: time.Minute}).Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := sent(); len(got) != 0 {
		t.Errorf("signals after a normal exit: %v", got)
	}

	failing, _ := writeScript(t, "exit 3\n")
	if _, err := (CmdEvaluator{Command: failing, Timeout: time.Minute}).Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario); err == nil {
		t.Fatal("a failing evaluator succeeded")
	}
	if got := sent(); len(got) != 0 {
		t.Errorf("signals after the evaluator exited on its own: %v", got)
	}
}

func TestCmdEvaluatorTimeoutSignalsTermBeforeKill(t *testing.T) {
	shortWaitDelay(t)
	sent := recordSignals(t)
	script, pidFile := writeScript(t, `sleep 60 &
echo $! > PIDFILE
wait
`)
	_, err := CmdEvaluator{Command: script, Timeout: 200 * time.Millisecond}.Evaluate(context.Background(), tactics.NewProfile("p1"), cmdScenario)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	gone(t, pidFile)
	got := sent()
	if len(got) == 0 || got[0] != syscall.SIGTERM {
		t.Errorf("signals = %v, want SIGTERM first", got)
	}
}
