package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
)

func runTrainer(t *testing.T, args ...string) (trainer.Result, int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := realMain(context.Background(), args, &out, &errb)
	var res trainer.Result
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("stdout %q: %v", out.String(), err)
		}
	}
	return res, code, errb.String()
}

func TestRunWithFakeEvaluatorHonoursCaps(t *testing.T) {
	store := filepath.Join(t.TempDir(), "s")
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--gens", "4", "--fights", "10000", "--budget", "1m")
	if code != 0 || res.Generations != 4 || res.StopReason != trainer.StopGenerations {
		t.Errorf("code %d result %+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(store, "zombie", "champion.json")); err != nil {
		t.Errorf("no champion written: %v", err)
	}

	res, _, _ = runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--gens", "100", "--fights", "50")
	if res.Fights > 50 || res.StopReason != trainer.StopFights {
		t.Errorf("fight cap: %+v", res)
	}
}

func TestDefaultKillFileInStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "s")
	os.MkdirAll(store, 0o755)
	os.WriteFile(filepath.Join(store, "KILL"), nil, 0o644)
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store)
	if code != 0 || res.StopReason != trainer.StopKilled || res.Fights != 0 {
		t.Errorf("code %d result %+v", code, res)
	}
}

func TestUsageErrors(t *testing.T) {
	store := t.TempDir()
	for _, args := range [][]string{
		{},
		{"nope"},
		{"run"},
		{"run", "--family", "zombie", "--scenario", "x", "--store", store, "--evaluator", "docker"},
		{"run", "--family", "zombie", "--scenario", "x", "--store", store, "--evaluator", "cmd:"},
		{"run", "--family", "zombie", "--scenario", "x", "--store", store, "--gens", "0"},
		{"run", "--family", "zombie", "--scenario", "x", "--store", store, "--budget", "0s"},
		{"run", "--family", "Bad/Family", "--scenario", "x", "--store", store},
	} {
		if _, code, _ := runTrainer(t, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
}

func TestFailingCmdEvaluatorExitsOne(t *testing.T) {
	store := filepath.Join(t.TempDir(), "s")
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "x", "--store", store,
		"--evaluator", "cmd:/nonexistent/evaluator", "--max-failures", "3")
	if code != 1 || res.StopReason != trainer.StopFailures || res.Fights != 3 {
		t.Errorf("code %d result %+v", code, res)
	}
}

func TestSecondTrainerOnTheSameFamilyIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	store, err := trainer.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	release, err := store.Lock(tactics.FamilyZombie)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	_, code, stderr := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", dir, "--gens", "2")
	if code != 2 || !strings.Contains(stderr, "already being trained") {
		t.Errorf("code %d stderr %q, want 2 and a lock refusal", code, stderr)
	}
	if _, code, _ := runTrainer(t, "run", "--family", "skeleton", "--scenario", "pack", "--store", dir, "--gens", "1"); code != 0 {
		t.Errorf("a different family was refused: exit %d", code)
	}
}

func TestLearnedTacticsAreRegisteredOnes(t *testing.T) {
	registered := life.TacticNames(tactics.FamilyZombie)
	if len(registered) == 0 {
		t.Skip("the engine registers no zombie tactics")
	}
	store := filepath.Join(t.TempDir(), "s")
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store, "--gens", "12")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, name := range res.Champion.Profile.TacticsFor(tactics.FamilyZombie) {
		if !slices.Contains(registered, name) {
			t.Errorf("champion uses %q, not one of %v", name, registered)
		}
	}

	_, code, _ = runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store, "--gens", "2", "--tactics=false")
	if code != 0 {
		t.Errorf("--tactics=false: exit %d", code)
	}
}
