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

// Blaze numbers. A blaze hovers a block or two over its mark, fires a volley
// of three small fireballs and then waits out its cooldown, so the volley in
// the air is what the shield answers and the gap after it is when ground is
// gained. Its own projectiles are what the fight reads; nothing else here is
// measured.
const (
	// fireVolleyNear is how close a small fireball in flight has to be
	// before the shield goes up. Bedrock shows no windup to read, so the
	// volley is answered from its projectiles: eight blocks is ahead of a
	// fireball's remaining flight, and one beyond it is another unit's fight
	// or one long spent.
	fireVolleyNear = 8.0
	// advanceWithin is how near the blaze's own cell a closing walk ends, so
	// the unit stands inside the reach its swings are answered at.
	advanceWithin = 1.5
	// snowEvery spaces the throws. A snowball needs no draw, so the cadence
	// is only spam control: one every half second spends the stack across
	// the volley's cooldown instead of in its first second, and stays inside
	// the use cooldown the server runs on thrown items.
	snowEvery = 10
	// snowBandGap is how far past the give-ground line the blaze has to be
	// before the unit closes again: the hysteresis keeps the fight from
	// flipping between a step forward and a step back at one boundary.
	snowBandGap = 3.0
	// blazeBackStep is how far behind the unit the give-ground walk is
	// swept for safe floor, a stride and a bit so the cell it enters is seen.
	blazeBackStep = 1.5

	snowballName = "snowball"
	fireballName = "small_fireball"

	// bodyHalf is the unit's half width, the same 0.3 the movement model
	// builds its body box from, so "the body is clear" is measured the way
	// the planner measures its footprint.
	bodyHalf = float32(0.3)
)

func init() {
	RegisterTactic(&blazeShieldTactic{})
	RegisterTactic(&blazeSnowTactic{})
}

// blazeDecline leaves the shared decisions to the engine: a fight the unit
// has been pulled out of — a creeper's blast, a retreat or a walk back to
// the leader — and a meal it wants to start are not a blaze tactic's to
// overrule.
func blazeDecline(f *Fight) bool {
	return f.Life.mode != modeFight || f.Life.warrior.healWant
}

// fireballNear says whether a blaze's small fireball is in flight this close
// to the unit. Bedrock gives the volley no windup, so it is read from its
// projectiles.
func (l *Life) fireballNear(p act.Pose, near float64) bool {
	found := false
	l.ents.Each(func(e belief.Entity) {
		if found || e.Player || name(e.Type) != fireballName {
			return
		}
		if e.Pos.Sub(p.Feet).Len() <= float32(near) {
			found = true
		}
	})
	return found
}

// inHotGround says whether the cell the unit stands in or the floor under it
// burns or melts. A volley ends; ground on fire does not, so the ground is
// left before anything else the fight could ask for.
func (l *Life) inHotGround(p act.Pose) bool {
	c := cellOf(p.Feet)
	if n, ok := l.world.Block(c); ok && !safeFloor(n) {
		return true
	}
	if n, ok := l.world.Block(protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}); ok && !safeFloor(n) {
		return true
	}
	return false
}

// bodyInHot says whether any cell the body box still overlaps at its own
// rows burns or melts. The feet cell can read clear while the body hangs
// over the burning one; a plan planned from such a pose cannot walk the
// unit out — the planner's sweep only offers a jump onto the block it
// overlaps — so the step out is finished only once the whole body is clear.
func (l *Life) bodyInHot(p act.Pose) bool {
	row := int32(math.Floor(float64(p.Feet[1])))
	for _, dy := range [2]float32{0, 1} {
		for _, sx := range [2]float32{p.Feet[0] - bodyHalf, p.Feet[0] + bodyHalf} {
			for _, sz := range [2]float32{p.Feet[2] - bodyHalf, p.Feet[2] + bodyHalf} {
				c := protocol.BlockPos{int32(math.Floor(float64(sx))), row + int32(dy), int32(math.Floor(float64(sz)))}
				if n, ok := l.world.Block(c); ok && !safeFloor(n) {
					return true
				}
			}
		}
	}
	return false
}

// stepOffHot walks one tick toward the nearest cell the unit can stand on
// safely at its own level, or, when it stands on top of a burning block —
// blown or knocked there — off that block's edge onto the floor below. The
// planner is no help from inside a burning cell — it models that cell as a
// wall, so no route starts there — and the movement model lets a body leave
// a box it already sits in, so the step out is taken straight and re-aimed
// every tick. Once the whole body is clear, the ordinary fight resumes.
func (l *Life) stepOffHot(a skill.Actuator, p act.Pose) error {
	c := cellOf(p.Feet)
	for _, drop := range [2]int32{0, -1} {
		for _, d := range [...][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
			q := protocol.BlockPos{c.X() + d[0], c.Y() + drop, c.Z() + d[1]}
			if safeStand(l.world, q) {
				eye := p.Feet.Add(mgl32.Vec3{0, 1.62, 0})
				yaw, pitch := lookAt(eye, mgl32.Vec3{float32(q.X()) + 0.5, p.Feet.Y(), float32(q.Z()) + 0.5})
				return a.Tick(act.Input{Yaw: yaw, Pitch: pitch, Forward: 1})
			}
		}
	}
	// Nowhere to step at this level or one below: hold. The deeper escapes —
	// swimming out, regrouping on the leader — own those cases when they
	// apply.
	return l.still(a)
}

// cellUnder is the standable cell in the blaze's column, at the level the
// unit fights on: a blaze hovers over its mark, and the ground under it is
// where a walking unit meets it. Asked from the blaze's own height, every
// neighbour is air — that is why the unit's own level leads.
func (l *Life) cellUnder(e belief.Entity, p act.Pose) (protocol.BlockPos, bool) {
	c := cellOf(e.Pos)
	c[1] = int32(math.Floor(float64(p.Feet.Y())))
	if g, ok := cellAt(l.world, c.X(), c.Z(), c.Y()); ok {
		return g, true
	}
	return l.standNear(c)
}

// closeOnBlaze walks toward the ground under the blaze on the shared chase
// plan, ending `within` short of it. The planner refuses lava, fire and
// water as walls, so the walk cannot end in them. A blaze beyond reach —
// hovering high, or over ground the unit cannot stand on — is not given up
// on: the unit stands facing it, which is also where the next volley is
// best met, and the plan is tried again after a beat.
func (l *Life) closeOnBlaze(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, within float64) error {
	goal, ok := l.cellUnder(e, p)
	if !ok {
		return l.stillFacing(a, p, e)
	}
	g3 := [3]int32{goal.X(), goal.Y(), goal.Z()}
	if l.chase == nil && l.chaseGoal == g3 && l.tick-l.chaseAt < chaseEvery {
		// A plan that found no way gets a beat before the search runs
		// again: the search is the costly part, and the ground does not
		// move between beats. The goal left behind by the failure is what
		// says the wait is for this fight, not for a fight not yet begun.
		return l.stillFacing(a, p, e)
	}
	if l.chase == nil || (l.tick-l.chaseAt >= chaseEvery && l.chaseGoal != g3) {
		l.chase = skill.NewNavigate(l.world, l.self, goal, within)
		l.chaseGoal, l.chaseAt = g3, l.tick
	}
	switch st, _ := l.chase.Step(ctx, a); st {
	case skill.Running:
		return nil
	case skill.Failed:
		l.chase, l.chaseAt, l.chaseGoal = nil, l.tick, g3
	}
	return l.stillFacing(a, p, e)
}

// blazeShield holds the shield through a blaze's volley, closes the ground
// between volleys and fights at arm's length, never standing on ground that
// burns. A breeze is fought the same way — its wind charges are answered as
// a volley, though a shield cannot stop one, and the closing walk is what
// brings the fight to it.
type blazeShieldTactic struct{}

func (t *blazeShieldTactic) Name() string           { return "blaze/shield" }
func (t *blazeShieldTactic) Family() tactics.Family { return tactics.FamilyBlaze }

func (t *blazeShieldTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	if blazeDecline(f) {
		return false, nil
	}
	l, p, e, s := f.Life, f.Pose, f.Target, f.Snapshot
	a := f.Actuator
	// The fight is shield and sword from here, so a half-drawn bow is let
	// go.
	l.cancelDraw(a)

	// The volley outranks everything the ground could ask: a blocked volley
	// saves the whole burst, and the burn it would have lit never starts.
	// Even from burning ground the shield is the better trade — the volley
	// is three hits the moment they land, the fire underfoot is slower than
	// the step out takes.
	if l.fireballNear(p, fireVolleyNear) {
		return true, l.raiseShield(a, p, s)
	}
	// A cell the unit burns or melts in — or one its body still hangs over —
	// is left before anything else: standing in fire wins nothing the fight
	// can pay for, and a chase planned from a brushing pose climbs the fire.
	if l.inHotGround(p) || l.bodyInHot(p) {
		return true, l.stepOffHot(a, p)
	}
	if inMeleeReach(p, e) {
		// At arm's length the role's own fight runs: its cadence, its
		// circling and its shield between swings are all wanted here.
		return true, l.warriorMelee(ctx, a, p, e, s)
	}
	if l.warrior.hold {
		// The order is to keep position: face what stands off, and let it
		// come — a blaze pressed on a held unit is met in reach.
		return true, l.stillFacing(a, p, e)
	}
	l.lowerShield(a)
	return true, l.closeOnBlaze(ctx, a, p, e, advanceWithin)
}

// blazeSnowTactic keeps a blaze at throw range and chips it with snowballs,
// which set a blaze burning where arrows do not. It fights only with a
// snowball stack on the hotbar: without one it declines, and the role's own
// fight runs.
type blazeSnowTactic struct {
	// throws is each unit's throw cadence. The tactic is one value shared
	// by every unit, each stepping on its own goroutine.
	throws unitScratch[snowThrow]
}

// snowThrow is when a unit last threw; thrown tells tick 0 from never.
type snowThrow struct {
	at     uint64
	thrown bool
}

func (t *blazeSnowTactic) Name() string           { return "blaze/snowball" }
func (t *blazeSnowTactic) Family() tactics.Family { return tactics.FamilyBlaze }

func (t *blazeSnowTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l, p, e, s := f.Life, f.Pose, f.Target, f.Snapshot
	snow, ok := snowballAt(s)
	if !ok {
		// Nothing to throw: the role's own fight is the better idea.
		return false, nil
	}
	// The Ranger's own fight already holds a band and shoots, and a drawn
	// bow beats snowballs from it; leave the bow to its holder.
	if l.warrior.role == RoleRanger && l.rangerArmed(s) {
		return false, nil
	}
	if blazeDecline(f) {
		return false, nil
	}
	a := f.Actuator
	if l.fireballNear(p, fireVolleyNear) {
		return true, l.raiseShield(a, p, s)
	}
	l.lowerShield(a)
	if l.inHotGround(p) || l.bodyInHot(p) {
		return true, l.stepOffHot(a, p)
	}
	band := f.Params.KiteBackoff
	if d := horizontal(p.Feet, e.Pos); d < band {
		// The blaze pressed in: give ground facing it, throwing on the way
		// while the ground lasts; against a wall, stand and throw.
		in := l.face(p, e)
		if l.rangerCanBackOff(p) && sweepClear(l.world, p.Feet, away(p, e), blazeBackStep) {
			in.Forward = -1
		}
		return true, t.throw(l, a, s, snow, in)
	} else if d > band+snowBandGap && !l.warrior.hold {
		if _, walkable := l.cellUnder(e, p); walkable {
			return true, l.closeOnBlaze(ctx, a, p, e, band)
		}
		// No ground under the blaze to reach: snowballs fly over the gap
		// the unit cannot walk, so it throws from where it stands.
	}
	return true, t.throw(l, a, s, snow, l.face(p, e))
}

// snowballAt is a snowball stack on the hotbar, when the unit carries one.
func snowballAt(s belief.Snapshot) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && it.Name == snowballName && it.Count > 0 {
			return it, true
		}
	}
	return belief.Item{}, false
}

// throw sends this tick's walk facing and, when the cadence is up, lets a
// snowball fly: the input's rotation lands before the throw's transaction,
// so the projectile leaves along where the unit has just turned to look.
func (t *blazeSnowTactic) throw(l *Life, a skill.Actuator, s belief.Snapshot, snow belief.Item, in act.Input) error {
	st := t.throws.of(l)
	if st.thrown && l.tick-st.at < snowEvery {
		return a.Tick(in)
	}
	st.at, st.thrown = l.tick, true
	if !s.HeldKnown || s.HeldSlot != snow.Slot {
		if err := a.SelectHotbar(snow.Slot, snow); err != nil {
			return err
		}
	}
	if err := a.Tick(in); err != nil {
		return err
	}
	return a.UseItem(snow.Slot, snow)
}
