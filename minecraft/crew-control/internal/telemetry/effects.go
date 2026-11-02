package telemetry

import (
	"fmt"
	"slices"
	"time"
)

// effectOther stands for any effect whose name is not in the closed list: an
// id newer than the controller, or a name the unit invented. The public feed
// and the wall tiles show only closed names, never a unit's own text.
const effectOther = "other"

// effectNames is the units' own table of status effects.
var effectNames = map[string]bool{
	"speed": true, "slowness": true, "haste": true, "mining_fatigue": true,
	"strength": true, "instant_health": true, "instant_damage": true,
	"jump_boost": true, "nausea": true, "regeneration": true,
	"resistance": true, "fire_resistance": true, "water_breathing": true,
	"invisibility": true, "blindness": true, "night_vision": true,
	"hunger": true, "weakness": true, "poison": true, "wither": true,
	"health_boost": true, "absorption": true, "saturation": true,
	"levitation": true, "fatal_poison": true, "conduit_power": true,
	"slow_falling": true, "bad_omen": true, "village_hero": true, "darkness": true,
	"trial_omen": true, "wind_charged": true, "weaving": true, "oozing": true,
	"infested": true, "raid_omen": true,
}

// harmfulEffects are the ones that stop a unit doing its work or kill it.
var harmfulEffects = map[string]bool{
	"poison": true, "wither": true, "fatal_poison": true, "levitation": true, "darkness": true,
	"weakness": true, "slowness": true, "mining_fatigue": true, "nausea": true, "blindness": true,
}

// maxEffects bounds how many effects one state is read for. The game has 36,
// so a longer list is a unit gone wrong, and each entry could be an event.
const maxEffects = 64

// IsHarmfulEffect reports whether a closed effect name is one that needs an
// operator's attention.
func IsHarmfulEffect(name string) bool { return harmfulEffects[name] }

func closedEffect(name string) string {
	if effectNames[name] {
		return name
	}
	return effectOther
}

// closedEffects is the sorted, de-duplicated closed names a unit's effects
// map to. It is what the public Status carries.
func closedEffects(in []LiveEffect) []string {
	var out []string
	for i, e := range in {
		if i >= maxEffects {
			break
		}
		out = append(out, closedEffect(e.Name))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

type heldEffect struct {
	name      string
	amplifier int
}

// observeEffects reports each effect the unit gained or lost since the last
// poll. The first poll of a session, and the first after the unit was
// offline, only sets the baseline, so a restart does not announce every
// effect the units already had. An effect whose amplifier or time changes is
// the same effect and not an event.
func (t *track) observeEffects(in []LiveEffect, now time.Time) []Event {
	cur := make(map[int]heldEffect, len(in))
	for i, e := range in {
		if i >= maxEffects {
			break
		}
		cur[e.ID] = heldEffect{closedEffect(e.Name), min(max(e.Amplifier, 0), 255)}
	}
	prev, seeded := t.effects, t.effectsSeeded
	t.effects, t.effectsSeeded = cur, true
	if !seeded {
		return nil
	}
	var out []Event
	for _, id := range sortedIDs(cur) {
		if _, had := prev[id]; !had {
			out = append(out, effectEvent("effect_gained", "gained", cur[id], now))
		}
	}
	for _, id := range sortedIDs(prev) {
		if _, has := cur[id]; !has {
			out = append(out, effectEvent("effect_lost", "lost", prev[id], now))
		}
	}
	return out
}

func sortedIDs(m map[int]heldEffect) []int {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func effectEvent(kind, verb string, e heldEffect, now time.Time) Event {
	level := "info"
	if kind == "effect_gained" && harmfulEffects[e.name] {
		level = "warn"
	}
	return Event{
		At: now, Kind: kind, Level: level, Text: fmt.Sprintf("%s %s", verb, e.name),
		Attrs: map[string]any{"effect": e.name, "amplifier": e.amplifier},
	}
}
