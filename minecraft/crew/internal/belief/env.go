package belief

import (
	"math"
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

// Day phases, from the world clock. A Bedrock day is 24000 ticks at 20 a
// second; the phases follow the game's own light and mob rules, not the
// calendar: monsters burn until dusk and spawn freely from night.
const (
	PhaseDawn  = "dawn"
	PhaseDay   = "day"
	PhaseDusk  = "dusk"
	PhaseNight = "night"

	dayLength = 24000
	// The tick each phase begins at; night runs through the end of the day
	// into dawn, which wraps past zero into day.
	duskStarts  = 12000
	nightStarts = 13800
	dawnStarts  = 22200

	ticksPerSecond = 20

	dayCycleRule = "dodaylightcycle"

	// maxBiomeColumns bounds the biome columns held (512 bytes each); past
	// it one is dropped, so a unit that wanders keeps its surroundings.
	maxBiomeColumns = 1024

	// BiomeOther stands for any biome the closed list does not name.
	BiomeOther = "other"
)

// Env is what the unit knows of the world around it, from the packets alone.
type Env struct {
	// DayPhase is empty until the server has told the unit the time.
	DayPhase string `json:"day_phase,omitempty"`
	// Biome is a name from a fixed list, or other; empty while the unit's
	// column, or the server's biome table, is not known.
	Biome   string `json:"biome,omitempty"`
	Raining bool   `json:"raining"`
}

// biomeNames is the closed list of biomes a unit reports by name.
var biomeNames = map[string]bool{
	"plains": true, "sunflower_plains": true, "desert": true, "extreme_hills": true, "forest": true,
	"flower_forest": true, "taiga": true, "swampland": true, "mangrove_swamp": true, "river": true,
	"frozen_river": true, "ice_plains": true, "ice_plains_spikes": true, "ice_mountains": true,
	"mushroom_island": true, "beach": true, "cold_beach": true, "stone_beach": true, "jungle": true,
	"jungle_edge": true, "bamboo_jungle": true, "birch_forest": true, "birch_forest_hills": true,
	"roofed_forest": true, "savanna": true, "savanna_plateau": true, "mesa": true, "mesa_plateau": true,
	"mesa_bryce": true, "ocean": true, "deep_ocean": true, "cold_ocean": true, "deep_cold_ocean": true,
	"lukewarm_ocean": true, "deep_lukewarm_ocean": true, "warm_ocean": true, "frozen_ocean": true,
	"deep_frozen_ocean": true, "meadow": true, "grove": true, "snowy_slopes": true, "jagged_peaks": true,
	"frozen_peaks": true, "stony_peaks": true, "cherry_grove": true, "dripstone_caves": true,
	"lush_caves": true, "deep_dark": true, "pale_garden": true, "extreme_hills_edge": true,
	"extreme_hills_plus_trees": true, "taiga_hills": true, "cold_taiga": true, "mega_taiga": true,
	"redwood_taiga": true, "forest_hills": true, "swampland_mutated": true, "stone_beach_mutated": true,
	"nether_wastes": true, "soul_sand_valley": true, "crimson_forest": true, "warped_forest": true,
	"basalt_deltas": true, "the_end": true, "end_highlands": true, "end_midlands": true,
	"small_end_islands": true, "end_barrens": true, "hell": true,
}

// ClosedBiome maps a server biome name onto the fixed list.
func ClosedBiome(name string) string {
	name = strings.TrimPrefix(name, "minecraft:")
	if biomeNames[name] {
		return name
	}
	return BiomeOther
}

// PhaseOf names the phase a time of day falls in; ticks may be any value,
// including a day count folded in or a negative.
func PhaseOf(ticks int64) string {
	t := ((ticks % dayLength) + dayLength) % dayLength
	switch {
	case t >= dawnStarts:
		return PhaseDawn
	case t >= nightStarts:
		return PhaseNight
	case t >= duskStarts:
		return PhaseDusk
	}
	return PhaseDay
}

// clock is the last time the server gave and when it gave it, so a status
// asked for seconds later reads the time now, not the time then.
type clock struct {
	known   bool
	ticks   int64
	at      time.Time
	stopped bool
}

func (c clock) now(at time.Time) int64 {
	if c.stopped {
		return c.ticks
	}
	elapsed := at.Sub(c.at)
	if elapsed < 0 {
		elapsed = 0
	}
	return c.ticks + int64(elapsed.Seconds()*ticksPerSecond)
}

type envState struct {
	clock     clock
	raining   bool
	biomeDefs map[uint16]string
	biomes    map[[2]int32]subchunk.ColumnBiomes
}

// SetClock gives the tracker the world time from the server's game data. With
// the day cycle off the server freezes the clock at lockTime.
func (t *Tracker) SetClock(worldTime int64, dayCycle bool, lockTime int32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ticks := worldTime
	if !dayCycle {
		ticks = int64(lockTime)
	}
	t.env.clock = clock{known: true, ticks: ticks, at: t.now(), stopped: !dayCycle}
}

// applyEnvLocked reports whether the packet was one the environment reads.
func (t *Tracker) applyEnvLocked(pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.SetTime:
		c := t.env.clock
		c.known, c.ticks, c.at = true, int64(p.Time), t.now()
		t.env.clock = c
	case *packet.GameRulesChanged:
		for _, r := range p.GameRules {
			if on, ok := r.Value.(bool); ok && strings.EqualFold(r.Name, dayCycleRule) {
				c := t.env.clock
				// Freeze at the time now, or resume from it.
				c.ticks, c.at, c.stopped = c.now(t.now()), t.now(), !on
				t.env.clock = c
			}
		}
	case *packet.LevelEvent:
		switch p.EventType {
		case packet.LevelEventStartRaining:
			t.env.raining = true
		case packet.LevelEventStopRaining:
			t.env.raining = false
		}
	case *packet.BiomeDefinitionList:
		t.env.biomeDefs = biomeTable(p)
	case *packet.LevelChunk:
		t.noteBiomesLocked(p)
	default:
		return false
	}
	return true
}

func biomeTable(p *packet.BiomeDefinitionList) map[uint16]string {
	defs := make(map[uint16]string, len(p.BiomeDefinitions))
	for _, d := range p.BiomeDefinitions {
		if d.BiomeID < 0 || d.NameIndex < 0 || int(d.NameIndex) >= len(p.StringList) {
			continue
		}
		defs[uint16(d.BiomeID)] = ClosedBiome(p.StringList[d.NameIndex])
	}
	return defs
}

// noteBiomesLocked keeps the biomes of a chunk in the unit's own dimension.
// They are decoration to the status, so a payload that does not parse is
// dropped quietly.
func (t *Tracker) noteBiomesLocked(p *packet.LevelChunk) {
	if p.Dimension != t.s.Dimension || p.CacheEnabled {
		return
	}
	payload := p.RawPayload
	if _, requestMode := p.SubChunkLimit.Value(); !requestMode {
		rest, err := subchunk.Skip(payload, int(p.SubChunkCount))
		if err != nil {
			return
		}
		payload = rest
	}
	b, err := subchunk.TopBiomes(payload)
	if err != nil {
		return
	}
	if t.env.biomes == nil {
		t.env.biomes = map[[2]int32]subchunk.ColumnBiomes{}
	}
	key := [2]int32{p.Position[0], p.Position[1]}
	if _, held := t.env.biomes[key]; !held && len(t.env.biomes) >= maxBiomeColumns {
		for k := range t.env.biomes {
			delete(t.env.biomes, k)
			break
		}
	}
	t.env.biomes[key] = b
}

func (t *Tracker) envLocked() Env {
	e := Env{Raining: t.env.raining}
	if t.env.clock.known {
		e.DayPhase = PhaseOf(t.env.clock.now(t.now()))
	}
	if t.s.PositionKnown && !math.IsNaN(float64(t.s.Position[0])) && !math.IsNaN(float64(t.s.Position[2])) {
		x, z := int32(math.Floor(float64(t.s.Position[0]))), int32(math.Floor(float64(t.s.Position[2])))
		if col, ok := t.env.biomes[[2]int32{x >> 4, z >> 4}]; ok {
			if name, known := t.env.biomeDefs[col[(x&15)<<4|(z&15)]]; known {
				e.Biome = name
			}
		}
	}
	return e
}
