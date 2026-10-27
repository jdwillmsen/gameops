package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

type env struct {
	t     *testing.T
	dir   string
	trust string
	keys  string
}

func newEnv(t *testing.T) *env {
	root := t.TempDir()
	e := &env{t: t, dir: filepath.Join(root, "state"), trust: filepath.Join(root, "trust"), keys: filepath.Join(root, "keys")}
	e.do("", false, "keygen", "--name", "owner", "--out", e.keys)
	if err := os.MkdirAll(e.trust, 0o755); err != nil {
		t.Fatal(err)
	}
	pub, _ := os.ReadFile(filepath.Join(e.keys, "owner.pub"))
	os.WriteFile(filepath.Join(e.trust, "owner.pub"), pub, 0o644)
	return e
}

func (e *env) do(stdin string, tty bool, args ...string) (string, error) {
	var out bytes.Buffer
	full := append([]string{"--dir", e.dir, "--trust", e.trust}, args...)
	err := run(full, strings.NewReader(stdin), &out, tty)
	return out.String(), err
}

func (e *env) profile(name, parent string) {
	pr := tactics.NewProfile(name)
	pr.Parent = parent
	pr.Default.ShotEvery = 40
	b, _ := json.Marshal(pr)
	f := filepath.Join(e.t.TempDir(), name+".json")
	os.WriteFile(f, b, 0o644)
	if _, err := e.do("", false, "add", f); err != nil {
		e.t.Fatal(err)
	}
}

func TestStatusStartsDisabledAndDefault(t *testing.T) {
	e := newEnv(t)
	out, err := e.do("", false, "status")
	if err != nil || !strings.Contains(out, "training: disabled") || !strings.Contains(out, "production: default") {
		t.Errorf("status = %q, %v", out, err)
	}
}

func TestKillSwitchNaming(t *testing.T) {
	e := newEnv(t)
	if out, _ := e.do("", false, "kill", "off"); !strings.Contains(out, "enabled") {
		t.Errorf("kill off = %q, want training enabled", out)
	}
	if out, _ := e.do("", false, "kill", "on"); !strings.Contains(out, "disabled") {
		t.Errorf("kill on = %q, want training disabled", out)
	}
	if out, _ := e.do("", false, "training", "on"); !strings.Contains(out, "enabled") {
		t.Errorf("training on = %q", out)
	}
	if _, err := e.do("", false, "kill", "maybe"); err == nil {
		t.Error("bad kill argument accepted")
	}
}

func TestPromoteFlowRequiresHumanApproval(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	if _, err := e.do("", false, "promote", "g1", "--to", "production"); err == nil {
		t.Fatal("promote without approval succeeded")
	}
	args := []string{"approve", "g1", "--by", "owner", "--key", filepath.Join(e.keys, "owner.key")}
	if _, err := e.do("anything\n", false, args...); err == nil || !strings.Contains(err.Error(), "human-run") {
		t.Errorf("approve without a terminal: %v", err)
	}
	if _, err := e.do("wrong\n", true, args...); err == nil {
		t.Error("approve with a wrong confirmation succeeded")
	}
	if _, err := e.do("", false, "promote", "g1", "--to", "production"); err == nil {
		t.Fatal("failed approvals still allowed promote")
	}

	prompt, _ := e.do("", true, args...)
	h := prompt[strings.Index(prompt, "hash ")+5:][:12]
	if _, err := e.do(h+"\n", true, args...); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := e.do("", false, "promote", "g1"); err == nil {
		t.Error("promote without --to production succeeded")
	}
	if _, err := e.do("", false, "promote", "g1", "--to", "production"); err != nil {
		t.Fatalf("promote after approval: %v", err)
	}
	out, _ := e.do("", false, "status")
	if !strings.Contains(out, "production: g1") {
		t.Errorf("status = %q", out)
	}
}

func TestApproveRefusesLooseKey(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	key := filepath.Join(e.keys, "owner.key")
	os.Chmod(key, 0o644)
	if _, err := e.do("x\n", true, "approve", "g1", "--by", "owner", "--key", key); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Errorf("loose key: %v", err)
	}
}

func TestLabSetRollbackCommands(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	e.profile("g2", "g1")
	if _, err := e.do("", false, "lab-set", "g2"); err != nil {
		t.Fatal(err)
	}
	out, _ := e.do("", false, "status")
	if !strings.Contains(out, "lab: g2 (chain g2 <- g1)") {
		t.Errorf("status = %q", out)
	}
	if _, err := e.do("", false, "rollback", "lab"); err != nil {
		t.Fatal(err)
	}
	out, _ = e.do("", false, "status", "--json")
	if !strings.Contains(out, `"live":"g1"`) {
		t.Errorf("json status = %q", out)
	}
}
