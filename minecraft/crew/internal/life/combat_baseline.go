package life

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// CombatBaseline tunes how a unit with no warrior role fights: when it runs,
// when it dares fight again, and how much gear it wants before it takes on a
// mob it could have avoided. Health is in half-hearts out of fullHealth. A nil
// baseline leaves the plain reactive defence in charge.
//
// The decision itself (decide) is a pure function of a situation, so every
// row of the rules is a table test; the glue in combat_baseline_fight.go only
// builds the situation from the world and carries the answer out.
type CombatBaseline struct {
	// FleeHealth is the health at or below which the unit walks away from
	// what it fights. Its default is the reflex's own lowHealth.
	FleeHealth float32
	// NightFleeHealth is the same bar at dusk and night for a unit short of
	// gear, which a mob there can take apart faster. At least FleeHealth.
	NightFleeHealth float32
	// ResumeHealth is the health a unit that fled must regain before it
	// fights again. It sits above both flee bars so a unit does not turn back
	// at the first heart, and at or below the health natural regeneration
	// reaches: eatBelowHurt feeds a hurt unit until food allows it.
	ResumeHealth float32
	// MinArmour is the armour rating (armourRating) under which the unit
	// counts as under-geared; a missing sword or axe counts too.
	MinArmour int
	// AvoidNight makes an under-geared unit give ground at dusk and night
	// instead of going out to meet a mob.
	AvoidNight bool
	// Shield lets the unit raise a shield from the hotbar against arrows.
	Shield bool
}

// DefaultCombatBaseline is the baseline the unit runs with when nothing
// overrides it.
func DefaultCombatBaseline() CombatBaseline {
	return CombatBaseline{
		FleeHealth:      lowHealth,
		NightFleeHealth: lowHealth + 2,
		ResumeHealth:    12,
		MinArmour:       8,
		AvoidNight:      true,
		Shield:          true,
	}
}

// Validate refuses a baseline whose bars cannot work together: a resume bar
// at or below a flee bar would make a unit that fled fight again at once, or
// never.
func (c CombatBaseline) Validate() error {
	switch {
	case c.FleeHealth < 1:
		return fmt.Errorf("flee health %v, want at least 1", c.FleeHealth)
	case c.NightFleeHealth < c.FleeHealth:
		return fmt.Errorf("night flee health %v is below flee health %v", c.NightFleeHealth, c.FleeHealth)
	case c.ResumeHealth <= c.NightFleeHealth:
		return fmt.Errorf("resume health %v must exceed night flee health %v", c.ResumeHealth, c.NightFleeHealth)
	case c.ResumeHealth > fullHealth:
		return fmt.Errorf("resume health %v is above full health %d", c.ResumeHealth, fullHealth)
	case c.MinArmour < 0:
		return errors.New("min armour is negative")
	}
	return nil
}

// stance is what the baseline has the unit do about its target this tick.
type stance uint8

const (
	// stanceFight is the plain fight: close in and strike, or give the
	// approach to the pathing layer.
	stanceFight stance = iota
	// stanceRetreat walks away from the target.
	stanceRetreat
	// stanceHold stands facing the target and does nothing else: a fused
	// creeper that cannot be left behind is not hit.
	stanceHold
	// stanceGuard raises the shield against an arrow or an archer's draw.
	stanceGuard
	// stanceWeave closes on an archer along a zigzag.
	stanceWeave
	// stanceCover walks out of an archer's line of sight.
	stanceCover
)

func (s stance) String() string {
	switch s {
	case stanceRetreat:
		return "retreat"
	case stanceHold:
		return "hold"
	case stanceGuard:
		return "guard"
	case stanceWeave:
		return "weave"
	case stanceCover:
		return "cover"
	}
	return "fight"
}

// threat is how a target is dealt with, which is not what it is called.
type threat uint8

const (
	threatMelee threat = iota
	threatCreeper
	threatArcher
)

// situation is everything decide reads. Distances are in blocks.
type situation struct {
	Threat threat
	Dist   float64
	// InReach says the target is within a blow.
	InReach bool
	// Cornered says a retreat has run out of road or of time (retreatLimit).
	Cornered bool
	// Fleeing is the health latch: the unit has dropped under its flee bar
	// and not yet regained ResumeHealth.
	Fleeing bool
	// Night and Under say it is dusk or night and the unit is under-geared.
	Night, Under bool
	// Lit and Charged are a creeper's fuse and its doubled blast.
	Lit, Charged bool
	// Shield says a shield is on the hotbar and the baseline may raise it.
	Shield bool
	// ArrowIn says an arrow is closing; Aimed says the archer's head is on
	// the unit; GuardTurn says this tick is in the guarded part of the cycle.
	ArrowIn, Aimed, GuardTurn bool
	// Stalled says the weave has stopped closing the gap.
	Stalled bool
}

// avoids says the unit should not go out to meet a mob: it is dusk or night
// and the unit is short of gear.
func (c CombatBaseline) avoids(in situation) bool { return c.AvoidNight && in.Night && in.Under }

// decide is the baseline's whole rulebook.
//
// A fleeing unit runs from everything it could outrun; one that is cornered
// fights whatever it can safely hit. A fused or charged creeper is never hit,
// cornered or not. An archer is closed on in a zigzag behind a shield, or its
// line of sight is broken when the unit is not fit or equipped to win the
// duel.
func (c CombatBaseline) decide(in situation) stance {
	switch in.Threat {
	case threatCreeper:
		return c.decideCreeper(in)
	case threatArcher:
		return c.decideArcher(in)
	}
	if in.Fleeing && !in.Cornered {
		return stanceRetreat
	}
	if c.avoids(in) && !in.InReach && !in.Cornered {
		return stanceRetreat
	}
	return stanceFight
}

func (c CombatBaseline) decideCreeper(in situation) stance {
	if in.Lit || in.Charged {
		if in.Cornered {
			return stanceHold
		}
		return stanceRetreat
	}
	if !in.Cornered {
		return stanceRetreat
	}
	// Out of road and the fuse not lit: one blow before it lights is the
	// best left, but not from a unit a blast would kill.
	if in.InReach && !in.Fleeing {
		return stanceFight
	}
	return stanceHold
}

func (c CombatBaseline) decideArcher(in situation) stance {
	if in.InReach {
		return stanceFight
	}
	if !in.Cornered && (in.Fleeing || c.avoids(in)) {
		return stanceCover
	}
	if in.Shield && (in.ArrowIn || (in.Aimed && in.GuardTurn && in.Dist > skelBreakOff)) {
		return stanceGuard
	}
	if in.Stalled {
		return stanceFight
	}
	return stanceWeave
}

// fleeBar is the health at or below which the unit runs, in the situation
// night and under describe.
func (c CombatBaseline) fleeBar(night, under bool) float32 {
	if c.AvoidNight && night && under {
		return c.NightFleeHealth
	}
	return c.FleeHealth
}

// latch is the flee decision with its memory: it trips at the flee bar and
// holds until health is back to ResumeHealth. Health the unit does not know
// trips nothing.
func (c CombatBaseline) latch(prev bool, health float32, known, night, under bool) bool {
	if !known {
		return false
	}
	switch {
	case health <= c.fleeBar(night, under):
		return true
	case health >= c.ResumeHealth:
		return false
	}
	return prev
}

// armourPoints are the defence points of each piece in helmet, chest, legs,
// boots order. They rate gear against a bar; they are not the game's exact
// damage reduction.
var armourPoints = map[string][4]int{
	"leather":   {1, 3, 2, 1},
	"golden":    {2, 5, 3, 1},
	"chainmail": {2, 5, 4, 1},
	"copper":    {2, 4, 3, 1},
	"iron":      {2, 6, 5, 2},
	"diamond":   {3, 8, 6, 3},
	"netherite": {3, 8, 6, 3},
	"turtle":    {2, 0, 0, 0},
}

// wornOutPct is the durability under which a piece is about to break and is
// no longer counted.
const wornOutPct = 15

// armourRating adds up the defence points of what the unit wears.
func armourRating(s belief.Snapshot) int {
	total := 0
	for _, p := range s.Armour {
		if p.DurabilityPct != nil && *p.DurabilityPct < wornOutPct {
			continue
		}
		mat, _, ok := strings.Cut(strings.TrimPrefix(p.Name, "minecraft:"), "_")
		if !ok {
			continue
		}
		for i, slot := range wornSlots {
			if slot == p.Slot {
				total += armourPoints[mat][i]
			}
		}
	}
	return total
}

// armed says the hotbar holds a sword or an axe.
func armed(s belief.Snapshot) bool {
	_, held := loadout(s)
	return weaponScore(held) >= axeBase
}

// underGeared says the unit lacks the weapon or the armour the baseline asks
// for before it takes a fight it could walk away from.
func (c CombatBaseline) underGeared(s belief.Snapshot) bool {
	return !armed(s) || armourRating(s) < c.MinArmour
}

// isNight says it is dusk or night in the overworld, where mobs spawn in the
// open. An unannounced time says nothing.
func isNight(s belief.Snapshot) bool {
	if s.Dimension != 0 {
		return false
	}
	return s.Env.DayPhase == belief.PhaseDusk || s.Env.DayPhase == belief.PhaseNight
}
