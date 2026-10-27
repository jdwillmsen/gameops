package life

import (
	"context"
	"math"
	"sync"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// pvpFleeLimit bounds a retreat to eat: a player runs as fast as the
	// unit, so a retreat that has not opened a gap in three seconds never
	// will, and the unit turns and fights instead.
	pvpFleeLimit = 60
	// pvpFleeCool is how long after the last retreat tick the unit may try
	// one again.
	pvpFleeCool = 100
	// pvpCloseSlack is how far beyond the strafe ring the unit still closes
	// by stepping in, rather than by planning a walk.
	pvpCloseSlack = 1.2
	// pvpStrafeStuck is how many strafe ticks that moved nowhere flip the
	// orbit: a wall or a body is in the way.
	pvpStrafeStuck = 3
	// pvpMaxStates caps the per-unit memory. It only holds tactical
	// ephemera, so overflowing it costs a forgotten cooldown, nothing more.
	pvpMaxStates = 64
)

// pvpTactic fights another player: strafe the ring at sword reach, hit from
// the fall, shield on the opponent's rhythm, and break off to eat. The axe
// variant adds the shield-breaking opener.
type pvpTactic struct {
	name string
	axe  bool
}

func init() {
	RegisterTactic(pvpTactic{name: "player/strafe"})
	RegisterTactic(pvpTactic{name: "player/axe-shield", axe: true})
}

func (t pvpTactic) Name() string           { return t.name }
func (t pvpTactic) Family() tactics.Family { return tactics.FamilyPlayer }

// pvpState is what a unit remembers between ticks of a player fight. It
// lives here and not in the tactic because one tactic value serves every
// unit in the process.
type pvpState struct {
	orbit    int8
	critEnd  uint64
	axeHit   uint64
	bowing   bool
	fleeTick int
	fleeLast uint64
	// strafeX and strafeZ are where the last strafe step began, and stuck
	// how many of them moved nowhere.
	strafeX, strafeZ float32
	stuck            uint8
}

var pvpStates = struct {
	sync.Mutex
	by map[*Life]*pvpState
}{by: map[*Life]*pvpState{}}

func pvpStateOf(l *Life) *pvpState {
	pvpStates.Lock()
	defer pvpStates.Unlock()
	if st, ok := pvpStates.by[l]; ok {
		return st
	}
	if len(pvpStates.by) >= pvpMaxStates {
		clear(pvpStates.by)
	}
	st := &pvpState{orbit: 1}
	pvpStates.by[l] = st
	return st
}

// Engage owns the tick whenever the target is a player the unit may fight;
// otherwise the role's own fight runs. A standing hold, regroup or retreat
// order, and any mode other than plain fighting, stay with the engine.
func (t pvpTactic) Engage(ctx context.Context, f *Fight) (bool, error) {
	l, e, p, s, a := f.Life, f.Target, f.Pose, f.Snapshot, f.Actuator
	if f.Family != tactics.FamilyPlayer || !e.Player || e.ID == 0 || l.warrior == nil || l.mode != modeFight {
		return false, nil
	}
	w := l.warrior
	switch w.order().Kind {
	case OrderHold, OrderRegroup, OrderRetreat:
		return false, nil
	}
	if math.Abs(float64(e.Pos.Y()-p.Feet.Y())) > threatHeight {
		return false, nil
	}
	st := pvpStateOf(l)
	d := horizontal(p.Feet, e.Pos)

	if l.tick-st.fleeLast > pvpFleeCool {
		st.fleeTick = 0
	}
	if s.HealthKnown && float64(s.Health) <= f.Params.HealBelow && l.healFood(s).Name != "" {
		if handled, err := st.eat(ctx, f, d); handled {
			return true, err
		}
	}

	st.bowing = l.rangerArmed(s) && bowing(d, st.bowing, f.Params.KiteBackoff)
	if st.bowing {
		return true, l.rangerShoot(ctx, a, p, e, s)
	}
	l.cancelDraw(a)

	lo, hi := strafeBand(f.Params.OrbitMin)
	if d > hi+pvpCloseSlack {
		st.critEnd = 0
		l.lowerShield(a)
		return true, l.warriorApproach(ctx, a, p, e)
	}
	if !pvpInReach(p, e.Pos) {
		return true, st.strafe(f, d, lo, hi)
	}
	// A walk left over from before the unit got here is stale, as in the
	// role's own melee.
	l.chase = nil

	if hitReady(l.tick, l.lastAttack) {
		return true, t.swing(f, st)
	}
	if _, ok := shieldAt(s); ok && (l.arrowIncoming(p) || blockNow(l.tick, pvpLastHurt(l, e))) {
		return true, pvpGuard(f)
	}
	l.lowerShield(a)
	return true, st.strafe(f, d, lo, hi)
}

// pvpLastHurt is the tick this opponent last landed a blow, zero when the
// unit's last attacker was someone else.
func pvpLastHurt(l *Life, e belief.Entity) uint64 {
	if !l.attackerPlayer || l.attacker != e.ID {
		return 0
	}
	return l.attackerUntil - attackerTicks
}

// eat is the break-off to eat a golden apple: eat where the opponent is far
// enough, otherwise gain ground, and fight on when ground cannot be gained.
func (st *pvpState) eat(ctx context.Context, f *Fight, d float64) (bool, error) {
	l, a, p, e := f.Life, f.Actuator, f.Pose, f.Target
	if d >= eatClear(f.Params.HealSafe) {
		st.fleeTick = 0
		l.cancelDraw(a)
		return true, l.startHeal(a, f.Snapshot)
	}
	if st.fleeTick >= pvpFleeLimit || l.retreating >= retreatLimit {
		return false, nil
	}
	st.fleeTick++
	st.fleeLast = l.tick
	l.cancelDraw(a)
	l.lowerShield(a)
	return true, l.retreat(ctx, a, p, e, retreatBlocks(modeRetreat))
}

// swing makes this tick's blow: the jump that earns a critical hit, the hold
// while falling, and the strike itself with the weapon the model picks.
func (t pvpTactic) swing(f *Fight, st *pvpState) error {
	l, a, p, e, s := f.Life, f.Actuator, f.Pose, f.Target, f.Snapshot
	l.lowerShield(a)
	if p.OnGround && l.tick >= st.critEnd && !l.arrowIncoming(p) {
		st.critEnd = l.tick + ticksOf(f.Params.CritWindow)
		in := l.face(p, e)
		in.Jump = true
		return a.Tick(in)
	}
	if !(p.OnGround || fallingCrit(p) || l.tick >= st.critEnd) {
		return a.Tick(l.face(p, e))
	}
	axe, haveAxe := axeAt(s)
	if meleeWeapon(l.tick, st.axeHit, haveAxe, t.axe) == weaponAxe {
		return st.axeStrike(f, axe)
	}
	l.warrior.struck = e.ID
	return l.strike(a, p, e, s)
}

// axeStrike is strike with the axe in hand, and notes the blow as the
// disable that lets the sword work.
func (st *pvpState) axeStrike(f *Fight, axe belief.Item) error {
	l, a, p, e, s := f.Life, f.Actuator, f.Pose, f.Target, f.Snapshot
	if !s.HeldKnown || s.HeldSlot != axe.Slot {
		if err := a.SelectHotbar(axe.Slot, axe); err != nil {
			return err
		}
	}
	if err := a.Tick(l.face(p, e)); err != nil {
		return err
	}
	l.lastAttack = l.tick
	if a.Attack(e.ID, axe.Slot, axe) == nil {
		st.axeHit = l.tick
		l.warrior.struck = e.ID
		l.noteSwing(e, false)
	}
	return nil
}

// axeAt is the best axe on the hotbar, when the kit gave one.
func axeAt(s belief.Snapshot) (belief.Item, bool) {
	var (
		best  belief.Item
		score int
	)
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots {
			continue
		}
		if sc := weaponScore(it); sc > axeBase && sc < swordBase && sc > score {
			best, score = it, sc
		}
	}
	return best, score > 0
}

// pvpGuard raises the shield facing the opponent: a shield only covers what
// is in front of it, so the facing is the point.
func pvpGuard(f *Fight) error {
	l, a, p, e, s := f.Life, f.Actuator, f.Pose, f.Target, f.Snapshot
	w := l.warrior
	in := l.face(p, e)
	if !w.shield {
		shield, ok := shieldAt(s)
		if !ok {
			return l.stillFacing(a, p, e)
		}
		if err := a.SelectHotbar(shield.Slot, shield); err != nil {
			return err
		}
		if err := a.UseItem(shield.Slot, shield); err != nil {
			return err
		}
		w.shield, w.shieldItem = true, shield
		in.StartUsing = true
	}
	return a.Tick(in)
}

// strafe walks one step round the target along the ring, leaning in or out
// to hold it. A step onto no safe floor is not taken: the orbit turns the
// other way, and with neither way safe the unit stands facing.
func (st *pvpState) strafe(f *Fight, d, lo, hi float64) error {
	l, a, p, e := f.Life, f.Actuator, f.Pose, f.Target
	to := mgl32.Vec2{e.Pos.X() - p.Feet.X(), e.Pos.Z() - p.Feet.Z()}
	if to.Len() < 1e-3 {
		return l.stillFacing(a, p, e)
	}
	to = to.Normalize()

	if (mgl32.Vec2{p.Feet.X() - st.strafeX, p.Feet.Z() - st.strafeZ}).Len() < 0.05 {
		if st.stuck++; st.stuck >= pvpStrafeStuck {
			st.orbit, st.stuck = -st.orbit, 0
		}
	} else {
		st.stuck = 0
	}
	st.strafeX, st.strafeZ = p.Feet.X(), p.Feet.Z()

	for range 2 {
		dir := strafeHeading(to, d, st.orbit, lo, hi)
		if l.stepSafe(p, dir) {
			return a.Tick(act.Input{
				Yaw:     float32(yawOf(dir)),
				Forward: tapForward(l.tick, l.lastAttack),
			})
		}
		st.orbit = -st.orbit
	}
	return l.stillFacing(a, p, e)
}

// stepSafe says whether a block's step along dir lands on floor fit to stand
// on, no more than a block down and not above the unit's feet, so a strafe
// never walks off a ledge or into a wall.
func (l *Life) stepSafe(p act.Pose, dir mgl32.Vec2) bool {
	next := p.Feet.Add(mgl32.Vec3{dir.X(), 0, dir.Y()})
	feet := cellOf(p.Feet)
	c, ok := cellAt(l.world, int32(math.Floor(float64(next.X()))), int32(math.Floor(float64(next.Z()))), feet.Y())
	return ok && c.Y() <= feet.Y() && c.Y() >= feet.Y()-1
}
