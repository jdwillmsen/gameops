package life

import (
	"math"

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
	// healClear is how far off the boss must be for a meal to finish before
	// it arrives. The health a warrior eats at is the profile's HealBelow.
	healClear float64
	// boom is how far the boss hurts through shield and armour, where a
	// Striker too hurt to join the melee has no business waiting.
	boom float64
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
	// flies says the boss spends the fight off the ground, so its height
	// does not take it out of the squad's reach to focus.
	flies bool
	// perchOnly says the boss can be hurt only while it stands still, and
	// the squad holds its ground the rest of the time.
	perchOnly bool
}

// wardenBoomRange is the sonic boom's horizontal reach, which no shield,
// armour or wall stops.
const wardenBoomRange = 15.0

// wardenProfile is fitted to the warden's numbers: a 30-point melee every
// 1.8 s that disables a shield for 5 s, a boom to 15 blocks and darkness to
// 20, and a follow range of 30. The 1.8 s is the documented cooldown, only
// roughly so and not measured on the lab; the Vanguard's guard window is
// timed off it, so a cadence that is off moves the window off the blow.
var wardenProfile = bossProfile{
	focusRange: 32,
	rangerNear: 21, rangerFar: 28,
	strikerNear: 8, strikerFar: 12,
	commitWithin: 6,
	commitAbove:  0.7 * fullHealth, fallBelow: 0.5 * fullHealth,
	healClear:  8,
	boom:       wardenBoomRange,
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
	"ender_dragon":   dragonProfile,

	"gameops:ground_dragon": groundDragonProfile,
}

// noRegroup is a regroupDown no squad reaches.
const noRegroup = math.MaxInt

// dragonFocusRange covers the whole End island: the dragon circles far wider
// than a warden ever strays, and losing it between perches would send the
// squad off to idle.
const dragonFocusRange = 128

// dragonProfile is the ender dragon, which only a hit from within about 6.4
// blocks of its origin hurts and which is worth reaching only while it
// perches. Every other number is unused: the squad neither keeps a band nor
// times a shield against it, and a regroup would walk away from the one
// window there is to hit it.
var dragonProfile = bossProfile{
	focusRange:  dragonFocusRange,
	healClear:   8,
	regroupDown: noRegroup,
	flies:       true,
	perchOnly:   true,
}

// groundDragonProfile is the ground-bound arena dragon: a short bite and a
// dodgeable fireball, no boom or darkness and no melee cadence worth timing
// a shield to. The warden's squad geometry fits it with a closer Ranger band.
var groundDragonProfile = func() bossProfile {
	p := ranged(wardenProfile)
	p.rangerNear, p.rangerFar = 10, 20
	p.strikerNear, p.strikerFar = 4, 8
	p.boom = 0
	return p
}()

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
	// start is where the unit stood when the fight began, and rally the
	// regroup point made from it, for a squad that does not know its own.
	start, rally mgl32.Vec3
	// regroup says enough of the squad is down to stop trading, and
	// regroupUntil when that stops, zero while the squad is not down.
	// rallied says the unit reached the regroup point, where a boss that
	// follows it into reach ends the regroup.
	regroup      bool
	regroupUntil uint64
	rallied      bool
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
	// nowhere from lastX, lastZ. blocked counts the stucks that made no
	// block of ground from blockX, blockZ, where the first one was, and
	// corneredUntil is when one shut in on every side tries walking again.
	strafe         int8
	slideUntil     uint64
	blocked        uint8
	blockX, blockZ float32
	corneredUntil  uint64
	lastX, lastZ   float32
	moving         bool
	stuck          uint8
	// stillAt is where the boss last moved to and stillSince the tick it got
	// there: a perched dragon sends no movement, so standing still is the
	// only sign of the perch.
	stillAt    mgl32.Vec3
	stillSince uint64
	// walk is the regroup to the start, or a dragon fight's approach to the
	// perch, replanned as a chase is, and walkRetry when one that found no
	// way is planned again.
	walk      skill.Skill
	walkGoal  [3]int32
	walkAt    uint64
	walkRetry uint64
}
