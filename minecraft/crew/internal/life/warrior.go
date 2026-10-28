package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Role is a warrior's job in the squad. The names are what crew-control's
// squad routes carry on the wire, so the unit client maps them one to one.
// The types below are squadmsg's, aliased: the client, the engine and
// crew-control's routes all speak the same shapes, so a *squadclient.Client
// implements the Squad interface as-is.
type Role = squadmsg.Role

const (
	// Vanguard holds the front: it takes the focus target head on, keeps
	// its shield up and gives ground least.
	RoleVanguard Role = "vanguard"
	// Striker flanks the focus target and lands critical hits.
	RoleStriker Role = "striker"
	// Ranger keeps its distance and shoots, meleeing only as a last resort.
	RoleRanger Role = "ranger"
)

// validRole reports whether r names a warrior the life engine drives.
func validRole(r string) (Role, bool) {
	switch Role(r) {
	case RoleVanguard, RoleStriker, RoleRanger:
		return Role(r), true
	}
	return "", false
}

// OrderKind is what the squad has been told to do. The zero value is no
// order standing, which fights as though focused.
type OrderKind = squadmsg.OrderKind

const (
	OrderNone    OrderKind = ""
	OrderFocus   OrderKind = "focus"
	OrderHold    OrderKind = "hold"
	OrderAdvance OrderKind = "advance"
	OrderRegroup OrderKind = "regroup"
	OrderRetreat OrderKind = "retreat"
)

// Order is one standing instruction to the squad, replaced wholesale when a
// new one arrives. Target names an entity type to prefer, empty for none;
// Seq is crew-control's, kept for the client that needs to drop stale ones.
type Order = squadmsg.Order

// CalloutKind is why a unit spoke up.
type CalloutKind = squadmsg.CalloutKind

const (
	CalloutLowHealth  CalloutKind = "low_health"
	CalloutNeedHeal   CalloutKind = "need_heal"
	CalloutTargetDown CalloutKind = "target_down"
)

// Callout is one unit speaking up for the squad's benefit. The unit client
// fills in who said it and when.
type Callout = squadmsg.Callout

// Squad is the chain of command the warrior engine fights inside. The unit's
// squad client implements it, so the engine and crew-control's routes meet
// in squadmsg's types with no adapter between them.
type Squad interface {
	// Leader is who the unit anchors on: its id and feet position, or !ok
	// when the unit has nobody to follow.
	Leader() (id string, pos mgl32.Vec3, ok bool)
	// Orders is the standing order.
	Orders() Order
	// Callout speaks for the squad's benefit; the implementation carries it
	// to crew-control.
	Callout(c Callout)
}

// Warrior distances are in blocks and durations in ticks of 50 ms. The
// tunable ones are what tactics.Defaults carries: every fight, the boss's
// included, reads them from the unit's Profile.
const (
	// focusRange is how far from the leader a warrior still commits to a
	// target, shared so every unit measuring from the same leader agrees.
	focusRange = 12.0
	// healBelow is the health a warrior heals itself from.
	healBelow = 14
	// healSafe is how far every enemy must be before a warrior eats with
	// its guard down.
	healSafe = 3.0
	// pressRange is how close a mob has to be to count as pressing a hurt
	// warrior into falling back: inside the mob's own swing range, not the
	// warrior's, or no hurt warrior could ever close to fight.
	pressRange = 1.6
	// regroupWithin is how close to the leader a hurt warrior regroups.
	regroupWithin = 3.0
	// kiteBackoff is how close a mob may get before a Ranger gives ground.
	kiteBackoff = 5.0
	// backStuckTicks is how many backing ticks that moved nowhere mean the
	// Ranger is against a wall or a body and must melee instead.
	backStuckTicks = 3
	// bowDraw is how long a bow is drawn before release: Bedrock's full
	// charge, which is the only one worth an arrow.
	bowDraw = 20
	// shotEvery spaces releases: the draw plus the server's own settle.
	shotEvery = 30
	// arrowNear is how close an arrow in flight is to a hit: a shield goes
	// up now, not at the next swing.
	arrowNear = 3.0
	// lineWidth is how far off a shooter's line to its mark a unit counts
	// as clear of it.
	lineWidth = 1.2
	// orbitMin is how close a circling unit keeps to its target: nearer
	// than this and the target swings free.
	orbitMin = 1.5
	// critWindow bounds a jump started for a critical hit: past it the
	// swing lands on the ground rather than mid-fall.
	critWindow = 12
	// calloutEvery rate-limits a callout that would otherwise repeat every
	// tick the condition stands.
	calloutEvery = 300

	// meleeShieldTicks is how long a landed melee blow keeps the shield
	// coming back up between swings.
	meleeShieldTicks = 12
)

// warriorState belongs to the stepping goroutine, like the rest of the fight.
type warriorState struct {
	role  Role
	squad Squad

	// p is the tuning this tick's decisions read, resolved for the target
	// the tick stands on.
	p tactics.Params

	// hold forbids chasing while the order stands: strike what comes into
	// reach, keep position otherwise.
	hold bool
	// healWant says eat now, decided fresh each tick by the warrior's part
	// of Engaged.
	healWant bool
	// healing says the meal under way is the warrior's own, not the idle
	// rotation's.
	healing bool
	// lowSpoken says the low_health callout for this slump went out.
	lowSpoken bool

	// orbit is the strafing direction around the target, flipped only when
	// the world blocks it.
	orbit int8
	// circX and circZ are where circling last started from, and circStuck
	// how many of its ticks moved nowhere: three say flip the orbit.
	circX, circZ float32
	circStuck    uint8

	// hurtUntil is when a melee blow just landed stops counting as
	// imminent: the next one is what the shield is for.
	hurtUntil uint64

	// shield says the shield is raised, with the stack it was raised with:
	// the kit may reshuffle the hotbar between fights, so the stack is
	// noted afresh each time the shield goes up.
	shield     bool
	shieldItem belief.Item

	// critUntil bounds the jump under way for a critical hit.
	critUntil uint64

	// drawing counts a bow draw under way, with the bow it was started
	// with: an interrupted draw is let go with the stack it named.
	drawing   bool
	drawTicks int
	drawItem  belief.Item
	lastShot  uint64
	// backing is the ground a Ranger is giving, with where it last was and
	// how many of its ticks moved nowhere.
	backing   bool
	backX     float32
	backZ     float32
	backStuck uint8
	// meleeLast says the bow was put away after being cornered: stay on the
	// sword until there is room to shoot again.
	meleeLast bool

	// walk is the leader-bound regroup, replanned the way a chase is.
	walk     skill.Skill
	walkGoal [3]int32
	walkAt   uint64

	// struck is the target this unit has landed a hit on, and prevTarget
	// the one the last tick stood on: together they tell a target_down from
	// a mob that merely walked off.
	struck     uint64
	prevTarget uint64

	// arrows holds where the arrows now in flight were last tick, so an
	// arrow closing on the unit can be told from one leaving it. Kept small
	// by dropping entries the scan does not refresh. arrowAt and arrowIn
	// memo the answer for the tick, as several asks come in one.
	arrows  map[uint64]arrowSample
	arrowAt uint64
	arrowIn bool

	// calloutUntil holds back a repeating callout.
	calloutUntil uint64

	// boss is the fight with a boss, when the target is one.
	boss bossState
}

// arrowSample is one arrow in flight, one tick ago.
type arrowSample struct {
	pos  mgl32.Vec3
	tick uint64
}

// SetSquad wires the squad client in, before the first tick. A warrior
// without one leads itself; a non-warrior ignores it.
func (l *Life) SetSquad(s Squad) {
	if l.warrior != nil {
		l.warrior.squad = s
	}
}

// reset forgets a fight's warrior state, as after a death.
func (w *warriorState) reset() {
	w.hold, w.healWant = false, false
	w.healing, w.lowSpoken = false, false
	w.shield = false
	w.critUntil = 0
	w.drawing, w.drawTicks, w.drawItem = false, 0, belief.Item{}
	w.backing, w.backStuck = false, 0
	w.meleeLast = false
	w.walk = nil
	w.struck, w.prevTarget = 0, 0
	clear(w.arrows)
	w.boss = bossState{}
}

// leader is where the warrior anchors: the leader's feet, or its own when it
// has nobody to follow. The second return says which.
func (w *warriorState) leader(p act.Pose) (mgl32.Vec3, bool) {
	if w == nil || w.squad == nil {
		return p.Feet, false
	}
	_, pos, ok := w.squad.Leader()
	if !ok {
		return p.Feet, false
	}
	return pos, true
}

// order is the standing order, or none without a squad.
func (w *warriorState) order() Order {
	if w == nil || w.squad == nil {
		return Order{}
	}
	return w.squad.Orders()
}

// callout speaks once now and then holds back repeats for a while, so one
// standing condition is one callout, not one per tick.
func (w *warriorState) callout(k CalloutKind, tick uint64) {
	if tick < w.calloutUntil {
		return
	}
	w.calloutUntil = tick + calloutEvery
	w.speak(k)
}

// speak calls the squad without a hold-down, for events that happen once.
func (w *warriorState) speak(k CalloutKind) {
	if w.squad != nil {
		w.squad.Callout(Callout{Kind: k})
	}
}

// warriorEngaged is the warrior's part of Engaged: pick the squad's shared
// target, then say how this tick's fight is carried — strike and circle,
// hold position, eat behind the line, or fall back on the leader.
func (l *Life) warriorEngaged(p act.Pose, s belief.Snapshot, hurt bool) bool {
	w := l.warrior
	anchor, leaderOK := w.leader(p)
	pr := l.Profile()
	best, m := l.warriorTarget(pr, p, anchor)
	w.p = paramsFor(pr, best)
	if hurt && l.enemyNear(p.Feet, meleeReach) {
		w.hurtUntil = l.tick + ticksOf(w.p.MeleeShieldTick)
	}

	order := w.order()
	w.hold = order.Kind == OrderHold
	w.healWant = false

	if s.HealthKnown && float64(s.Health) <= w.p.HealBelow {
		if !w.lowSpoken {
			// One callout per slump, not per tick, and not rate-limited:
			// the hold-down belongs to the standing need_heal that may
			// follow on the same tick.
			w.lowSpoken = true
			w.speak(CalloutLowHealth)
		}
		if l.enemyNear(p.Feet, w.p.PressRange) {
			// Pressed at hurt health — apples or not, the squad should
			// know. Fall back on the leader; without one run, until
			// running has been tried and failed, which is when a cornered
			// unit stands and fights. Already at the leader, nowhere is
			// safer: the fight is on.
			w.callout(CalloutNeedHeal, l.tick)
			atLeader := leaderOK && horizontal(p.Feet, anchor) <= w.p.RegroupWithin
			switch {
			case leaderOK && !atLeader:
				m = modeRegroup
			case best.ID != 0 && !atLeader && l.retreating < retreatLimit:
				m = modeRetreat
			}
		} else if !l.enemyNear(p.Feet, w.p.HealSafe) {
			w.healWant = l.healFood(s).Name != ""
			if !w.healWant {
				// Nothing to eat: say so, and keep the old reflex of
				// running when near death.
				w.callout(CalloutNeedHeal, l.tick)
				if best.ID != 0 && s.Health <= lowHealth && l.retreating < retreatLimit {
					m = modeRetreat
				}
			}
		}
		// An enemy between pressRange and healSafe would abort the meal the
		// tick it started, so healWant stays clear and the unit fights: a
		// meal that cannot finish is worse than the health it might restore.
	} else if s.HealthKnown && float64(s.Health) > w.p.HealBelow {
		w.lowSpoken = false
	}

	m = l.bossEngaged(p, s, hurt, best, m)

	switch {
	case order.Kind == OrderRegroup && leaderOK && horizontal(p.Feet, anchor) > w.p.RegroupWithin:
		m = modeRegroup
	case order.Kind == OrderRetreat && best.ID != 0:
		m = modeRetreat
	}

	if w.prevTarget != 0 && w.struck == w.prevTarget {
		if _, ok := l.ents.Get(w.prevTarget); !ok {
			w.speak(CalloutTargetDown)
			w.struck = 0
		}
	}
	w.prevTarget = best.ID
	return l.engage(p, best, m)
}

// warriorTarget is the squad's shared focus, overridden only by what is about
// to blast the unit or by a player it must give way to.
func (l *Life) warriorTarget(pr tactics.Profile, p act.Pose, anchor mgl32.Vec3) (belief.Entity, mode) {
	q := focusQuery{
		anchor:        anchor,
		self:          p.Feet,
		tick:          l.tick,
		ignore:        l.ignore,
		attacker:      l.attacker,
		attackerUntil: l.attackerUntil,
		wantType:      name(l.warrior.order().Target),
		focusRange:    func(e belief.Entity) float64 { return pr.For(familyOf(e)).FocusRange },
		player:        l.mayFightPlayer,
	}
	best, found := focusTarget(l.ents, q)
	// A creeper about to blast is answered first, as the reactive defence
	// answers it: by leaving.
	if e, ok := l.nearestOfKind(p, flee, l.creeperWatch(pr, p)); ok {
		return e, modeRetreat
	}
	if found {
		if e, ok := l.preferredTarget(pr, p, q, best); ok {
			best = e
		}
		return best, modeFight
	}
	if e, ok := l.bossNear(anchor); ok {
		return e, modeFight
	}
	if l.attackerPlayer && l.tick < l.attackerUntil && l.tick < l.stepBackEnd() {
		if e, ok := l.ents.Get(l.attacker); ok && horizontal(p.Feet, e.Pos) <= attributionRange*2 {
			return e, modeStepBack
		}
	}
	return belief.Entity{}, modeFight
}

// nearestOfKind is the closest mob of a kind within reach of the unit's own
// feet, as a creeper check is.
func (l *Life) nearestOfKind(p act.Pose, k kind, within float64) (belief.Entity, bool) {
	var (
		best  belief.Entity
		bestD = math.MaxFloat64
	)
	l.ents.Each(func(e belief.Entity) {
		if e.Player || kindOf(e.Type) != k {
			return
		}
		if d := horizontal(p.Feet, e.Pos); d <= within && d < bestD {
			best, bestD = e, d
		}
	})
	return best, best.ID != 0
}

// enemyNear says whether anything the unit would fight stands within blocks
// of it: hostile, a provoked one that hurt it, a creeper, whose blast
// is as bad as a bite while eating, or a player the duel policy lets it fight.
func (l *Life) enemyNear(feet mgl32.Vec3, within float64) bool {
	found := false
	l.ents.Each(func(e belief.Entity) {
		if found {
			return
		}
		if e.Player {
			if !l.mayFightPlayer(e) {
				return
			}
		} else if k := kindOf(e.Type); k == other || (k == provoked && !l.isSuspect(e.ID)) {
			return
		}
		if math.Abs(float64(e.Pos.Y()-feet.Y())) > threatHeight {
			return
		}
		if horizontal(feet, e.Pos) <= within {
			found = true
		}
	})
	return found
}

// arrowIncoming says whether an arrow in flight is closing on the unit: the
// shield raised this tick is the one that meets it. The unit's own arrows
// leave it, which is what the sample comparison is for. Several asks come
// within one tick, and the samples are this tick's already after the first,
// so the answer is made once and read back.
func (l *Life) arrowIncoming(p act.Pose) bool {
	w := l.warrior
	if w.arrowAt == l.tick {
		return w.arrowIn
	}
	w.arrowAt, w.arrowIn = l.tick, l.scanArrows(p)
	return w.arrowIn
}

func (l *Life) scanArrows(p act.Pose) bool {
	w := l.warrior
	incoming := false
	if w.arrows == nil {
		w.arrows = map[uint64]arrowSample{}
	}
	if len(w.arrows) > 16 {
		for id, a := range w.arrows {
			if a.tick < l.tick-1 {
				delete(w.arrows, id)
			}
		}
	}
	l.ents.Each(func(e belief.Entity) {
		if name(e.Type) != "arrow" {
			return
		}
		d := e.Pos.Sub(p.Feet).Len()
		if float64(d) > w.p.ArrowNear*1.5 {
			return
		}
		if a, ok := w.arrows[e.ID]; ok && a.tick == l.tick-1 {
			if d < a.pos.Sub(p.Feet).Len() {
				incoming = true
			}
		}
		w.arrows[e.ID] = arrowSample{pos: e.Pos, tick: l.tick}
	})
	return incoming
}

// warriorFight sends this tick's input on the warrior's terms. The meal under
// way comes first, then the target family's own tactic, then the standing
// decisions of Engaged, then the role's own fight.
func (l *Life) warriorFight(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	w := l.warrior
	pr := l.Profile()
	w.p = paramsFor(pr, l.target)
	l.idle = idleState{}
	l.stopKit(a)
	if l.eat.active && !w.healing {
		// A meal the idle rotation started, cut short by the fight.
		l.stopEating(a, s)
	}
	p := a.Pose()

	if l.eat.active && w.healing {
		if l.mealThreatened(p) {
			// An enemy closed on the meal: let it go and fight.
			l.stopEating(a, s)
			w.healing = false
		} else {
			return l.warriorHealing(a, s)
		}
	}
	e, ok := l.ents.Get(l.target.ID)

	// A family's own tactic is offered the tick before any shared decision,
	// so it may answer a creeper other than by leaving. A meal under way is
	// finished first: no tactic has to know the eating state machine.
	if ok {
		if handled, err := l.familyTactic(ctx, pr, a, p, e, s); handled || err != nil {
			return err
		}
	}

	// What is about to blast the unit outranks a meal, and a meal outranks
	// the walk to the leader: eat where it is safe, walk when it is not.
	switch l.mode {
	case modeRetreat, modeStepBack:
		l.cancelDraw(a)
		if !ok {
			return l.still(a)
		}
		l.lowerShield(a)
		return l.retreat(ctx, a, p, e, retreatBlocks(l.mode))
	}

	if w.healWant && !w.drawing {
		return l.startHeal(a, s)
	}

	if l.mode == modeRegroup {
		l.cancelDraw(a)
		return l.regroup(ctx, a, p, s)
	}

	if !ok {
		// The grace after a target vanished: stand ready, shield against
		// what is still coming.
		l.cancelDraw(a)
		if l.arrowIncoming(p) {
			return l.raiseShield(a, p, s)
		}
		return l.still(a)
	}

	if w.boss.active && e.ID == w.boss.id {
		return l.bossFight(ctx, a, p, e, s)
	}
	if w.role == RoleRanger && l.rangerArmed(s) {
		return l.rangerShoot(ctx, a, p, e, s)
	}
	// A ranger that lost its bow mid-draw takes up the sword cleanly.
	l.cancelDraw(a)
	return l.warriorMelee(ctx, a, p, e, s)
}

// retreatBlocks is how far a backing-off mode walks before it replans.
func retreatBlocks(m mode) float64 {
	if m == modeStepBack {
		return 4
	}
	return 8
}

// stowShield lowers a shield a fight left raised, so the hand is free for
// whatever comes next. It sends no input; the caller's own tick does.
func (l *Life) stowShield(a skill.Actuator) {
	if l.warrior != nil {
		l.lowerShield(a)
	}
}

// healFood is what a hurt warrior eats: a golden apple first, then the
// richest food it holds.
func (l *Life) healFood(s belief.Snapshot) belief.Item {
	var best belief.Item
	bestPts := 0
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots {
			continue
		}
		if it.Name == "golden_apple" {
			return it
		}
		if pts := foods[it.Name]; pts > bestPts {
			best, bestPts = it, pts
		}
	}
	return best
}

// startHeal seeds the meal with the heal food and steps it; the eat state
// machine already sends exactly one input per tick.
func (l *Life) startHeal(a skill.Actuator, s belief.Snapshot) error {
	food := l.healFood(s)
	if food.Name == "" {
		return l.still(a)
	}
	l.lowerShield(a)
	l.warrior.healing = true
	l.eat = eatState{active: true, slot: food.Slot, food: food, startHunger: s.Hunger, startCount: food.Count, blockedUntil: l.eat.blockedUntil}
	if s.HeldKnown && s.HeldSlot == food.Slot {
		l.eat.phase = eatHold
	}
	return l.warriorHealing(a, s)
}

// warriorHealing steps the meal and drops the healing flag once it ends,
// so a fight cut short by an enemy closes in is judged afresh next tick.
func (l *Life) warriorHealing(a skill.Actuator, s belief.Snapshot) error {
	err := l.eating(a, s)
	if !l.eat.active {
		l.warrior.healing = false
	}
	return err
}

// regroup walks a hurt warrior back to its leader, shield raised against
// what follows it, until it is close enough to stand and fight.
func (l *Life) regroup(ctx context.Context, a skill.Actuator, p act.Pose, s belief.Snapshot) error {
	w := l.warrior
	anchor, ok := w.leader(p)
	if !ok {
		return l.still(a)
	}
	if l.arrowIncoming(p) {
		return l.raiseShield(a, p, s)
	}
	l.lowerShield(a)
	cell := cellOf(anchor)
	if w.walk == nil || (l.tick-w.walkAt >= chaseEvery && w.walkGoal != cell) {
		goal, ok := l.standNear(cell)
		if !ok {
			return l.still(a)
		}
		w.walk = skill.NewNavigate(l.world, l.self, goal, w.p.RegroupWithin)
		w.walkGoal, w.walkAt = cell, l.tick
	}
	st, _ := w.walk.Step(ctx, a)
	if st == skill.Running {
		return nil
	}
	w.walk = nil
	return l.still(a)
}
