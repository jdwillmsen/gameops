package config

import "fmt"

// Combat is the settings of the combat baseline, which a unit with no warrior
// role fights by (see life.CombatBaseline). Health is in half-hearts out of 20.
type Combat struct {
	// Baseline turns the baseline on; off leaves the plain reactive defence.
	Baseline bool
	// FleeHealth, NightFleeHealth and ResumeHealth are the health bars: run at
	// or under the first, or the second for an under-geared unit at dusk and
	// night, and fight again only once back at the third. RETREAT_HEALTH, when
	// higher than a flee bar, takes its place.
	FleeHealth      int
	NightFleeHealth int
	ResumeHealth    int
	// MinArmour is the armour rating under which a unit counts as
	// under-geared.
	MinArmour int
	// AvoidNight makes an under-geared unit give ground at dusk and night.
	AvoidNight bool
	// Shield lets the unit raise a shield against arrows.
	Shield bool
}

// The defaults restate life.DefaultCombatBaseline, which config does not
// import; a test in cmd/crew keeps the two equal.
const (
	defaultFleeHealth      = 6
	defaultNightFleeHealth = 8
	defaultResumeHealth    = 12
	defaultMinArmour       = 8
	fullHealth             = 20
	// maxResumeHealth restates life's cap: regeneration reaches no higher,
	// so a higher bar could keep a unit that cannot regenerate fleeing.
	maxResumeHealth = 18
	maxMinArmour    = 30
)

// withRetreat checks the bars as the unit will run them: the reflex's
// RETREAT_HEALTH raises the flee bars when it is higher, so the resume bar
// must clear that too.
func (c Combat) withRetreat(retreat int) error {
	if !c.Baseline {
		return nil
	}
	flee := max(c.FleeHealth, retreat)
	night := max(c.NightFleeHealth, flee)
	if c.ResumeHealth <= night {
		return fmt.Errorf("COMBAT_RESUME_HEALTH (%d) must exceed the flee bars in force (day %d, night %d); RETREAT_HEALTH (%d) raises them", c.ResumeHealth, flee, night, retreat)
	}
	return nil
}

func combatSettings() (Combat, error) {
	var c Combat
	var err error
	if c.Baseline, err = boolDefault("COMBAT_BASELINE", true); err != nil {
		return Combat{}, err
	}
	if c.FleeHealth, err = intInRange("COMBAT_FLEE_HEALTH", defaultFleeHealth, 1, fullHealth-2); err != nil {
		return Combat{}, err
	}
	if c.NightFleeHealth, err = intInRange("COMBAT_NIGHT_FLEE_HEALTH", defaultNightFleeHealth, 1, fullHealth-1); err != nil {
		return Combat{}, err
	}
	if c.ResumeHealth, err = intInRange("COMBAT_RESUME_HEALTH", defaultResumeHealth, 2, maxResumeHealth); err != nil {
		return Combat{}, err
	}
	if c.MinArmour, err = intInRange("COMBAT_MIN_ARMOUR", defaultMinArmour, 0, maxMinArmour); err != nil {
		return Combat{}, err
	}
	if c.AvoidNight, err = boolDefault("COMBAT_AVOID_NIGHT", true); err != nil {
		return Combat{}, err
	}
	if c.Shield, err = boolDefault("COMBAT_SHIELD", true); err != nil {
		return Combat{}, err
	}
	if !c.Baseline {
		return c, nil
	}
	// A bar for the dusk and night must not sit under the daytime one, and
	// the resume bar must clear both, or a unit that fled would fight again
	// at once or never.
	if c.NightFleeHealth < c.FleeHealth {
		return Combat{}, fmt.Errorf("COMBAT_NIGHT_FLEE_HEALTH (%d) is below COMBAT_FLEE_HEALTH (%d)", c.NightFleeHealth, c.FleeHealth)
	}
	if c.ResumeHealth <= c.NightFleeHealth {
		return Combat{}, fmt.Errorf("COMBAT_RESUME_HEALTH (%d) must exceed COMBAT_NIGHT_FLEE_HEALTH (%d)", c.ResumeHealth, c.NightFleeHealth)
	}
	return c, nil
}
