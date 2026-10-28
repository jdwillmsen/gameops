package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Illager tactic durations are in ticks of 50 ms, distances in blocks.
const (
	// illagerGuardCycle is one guard-and-advance round against a crossbow:
	// about the time a pillager takes to reload and loose a bolt, so each
	// round gives it one shot into a raised shield and one walk toward it.
	illagerGuardCycle = 30
	// illagerAdvanceMin is the part of each round the shield is down for, so
	// a long MeleeShieldTick can never hold the unit in place under fire.
	illagerAdvanceMin = 10
	// illagerAimCone is how far, in degrees either side, a pillager's head
	// may point off the unit and still count as aiming at it: the same
	// reading of "about to go for me" as the boss fight uses.
	illagerAimCone = 30.0
	// illagerRehomeCells is how far a walk's goal may lag a redirected
	// target before it is replanned at once rather than on the chase cadence.
	illagerRehomeCells = 3
)

// illagerTactic fights the illager family in a fixed kill order. The order
// is a list of entity types, tried through the squad's own focus choice as
// its preferred type, so the target still comes from focusTarget: the same
// anchor, ignore map and suspect window as every other unit's, with the
// order of preference as the only change. Units sharing a profile and a
// kill order agree on the target without a message, as they do by default.
type illagerTactic struct {
	name string
	kill func(Role) []string
	// shared is the kill order when it is the same for every role, which is
	// what lets it steer the squad's focus: a role-specific order would give
	// each unit its own answer and the squad would stop agreeing.
	shared []string
}

func (t illagerTactic) Name() string         { return t.name }
func (illagerTactic) Family() tactics.Family { return tactics.FamilyIllager }

// Kill orders. The evoker comes first because its fangs and vexes are the
// damage nobody can block; the pillager next because its bolts reach the
// whole squad; the vindicator last because it only ever hurts what it
// reaches. A vex is the last resort: it is fast and dies in few hits, so it
// is swatted when it is in the way rather than chased.
var (
	killPriority = []string{"evoker", "evocation_illager", "pillager", "vindicator", "vex"}
	// The Vanguard takes the vindicator the way it takes any melee brute,
	// and the casters are left to the units that can reach or outrange them.
	killVanguard = []string{"vindicator", "pillager", "evoker", "evocation_illager", "vex"}
	killStriker  = []string{"evoker", "evocation_illager", "pillager", "vindicator", "vex"}
	killRanger   = []string{"pillager", "evoker", "evocation_illager", "vindicator", "vex"}
)

func init() {
	RegisterTactic(illagerTactic{name: "illager/priority", kill: func(Role) []string { return killPriority }, shared: killPriority})
	RegisterTactic(illagerTactic{name: "illager/split", kill: func(r Role) []string {
		switch r {
		case RoleVanguard:
			return killVanguard
		case RoleStriker:
			return killStriker
		case RoleRanger:
			return killRanger
		}
		return killPriority
	}})
}

// Engage takes the tick when the kill order names a target other than the
// squad's pick, or when the target is one it has a better approach to. It
// declines while the unit is eating, backing off, holding or regrouping,
// and whenever a standing order names the target type: the order stands.
func (t illagerTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l := f.Life
	w := l.warrior
	if w == nil || l.mode != modeFight || w.hold || w.healWant || w.healing || w.boss.active {
		return false, nil
	}
	o := w.order()
	switch o.Kind {
	case OrderHold, OrderRegroup, OrderRetreat:
		return false, nil
	}
	target := f.Target
	if o.Target == "" {
		e, ok := l.illagerFocus(f.Profile, f.Pose, t.kill(w.role))
		if !ok {
			return false, nil
		}
		target = e
	}
	return l.illagerFight(ctx, f, target)
}

// Prefer names the illager the kill order wants from a mixed pack, so a
// zombie with a lower id no longer decides who the squad swings at before an
// evoker. Health is known to nobody, so the lowest id breaks ties inside a
// type, as the shared choice does.
func (t illagerTactic) Prefer(_ *Fight, candidates []belief.Entity) (belief.Entity, bool) {
	for _, want := range t.shared {
		for _, c := range candidates {
			if name(c.Type) == want {
				return c, true
			}
		}
	}
	return belief.Entity{}, false
}

// illagerFocus is the squad's focus with the kill order as the preferred
// type: the first type in the order that stands in focus range is the
// target, and the usual health and id tie-break picks among its kind.
func (l *Life) illagerFocus(pr tactics.Profile, p act.Pose, order []string) (belief.Entity, bool) {
	anchor, _ := l.warrior.leader(p)
	q := focusQuery{
		anchor:        anchor,
		self:          p.Feet,
		tick:          l.tick,
		ignore:        l.ignore,
		attacker:      l.attacker,
		attackerUntil: l.attackerUntil,
		focusRange:    func(e belief.Entity) float64 { return pr.For(familyOf(e)).FocusRange },
	}
	for _, want := range order {
		q.wantType = want
		if e, ok := focusTarget(l.ents, q); ok && name(e.Type) == want {
			return e, true
		}
	}
	return belief.Entity{}, false
}

// illagerFight carries the tick against e. It reports unhandled when the
// role's own fight on the squad's target is the better idea, so a lone
// vindicator is fought exactly as it is without the tactic.
func (l *Life) illagerFight(ctx context.Context, f *Fight, e belief.Entity) (bool, error) {
	a, p, s, w := f.Actuator, f.Pose, f.Snapshot, l.warrior
	redirected := e.ID != f.Target.ID
	armed := w.role == RoleRanger && l.rangerArmed(s)

	// A walk planned for the squad's pick leads the unit away from the
	// target the kill order chose; replanning waits for the chase cadence
	// otherwise.
	if redirected && l.chase != nil && cellsApart(l.chaseGoal, cellOf(e.Pos)) > illagerRehomeCells {
		l.chase = nil
	}

	if !armed {
		if name(e.Type) != "vex" && !inMeleeReach(p, e) && l.tick-l.lastAttack >= attackEvery {
			if v, ok := l.vexInReach(p); ok {
				// A vex at arm's length hits harder than the swing it costs
				// to remove it, and the cadence would otherwise go unused
				// on the walk.
				l.lowerShield(a)
				return true, l.strike(a, p, v, s)
			}
		}
		switch name(e.Type) {
		case "evoker", "evocation_illager":
			return true, l.rushEvoker(ctx, a, p, e, s)
		case "pillager":
			guard := min(ticksOf(f.Params.MeleeShieldTick), illagerGuardCycle-illagerAdvanceMin)
			if l.guardAgainstBolts(a, p, e, s, guard) {
				return true, l.raiseShield(a, p, s)
			}
		}
	}

	if !redirected {
		return false, nil
	}
	if armed {
		return true, l.rangerShoot(ctx, a, p, e, s)
	}
	l.cancelDraw(a)
	return true, l.warriorMelee(ctx, a, p, e, s)
}

// rushEvoker closes on an evoker without the pauses the melee fight takes:
// no shield, because the fangs come up out of the ground rather than at the
// unit's front; no critical-hit jump and no circling, because every tick
// spent off the evoker is another cast. It swings as soon as it is in reach
// and stands planted between swings so it is not pulled off the target.
func (l *Life) rushEvoker(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior
	l.lowerShield(a)
	if !inMeleeReach(p, e) {
		w.critUntil = 0
		return l.warriorApproach(ctx, a, p, e)
	}
	l.chase = nil
	if l.tick-l.lastAttack >= attackEvery {
		w.struck = e.ID
		return l.strike(a, p, e, s)
	}
	return l.stillFacing(a, p, e)
}

// guardAgainstBolts says whether this tick of an approach on a pillager is
// one to spend behind the shield. A bolt closing always is. Otherwise the
// unit guards for the first guard ticks of each round while a pillager
// aims at it from beyond arm's length, and walks for the rest, so it keeps
// closing under fire instead of standing to be shot at. Once in reach the
// melee fight's own shield timing takes over.
func (l *Life) guardAgainstBolts(a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot, guard uint64) bool {
	if inMeleeReach(p, e) {
		return false
	}
	if _, ok := shieldAt(s); !ok {
		return false
	}
	if l.arrowIncoming(p) {
		return true
	}
	return l.tick%illagerGuardCycle < guard && l.crossbowAimed(p)
}

// crossbowAimed says whether a pillager within shooting range, on the
// level, has its head turned on the unit.
func (l *Life) crossbowAimed(p act.Pose) bool {
	aimed := false
	l.ents.Each(func(e belief.Entity) {
		if aimed || e.Player || name(e.Type) != "pillager" {
			return
		}
		if math.Abs(float64(e.Pos.Y()-p.Feet.Y())) > threatHeight {
			return
		}
		if d := horizontal(p.Feet, e.Pos); d > shooterRange || d <= meleeReach {
			return
		}
		to := mgl32.Vec2{p.Feet.X() - e.Pos.X(), p.Feet.Z() - e.Pos.Z()}
		off := math.Mod(float64(e.HeadYaw)-yawOf(to), 360)
		if off > 180 {
			off -= 360
		} else if off < -180 {
			off += 360
		}
		aimed = math.Abs(off) <= illagerAimCone
	})
	return aimed
}

// vexInReach is the nearest vex within the unit's melee reach.
func (l *Life) vexInReach(p act.Pose) (belief.Entity, bool) {
	var (
		best  belief.Entity
		bestD = math.MaxFloat64
	)
	l.ents.Each(func(e belief.Entity) {
		if e.Player || name(e.Type) != "vex" || !inMeleeReach(p, e) {
			return
		}
		if d := horizontal(p.Feet, e.Pos); d < bestD {
			best, bestD = e, d
		}
	})
	return best, best.ID != 0
}

// cellsApart is the larger of the two cells' horizontal offsets.
func cellsApart(a, b protocol.BlockPos) int32 {
	dx, dz := a.X()-b.X(), a.Z()-b.Z()
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	return max(dx, dz)
}
