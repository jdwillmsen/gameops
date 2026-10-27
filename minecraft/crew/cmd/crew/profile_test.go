package main

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
)

// fakeProfileSource stands in for the squad client: it serves one profile or none and
// counts the asks.
type fakeProfileSource struct {
	mu    sync.Mutex
	pr    tactics.Profile
	has   bool
	asked int
}

func (f *fakeProfileSource) set(pr tactics.Profile, has bool) {
	f.mu.Lock()
	f.pr, f.has = pr, has
	f.mu.Unlock()
}

func (f *fakeProfileSource) FetchProfile(context.Context) (tactics.Profile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.pr, f.has
}

func (f *fakeProfileSource) asks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

type fakeProfileTarget struct{ got []tactics.Profile }

func (f *fakeProfileTarget) SetProfile(pr tactics.Profile) error {
	f.got = append(f.got, pr)
	return nil
}

func controlDir(t *testing.T, switchOn bool) string {
	t.Helper()
	dir := t.TempDir()
	if switchOn {
		if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func labConfig(dir string) config.Config {
	return config.Config{Role: "striker", Squad: "lab-alpha", TacticsControlDir: dir}
}

func candidate(name string, focus float64) tactics.Profile {
	pr := tactics.NewProfile(name)
	pr.Default.FocusRange = focus
	return pr
}

func TestAServedProfileReachesTheUnit(t *testing.T) {
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	src.set(candidate("champ-1", 13), true)
	ps := newProfileSync(labConfig(controlDir(t, true)), src, dst, nopLog{})
	ps.step(context.Background())
	if len(dst.got) != 1 || dst.got[0].Name != "champ-1" || dst.got[0].Default.FocusRange != 13 {
		t.Fatalf("SetProfile calls = %+v, want champ-1", dst.got)
	}
	// An unchanged answer costs no swap.
	ps.step(context.Background())
	if len(dst.got) != 1 {
		t.Errorf("SetProfile called %d times for one profile", len(dst.got))
	}
}

func TestTheKillSwitchPutsALabUnitBackOnTheDefault(t *testing.T) {
	dir := controlDir(t, true)
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	src.set(candidate("champ-1", 13), true)
	ps := newProfileSync(labConfig(dir), src, dst, nopLog{})
	ps.step(context.Background())

	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := src.asks()
	ps.step(context.Background())
	if len(dst.got) != 2 {
		t.Fatalf("SetProfile calls = %d, want the candidate and then the default", len(dst.got))
	}
	if got := dst.got[1]; got.Default != tactics.Defaults() || len(got.Overrides) != 0 {
		t.Errorf("after the kill switch the profile is %+v, want the shipped default", got)
	}
	if src.asks() != asked {
		t.Error("a unit whose training is off still asked the squad for a profile")
	}

	// Back on, the served profile returns.
	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps.step(context.Background())
	if len(dst.got) != 3 || dst.got[2].Name != "champ-1" {
		t.Errorf("after switching on again: %+v", dst.got)
	}
}

func TestAMissingKillSwitchFileIsOff(t *testing.T) {
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	src.set(candidate("champ-1", 13), true)
	ps := newProfileSync(labConfig(controlDir(t, false)), src, dst, nopLog{})
	ps.step(context.Background())
	if src.asks() != 0 || len(dst.got) != 1 || dst.got[0].Default != tactics.Defaults() {
		t.Errorf("asks=%d calls=%+v, want the default and no fetch", src.asks(), dst.got)
	}
}

func TestNoProfileMeansTheDefault(t *testing.T) {
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	src.set(candidate("champ-1", 13), true)
	ps := newProfileSync(labConfig(controlDir(t, true)), src, dst, nopLog{})
	ps.step(context.Background())

	// The client reports false for a 404 and for a failure past its stale
	// window alike.
	src.set(tactics.Profile{}, false)
	ps.step(context.Background())
	if len(dst.got) != 2 || dst.got[1].Default != tactics.Defaults() {
		t.Fatalf("calls = %+v, want a return to the default", dst.got)
	}
}

func TestOnlyALabUnitWithAControlDirSyncs(t *testing.T) {
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	src.set(candidate("champ-1", 13), true)
	dir := controlDir(t, true)
	for name, cfg := range map[string]config.Config{
		"production squad": {Role: "striker", Squad: "alpha", TacticsControlDir: dir},
		"prefix inside":    {Role: "striker", Squad: "alpha-lab-1", TacticsControlDir: dir},
		"flag unset":       {Role: "striker", Squad: "lab-alpha"},
		"no squad":         {Role: "striker", TacticsControlDir: dir},
	} {
		if ps := newProfileSync(cfg, src, dst, nopLog{}); ps != nil {
			t.Errorf("%s: got a profile sync", name)
		}
	}
	if newProfileSync(labConfig(dir), nil, dst, nopLog{}) != nil {
		t.Error("a unit with no squad client got a profile sync")
	}
	if src.asks() != 0 || len(dst.got) != 0 {
		t.Errorf("asks=%d calls=%d, want none", src.asks(), len(dst.got))
	}
}

// A new profile mid-fight swaps the tuning the running Life reads, with no
// restart.
func TestAHotUpdateReachesARunningLife(t *testing.T) {
	tracker := belief.New(time.Now)
	lf := life.New(life.Config{Defend: true}, blocks.New(noNames{}, 4), tracker.Snapshot, nopLog{}, rand.New(rand.NewSource(1)))
	src := &fakeProfileSource{}
	src.set(candidate("champ-1", 13), true)
	ps := newProfileSync(labConfig(controlDir(t, true)), src, lf, nopLog{})

	ps.step(context.Background())
	if got := lf.Profile(); got.Name != "champ-1" || got.Default.FocusRange != 13 {
		t.Fatalf("profile in force = %q focus %v, want champ-1", got.Name, got.Default.FocusRange)
	}
	src.set(candidate("champ-2", 15), true)
	ps.step(context.Background())
	if got := lf.Profile(); got.Name != "champ-2" || got.Default.FocusRange != 15 {
		t.Fatalf("after the update the profile is %q focus %v, want champ-2", got.Name, got.Default.FocusRange)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	src, dst := &fakeProfileSource{}, &fakeProfileTarget{}
	ps := newProfileSync(labConfig(controlDir(t, true)), src, dst, nopLog{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ps.run(ctx, time.Millisecond); close(done) }()
	waitFor(t, func() bool { return src.asks() >= 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run outlived its context")
	}
}
