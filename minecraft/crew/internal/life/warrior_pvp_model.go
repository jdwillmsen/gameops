package life

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// The functions here are the Bedrock model a player-versus-player fight is
// reasoned in. They are pure: a tick count, a distance or a pose in, a
// decision out, so each assumption below is pinned by a test instead of
// hidden inside a fight loop.
//
// Assumptions, from Bedrock's combat rules as the engine already relies on
// them (and where it does not, flagged as unverified):
//   - There is no attack-strength meter. Java's 1.9 cooldown does not exist;
//     the only brake on a swing is that a struck player ignores a blow no
//     stronger than the one before for ten ticks.
//   - A critical hit needs the attacker to be falling, off the ground.
//   - A shield blocks what arrives in front of it, and an axe blow disables
//     it for five seconds. The exact Bedrock figure is unverified; five
//     seconds is the vanilla one and errs towards re-axing sooner than later.
//   - Nothing on the wire shows whether an opposing player holds a shield up
//     or what it wields, so the model never assumes it can see either.
const (
	// pvpHitGap is the ten-tick window in which a repeat blow is wasted; it
	// is the engine's own swing cadence.
	pvpHitGap = attackEvery
	// pvpReach is the distance a swing is committed from. Vanilla caps a
	// survival attack near three blocks; the engine's reach leaves the
	// slack a laggy server position needs.
	pvpReach = meleeReach
	// pvpReachMargin keeps the strafe ring just inside pvpReach, so a step
	// of opposing movement does not carry the unit out of its own reach.
	pvpReachMargin = 0.2
	// shieldDisableTicks is how long an axe blow keeps a shield down.
	shieldDisableTicks = 100
	// pvpBlockLead is how many ticks before a predicted blow the shield goes
	// up: a use takes a tick to register, and one more is slack.
	pvpBlockLead = 3
	// pvpBlockMemory is how long a landed blow keeps predicting the next:
	// past it the opponent's rhythm is stale and the unit strafes instead.
	pvpBlockMemory = 4 * pvpHitGap
	// pvpBowHysteresis separates drawing the bow from putting it away, so a
	// target hovering at the boundary does not flip the weapon every tick.
	pvpBowHysteresis = 2.0
	// pvpEatClearFactor scales the engine's eating distance for a player,
	// who closes on a meal far faster than a mob that merely walks.
	pvpEatClearFactor = 2.0
)

// weaponKind is the weapon a PvP tick commits to.
type weaponKind uint8

const (
	weaponSword weaponKind = iota
	weaponAxe
	weaponBow
)

// hitReady says whether a swing now is worth making: the hit window of the
// last one has passed. last is the tick of the previous swing.
func hitReady(now, last uint64) bool {
	return now-last >= pvpHitGap
}

// pvpInReach says whether a target stands within a committed swing of the unit.
func pvpInReach(p act.Pose, target mgl32.Vec3) bool {
	return inMeleeReachAt(p.Feet, target)
}

// shieldDisabled says whether a shield is still down from the unit's last axe
// blow. axeHit zero means no blow landed, so nothing is disabled.
func shieldDisabled(now, axeHit uint64) bool {
	return axeHit != 0 && now-axeHit < shieldDisableTicks
}

// meleeWeapon picks sword or axe for a swing at an opponent who may be
// shielding. The shield cannot be seen, so with assumeShield the unit opens
// with the axe whenever the last disable has run out and spends the rest of
// the window on the sword. Without an axe, or without the assumption, it is
// the sword.
func meleeWeapon(now, axeHit uint64, haveAxe, assumeShield bool) weaponKind {
	if assumeShield && haveAxe && !shieldDisabled(now, axeHit) {
		return weaponAxe
	}
	return weaponSword
}

// bowing says whether the bow is the weapon at this distance. Drawn from
// beyond near+hysteresis and kept until the target comes inside near, where
// a draw cannot be finished. near is the engine's kite distance.
func bowing(dist float64, wasBowing bool, near float64) bool {
	if wasBowing {
		return dist > near
	}
	return dist > near+pvpBowHysteresis
}

// strafeBand is the distance ring the unit circles at: outside orbitMin,
// where the target swings free, and inside the reach. The inner edge never
// closes to less than 0.4 of the outer so a large orbitMin cannot collapse
// the ring to a line.
func strafeBand(orbitMin float64) (lo, hi float64) {
	hi = pvpReach - pvpReachMargin
	return math.Min(orbitMin, hi-0.4), hi
}

// strafeHeading is the unit direction one strafe step goes in: tangent to
// the target while inside the ring, spiralling in when beyond it and out
// when too close. to is the unit vector from the unit to the target and
// orbit is +1 or -1.
func strafeHeading(to mgl32.Vec2, dist float64, orbit int8, lo, hi float64) mgl32.Vec2 {
	// 90 degrees is the tangent; 45 leans towards the target and 135 away.
	angle := math.Pi / 2
	switch {
	case dist > hi:
		angle = math.Pi / 4
	case dist < lo:
		angle = 3 * math.Pi / 4
	}
	angle *= float64(orbit)
	sin, cos := float32(math.Sin(angle)), float32(math.Cos(angle))
	return mgl32.Vec2{to.X()*cos - to.Y()*sin, to.X()*sin + to.Y()*cos}
}

// blockNow says whether to hold the shield up this tick, given when the
// opponent last landed a blow on the unit (zero for never). Opposing swings
// come on the same ten-tick rhythm as the unit's own, so the next ones are
// predicted from the last, and the shield goes up just ahead of each. With
// no landed blow, or a stale one, there is nothing to predict from and the
// answer is no: strafing is the dodge until the opponent shows its rhythm.
func blockNow(now, lastHurt uint64) bool {
	if lastHurt == 0 || now < lastHurt || now-lastHurt > pvpBlockMemory {
		return false
	}
	phase := (now - lastHurt) % pvpHitGap
	return phase >= pvpHitGap-pvpBlockLead
}

// fallingCrit says whether a swing now is a critical hit: off the ground and
// already on the way down.
func fallingCrit(p act.Pose) bool {
	return !p.OnGround && p.Rise() < 0
}

// tapForward is the walk input for a strafe tick. The tick of a swing and
// the next stand still: the unit does not carry its own momentum through the
// target, and the sprint it drops there is what the next blow's extra
// knockback needs started afresh.
func tapForward(now, lastSwing uint64) float32 {
	if lastSwing != 0 && now-lastSwing < 2 {
		return 0
	}
	return 1
}

// sprintFood is the hunger at or below which the game refuses a sprint.
const sprintFood = 6

// canSprint says whether the unit has the food to sprint. An unknown hunger
// does not: a sprint the server refuses is a movement it corrects.
func canSprint(s belief.Snapshot) bool {
	return s.HungerKnown && s.Hunger > sprintFood
}

// eatClear is how far an opposing player must stand before a hurt unit eats
// with its guard down.
func eatClear(healSafe float64) float64 {
	return healSafe * pvpEatClearFactor
}
