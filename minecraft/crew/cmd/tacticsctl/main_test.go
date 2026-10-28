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
	e.do("", true, "keygen", "--name", "owner", "--out", e.keys)
	if err := os.MkdirAll(e.trust, 0o700); err != nil {
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
	pr.Evidence = "zombie/scorecards/" + name + ".jsonl"
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
	if out, _ := e.do("", true, "kill", "off"); !strings.Contains(out, "enabled") {
		t.Errorf("kill off = %q, want training enabled", out)
	}
	if out, _ := e.do("", false, "kill", "on"); !strings.Contains(out, "disabled") {
		t.Errorf("kill on = %q, want training disabled", out)
	}
	if out, _ := e.do("", true, "training", "on"); !strings.Contains(out, "enabled") {
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
	if _, err := e.do(prompt[strings.Index(prompt, "hash ")+5:][:12]+"\n", true, args...); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := e.do("", false, "promote", "g1", "--to", "production"); err == nil {
		t.Fatal("promote before lab-set succeeded")
	} else if os.Getuid() == 0 && !strings.Contains(err.Error(), "lab") {
		t.Fatalf("promote before lab-set: %v", err)
	}
	if _, err := e.do("", false, "lab-set", "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.do("", false, "promote", "g1"); err == nil {
		t.Error("promote without --to production succeeded")
	}
	_, err := e.do("", false, "promote", "g1", "--to", "production")
	if os.Getuid() != 0 {
		// A test cannot chown its trust directory to root, so the refusal
		// is the most the real binary can show here.
		if err == nil || !strings.Contains(err.Error(), "not owned by root") {
			t.Fatalf("promote with a trust dir the agent account owns = %v", err)
		}
		return
	}
	if err != nil {
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

func TestPromoteNeedsExplicitTrustDirectory(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	e.do("", false, "lab-set", "g1")
	prompt, _ := e.do("", true, "approve", "g1", "--by", "owner", "--key", filepath.Join(e.keys, "owner.key"))
	h := prompt[strings.Index(prompt, "hash ")+5:][:12]
	if _, err := e.do(h+"\n", true, "approve", "g1", "--by", "owner", "--key", filepath.Join(e.keys, "owner.key")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TACTICSCTL_TRUST", "")
	var out bytes.Buffer
	err := run([]string{"--dir", e.dir, "promote", "g1", "--to", "production"}, strings.NewReader(""), &out, false)
	if err == nil || !strings.Contains(err.Error(), "--trust") {
		t.Fatalf("promote without a trust directory = %v", err)
	}

	t.Setenv("TACTICSCTL_TRUST", e.trust)
	err = run([]string{"--dir", e.dir, "promote", "g1", "--to", "production"}, strings.NewReader(""), &out, false)
	if os.Getuid() != 0 && (err == nil || strings.Contains(err.Error(), "--trust")) {
		t.Fatalf("promote with TACTICSCTL_TRUST should reach the ownership check, got %v", err)
	}
}

func TestPromoteRefusesOpenTrustDirectory(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	e.do("", false, "lab-set", "g1")
	os.Chmod(e.trust, 0o777)
	if _, err := e.do("", false, "promote", "g1", "--to", "production"); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Errorf("promote with a world-writable trust dir = %v", err)
	}
}

func TestHumanOnlyCommandsNeedATerminal(t *testing.T) {
	e := newEnv(t)
	for name, args := range map[string][]string{
		"training on": {"training", "on"},
		"kill off":    {"kill", "off"},
		"keygen":      {"keygen", "--name", "other", "--out", filepath.Join(t.TempDir(), "k")},
	} {
		if _, err := e.do("", false, args...); err == nil || !strings.Contains(err.Error(), "human-run") {
			t.Errorf("%s without a terminal = %v", name, err)
		}
	}
	if trainingOn(t, e) {
		t.Error("a refused command still enabled training")
	}
	// Turning training off never needs a human.
	e.do("", true, "training", "on")
	for _, args := range [][]string{{"training", "off"}, {"kill", "on"}} {
		e.do("", true, "training", "on")
		if out, err := e.do("", false, args...); err != nil || !strings.Contains(out, "disabled") {
			t.Errorf("%v without a terminal = %q, %v", args, out, err)
		}
	}
}

func trainingOn(t *testing.T, e *env) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, "training"))
	return err == nil && strings.TrimSpace(string(b)) == "on"
}

func TestKeygenWarnsToGenerateOffTheDevbox(t *testing.T) {
	e := newEnv(t)
	out, err := e.do("", true, "keygen", "--name", "second", "--out", e.keys)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "warning: generate approver keys off the devbox") {
		t.Errorf("keygen output lacks the off-devbox warning: %q", out)
	}
	if _, err := e.do("", true, "keygen", "--name", "third"); err == nil {
		t.Error("keygen without --out picked a default path")
	}
}

func TestKeygenRefusesToOverwriteAndBadNames(t *testing.T) {
	e := newEnv(t)
	before, _ := os.ReadFile(filepath.Join(e.keys, "owner.key"))
	if _, err := e.do("", true, "keygen", "--name", "owner", "--out", e.keys); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second keygen = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(e.keys, "owner.key"))
	if !bytes.Equal(before, after) {
		t.Error("keygen overwrote an existing private key")
	}
	for _, name := range []string{"../evil", "Owner", "a b", "x.y", "a/b", "under_score"} {
		if _, err := e.do("", true, "keygen", "--name", name, "--out", e.keys); err == nil {
			t.Errorf("keygen accepted name %q", name)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.keys), "evil.key")); err == nil {
		t.Error("a traversing name wrote outside --out")
	}
}

func TestKeygenDoesNotFollowASymlinkedKeyPath(t *testing.T) {
	e := newEnv(t)
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("precious"), 0o600)
	os.Symlink(victim, filepath.Join(e.keys, "link.key"))
	if _, err := e.do("", true, "keygen", "--name", "link", "--out", e.keys); err == nil {
		t.Error("keygen wrote through a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Errorf("symlink target overwritten: %q", b)
	}
	if _, err := os.Stat(filepath.Join(e.keys, "link.pub")); err == nil {
		t.Error("a half-made key pair was left behind")
	}
}

func TestApproveRefusesSymlinkedKey(t *testing.T) {
	e := newEnv(t)
	e.profile("g1", "")
	link := filepath.Join(t.TempDir(), "key")
	os.Symlink(filepath.Join(e.keys, "owner.key"), link)
	if _, err := e.do("x\n", true, "approve", "g1", "--by", "owner", "--key", link); err == nil {
		t.Error("approve read its private key through a symlink")
	}
}
