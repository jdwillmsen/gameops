package belief

import (
	"cmp"
	"slices"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Effect is one status effect the server has put on the unit. Duration is
// in ticks, and negative means it does not expire.
type Effect struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Amplifier int    `json:"amplifier"`
	Duration  int    `json:"duration"`
}

// effectNames are the ids the game sends, without the minecraft: prefix.
// The names are Bedrock's own, so the hero effect is village_hero here and
// not Java's hero_of_the_village. An id the table does not know keeps an
// empty name; the reader then has the number.
var effectNames = map[int32]string{
	1: "speed", 2: "slowness", 3: "haste", 4: "mining_fatigue",
	5: "strength", 6: "instant_health", 7: "instant_damage",
	8: "jump_boost", 9: "nausea", 10: "regeneration",
	11: "resistance", 12: "fire_resistance", 13: "water_breathing",
	14: "invisibility", 15: "blindness", 16: "night_vision",
	17: "hunger", 18: "weakness", 19: "poison", 20: "wither",
	21: "health_boost", 22: "absorption", 23: "saturation",
	24: "levitation", 25: "fatal_poison", 26: "conduit_power",
	27: "slow_falling", 28: "bad_omen", 29: "village_hero", 30: "darkness",
	31: "trial_omen", 32: "wind_charged", 33: "weaving", 34: "oozing",
	35: "infested", 36: "raid_omen",
}

// applyEffectLocked records what the server did to the unit's own effects.
// The save holds none of them: they are live state or they are nothing.
func (t *Tracker) applyEffectLocked(p *packet.MobEffect) {
	if p.EntityRuntimeID != t.self {
		return
	}
	if t.effects == nil {
		t.effects = map[int32]Effect{}
	}
	switch p.Operation {
	case packet.MobEffectAdd, packet.MobEffectModify:
		t.effects[p.EffectType] = Effect{
			ID:        int(p.EffectType),
			Name:      effectNames[p.EffectType],
			Amplifier: int(p.Amplifier),
			Duration:  int(p.Duration),
		}
	case packet.MobEffectRemove:
		delete(t.effects, p.EffectType)
	}
}

func (t *Tracker) effectsLocked() []Effect {
	out := make([]Effect, 0, len(t.effects))
	for _, e := range t.effects {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Effect) int { return cmp.Compare(a.ID, b.ID) })
	return out
}
