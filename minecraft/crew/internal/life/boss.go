package life

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// bossProfile is how the squad fights one boss. Distances are in blocks from
// the boss, health in points of the unit's 20, durations in ticks.
type bossProfile struct {
	// focusRange is how far from the leader the boss stays the squad's
	// target: past the Ranger's band, or it would lose the boss it backs off.
	focusRange float64
	// rangerNear and rangerFar are the Ranger's band.
	rangerNear, rangerFar float64
	// strikerNear and strikerFar are where the Striker waits its turn.
	strikerNear, strikerFar float64
	// commitWithin is how close the Vanguard must be to the boss before the
	// Striker joins the melee: the boss must have someone else to hit.
	commitWithin float64
	// commitAbove is the health the Striker needs to join the melee, and
	// fallBelow the health it gives up the fight at, until it is back
	// above commitAbove.
	commitAbove, fallBelow float32
	// healBelow is the health a warrior eats at, and healClear how far off
	// the boss must be for the meal to finish before it arrives.
	healBelow float32
	healClear float64
	// darkness is the radius the boss darkens, zero for none. Only the
	// Vanguard, whose place is at the boss, stands inside it for long.
	darkness float64
	// meleeEvery is the boss's melee cadence, zero when its melee is not
	// worth timing a shield to; shieldBreak is how long a blow on a raised
	// shield disables it.
	meleeEvery, shieldBreak uint64
	// facingCone is how far, in degrees either side, the boss's head may
	// point off a Ranger and still count as facing it.
	facingCone float64
	// regroupDown is how many warriors down sends the rest back to the
	// start: two trading with a boss lose what three could not hold.
	regroupDown int
}

// wardenBoomRange is the sonic boom's horizontal reach, which no shield,
// armour or wall stops.
const wardenBoomRange = 15.0

// wardenProfile is fitted to the warden's numbers: a 30-point melee every
// 1.8 s that disables a shield for 5 s, a boom to 15 blocks and darkness to
// 20, and a follow range of 30.
var wardenProfile = bossProfile{
	focusRange: 32,
	rangerNear: 21, rangerFar: 28,
	strikerNear: 8, strikerFar: 12,
	commitWithin: 6,
	commitAbove:  0.7 * fullHealth, fallBelow: 0.5 * fullHealth,
	healBelow: healBelow, healClear: 8,
	darkness:   20,
	meleeEvery: 36, shieldBreak: 100,
	facingCone:  30,
	regroupDown: 2,
}

// bossProfiles holds what is fought as a boss. The wither and the elder
// guardian get the warden's geometry without its darkness or its melee
// timing: both hurt from range, so the bands still keep the squad spread.
var bossProfiles = map[string]bossProfile{
	"warden":         wardenProfile,
	"wither":         ranged(wardenProfile),
	"elder_guardian": ranged(wardenProfile),
}

func ranged(p bossProfile) bossProfile {
	p.darkness, p.meleeEvery, p.shieldBreak = 0, 0, 0
	return p
}

// bossOf is the profile an entity type is fought by, !ok for anything that
// is not a boss.
func bossOf(entityType string) (bossProfile, bool) {
	p, ok := bossProfiles[name(entityType)]
	return p, ok
}

// Roster is what a squad may know beyond its leader. The engine asks for it
// only in a boss fight; a Squad without it fights the boss with the leader
// standing in for the Vanguard and no regroup on losses.
type Roster interface {
	// Member is where the warrior in a role last reported from, !ok while
	// it is down or unheard.
	Member(r Role) (mgl32.Vec3, bool)
	// Down is how many of the squad's warriors are down now.
	Down() int
	// Start is where the squad started the fight, !ok when unknown.
	Start() (mgl32.Vec3, bool)
}

// bossState is a warrior's boss fight, decided fresh each tick by Engaged
// apart from what spans ticks.
type bossState struct {
	// active says this tick's target is a boss, fought by prof.
	active bool
	prof   bossProfile
	id     uint64
	// start is where the unit stood when the fight began, for a squad that
	// does not know its own start.
	start      mgl32.Vec3
	startKnown bool
	// regroup says enough of the squad is down to stop trading.
	regroup bool
	// hitAt is when the boss's melee last landed on the unit, and
	// shieldOutUntil when a shield it hit is usable again.
	hitAt          uint64
	shieldOutUntil uint64
	// fallen says the Striker gave up the melee at low health, and stays
	// out until healed past the commit line.
	fallen bool
	// strafe is the side a unit steps to when faced or blocked, and
	// slideUntil when a blocked one tries backing off again. stuck counts
	// the walking ticks, moving says last tick's was one, that moved
	// nowhere from lastX, lastZ.
	strafe       int8
	slideUntil   uint64
	lastX, lastZ float32
	moving       bool
	stuck        uint8
	// walk is the regroup to the start, replanned as a chase is.
	walk     skill.Skill
	walkGoal [3]int32
	walkAt   uint64
}
