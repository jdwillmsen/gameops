package telemetry

import (
	"fmt"
	"math"
	"time"
)

// Fight outcomes, the closed list of fight_ended. The unit sends counts and
// no verdict, so these are inferred from them, and the README says how.
const (
	OutcomeWin        = "win"
	OutcomeLoss       = "loss"
	OutcomeDisengaged = "disengaged"
	OutcomeOther      = "other"
)

// FightOutcomes lists every outcome a fight_ended can carry.
var FightOutcomes = []string{OutcomeWin, OutcomeLoss, OutcomeDisengaged, OutcomeOther}

// fightQuiet is how long a unit's fight counts must hold still before the
// fight counts as over. A fight is a run of swings and blows; a gap this long
// in one is a different fight.
const fightQuiet = 10 * time.Second

// FightTotals is a warrior's running totals since its process started, as its
// state reports them.
type FightTotals struct {
	Swings uint64
	Hits   uint64
	Deaths uint64
	// Damage is health points lost.
	Damage float64
}

// Since is the growth of the totals over an earlier reading. ok is false when
// any total went down, which only a restarted unit does: the earlier reading
// then says nothing about this one.
func (t FightTotals) Since(prev FightTotals) (d FightTotals, ok bool) {
	if t.Swings < prev.Swings || t.Hits < prev.Hits || t.Deaths < prev.Deaths || t.Damage < prev.Damage {
		return FightTotals{}, false
	}
	return FightTotals{t.Swings - prev.Swings, t.Hits - prev.Hits, t.Deaths - prev.Deaths, t.Damage - prev.Damage}, true
}

// fightWire is tasks.fight in a unit's state; absent from a unit that keeps
// none and from older units.
type fightWire struct {
	Total struct {
		Swings      uint64  `json:"swings"`
		HitsLanded  uint64  `json:"hits_landed"`
		DamageTaken float64 `json:"damage_taken"`
		Deaths      uint64  `json:"deaths"`
	} `json:"total"`
}

// totals converts a reading, refusing one that cannot be a count.
func (f *fightWire) totals() *FightTotals {
	if f == nil || math.IsNaN(f.Total.DamageTaken) || math.IsInf(f.Total.DamageTaken, 0) || f.Total.DamageTaken < 0 {
		return nil
	}
	return &FightTotals{Swings: f.Total.Swings, Hits: f.Total.HitsLanded, Deaths: f.Total.Deaths, Damage: f.Total.DamageTaken}
}

type openFight struct {
	FightTotals
	last time.Time
}

// observeFight turns changes in a unit's fight totals into fight_ended
// events. The first reading, and the first after the unit was offline or lost
// its fight field, only sets the baseline: totals carry the unit's whole
// history, none of which happened in this window.
func (t *track) observeFight(cur *FightTotals, now time.Time) []Event {
	if cur == nil {
		out := t.endFight(OutcomeOther, now)
		t.fightPrev = nil
		return out
	}
	prev := t.fightPrev
	c := *cur
	t.fightPrev = &c
	if prev == nil {
		return nil
	}
	d, ok := cur.Since(*prev)
	if !ok {
		// The unit restarted, so whatever fight was under way was cut off.
		return t.endFight(OutcomeOther, now)
	}
	if d.Swings > 0 || d.Hits > 0 || d.Damage > 0 {
		if t.fight == nil {
			t.fight = &openFight{}
		}
		t.fight.Swings += d.Swings
		t.fight.Hits += d.Hits
		t.fight.Damage += d.Damage
		t.fight.last = now
	}
	if t.fight == nil {
		return nil
	}
	t.fight.Deaths += d.Deaths
	switch {
	case t.fight.Deaths > 0:
		return t.endFight(OutcomeLoss, now)
	case now.Sub(t.fight.last) >= fightQuiet:
		return t.endFight(inferOutcome(t.fight.FightTotals), now)
	}
	return nil
}

// inferOutcome reads a finished fight's counts: a death is a loss, landing
// blows and living is a win, and anything else, such as taking damage or
// swinging at nothing, is a disengagement. The unit sends no verdict, so a
// win here means the unit survived having hit something, not that it killed it.
func inferOutcome(f FightTotals) string {
	switch {
	case f.Deaths > 0:
		return OutcomeLoss
	case f.Hits > 0:
		return OutcomeWin
	}
	return OutcomeDisengaged
}

// endFight closes the open fight, if any, with the given outcome.
func (t *track) endFight(outcome string, now time.Time) []Event {
	f := t.fight
	t.fight = nil
	if f == nil {
		return nil
	}
	level := "info"
	switch outcome {
	case OutcomeWin:
		level = "ok"
	case OutcomeLoss:
		level = "warn"
	}
	damage := math.Round(f.Damage*10) / 10
	return []Event{{
		At: now, Kind: "fight_ended", Level: level,
		Text:  fmt.Sprintf("fight ended: %s", outcome),
		Attrs: map[string]any{"swings": f.Swings, "hits": f.Hits, "damage": damage, "outcome": outcome},
	}}
}
