package arena

import (
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// FightObserver is told what a fight's own polling saw. The Bedrock console
// returns only command replies: it logs no damage amounts and no reliable
// death messages, so deaths and kills come from the director's standing
// checks and mob counts, never from log lines.
type FightObserver interface {
	FightStart(at time.Time)
	WarriorDied(name string)
	MobsKilled(n int)
}

// FightMeta is what a scorecard says about a fight beyond the numbers.
type FightMeta struct {
	RunID    string
	Scenario string
	Family   tactics.Family
	Opponent string
	Profile  string
	Side     string
	Seed     int64
	// Warriors are listed in Deaths even with zero deaths, so two fights of
	// the same squad have the same keys.
	Warriors []string
}

// Recorder turns the events of one fight at a time into a scorecard. It is
// reused across the runs of a boss test: FightStart resets it.
//
// DamageDealt and DamageTaken stay zero. The console reports neither, and
// health is not exposed by querytarget, so the trainer's score rests on
// outcome, time, kills and deaths until a damage source exists.
type Recorder struct {
	Meta FightMeta

	start  time.Time
	kills  int
	deaths map[string]int
}

// FightStart begins a fight, forgetting any events of an unfinished one.
func (r *Recorder) FightStart(at time.Time) {
	r.start = at
	r.kills = 0
	r.deaths = map[string]int{}
}

func (r *Recorder) WarriorDied(name string) {
	if r.deaths == nil {
		r.deaths = map[string]int{}
	}
	r.deaths[name]++
}

func (r *Recorder) MobsKilled(n int) {
	if n > 0 {
		r.kills += n
	}
}

// Finish emits the scorecard of the fight in progress. outcome is one of the
// tactics outcomes; use BossScoreOutcome or RunScoreOutcome to map arena ones.
func (r *Recorder) Finish(outcome string, seconds float64) tactics.Scorecard {
	deaths := map[string]int{}
	for _, w := range r.Meta.Warriors {
		deaths[w] = 0
	}
	for name, n := range r.deaths {
		deaths[name] = n
	}
	ts := ""
	if !r.start.IsZero() {
		ts = r.start.UTC().Format(time.RFC3339)
	}
	return tactics.Scorecard{
		TS:       ts,
		RunID:    r.Meta.RunID,
		Scenario: r.Meta.Scenario,
		Family:   r.Meta.Family,
		Opponent: r.Meta.Opponent,
		Profile:  r.Meta.Profile,
		Side:     r.Meta.Side,
		Outcome:  outcome,
		Seconds:  seconds,
		Kills:    r.kills,
		Deaths:   deaths,
		Seed:     r.Meta.Seed,
	}
}

// BossScoreOutcome maps a boss-test outcome onto the scorecard's.
func BossScoreOutcome(o string) string {
	switch o {
	case BossKilled:
		return tactics.OutcomeWin
	case BossWipe:
		return tactics.OutcomeLoss
	case BossTimeout:
		return tactics.OutcomeTimeout
	}
	return tactics.OutcomeAborted
}

// RunScoreOutcome maps a wave-run outcome onto the scorecard's. An errored
// run measured nothing.
func RunScoreOutcome(o string) string {
	switch o {
	case OutcomeBossDown, OutcomeCleared:
		return tactics.OutcomeWin
	case OutcomeWipe:
		return tactics.OutcomeLoss
	case OutcomeTimeout:
		return tactics.OutcomeTimeout
	}
	return tactics.OutcomeAborted
}

// MobFamily files a mob id under the opponent family a strategy is learned
// for. Ids it does not know are the default family.
func MobFamily(mob string) tactics.Family {
	id := strings.TrimPrefix(mob, "minecraft:")
	switch id {
	case "zombie", "husk", "drowned", "zombie_villager", "zombie_pigman", "zombified_piglin":
		return tactics.FamilyZombie
	case "skeleton", "stray", "bogged", "wither_skeleton":
		return tactics.FamilySkeleton
	case "creeper":
		return tactics.FamilyCreeper
	case "spider", "cave_spider":
		return tactics.FamilySpider
	case "blaze":
		return tactics.FamilyBlaze
	case "enderman":
		return tactics.FamilyEnderman
	case "witch":
		return tactics.FamilyWitch
	case "pillager", "vindicator", "evoker", "illusioner", "ravager":
		return tactics.FamilyIllager
	case "piglin", "piglin_brute", "hoglin", "zoglin":
		return tactics.FamilyBrute
	case "warden", "wither", "ender_dragon", "elder_guardian":
		return tactics.FamilyBoss
	}
	return tactics.FamilyDefault
}
