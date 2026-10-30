package main

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

// profilePoll is how often the unit re-reads the kill switch and asks the
// client for the squad's profile. The client caches its fetch, so this bounds
// how late a switch or a new profile takes hold, not the server's load.
const profilePoll = 5 * time.Second

// labSquadPrefix mirrors the squad service's mark of a lab squad; the id is
// the only mark that survives a restart of the in-memory squad store.
const labSquadPrefix = "lab-"

func labSquad(id string) bool { return strings.HasPrefix(id, labSquadPrefix) }

type profileSource interface {
	FetchProfile(ctx context.Context) (tactics.Profile, bool)
}

type profileTarget interface {
	SetProfile(tactics.Profile) error
}

// profileSync keeps one lab unit's tuning in step with its squad's profile.
// It is built only for a lab squad with a control directory, so every other
// unit never reaches the network for a profile.
type profileSync struct {
	src        profileSource
	dst        profileTarget
	controlDir string
	log        squadclient.Logger

	// state, when set, is told what the sync did so the status API can report
	// it. The status server starts before the sync exists, hence the hand-off.
	state *tacticsState

	// applied is the key of the profile last handed to dst, so an unchanged
	// answer costs no swap and no log line.
	applied string
}

// newProfileSync returns nil unless the unit is a lab warrior with a control
// directory configured: unset leaves the shipped behaviour untouched.
func newProfileSync(cfg config.Config, src profileSource, dst profileTarget, log squadclient.Logger) *profileSync {
	if src == nil || cfg.TacticsControlDir == "" || !labSquad(cfg.Squad) {
		return nil
	}
	return &profileSync{src: src, dst: dst, controlDir: cfg.TacticsControlDir, log: log}
}

func (p *profileSync) run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.step(ctx)
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

// step applies what the unit should fight with now. The kill switch comes
// first and is read from disk each time, so turning training off ends a
// candidate's hold within one poll and without asking the server. A missing
// profile — the squad serves none, or the client's stale window ran out —
// is the shipped behaviour.
func (p *profileSync) step(ctx context.Context) {
	want := tactics.NewProfile(tacticsctl.DefaultName)
	key := tacticsctl.DefaultName
	training := tacticsctl.TrainingEnabled(p.controlDir)
	p.state.noteTraining(training)
	if training {
		if pr, ok := p.src.FetchProfile(ctx); ok {
			h, err := tacticsctl.Hash(pr)
			if err != nil {
				// A profile the contract refuses leaves the one in force.
				p.log.Error("squad_profile_refused", map[string]any{"error": err.Error()})
				return
			}
			want, key = pr, h
		}
	}
	if key == p.applied {
		return
	}
	if err := p.dst.SetProfile(want); err != nil {
		p.log.Error("squad_profile_refused", map[string]any{"error": err.Error()})
		return
	}
	p.applied = key
	p.state.noteApplied(time.Now())
	p.log.Info("squad_profile_applied", map[string]any{"profile": want.Name})
}

// profileReader is the unit's tuning in force.
type profileReader interface {
	Profile() tactics.Profile
}

// tacticsState reports what the unit fights with. The profile and its hash
// come from the fighting side, so they are what is running and not what was
// last fetched; the sync adds when it swapped the profile in and the kill
// switch it last read. The zero sync parts stay unreported.
type tacticsState struct {
	src       profileReader
	appliedAt atomic.Pointer[time.Time]
	training  atomic.Pointer[bool]
}

func newTacticsState(src profileReader) *tacticsState { return &tacticsState{src: src} }

// noteApplied and noteTraining accept a nil receiver so a unit with no sync
// calls them without a check.
func (t *tacticsState) noteApplied(at time.Time) {
	if t != nil {
		t.appliedAt.Store(&at)
	}
}

func (t *tacticsState) noteTraining(on bool) {
	if t != nil {
		t.training.Store(&on)
	}
}

func (t *tacticsState) Tactics() statusapi.Tactics {
	pr := t.src.Profile()
	// A profile the engine accepted always hashes; an empty hash only ever
	// reads as "does not match" to the trainer.
	h, _ := tactics.Hash(pr)
	return statusapi.Tactics{Profile: pr.Name, Hash: h, AppliedAt: t.appliedAt.Load(), TrainingEnabled: t.training.Load()}
}
