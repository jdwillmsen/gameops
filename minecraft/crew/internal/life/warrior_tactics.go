package life

import (
	"context"
	"sort"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Fight is one warrior tick as a family tactic sees it. It is a struct, not
// a parameter list, so a tactic that needs more can be given it without
// every other tactic's signature changing.
type Fight struct {
	// Life is the unit: its warrior state, its entities and the shared
	// fight helpers (strike, circle, raiseShield, retreat, rangerShoot).
	Life     *Life
	Actuator skill.Actuator
	Pose     act.Pose
	// Target is the squad's focus as of this tick, at its current position.
	Target   belief.Entity
	Snapshot belief.Snapshot
	// Params is the tuning resolved for Target's family.
	Params tactics.Params
	Family tactics.Family
}

// FamilyTactic fights one opponent family its own way. Engage owns the tick
// when it reports handled: it must then have sent exactly one input, as
// every fight tick does. Unhandled, it must have sent nothing, and the
// role's own fight runs. An error is the tick's error either way.
//
// A family may have many tactics; a unit's profile names the one it fights
// that family with, so each is a variant the trainer can compare.
type FamilyTactic interface {
	// Name identifies the variant in a profile, e.g. "creeper/backoff". It
	// is unique across the process.
	Name() string
	Family() tactics.Family
	Engage(ctx context.Context, f *Fight) (handled bool, err error)
}

// tacticRegistry is shared by every unit in the process. Tactics register
// from init, but a lab reload may register while units step, hence the
// lock.
var tacticRegistry = struct {
	sync.RWMutex
	by map[string]FamilyTactic
}{by: map[string]FamilyTactic{}}

// RegisterTactic makes t available to profiles under its name. Two tactics
// sharing a name would make a profile ambiguous, so that is a programming
// error caught at init rather than a choice between them.
func RegisterTactic(t FamilyTactic) {
	tacticRegistry.Lock()
	defer tacticRegistry.Unlock()
	if _, dup := tacticRegistry.by[t.Name()]; dup {
		panic("life: tactic " + t.Name() + " registered twice")
	}
	tacticRegistry.by[t.Name()] = t
}

// TacticNames lists the tactic variants registered for a family, sorted.
func TacticNames(f tactics.Family) []string {
	tacticRegistry.RLock()
	defer tacticRegistry.RUnlock()
	var names []string
	for n, t := range tacticRegistry.by {
		if t.Family() == f {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// tacticNamed is the registered tactic with that name, if it fights f. A
// profile naming another family's tactic must not run it against this one.
func tacticNamed(name string, f tactics.Family) (FamilyTactic, bool) {
	if name == "" {
		return nil, false
	}
	tacticRegistry.RLock()
	defer tacticRegistry.RUnlock()
	t, ok := tacticRegistry.by[name]
	if !ok || t.Family() != f {
		return nil, false
	}
	return t, true
}

// familyTactic offers the tick to the tactic the unit's profile names for
// e's family. No name, or one nothing is registered under, leaves the role's
// own fight to run: the default profile is the untouched warrior.
func (l *Life) familyTactic(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) (bool, error) {
	fam := familyOf(e)
	pr := l.Profile()
	t, ok := tacticNamed(pr.TacticFor(fam), fam)
	if !ok {
		return false, nil
	}
	handled, err := t.Engage(ctx, &Fight{Life: l, Actuator: a, Pose: p, Target: e, Snapshot: s, Params: l.warrior.p, Family: fam})
	if err != nil {
		l.noteTacticError(pr.TacticFor(fam), fam, err)
	}
	return handled, err
}

// tacticErrorEvery is the ticks (50 ms each) between two logs of one tactic's
// error: the task runner drops the error, so this line is the only trace.
const tacticErrorEvery = 20 * 60

func (l *Life) noteTacticError(name string, fam tactics.Family, err error) {
	if last, ok := l.tacticErrAt[name]; ok && l.tick-last < tacticErrorEvery {
		return
	}
	if l.tacticErrAt == nil {
		l.tacticErrAt = map[string]uint64{}
	}
	l.tacticErrAt[name] = l.tick
	l.logEvent("tactic_error", map[string]any{"tactic": name, "family": string(fam), "error": err.Error()})
}
