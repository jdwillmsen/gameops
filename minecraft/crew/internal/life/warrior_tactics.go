package life

import (
	"context"
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
type FamilyTactic interface {
	Family() tactics.Family
	Engage(ctx context.Context, f *Fight) (handled bool, err error)
}

// tacticRegistry is shared by every unit in the process. Tactics register
// from init, but a lab reload may register while units step, hence the
// lock.
var tacticRegistry = struct {
	sync.RWMutex
	by map[tactics.Family]FamilyTactic
}{by: map[tactics.Family]FamilyTactic{}}

// RegisterTactic makes t the tactic for its family, replacing any before
// it: one family has one tactic in force.
func RegisterTactic(t FamilyTactic) {
	tacticRegistry.Lock()
	defer tacticRegistry.Unlock()
	tacticRegistry.by[t.Family()] = t
}

// tacticFor is the tactic in force for a family.
func tacticFor(f tactics.Family) (FamilyTactic, bool) {
	tacticRegistry.RLock()
	defer tacticRegistry.RUnlock()
	t, ok := tacticRegistry.by[f]
	return t, ok
}

// familyTactic offers the tick to the tactic registered for e's family.
func (l *Life) familyTactic(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) (bool, error) {
	fam := familyOf(e)
	t, ok := tacticFor(fam)
	if !ok {
		return false, nil
	}
	return t.Engage(ctx, &Fight{Life: l, Actuator: a, Pose: p, Target: e, Snapshot: s, Params: l.warrior.p, Family: fam})
}
