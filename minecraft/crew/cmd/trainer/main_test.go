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
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
)

// switchDir makes a control directory whose training switch says value.
func switchDir(t *testing.T, value string) string {
	t.Helper()
	dir := t.TempDir()
	if value != "" {
		if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// runTrainer runs with training switched on unless the test names its own
// --control-dir.
func runTrainer(t *testing.T, args ...string) (trainer.Result, int, string) {
	t.Helper()
	t.Setenv("TACTICSCTL_DIR", switchDir(t, "on\n"))
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

func TestTrainingSwitchGatesTheRun(t *testing.T) {
	for name, value := range map[string]string{"missing": "", "off": "off\n", "garbled": "ON please\n", "empty": "\n"} {
		store := filepath.Join(t.TempDir(), "s")
		res, code, stderr := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
			"--control-dir", switchDir(t, value))
		if code != 3 || res.StopReason != trainer.StopDisabled || res.Fights != 0 {
			t.Errorf("%s: code %d result %+v", name, code, res)
		}
		if !strings.Contains(stderr, "training switch") {
			t.Errorf("%s: stderr %q does not say why", name, stderr)
		}
		if _, err := os.Stat(filepath.Join(store, "zombie", "champion.json")); err == nil {
			t.Errorf("%s: a disabled run wrote a champion", name)
		}
	}
	store := filepath.Join(t.TempDir(), "s")
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--control-dir", switchDir(t, "on\n"), "--gens", "2")
	if code != 0 || res.StopReason != trainer.StopGenerations {
		t.Errorf("switch on: code %d result %+v", code, res)
	}
}

func TestSwitchTurnedOffMidRunStopsIt(t *testing.T) {
	dir := switchDir(t, "on\n")
	store := filepath.Join(t.TempDir(), "s")
	script := filepath.Join(t.TempDir(), "eval.sh")
	body := "#!/bin/sh\ncat >/dev/null\nprintf 'off\\n' > " + filepath.Join(dir, tacticsctl.TrainingFile) +
		"\necho '{\"profile\":\"'$TRAINER_PROFILE'\",\"outcome\":\"win\",\"seconds\":30}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	res, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--control-dir", dir, "--evaluator", "cmd:"+script)
	if code != 3 || res.StopReason != trainer.StopDisabled || res.Fights != 1 {
		t.Errorf("code %d result %+v, want disabled after the first fight", code, res)
	}
}

func TestEvaluatorTargetMustBeTheLab(t *testing.T) {
	store := filepath.Join(t.TempDir(), "s")
	for _, cmd := range []string{
		"trainbridge --lab fwb-minecraft",
		"trainbridge --lab k8s:minecraft/fwb-0",
		"trainbridge --lab=prod",
		"trainbridge --lab k8s:lab/fwbmc-0",
		"trainbridge --lab crew-lab --squad alpha",
		"trainbridge --lab crew-lab --squad lab-alpha --squad-b prod",
		"trainbridge -lab",
	} {
		_, code, stderr := runTrainer(t, "run", "--family", "zombie", "--scenario", "x", "--store", store, "--evaluator", "cmd:"+cmd)
		if code != 2 {
			t.Errorf("%q: exit %d (%s), want a refusal before any fight", cmd, code, stderr)
		}
	}
	if _, code, _ := runTrainer(t, "run", "--family", "zombie", "--scenario", "x", "--store", store, "--max-failures", "1",
		"--evaluator", "cmd:/nonexistent/trainbridge --lab crew-lab --squad lab-alpha --squad-b lab-bravo"); code != 1 {
		t.Errorf("a lab target was refused: exit %d, want 1 (evaluator failed to run)", code)
	}
}

func TestEvaluatorGetsTheRunsSwitchAndKillFile(t *testing.T) {
	dir := switchDir(t, "on\n")
	store := filepath.Join(t.TempDir(), "s")
	seen := filepath.Join(t.TempDir(), "env")
	script := filepath.Join(t.TempDir(), "eval.sh")
	body := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n%s\\n' \"$TACTICSCTL_DIR\" \"$TRAINER_STOP_FILE\" > " + seen +
		"\necho '{\"profile\":\"'$TRAINER_PROFILE'\",\"outcome\":\"win\",\"seconds\":30}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, code, stderr := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--control-dir", dir, "--gens", "1", "--evaluator", "cmd:"+script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got, _ := os.ReadFile(seen)
	if want := dir + "\n" + filepath.Join(store, "KILL") + "\n"; string(got) != want {
		t.Errorf("evaluator saw %q, want %q", got, want)
	}

	custom := filepath.Join(t.TempDir(), "STOP")
	if _, code, stderr := runTrainer(t, "run", "--family", "zombie", "--scenario", "pack", "--store", store,
		"--control-dir", dir, "--gens", "1", "--kill-file", custom, "--evaluator", "cmd:"+script); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got, _ = os.ReadFile(seen)
	if want := dir + "\n" + custom + "\n"; string(got) != want {
		t.Errorf("evaluator saw %q with --kill-file, want %q", got, want)
	}
}
