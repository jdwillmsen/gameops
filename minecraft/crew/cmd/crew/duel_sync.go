package main

import (
	"context"
	"slices"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

const (
	// duelPoll is how often the unit re-reads the kill switch and asks the
	// client for the squad's duel. The client wakes on the squad's poll, so
	// a go or an over lands well inside this.
	duelPoll = time.Second
	// duelLease is how long one answer keeps a go alive. A stalled sync
	// goroutine, which would otherwise freeze the last go in place, runs
	// the lease out and the unit stops hunting.
	duelLease = 5 * time.Second
)

type duelSource interface {
	FetchDuel(ctx context.Context) (squadclient.Duel, bool)
}

type pvpTarget interface {
	SetPvPPolicy(life.PvPPolicy)
}

// duelView is one answer's worth of who this unit may hunt.
type duelView struct {
	enemies map[string]bool
	out     map[string]bool
	until   time.Time
}

// squadPvP is the engine's rule for fighting players, fed by the squad. It
// is closed by default and by every doubt: no view, a lapsed lease, a
// player that is not an enemy and one that is out all answer false.
type squadPvP struct {
	view atomic.Pointer[duelView]
	now  func() time.Time
}

func (p *squadPvP) MayTarget(player string) bool {
	v := p.view.Load()
	return v != nil && p.now().Before(v.until) && v.enemies[player] && !v.out[player]
}

func nameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// duelSync keeps one lab warrior's player-fighting rule in step with its
// squad's duel. It is built only for a warrior on a lab squad with a control
// directory, so every other unit never reaches the network for a duel and
// never gets a policy.
type duelSync struct {
	src        duelSource
	dst        pvpTarget
	self       string
	controlDir string
	log        squadclient.Logger

	pol       *squadPvP
	installed bool
	// applied is the state and round last logged, so an unchanged answer
	// costs no log line.
	applied string
}

func newDuelSync(cfg config.Config, src duelSource, dst pvpTarget, log squadclient.Logger) *duelSync {
	if src == nil || dst == nil || !cfg.Role.IsWarrior() || cfg.Username == "" ||
		cfg.TacticsControlDir == "" || !labSquad(cfg.Squad) {
		return nil
	}
	return &duelSync{src: src, dst: dst, self: cfg.Username, controlDir: cfg.TacticsControlDir, log: log,
		pol: &squadPvP{now: time.Now}}
}

func (d *duelSync) run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		d.step(ctx)
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

// step applies what the unit may do now. The kill switch comes first and is
// read from disk each time, so turning training off ends a duel within one
// poll and without asking the server. Anything but a go in which this unit
// is a named fighter leaves the rule closed; an idle or finished duel
// removes it, and a duel that cannot be read keeps it closed rather than
// guessing.
func (d *duelSync) step(ctx context.Context) {
	if !tacticsctl.TrainingEnabled(d.controlDir) {
		d.remove("training_off")
		return
	}
	duel, ok := d.src.FetchDuel(ctx)
	switch {
	case !ok:
		d.pol.view.Store(nil)
		d.note("unavailable", "")
	case duel.State == squadclient.DuelIdle || duel.State == squadclient.DuelOver:
		d.remove(duel.State)
	case duel.State == squadclient.DuelArmed || duel.State == squadclient.DuelGo:
		d.install()
		if duel.State == squadclient.DuelGo && slices.Contains(duel.Fighters, d.self) {
			d.pol.view.Store(&duelView{enemies: nameSet(duel.Enemies), out: nameSet(duel.Eliminated),
				until: d.pol.now().Add(duelLease)})
		} else {
			d.pol.view.Store(nil)
		}
		d.note(duel.State, duel.Round)
	default:
		d.pol.view.Store(nil)
		d.note("unknown", duel.Round)
	}
}

func (d *duelSync) install() {
	if !d.installed {
		d.dst.SetPvPPolicy(d.pol)
		d.installed = true
	}
}

func (d *duelSync) remove(why string) {
	d.pol.view.Store(nil)
	if d.installed {
		d.dst.SetPvPPolicy(nil)
		d.installed = false
	}
	d.note(why, "")
}

func (d *duelSync) note(state, round string) {
	key := state + "/" + round
	if key == d.applied {
		return
	}
	d.applied = key
	d.log.Info("squad_duel", map[string]any{"state": state, "round": round})
}
