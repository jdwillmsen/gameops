package tacticsctl

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

type fixture struct {
	t    *testing.T
	s    *Store
	priv ed25519.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	s := New(filepath.Join(root, "state"), filepath.Join(root, "trust"))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.TrustDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.TrustDir, "owner.pub"), []byte(hex.EncodeToString(pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, s: s, priv: priv}
}

func (f *fixture) candidate(name, parent string, shot float64) tactics.Profile {
	f.t.Helper()
	pr := tactics.NewProfile(name)
	pr.Parent = parent
	pr.Default.ShotEvery = shot
	if err := f.s.AddCandidate(pr); err != nil {
		f.t.Fatal(err)
	}
	return pr
}

func (f *fixture) approve(pr tactics.Profile) {
	f.t.Helper()
	h, _ := Hash(pr)
	if err := f.s.SaveApproval(NewApproval(f.priv, pr.Name, h, "owner", "2026-10-27T00:00:00Z")); err != nil {
		f.t.Fatal(err)
	}
}

func TestTrainingDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	if TrainingEnabled(dir) {
		t.Error("missing kill-switch file enabled training")
	}
	if TrainingEnabled(filepath.Join(dir, "nope")) {
		t.Error("missing directory enabled training")
	}
	s := New(dir, "")
	for _, garbage := range []string{"", "ON", "yes", "on off", "1"} {
		os.WriteFile(s.TrainingPath(), []byte(garbage), 0o644)
		if TrainingEnabled(dir) {
			t.Errorf("file %q enabled training", garbage)
		}
	}
	if err := s.SetTraining(true); err != nil || !TrainingEnabled(dir) {
		t.Fatalf("training on: enabled=%v err=%v", TrainingEnabled(dir), err)
	}
	if err := s.SetTraining(false); err != nil || TrainingEnabled(dir) {
		t.Fatalf("training off: enabled=%v err=%v", TrainingEnabled(dir), err)
	}
}

func TestKillSwitchSendsLabUnitsToDefault(t *testing.T) {
	f := newFixture(t)
	f.candidate("c1", "", 40)
	if err := f.s.LabSet("c1"); err != nil {
		t.Fatal(err)
	}
	if got := f.s.Effective(Lab).Default.ShotEvery; got != 30 {
		t.Errorf("training disabled but lab unit got shotEvery %v, want default 30", got)
	}
	f.s.SetTraining(true)
	if got := f.s.Effective(Lab).Default.ShotEvery; got != 40 {
		t.Errorf("training enabled, lab unit got shotEvery %v, want 40", got)
	}
}

func TestLabOnly(t *testing.T) {
	for _, ok := range []string{"lab", "crew-lab", "k8s:minecraft-lab/crew-lab-0", "LAB"} {
		if err := LabOnly(ok); err != nil {
			t.Errorf("LabOnly(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "fwb", "prd", "prod", "production", "k8s:minecraft/fwb-0", "k8s:minecraft-prd/bedrock-0",
		"fwb-lab", "lab-prod", "labrador", "bedrock", "k8s:lab/fwb-0"} {
		err := LabOnly(bad)
		if !errors.Is(err, ErrNotLab) {
			t.Errorf("LabOnly(%q) = %v, want ErrNotLab", bad, err)
		}
	}
}

func TestLabSetAndStatusChain(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.candidate("g3", "g2", 45)
	if err := f.s.LabSet("g3"); err != nil {
		t.Fatal(err)
	}
	st := f.s.Status()
	lab := st.Stages[0]
	if lab.Live != "g3" || strings.Join(lab.Chain, ">") != "g3>g2>g1" || lab.Error != "" {
		t.Errorf("lab status = %+v", lab)
	}
	if prod := st.Stages[1]; prod.Live != DefaultName {
		t.Errorf("production = %+v, want default", prod)
	}
	if err := f.s.LabSet("missing"); err == nil {
		t.Error("lab-set of an unknown candidate succeeded")
	}
}

func TestLabSetRefusesBrokenLineage(t *testing.T) {
	f := newFixture(t)
	f.candidate("orphan", "gone", 40)
	if err := f.s.LabSet("orphan"); err == nil {
		t.Error("profile with a missing ancestor was set")
	}
	if _, ok, _ := f.s.Pointer(Lab); ok {
		t.Error("failed lab-set moved the pointer")
	}
}

func TestRollbackChain(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.candidate("g3", "g2", 45)
	f.s.LabSet("g3")

	if err := f.s.Rollback(Lab, ""); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := f.s.Pointer(Lab); p.Name != "g2" {
		t.Errorf("default rollback went to %q, want parent g2", p.Name)
	}
	if got := f.s.Effective(Lab); got.Name != DefaultName {
		t.Errorf("effective with training off = %q", got.Name)
	}
	f.s.SetTraining(true)
	if got := f.s.Effective(Lab); got.Name != "g2" || got.Default.ShotEvery != 40 {
		t.Errorf("effective after rollback = %q %v", got.Name, got.Default.ShotEvery)
	}

	f.s.LabSet("g3")
	if err := f.s.Rollback(Lab, "g1"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := f.s.Pointer(Lab); p.Name != "g1" {
		t.Errorf("rollback --to g1 went to %q", p.Name)
	}
	if err := f.s.Rollback(Lab, "g3"); err == nil {
		t.Error("rollback to a descendant (not an ancestor) succeeded")
	}
	if err := f.s.Rollback(Lab, "stranger"); err == nil {
		t.Error("rollback to an unrelated name succeeded")
	}

	if err := f.s.Rollback(Lab, ""); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := f.s.Pointer(Lab); p.Name != DefaultName {
		t.Errorf("rolling back past the root left %q, want default", p.Name)
	}
	if err := f.s.Rollback(Lab, ""); err == nil {
		t.Error("rollback with nothing live succeeded")
	}
}

func TestRollbackToDefaultExplicit(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.s.LabSet("g2")
	if err := f.s.Rollback(Lab, DefaultName); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := f.s.Pointer(Lab); p.Name != DefaultName {
		t.Errorf("pointer = %q", p.Name)
	}
}

func TestPromoteRefusesWithoutApproval(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	if err := f.s.Promote("g1"); err == nil {
		t.Fatal("promote without approval succeeded")
	}
	if _, ok, _ := f.s.Pointer(Production); ok {
		t.Error("refused promote still wrote the production pointer")
	}
	if _, err := os.Stat(filepath.Join(f.s.Root, "production", "g1.json")); err == nil {
		t.Error("refused promote still copied the profile into production")
	}
}

func TestPromoteRefusesWrongHash(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	other := f.candidate("g2", "", 36)
	f.approve(other)

	if err := f.s.Promote("g1"); err == nil {
		t.Error("promote of g1 passed on an approval for g2")
	}

	// An approval of g1's name and a stale hash must not cover edited content.
	stale := tactics.NewProfile("g1")
	stale.Default.ShotEvery = 99
	sh, _ := Hash(stale)
	a := NewApproval(f.priv, "g1", sh, "owner", "t")
	b, _ := Hash(f.mustCandidate("g1"))
	os.MkdirAll(filepath.Join(f.s.Root, "approvals"), 0o755)
	data := strings.Replace(mustJSON(a), sh, b, 1)
	os.WriteFile(f.s.approvalPath(b), []byte(data), 0o644)
	if err := f.s.Promote("g1"); err == nil {
		t.Error("approval whose signature covers another hash was accepted")
	}
}

func (f *fixture) mustCandidate(name string) tactics.Profile {
	pr, err := f.s.Candidate(name)
	if err != nil {
		f.t.Fatal(err)
	}
	return pr
}

func TestPromoteRefusesUntrustedOrForgedApprover(t *testing.T) {
	f := newFixture(t)
	pr := f.candidate("g1", "", 35)
	h, _ := Hash(pr)

	_, evil, _ := ed25519.GenerateKey(rand.Reader)
	if err := f.s.SaveApproval(NewApproval(evil, "g1", h, "owner", "t")); err == nil {
		t.Error("approval signed by a key that is not the trusted one was saved")
	}
	if err := f.s.SaveApproval(NewApproval(evil, "g1", h, "intruder", "t")); err == nil {
		t.Error("approval from an unregistered approver was saved")
	}
	if err := f.s.Promote("g1"); err == nil {
		t.Error("promote passed with no valid approval on file")
	}

	// Even a forged file dropped straight into approvals/ is rejected.
	bad := NewApproval(evil, "g1", h, "owner", "t")
	os.MkdirAll(filepath.Dir(f.s.approvalPath(h)), 0o755)
	os.WriteFile(f.s.approvalPath(h), []byte(mustJSON(bad)), 0o644)
	if err := f.s.Promote("g1"); err == nil {
		t.Error("forged approval file promoted a profile")
	}
}

func TestPromoteWithApprovalAndProductionRollback(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	g2 := f.candidate("g2", "g1", 40)
	g3 := f.candidate("g3", "g2", 45)

	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatal(err)
	}
	f.approve(g3)
	if err := f.s.Promote("g3"); err != nil {
		t.Fatal(err)
	}
	prod, err := f.s.Live(Production)
	if err != nil || prod.Name != "g3" {
		t.Fatalf("production = %q, %v", prod.Name, err)
	}
	if got := f.s.Effective(Production).Name; got != "g3" {
		t.Errorf("effective production = %q (kill switch must not touch production)", got)
	}
	if c := f.s.Status().Stages[1]; strings.Join(c.Chain, ">") != "g3>g2>g1" {
		t.Errorf("production chain = %v", c.Chain)
	}

	// g2 is g3's parent but never ran in production, so it was never approved.
	if err := f.s.Rollback(Production, ""); err == nil {
		t.Error("production rolled back to a profile it never ran")
	}
	if err := f.s.Rollback(Production, "g1"); err != nil {
		t.Errorf("rollback to previously promoted g1: %v", err)
	}
	if p, _, _ := f.s.Pointer(Production); p.Name != "g1" {
		t.Errorf("production = %q", p.Name)
	}
	_ = g2
}

func TestPromoteRequiresApprovalNamingTheProfile(t *testing.T) {
	f := newFixture(t)
	pr := f.candidate("g1", "", 35)
	h, _ := Hash(pr)
	if err := f.s.SaveApproval(NewApproval(f.priv, "something-else", h, "owner", "t")); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Promote("g1"); err == nil {
		t.Error("approval naming another profile was accepted")
	}
}

func TestOnlyPromoteAndRollbackWriteProduction(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.s.LabSet("g2")
	f.s.Rollback(Lab, "")
	f.s.SetTraining(true)
	if _, err := os.Stat(filepath.Join(f.s.Root, string(Production))); err == nil {
		t.Error("lab-set, rollback, kill switch or candidate filing created production state")
	}
	if err := f.s.Rollback(Production, ""); err == nil {
		t.Error("rollback on an untouched production succeeded")
	}
}

func TestImmutableNames(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	changed := tactics.NewProfile("g1")
	changed.Default.ShotEvery = 50
	if err := f.s.AddCandidate(changed); err == nil {
		t.Error("a different profile replaced candidate g1")
	}
	same := tactics.NewProfile("g1")
	same.Default.ShotEvery = 35
	if err := f.s.AddCandidate(same); err != nil {
		t.Errorf("re-filing identical g1: %v", err)
	}
	for _, bad := range []string{"", "default", "../x", "a/b", ".hidden", "LIVE x"} {
		if err := f.s.AddCandidate(tactics.NewProfile(bad)); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

func TestTamperedLiveProfileFallsBackToDefault(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.s.LabSet("g1")
	f.s.SetTraining(true)
	path := filepath.Join(f.s.Root, "lab", "g1.json")
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), "35", "36", 1)), 0o644)
	if got := f.s.Effective(Lab); got.Name != DefaultName {
		t.Errorf("tampered profile served as %q", got.Name)
	}
	if st := f.s.Status().Stages[0]; st.Error == "" {
		t.Error("status did not flag the tampered profile")
	}
}

func TestWriteAtomicInterruptedKeepsOldContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LIVE")
	if err := writeAtomic(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	defer func() { renameFile = orig }()
	renameFile = func(string, string) error { return errors.New("power cut") }
	if err := writeAtomic(path, []byte("new content that is longer"), 0o644); err == nil {
		t.Fatal("write reported success though the rename failed")
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Errorf("file = %q after interrupted write, want old", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("interrupted write left %d files, want only LIVE", len(ents))
	}
}

func TestInterruptedPointerWriteLeavesStageIntact(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.s.LabSet("g1")

	orig := renameFile
	defer func() { renameFile = orig }()
	renameFile = func(oldpath, newpath string) error {
		if filepath.Base(newpath) == livePointer {
			return errors.New("power cut")
		}
		return orig(oldpath, newpath)
	}
	if err := f.s.LabSet("g2"); err == nil {
		t.Fatal("lab-set reported success though the pointer write failed")
	}
	renameFile = orig
	if p, _, _ := f.s.Pointer(Lab); p.Name != "g1" {
		t.Errorf("pointer = %q after interrupted lab-set, want g1", p.Name)
	}
	if _, err := f.s.Live(Lab); err != nil {
		t.Errorf("stage unreadable after interrupted write: %v", err)
	}
}

func TestStrayTempFilesAndTornHistoryAreHarmless(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.s.LabSet("g1")
	lab := filepath.Join(f.s.Root, "lab")
	os.WriteFile(filepath.Join(lab, ".tmp-123"), []byte(`{"name":"trunc`), 0o644)
	hist, _ := os.OpenFile(filepath.Join(lab, historyFile), os.O_APPEND|os.O_WRONLY, 0o644)
	hist.WriteString(`{"ts":"2026-`)
	hist.Close()
	if names, _ := f.s.Candidates(); len(names) != 1 {
		t.Errorf("candidates = %v", names)
	}
	if pr, err := f.s.Live(Lab); err != nil || pr.Name != "g1" {
		t.Errorf("live = %q, %v", pr.Name, err)
	}
}

func mustJSON(a Approval) string {
	return `{"profile":"` + a.Profile + `","hash":"` + a.Hash + `","approver":"` + a.Approver + `","ts":"` + a.TS + `","sig":"` + a.Sig + `"}`
}
