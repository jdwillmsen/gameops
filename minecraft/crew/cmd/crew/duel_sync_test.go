package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

type fakeDuelSource struct {
	mu    sync.Mutex
	d     squadclient.Duel
	has   bool
	asked int
}

func (f *fakeDuelSource) set(d squadclient.Duel, has bool) {
	f.mu.Lock()
	f.d, f.has = d, has
	f.mu.Unlock()
}

func (f *fakeDuelSource) FetchDuel(context.Context) (squadclient.Duel, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.d, f.has
}

func (f *fakeDuelSource) asks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

type fakePvPTarget struct {
	pol   life.PvPPolicy
	calls int
}

func (f *fakePvPTarget) SetPvPPolicy(p life.PvPPolicy) { f.pol, f.calls = p, f.calls+1 }

func duelConfig(dir string) config.Config {
	return config.Config{Role: "striker", Squad: "lab-red", Username: "Rusty", TacticsControlDir: dir}
}

func goDuel(eliminated ...string) squadclient.Duel {
	return squadclient.Duel{State: squadclient.DuelGo, Round: "r1", Fighters: []string{"Rusty"},
		Enemies: []string{"Tinder", "Agate"}, Eliminated: eliminated, Crew: []string{"Rusty", "Tinder", "Agate"}}
}

func mayTarget(t *testing.T, dst *fakePvPTarget, player string) bool {
	t.Helper()
	return dst.pol != nil && dst.pol.MayTarget(player)
}

func TestOnlyALabWarriorWithTheSwitchDirGetsADuelSync(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	dir := controlDir(t, true)
	for name, cfg := range map[string]config.Config{
		"production squad": {Role: "striker", Squad: "alpha", Username: "Rusty", TacticsControlDir: dir},
		"prefix inside":    {Role: "striker", Squad: "alpha-lab-1", Username: "Rusty", TacticsControlDir: dir},
		"flag unset":       {Role: "striker", Squad: "lab-red", Username: "Rusty"},
		"no squad":         {Role: "striker", Username: "Rusty", TacticsControlDir: dir},
		"not a warrior":    {Role: "", Squad: "lab-red", Username: "Rusty", TacticsControlDir: dir},
		"no player name":   {Role: "striker", Squad: "lab-red", TacticsControlDir: dir},
	} {
		if ds := newDuelSync(cfg, src, dst, nopLog{}); ds != nil {
			t.Errorf("%s: got a duel sync", name)
		}
	}
	if newDuelSync(duelConfig(dir), nil, dst, nopLog{}) != nil {
		t.Error("a unit with no squad client got a duel sync")
	}
	if src.asks() != 0 || dst.calls != 0 {
		t.Errorf("asks=%d installs=%d, want none", src.asks(), dst.calls)
	}
}

func TestAGoNamingTheUnitOpensFireOnlyOnItsEnemies(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})

	src.set(squadclient.Duel{State: squadclient.DuelArmed, Fighters: []string{"Rusty"}, Enemies: []string{"Tinder"}}, true)
	ds.step(context.Background())
	if dst.pol == nil || mayTarget(t, dst, "Tinder") {
		t.Fatal("armed: want a policy that holds fire")
	}

	src.set(goDuel("Agate"), true)
	ds.step(context.Background())
	for player, want := range map[string]bool{"Tinder": true, "Agate": false, "Rusty": false, "Steve": false, "": false} {
		if got := mayTarget(t, dst, player); got != want {
			t.Errorf("go: MayTarget(%q) = %v, want %v", player, got, want)
		}
	}
	if dst.calls != 1 {
		t.Errorf("the policy was installed %d times, want once", dst.calls)
	}

	// An elimination reaches the unit with the next answer.
	src.set(goDuel("Agate", "Tinder"), true)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("an eliminated player is still a target")
	}
}

// A go that names other fighters is not this unit's fight.
func TestAGoThatDoesNotNameTheUnitLeavesItHoldingFire(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})
	d := goDuel()
	d.Fighters = []string{"Slate"}
	src.set(d, true)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("a unit outside the roster hunts the enemy")
	}
}

func TestIdleOrOverRemovesThePolicy(t *testing.T) {
	for _, state := range []string{squadclient.DuelIdle, squadclient.DuelOver} {
		src, dst := &fakeDuelSource{}, &fakePvPTarget{}
		ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})
		src.set(goDuel(), true)
		ds.step(context.Background())
		src.set(squadclient.Duel{State: state, Round: "r1"}, true)
		ds.step(context.Background())
		if dst.pol != nil {
			t.Errorf("%s: the policy is still installed", state)
		}
		ds.step(context.Background())
		if dst.calls != 2 {
			t.Errorf("%s: policy calls = %d, want install and removal only", state, dst.calls)
		}
	}
}

// Anything short of a readable go closes the rule: a lost squad, a state the
// unit does not know, and a lease that ran out because the sync stalled.
func TestAnythingButAReadableGoFailsClosed(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})
	src.set(goDuel(), true)
	ds.step(context.Background())
	if !mayTarget(t, dst, "Tinder") {
		t.Fatal("go did not open fire")
	}

	src.set(squadclient.Duel{}, false)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("a fetch failure left the unit hunting")
	}

	src.set(goDuel(), true)
	ds.step(context.Background())
	src.set(squadclient.Duel{State: "fight", Fighters: []string{"Rusty"}, Enemies: []string{"Tinder"}}, true)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("an unknown state left the unit hunting")
	}

	src.set(goDuel(), true)
	ds.step(context.Background())
	now := time.Now()
	ds.pol.now = func() time.Time { return now.Add(duelLease + time.Second) }
	if mayTarget(t, dst, "Tinder") {
		t.Error("a go whose lease ran out still hunts")
	}
}

func TestTheKillSwitchEndsADuelWithoutAskingTheSquad(t *testing.T) {
	dir := controlDir(t, true)
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(dir), src, dst, nopLog{})
	src.set(goDuel(), true)
	ds.step(context.Background())
	if !mayTarget(t, dst, "Tinder") {
		t.Fatal("go did not open fire")
	}

	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := src.asks()
	ds.step(context.Background())
	if dst.pol != nil {
		t.Error("training is off but the policy stands")
	}
	if src.asks() != asked {
		t.Error("a unit whose training is off still asked the squad for a duel")
	}

	// Switched back on, a still-running duel is heard again.
	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ds.step(context.Background())
	if !mayTarget(t, dst, "Tinder") {
		t.Error("the duel was not heard again after the switch came back")
	}
}

func TestAMissingSwitchFileMeansNoDuel(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, false)), src, dst, nopLog{})
	src.set(goDuel(), true)
	ds.step(context.Background())
	if src.asks() != 0 || dst.pol != nil {
		t.Errorf("asks=%d pol=%v, want no fetch and no policy", src.asks(), dst.pol)
	}
}

func TestDuelSyncRunStopsWithItsContext(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ds.run(ctx, time.Millisecond); close(done) }()
	waitFor(t, func() bool { return src.asks() >= 2 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop with its context")
	}
}

// The squad service vouches for the crew names; an enemy it does not vouch for
// is never hunted, however the duel came to name it. Closed by default: a
// server that sends no crew list makes every enemy a stranger.
func TestOnlyCrewUnitsMayBeTargetedWhateverTheDuelNames(t *testing.T) {
	src, dst := &fakeDuelSource{}, &fakePvPTarget{}
	ds := newDuelSync(duelConfig(controlDir(t, true)), src, dst, nopLog{})
	duel := goDuel()
	duel.Enemies = []string{"Tinder", "Steve99"}
	src.set(duel, true)
	ds.step(context.Background())
	if !mayTarget(t, dst, "Tinder") {
		t.Error("a crew enemy was refused")
	}
	if mayTarget(t, dst, "Steve99") {
		t.Error("a human named as an enemy may be targeted")
	}

	duel.Crew = nil
	src.set(duel, true)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("with no crew list from the squad, an enemy may still be targeted")
	}

	duel.Crew = []string{"Rusty", "Tinder"}
	duel.Eliminated = []string{"Tinder"}
	src.set(duel, true)
	ds.step(context.Background())
	if mayTarget(t, dst, "Tinder") {
		t.Error("an eliminated crew enemy may still be targeted")
	}
}
