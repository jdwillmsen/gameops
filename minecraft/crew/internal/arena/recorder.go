package arena

import (
	"context"
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
	// Spawned is how many mobs the fight summoned, when that is known; zero
	// means unknown. Kills never exceed it.
	Spawned int
}

// Recorder turns the events of one fight at a time into a scorecard. It is
// reused across the runs of a boss test: FightStart resets it.
//
// Kills are inferred from the fall in the live-mob count, so they are an upper
// bound: a mob that despawns, wanders off the counted area or never spawned
// reads as a kill. Meta.Spawned caps the total where the summoned count is
// known; where it is not, treat Kills as approximate.
//
// DamageTaken and DamageDealt are filled from Damage when one is set, as the
// growth of each warrior's totals over the fight, and stay zero otherwise or
// when a reading fails, so the trainer's score then rests on outcome, time,
// kills and deaths alone. The console reports no health, which is why units
// are asked instead. Dealt is the sum of the warriors' own counts, so several
// warriors on one mob each credit a teammate's blow to themselves and the sum
// is an upper bound.
type Recorder struct {
	Meta FightMeta
	// Damage supplies each warrior's lifetime damage; nil means none. A source
	// that is also a TotalsSource supplies damage dealt too.
	Damage DamageSource
	// Logf, when set, hears why a damage reading was skipped.
	Logf func(format string, args ...any)

	start  time.Time
	kills  int
	deaths map[string]int
	before map[string]UnitDamage
}

// FightStart begins a fight, forgetting any events of an unfinished one.
func (r *Recorder) FightStart(at time.Time) {
	r.start = at
	r.kills = 0
	r.deaths = map[string]int{}
	r.before = r.damageTotals()
}

// damageTotals reads the warriors' totals, or nil when it cannot.
func (r *Recorder) damageTotals() map[string]UnitDamage {
	if r.Damage == nil || len(r.Meta.Warriors) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), damageReadTimeout)
	defer cancel()
	totals, err := readDamage(ctx, r.Damage, r.Meta.Warriors)
	if err != nil && r.Logf != nil {
		r.Logf("damage reading incomplete: %v", err)
	}
	return totals
}

// damageSince is what the warriors lost and dealt since FightStart. A unit
// missing from either reading is left out, and one whose total fell restarted
// mid-fight, so that count is unknown rather than negative.
func (r *Recorder) damageSince() UnitDamage {
	var sum UnitDamage
	if len(r.before) == 0 {
		return sum
	}
	for _, g := range damageGrowth(r.before, r.damageTotals()) {
		sum.Taken += g.Taken
		sum.Dealt += g.Dealt
	}
	return sum
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
	dmg := r.damageSince()
	return tactics.Scorecard{
		TS:          ts,
		RunID:       r.Meta.RunID,
		Scenario:    r.Meta.Scenario,
		Family:      r.Meta.Family,
		Opponent:    r.Meta.Opponent,
		Profile:     r.Meta.Profile,
		Side:        r.Meta.Side,
		Outcome:     outcome,
		Seconds:     seconds,
		Kills:       r.cappedKills(),
		Deaths:      deaths,
		DamageDealt: dmg.Dealt,
		DamageTaken: dmg.Taken,
		Seed:        r.Meta.Seed,
	}
}

func (r *Recorder) cappedKills() int {
	if r.Meta.Spawned > 0 {
		return min(r.kills, r.Meta.Spawned)
	}
	return r.kills
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
// for, the same way the warrior files it in a fight. Ids it does not know are
// the default family.
func MobFamily(mob string) tactics.Family { return tactics.FamilyOfType(mob) }
