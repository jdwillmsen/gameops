package camera

import (
	"slices"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

// maxBiomeColumns bounds the replay held for new viewers (about 300 bytes a
// column). Past it a random column is dropped, so a unit that wanders far
// keeps a current picture rather than the first one it saw.
const maxBiomeColumns = 4096

const dayCycleRule = "dodaylightcycle"

type worldClock struct {
	known   bool
	ticks   int32
	stopped bool
}

type biomeDef struct {
	name        string
	temperature float64
	downfall    float64
}

// SetClock gives the hub the world time from the server's game data. With the
// day cycle off the server freezes the clock at lockTime.
func (h *Hub) SetClock(worldTime int64, dayCycle bool, lockTime int32) {
	ticks := int32(worldTime % 24000)
	if !dayCycle {
		ticks = lockTime
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setClockLocked(worldClock{known: true, ticks: ticks, stopped: !dayCycle})
}

func (h *Hub) setClockLocked(c worldClock) {
	if h.clock == c {
		return
	}
	h.clock = c
	h.pushOrderedLocked(encodeTime(c.ticks, c.stopped))
}

func (h *Hub) setTime(p *packet.SetTime) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.clock
	c.known, c.ticks = true, p.Time
	h.setClockLocked(c)
}

func (h *Hub) gameRules(rules []protocol.GameRule) {
	for _, r := range rules {
		if !strings.EqualFold(r.Name, dayCycleRule) {
			continue
		}
		on, ok := r.Value.(bool)
		if !ok {
			continue
		}
		h.mu.Lock()
		c := h.clock
		c.stopped = !on
		h.setClockLocked(c)
		h.mu.Unlock()
	}
}

// biomeDefinitions remembers each biome's climate; the tint colours come from
// sampling the atlas's colour maps, so the table is built once both are known.
func (h *Hub) biomeDefinitions(p *packet.BiomeDefinitionList) {
	defs := make(map[uint16]biomeDef, len(p.BiomeDefinitions))
	for _, d := range p.BiomeDefinitions {
		if d.BiomeID < 0 || int(d.NameIndex) < 0 || int(d.NameIndex) >= len(p.StringList) {
			continue
		}
		defs[uint16(d.BiomeID)] = biomeDef{
			name:        strings.TrimPrefix(p.StringList[d.NameIndex], "minecraft:"),
			temperature: float64(d.Temperature),
			downfall:    float64(d.Downfall),
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.biomeDefs = defs
	h.rebuildBiomeTableLocked()
}

func (h *Hub) rebuildBiomeTableLocked() {
	if h.atlasObj == nil || len(h.biomeDefs) == 0 {
		return
	}
	table := h.atlasObj.Table()
	ids := make([]uint16, 0, len(h.biomeDefs))
	for id := range h.biomeDefs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	tints := make([]biomeTint, 0, len(ids))
	for _, id := range ids {
		d := h.biomeDefs[id]
		g := h.atlasObj.TintColor(atlas.TintGrass, d.temperature, d.downfall)
		f := h.atlasObj.TintColor(atlas.TintFoliage, d.temperature, d.downfall)
		w := table.WaterColor(d.name)
		tints = append(tints, biomeTint{ID: id, Grass: [3]uint8{g.R, g.G, g.B}, Foliage: [3]uint8{f.R, f.G, f.B}, Water: [3]uint8{w.R, w.G, w.B}, WaterAlpha: uint8(table.WaterAlpha(d.name)*255 + .5)})
	}
	h.biomeTable = encodeBiomeTable(tints)
	h.pushOrderedLocked(h.biomeTable)
}

// noteBiomes reads the biomes at the front of payload for the column at pos.
// They are optional decoration, so a payload that does not parse is dropped
// without counting as a decode failure.
func (h *Hub) noteBiomes(pos protocol.ChunkPos, payload []byte) {
	b, err := subchunk.TopBiomes(payload)
	if err != nil {
		return
	}
	frame := encodeBiomes(pos[0], pos[1], &b)
	key := [2]int32{pos[0], pos[1]}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, held := h.biomes[key]; !held && len(h.biomes) >= maxBiomeColumns {
		for k := range h.biomes {
			delete(h.biomes, k)
			break
		}
	}
	h.biomes[key] = frame
	h.broadcastLocked(func(v *Viewer) bool { return v.noteBiomes(key, frame) })
}

func (h *Hub) heldBiomes(col [2]int32) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.biomes[col]
}

// skyHeadLocked is what a new connection needs to light and tint the world;
// the biome columns follow with the sub-chunks they colour.
func (h *Hub) skyHeadLocked() [][]byte {
	var out [][]byte
	if h.clock.known {
		out = append(out, encodeTime(h.clock.ticks, h.clock.stopped))
	}
	if h.biomeTable != nil {
		out = append(out, h.biomeTable)
	}
	if h.weather != (weather{}) {
		out = append(out, encodeWeather(h.weather))
	}
	return out
}
