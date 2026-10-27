package trainer

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestLockExcludesSecondHolder(t *testing.T) {
	s := newStore(t)
	release, err := s.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Lock(tactics.FamilyZombie)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock = %v, want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "pid") {
		t.Errorf("refusal %q does not name the holder", err)
	}
	other, err := s.Lock(tactics.FamilySkeleton)
	if err != nil {
		t.Fatalf("another family must not be blocked: %v", err)
	}
	other()
	release()
	again, err := s.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestLockGoroutinesRaceForOneWinner(t *testing.T) {
	s := newStore(t)
	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	var won, refused int
	hold := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := s.Lock(tactics.FamilyZombie)
			mu.Lock()
			if err == nil {
				won++
			} else if errors.Is(err, ErrLocked) {
				refused++
			} else {
				t.Errorf("lock: %v", err)
			}
			mu.Unlock()
			if err == nil {
				<-hold
				release()
			}
		}()
	}
	close(start)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		done := won+refused == n
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(hold)
	wg.Wait()
	if won != 1 || refused != n-1 {
		t.Errorf("won=%d refused=%d, want 1 and %d", won, refused, n-1)
	}
}

func TestRunRefusesLockedFamilyWithoutFighting(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.MaxGenerations = 2
	release, err := cfg.Store.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fake := cfg.Eval.(*Fake)
	if _, err := Run(context.Background(), cfg); !errors.Is(err, ErrLocked) {
		t.Fatalf("Run on a locked family = %v, want ErrLocked", err)
	}
	if fake.Calls() != 0 {
		t.Errorf("a refused run still evaluated %d fights", fake.Calls())
	}
	if _, ok, _ := cfg.Store.Champion(tactics.FamilyZombie); ok {
		t.Error("a refused run wrote a champion")
	}
}

func TestRunReleasesLockWhenDone(t *testing.T) {
	cfg := baseConfig(t, NewFake(tactics.FamilyZombie))
	cfg.MaxGenerations = 1
	cfg.Evals = 2
	cfg.Lambda = 1
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	release, err := cfg.Store.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatalf("lock still held after Run: %v", err)
	}
	release()
}

// TestLockAcrossProcesses re-executes the test binary as a second trainer
// process holding the lock, which is the case flock exists for.
func TestLockAcrossProcesses(t *testing.T) {
	s := newStore(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockHolderProcess$")
	cmd.Env = append(os.Environ(), "TRAINER_LOCK_HELPER="+s.Dir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		cmd.Wait()
	}()
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper did not take the lock, said %q", line)
	}
	if _, err := s.Lock(tactics.FamilyZombie); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock held by another process = %v, want ErrLocked", err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	release, err := s.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatalf("lock after the holder exited: %v", err)
	}
	release()
}

func TestLockHolderProcess(t *testing.T) {
	dir := os.Getenv("TRAINER_LOCK_HELPER")
	if dir == "" {
		t.Skip("helper for TestLockAcrossProcesses")
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	os.Stdout.WriteString("locked\n")
	os.Stdin.Read(make([]byte, 1))
}
