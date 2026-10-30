package tacticsctl

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

type fixture struct {
	t     *testing.T
	s     *Store
	priv  ed25519.PrivateKey
	signs int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	asTrustOwner(t)
	root := t.TempDir()
	// A permissive umask would leave the temp root group-writable, which the
	// trust check rightly refuses above the trust directory.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(root, "state"), filepath.Join(root, "trust"))
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.TrustDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.TrustDir, "owner.pub"), []byte(hex.EncodeToString(pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, s: s, priv: priv}
}

// asTrustOwner makes the test's own account the one that may own a trust
// directory, because a test cannot chown anything to root.
func asTrustOwner(t *testing.T) {
	t.Helper()
	old := trustOwnerUID
	trustOwnerUID = uint32(os.Getuid())
	t.Cleanup(func() { trustOwnerUID = old })
}

func (f *fixture) candidate(name, parent string, shot float64) tactics.Profile {
	f.t.Helper()
	pr := tactics.NewProfile(name)
	pr.Parent = parent
	pr.Default.ShotEvery = shot
	pr.Evidence = "zombie/scorecards/" + name + ".jsonl"
	if err := f.s.AddCandidate(pr); err != nil {
		f.t.Fatal(err)
	}
	return pr
}

func (f *fixture) lab(name string) {
	f.t.Helper()
	if err := f.s.LabSet(name); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) approve(pr tactics.Profile) {
	f.t.Helper()
	if err := f.s.SaveApproval(f.sign(pr, "owner")); err != nil {
		f.t.Fatal(err)
	}
}

const farFuture = "2099-01-01T00:00:00Z"

// sign makes the approval a human would make now: for production as it stands,
// valid far into the future.
func (f *fixture) sign(pr tactics.Profile, by string) Approval {
	f.t.Helper()
	h, _ := Hash(pr)
	prev, err := f.s.ProductionHash()
	if err != nil {
		f.t.Fatal(err)
	}
	// A fresh approval differs from an earlier one in its timestamp, as a human's
	// does, so it is not mistaken for a spent signature.
	f.signs++
	ts := time.Date(2026, 10, 27, 0, 0, f.signs, 0, time.UTC).Format(time.RFC3339)
	return NewApproval(f.priv, pr.Name, h, by, ts, prev, farFuture)
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
	for _, ok := range []string{"crew-lab", "k8s:minecraft-lab/crew-lab-0", "CREW-LAB", "lab-alpha", "k8s:crew/crew-lab-0", "compose-crew-lab"} {
		if err := LabOnly(ok); err != nil {
			t.Errorf("LabOnly(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "fwb", "prd", "prod", "production", "k8s:minecraft/fwb-0", "k8s:minecraft-prd/bedrock-0",
		"fwb-lab", "lab-prod", "labrador", "bedrock", "k8s:lab/fwb-0", "k8s:lab/fwbmc-0", "lab", "LAB", "k8s:lab/anything-0",
		"crew-labx", "acrew-lab", "crewlab", "crew-lab-prod", "fwbmc-crew-lab", "lab-alpha/../fwb", "lab-", "lab_x"} {
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
	a := NewApproval(f.priv, "g1", sh, "owner", "t", NoProduction, farFuture)
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
	if err := f.s.SaveApproval(NewApproval(evil, "g1", h, "owner", "t", NoProduction, farFuture)); err == nil {
		t.Error("approval signed by a key that is not the trusted one was saved")
	}
	if err := f.s.SaveApproval(NewApproval(evil, "g1", h, "intruder", "t", NoProduction, farFuture)); err == nil {
		t.Error("approval from an unregistered approver was saved")
	}
	if err := f.s.Promote("g1"); err == nil {
		t.Error("promote passed with no valid approval on file")
	}

	// Even a forged file dropped straight into approvals/ is rejected.
	bad := NewApproval(evil, "g1", h, "owner", "t", NoProduction, farFuture)
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

	f.lab("g1")
	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatal(err)
	}
	f.lab("g3")
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
	if err := f.s.SaveApproval(NewApproval(f.priv, "something-else", h, "owner", "t", NoProduction, farFuture)); err != nil {
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
	same.Evidence = "zombie/scorecards/g1.jsonl"
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

// ready makes a profile fully promotable: lab-run and approved.
func (f *fixture) ready(name string) {
	f.t.Helper()
	f.lab(name)
	f.approve(f.mustCandidate(name))
}

func TestPromoteRefusesDefaultTrustDirectory(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	f.s = New(f.s.Root, "")
	pub, _ := os.ReadFile(filepath.Join(filepath.Dir(f.s.Root), "trust", "owner.pub"))
	os.MkdirAll(f.s.TrustDir, 0o700)
	os.WriteFile(filepath.Join(f.s.TrustDir, "owner.pub"), pub, 0o644)
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "explicit trust") {
		t.Fatalf("promote with the default trust dir = %v", err)
	}
	if _, ok, _ := f.s.Pointer(Production); ok {
		t.Error("refused promote wrote the production pointer")
	}
}

func TestPromoteRefusesTrustDirectoryUnderStore(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	inner := filepath.Join(f.s.Root, "keys", "trust")
	pub, _ := os.ReadFile(filepath.Join(f.s.TrustDir, "owner.pub"))
	os.MkdirAll(inner, 0o700)
	os.WriteFile(filepath.Join(inner, "owner.pub"), pub, 0o644)
	for name, dir := range map[string]string{
		"nested":      inner,
		"explicit":    filepath.Join(f.s.Root, "trusted"),
		"dotdot":      filepath.Join(f.s.Root, "keys", "..", "keys", "trust"),
		"via-symlink": mustSymlink(t, inner, filepath.Join(t.TempDir(), "link")),
	} {
		s := New(f.s.Root, dir)
		if err := s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "under the store") {
			t.Errorf("%s: promote with trust dir %s = %v", name, dir, err)
		}
	}
}

func mustSymlink(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestPromoteRefusesWritableTrustDirectory(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o720} {
		if err := os.Chmod(f.s.TrustDir, mode); err != nil {
			t.Fatal(err)
		}
		if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
			t.Errorf("trust dir mode %04o: promote = %v", mode, err)
		}
	}
	// Readable by others is fine: public keys are public.
	os.Chmod(f.s.TrustDir, 0o755)
	if err := f.s.Promote("g1"); err != nil {
		t.Errorf("0755 trust dir refused: %v", err)
	}
}

func TestPromoteRefusesWritableTrustedKey(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	if err := os.Chmod(filepath.Join(f.s.TrustDir, "owner.pub"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Errorf("promote with a world-writable key = %v", err)
	}
}

func TestPromoteRefusesProfileNeverRunInLab(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	f.approve(g1)
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "lab") {
		t.Fatalf("promote of a profile never lab-set = %v", err)
	}
	f.lab("g1")
	if err := f.s.Promote("g1"); err != nil {
		t.Fatalf("promote after lab-set: %v", err)
	}
}

func TestPromoteRefusesMissingOrBorrowedEvidence(t *testing.T) {
	f := newFixture(t)
	for name, evidence := range map[string]string{
		"none":     "",
		"absolute": "/etc/passwd",
		"dotdot":   "../zombie/scorecards/e.jsonl",
		"other":    "zombie/scorecards/someone-else.jsonl",
		"suffix":   "zombie/scorecards/x.txt",
		"flat":     "scorecards.jsonl",
	} {
		pr := tactics.NewProfile("e-" + name)
		pr.Evidence = evidence
		if err := f.s.AddCandidate(pr); err != nil {
			t.Fatal(err)
		}
		f.lab(pr.Name)
		f.approve(pr)
		if err := f.s.Promote(pr.Name); err == nil || !strings.Contains(err.Error(), "evidence") {
			t.Errorf("%s: promote with evidence %q = %v", name, evidence, err)
		}
	}
}

func TestNoApprovalWithoutThePrivateKey(t *testing.T) {
	f := newFixture(t)
	pr := f.candidate("g1", "", 35)
	f.lab("g1")
	h, _ := Hash(pr)

	// An agent can write files, but it cannot sign for a trusted approver.
	_, agentKey, _ := ed25519.GenerateKey(rand.Reader)
	forged := NewApproval(agentKey, "g1", h, "owner", "2026-10-27T00:00:00Z", NoProduction, farFuture)
	if err := f.s.SaveApproval(forged); err == nil {
		t.Error("SaveApproval accepted a signature from a key that is not the approver's")
	}
	unsigned := Approval{Profile: "g1", Hash: h, Approver: "owner", TS: "t"}
	if err := f.s.SaveApproval(unsigned); err == nil {
		t.Error("SaveApproval accepted an unsigned approval")
	}
	os.MkdirAll(filepath.Dir(f.s.approvalPath(h)), 0o755)
	os.WriteFile(f.s.approvalPath(h), []byte(mustJSON(forged)), 0o644)
	if err := f.s.Promote("g1"); err == nil {
		t.Error("a forged approval file promoted a profile")
	}
	if _, ok, _ := f.s.Pointer(Production); ok {
		t.Error("production pointer written without a valid approval")
	}
}

func TestPromoteRefusesTrustDirectoryNotOwnedByRoot(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	trustOwnerUID = 0
	if os.Getuid() == 0 {
		t.Skip("the test account is root, so its own directory is trusted")
	}
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "not owned by root") {
		t.Fatalf("promote with a trust dir owned by an agent account = %v", err)
	}
	if _, ok, _ := f.s.Pointer(Production); ok {
		t.Error("refused promote wrote the production pointer")
	}
}

func TestPromoteRefusesWritableParentOfTrustDirectory(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	parent := filepath.Join(filepath.Dir(f.s.Root), "shared")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(parent, "trust")
	if err := os.Rename(f.s.TrustDir, trust); err != nil {
		t.Fatal(err)
	}
	f.s.TrustDir = trust
	if err := f.s.Promote("g1"); err != nil {
		t.Fatalf("closed parent refused: %v", err)
	}
	if err := os.Chmod(parent, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "above the trust directory") {
		t.Errorf("promote with a group-writable parent = %v", err)
	}
}

func TestTrustChecksTheResolvedPathNotTheLink(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	link := mustSymlink(t, f.s.TrustDir, filepath.Join(t.TempDir(), "link"))
	if err := os.Chmod(f.s.TrustDir, 0o777); err != nil {
		t.Fatal(err)
	}
	s := New(f.s.Root, link)
	if err := s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Errorf("promote through a link to an open directory = %v", err)
	}
}

func TestTrustedKeyMustNotBeASymlink(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.ready("g1")
	real := filepath.Join(t.TempDir(), "owner.pub")
	pub, _ := os.ReadFile(filepath.Join(f.s.TrustDir, "owner.pub"))
	os.WriteFile(real, pub, 0o644)
	os.Remove(filepath.Join(f.s.TrustDir, "owner.pub"))
	mustSymlink(t, real, filepath.Join(f.s.TrustDir, "owner.pub"))
	if err := f.s.Promote("g1"); err == nil {
		t.Error("an approver key that is a symlink was trusted")
	}
}

func TestPointRestoresLiveWhenHistoryCannotBeWritten(t *testing.T) {
	f := newFixture(t)
	f.candidate("g1", "", 35)
	f.candidate("g2", "g1", 40)
	f.lab("g1")
	hist := filepath.Join(f.s.Root, "lab", historyFile)
	if err := os.Remove(hist); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(hist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.s.LabSet("g2"); err == nil {
		t.Fatal("lab-set succeeded although history could not be written")
	}
	if p, _, _ := f.s.Pointer(Lab); p.Name != "g1" {
		t.Errorf("lab pointer = %q after a failed history write, want g1", p.Name)
	}

	// First ever set: no pointer to restore, so none may remain.
	g := newFixture(t)
	g.candidate("g1", "", 35)
	if err := os.MkdirAll(filepath.Join(g.s.Root, "lab", historyFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := g.s.LabSet("g1"); err == nil {
		t.Fatal("first lab-set succeeded although history could not be written")
	}
	if _, ok, _ := g.s.Pointer(Lab); ok {
		t.Error("lab pointer left behind with no history entry")
	}
}

func TestApprovalIsSingleUse(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	f.lab("g1")
	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatal(err)
	}
	h, _ := Hash(g1)
	if _, err := os.Stat(f.s.approvalPath(h)); err == nil {
		t.Error("promote left its approval in place")
	}
	if used, _ := filepath.Glob(filepath.Join(f.s.Root, "approvals", "used", h+"-*.json")); len(used) != 1 {
		t.Errorf("spent approvals = %v, want one", used)
	}

	if err := f.s.Rollback(Production, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "no approval") {
		t.Fatalf("re-promote after rollback without a new approval = %v", err)
	}
	if p, _, _ := f.s.Pointer(Production); p.Name != DefaultName {
		t.Errorf("production = %q after a refused re-promote, want default", p.Name)
	}

	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Errorf("promote with a fresh approval: %v", err)
	}
}

func TestSpentApprovalCannotBeReplayedFromUsed(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	f.lab("g1")
	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatal(err)
	}
	f.s.Rollback(Production, "")
	h, _ := Hash(g1)
	used, _ := filepath.Glob(filepath.Join(f.s.Root, "approvals", "used", h+"-*.json"))
	if len(used) != 1 {
		t.Fatalf("spent approvals = %v", used)
	}
	// Only approvals/<hash>.json counts; the used directory is a record.
	if err := f.s.Promote("g1"); err == nil {
		t.Error("a spent approval still promoted")
	}
}

func TestPromoteChecksEvidenceFileWhenStoreIsKnown(t *testing.T) {
	f := newFixture(t)
	pr := f.candidate("g1", "", 35)
	f.lab("g1")
	f.approve(pr)

	store := t.TempDir()
	f.s.EvidenceStore = store
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("promote with the evidence file missing = %v", err)
	}
	ev := filepath.Join(store, "zombie", "scorecards", "g1.jsonl")
	os.MkdirAll(filepath.Dir(ev), 0o755)
	os.WriteFile(ev, nil, 0o644)
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("promote with an empty evidence file = %v", err)
	}
	os.Remove(ev)
	os.Symlink("/etc/hostname", ev)
	if err := f.s.Promote("g1"); err == nil {
		t.Fatal("promote with a symlinked evidence file")
	}
	os.Remove(ev)
	os.WriteFile(ev, []byte("{}\n"), 0o644)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatalf("promote with the evidence in place: %v", err)
	}
}

func TestPromoteWithoutEvidenceStoreOnlyChecksThePath(t *testing.T) {
	f := newFixture(t)
	pr := f.candidate("g1", "", 35)
	f.lab("g1")
	f.approve(pr)
	if err := f.s.Promote("g1"); err != nil {
		t.Errorf("fallback without a store: %v", err)
	}
}

func TestDefaultDirFollowsTheEnvironmentThenHome(t *testing.T) {
	t.Setenv("TACTICSCTL_DIR", "/srv/tactics")
	if got := DefaultDir(); got != "/srv/tactics" {
		t.Errorf("DefaultDir with env = %q", got)
	}
	t.Setenv("TACTICSCTL_DIR", "")
	t.Setenv("HOME", "/home/someone")
	if got := DefaultDir(); got != "/home/someone/.local/state/crew-tactics" {
		t.Errorf("DefaultDir without env = %q", got)
	}
}

func TestPromoteRefusesExpiredOrUndatedApproval(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	f.lab("g1")
	h, _ := Hash(g1)
	save := func(a Approval) {
		t.Helper()
		if err := f.s.SaveApproval(a); err != nil {
			t.Fatal(err)
		}
	}

	save(NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", NoProduction, "2026-10-27T01:00:00Z"))
	f.s.now = func() time.Time { return time.Date(2026, 10, 27, 1, 0, 0, 0, time.UTC) }
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("approval at its deadline = %v, want expired", err)
	}

	save(NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", NoProduction, ""))
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "expiry") {
		t.Errorf("approval with no expiry = %v", err)
	}
	save(NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", "", farFuture))
	if err := f.s.Promote("g1"); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Errorf("approval with no previous hash = %v", err)
	}
	if _, ok, _ := f.s.Pointer(Production); ok {
		t.Error("a refused approval still moved production")
	}

	f.s.now = func() time.Time { return time.Date(2026, 10, 27, 0, 59, 59, 0, time.UTC) }
	save(NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", NoProduction, "2026-10-27T01:00:00Z"))
	if err := f.s.Promote("g1"); err != nil {
		t.Errorf("approval one second before its deadline: %v", err)
	}
}

func TestApprovalIsBoundToTheProductionItReplaces(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	g2 := f.candidate("g2", "g1", 40)
	f.lab("g1")
	f.lab("g2")

	// Approved while production ran the default, then g1 went live first.
	f.approve(g2)
	f.approve(g1)
	if err := f.s.Promote("g1"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Promote("g2"); err == nil || !strings.Contains(err.Error(), "production ran none") {
		t.Fatalf("approval made for the default applied over g1 = %v", err)
	}

	f.approve(g2)
	if err := f.s.Promote("g2"); err != nil {
		t.Errorf("approval made for the production it replaces: %v", err)
	}
}

func TestSignedFieldsCannotBeEdited(t *testing.T) {
	f := newFixture(t)
	g1 := f.candidate("g1", "", 35)
	f.lab("g1")
	h, _ := Hash(g1)
	a := NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", NoProduction, "2000-01-01T00:00:00Z")
	a.Expires = farFuture
	os.MkdirAll(filepath.Dir(f.s.approvalPath(h)), 0o755)
	os.WriteFile(f.s.approvalPath(h), []byte(mustJSON(a)), 0o644)
	if err := f.s.Promote("g1"); err == nil {
		t.Error("an approval with an extended expiry promoted a profile")
	}
	a = NewApproval(f.priv, "g1", h, "owner", "2026-10-27T00:00:00Z", "deadbeef", farFuture)
	a.Prev = NoProduction
	os.WriteFile(f.s.approvalPath(h), []byte(mustJSON(a)), 0o644)
	if err := f.s.Promote("g1"); err == nil {
		t.Error("an approval with an edited previous hash promoted a profile")
	}
}

func TestUsedApprovalCopiedBackFailsAfterRollback(t *testing.T) {
	for _, tc := range []struct {
		name string
		to   string
	}{{"to default", "default"}, {"to the previous profile", "g1"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			g1 := f.candidate("g1", "", 35)
			g2 := f.candidate("g2", "g1", 40)
			f.lab("g2")
			f.lab("g1")
			f.approve(g1)
			if err := f.s.Promote("g1"); err != nil {
				t.Fatal(err)
			}
			f.approve(g2)
			h2, _ := Hash(g2)
			live, _ := os.ReadFile(f.s.approvalPath(h2))
			if err := f.s.Promote("g2"); err != nil {
				t.Fatal(err)
			}
			used, _ := filepath.Glob(filepath.Join(f.s.Root, "approvals", "used", h2+"-*.json"))
			if len(used) != 1 {
				t.Fatalf("spent approvals = %v", used)
			}
			if err := f.s.Rollback(Production, tc.to); err != nil {
				t.Fatal(err)
			}

			copied, _ := os.ReadFile(used[0])
			if !bytes.Equal(copied, live) {
				t.Fatal("the used file is not the approval that was spent")
			}
			os.WriteFile(f.s.approvalPath(h2), copied, 0o644)
			if err := f.s.Promote("g2"); err == nil {
				t.Error("a used approval copied back promoted again after a rollback")
			}
			if p, _, _ := f.s.Pointer(Production); p.Name != tc.to {
				t.Errorf("production = %q after the refused replay, want %q", p.Name, tc.to)
			}
		})
	}
}

func TestLabOnlyAcceptsTheKnownClusterLabAndNothingLikeIt(t *testing.T) {
	if err := LabOnly(KnownLab); err != nil {
		t.Fatalf("the real crew lab is refused: %v", err)
	}
	for _, bad := range []string{
		KnownLab + "x",
		"k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-1",
		"k8s:jdwillmsen-prd/jdwillmsen-fwb-prd-minecraft-bedrock-0",
		"K8S:JDWILLMSEN-PRD/JDWILLMSEN-CREW-LAB-PRD-MINECRAFT-BEDROCK-0",
		"k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-0/fwb",
	} {
		if err := LabOnly(bad); err == nil {
			t.Errorf("%q passed LabOnly", bad)
		}
	}
}
