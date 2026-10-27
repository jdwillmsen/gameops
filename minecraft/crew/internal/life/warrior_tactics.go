package life

import (
	"context"
	"maps"
	"slices"
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
// A family may have many tactics; a unit's profile lists the ones it fights
// that family with, in order, so each is a variant the trainer can compare
// and a tactic that declines hands the tick to the next.
type FamilyTactic interface {
	// Name identifies the variant in a profile, e.g. "creeper/backoff". It
	// is unique across the process.
	Name() string
	Family() tactics.Family
	Engage(ctx context.Context, f *Fight) (handled bool, err error)
}

// TargetPreferrer is a FamilyTactic that also knows which of a mixed pack
// the squad should focus. The shared choice picks by health and id alone, so
// an evoker standing beside a zombie is only the target by luck of its id;
// a tactic that fights the evoker better first can name it instead.
//
// Prefer is asked for every unit on the profile, and the squad agrees on a
// focus without a message only if every unit gets the same answer. It must
// therefore decide from the candidates alone, which are the same mobs in the
// same id order for every unit, and not from the unit's role, position or
// state: f carries the unit, its pose, the default pick as Target and the
// params for its family, but no actuator or snapshot. It reports false to
// leave the default pick, and a target outside candidates is ignored.
type TargetPreferrer interface {
	FamilyTactic
	Prefer(f *Fight, candidates []belief.Entity) (belief.Entity, bool)
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

// familyTactic offers the tick to the tactics the unit's profile names for
// e's family, in the order it lists them: the first to take the tick fights
// it. A name nothing is registered under, or one that fights another family,
// is skipped. No name left leaves the role's own fight to run: the default
// profile is the untouched warrior.
func (l *Life) familyTactic(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) (bool, error) {
	fam := familyOf(e)
	pr := l.Profile()
	var f *Fight
	for _, name := range pr.TacticsFor(fam) {
		t, ok := tacticNamed(name, fam)
		if !ok {
			continue
		}
		if f == nil {
			f = &Fight{Life: l, Actuator: a, Pose: p, Target: e, Snapshot: s, Params: l.warrior.p, Family: fam}
		}
		if handled, err := t.Engage(ctx, f); handled || err != nil {
			return handled, err
		}
	}
	return false, nil
}

// preferredTarget lets the profile's tactics steer the squad's focus within a
// mixed pack. Families are asked in name order and each family's tactics in
// the profile's order, so every unit on a profile asks the same questions of
// the same candidates. A player in focus, an order naming a target type and
// an order that holds, regroups or retreats stand: none is a mob preference
// to override, and the squad is told them all alike.
func (l *Life) preferredTarget(p act.Pose, q focusQuery, best belief.Entity) (belief.Entity, bool) {
	if best.Player || q.wantType != "" || l.warrior == nil {
		return best, false
	}
	switch l.warrior.order().Kind {
	case OrderHold, OrderRegroup, OrderRetreat:
		return best, false
	}
	pr := l.Profile()
	if len(pr.Tactics) == 0 {
		return best, false
	}
	cands := focusCandidates(l.ents, q)
	if len(cands) < 2 {
		return best, false
	}
	present := map[tactics.Family]bool{}
	for _, c := range cands {
		present[familyOf(c)] = true
	}
	for _, fam := range slices.Sorted(maps.Keys(present)) {
		for _, n := range pr.TacticsFor(fam) {
			t, ok := tacticNamed(n, fam)
			if !ok {
				continue
			}
			tp, ok := t.(TargetPreferrer)
			if !ok {
				continue
			}
			f := &Fight{Life: l, Pose: p, Target: best, Params: pr.For(fam), Family: fam}
			if e, ok := tp.Prefer(f, cands); ok && slices.ContainsFunc(cands, func(c belief.Entity) bool { return c.ID == e.ID }) {
				return e, true
			}
		}
	}
	return best, false
}
