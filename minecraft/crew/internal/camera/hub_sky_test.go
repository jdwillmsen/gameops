package camera

import (
	"encoding/binary"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func TestViewerGetsTheClockOnConnectAndWhenItChanges(t *testing.T) {
	h, _ := newLocatedHub(t, 2)
	h.SetClock(30000, true, 0)
	v, _ := h.Attach()
	ms, _ := drain(t, v)
	f := ofType(ms, frameTime)
	if len(f) != 1 || int32(binary.LittleEndian.Uint32(f[0][1:])) != 6000 || f[0][5] != 0 {
		t.Fatalf("connect time frames %v, want 6000 ticks running", f)
	}

	h.Apply(&packet.SetTime{Time: 13000}, &sink{})
	h.Apply(&packet.GameRulesChanged{GameRules: []protocol.GameRule{{Name: "dodaylightcycle", Value: false}}}, &sink{})
	ms, _ = drain(t, v)
	f = ofType(ms, frameTime)
	if len(f) != 2 || int32(binary.LittleEndian.Uint32(f[0][1:])) != 13000 || f[1][5] != timeStopped {
		t.Fatalf("update frames %v, want 13000 then stopped", f)
	}
}

func TestAStoppedClockSendsTheLockedTime(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	h.SetClock(99999, false, 18000)
	v, _ := h.Attach()
	ms, _ := drain(t, v)
	f := ofType(ms, frameTime)
	if len(f) != 1 || int32(binary.LittleEndian.Uint32(f[0][1:])) != 18000 || f[0][5] != timeStopped {
		t.Fatalf("frames %v, want 18000 stopped", f)
	}
}

func uniformBiome(id int32) []byte { return []byte{1, byte(id << 1), 0} }

func TestBiomesRideAheadOfTheirFirstSubChunk(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	h.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiome(5)}, &sink{})
	hold(h, [3]int32{0, 0, 0}, [3]int32{0, 1, 0})
	v, _ := h.Attach()
	ms, _ := drain(t, v)

	var order []byte
	for _, m := range ms {
		if m[0] == frameBiomes || m[0] == frameSubChunk {
			order = append(order, m[0])
		}
	}
	if string(order) != string([]byte{frameBiomes, frameSubChunk, frameSubChunk}) {
		t.Fatalf("order %v, want one biome frame then the two sub-chunks", order)
	}
	b := ofType(ms, frameBiomes)[0]
	if n := binary.LittleEndian.Uint16(b[9:]); n != 1 || binary.LittleEndian.Uint16(b[11:]) != 5 || len(b) != 13 {
		t.Fatalf("biome frame % x, want a one-entry palette of biome 5", b)
	}

	// A resent column reaches a viewer that already has it, in order.
	h.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: uniformBiome(7)}, &sink{})
	ms, _ = drain(t, v)
	if b := ofType(ms, frameBiomes); len(b) != 1 || binary.LittleEndian.Uint16(b[0][11:]) != 7 {
		t.Fatalf("update frames %v, want the new biome", b)
	}
}

func TestMalformedBiomesAreIgnored(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	h.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}, SubChunkLimit: protocol.Option(int32(-1)), RawPayload: []byte{9, 9}}, &sink{})
	if h.Stats().DecodeFailures != 0 || len(h.biomes) != 0 {
		t.Fatalf("failures %d, held %d; biomes are optional", h.Stats().DecodeFailures, len(h.biomes))
	}
}

func TestBiomeFrameIndexesItsPalette(t *testing.T) {
	var b subchunk.ColumnBiomes
	b[0], b[255] = 3, 9
	f := encodeBiomes(-2, 4, &b)
	if n := binary.LittleEndian.Uint16(f[9:]); n != 3 || len(f) != 11+6+256 {
		t.Fatalf("palette %d, length %d; want 3 entries and 256 indices", n, len(f))
	}
	idx := f[17:]
	if idx[0] != 0 || idx[1] != 1 || idx[255] != 2 {
		t.Fatalf("indices %d %d %d, want 0 1 2", idx[0], idx[1], idx[255])
	}
}

func biomeDefs() *packet.BiomeDefinitionList {
	return &packet.BiomeDefinitionList{
		StringList: []string{"minecraft:plains", "swamp"},
		BiomeDefinitions: []protocol.BiomeDefinition{
			{NameIndex: 0, BiomeID: 1, Temperature: 0.8, Downfall: 0.4},
			{NameIndex: 1, BiomeID: 6, Temperature: 0.8, Downfall: 0.9},
		},
	}
}

func TestBiomeTableNeedsBothTheDefinitionsAndTheAtlas(t *testing.T) {
	for name, order := range map[string]func(h *Hub){
		"definitions first": func(h *Hub) { h.Apply(biomeDefs(), &sink{}); _ = h.SetAtlas(testAtlas(t)) },
		"atlas first":       func(h *Hub) { _ = h.SetAtlas(testAtlas(t)); h.Apply(biomeDefs(), &sink{}) },
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newLocatedHub(t, 2)
			v, _ := h.Attach()
			drain(t, v)
			order(h)
			tables := ofType(mustDrain(t, v), frameBiomeTable)
			if len(tables) != 1 {
				t.Fatalf("live viewer got %d tables, want 1", len(tables))
			}
			f := tables[0]
			if n := binary.LittleEndian.Uint16(f[1:]); n != 2 || len(f) != 3+2*12 {
				t.Fatalf("entries %d, length %d", n, len(f))
			}
			swamp := f[3+12:]
			if id := binary.LittleEndian.Uint16(swamp); id != 6 {
				t.Fatalf("second entry id %d, want 6", id)
			}
			if w := swamp[8:11]; w[0] != 0x61 || w[1] != 0x7b || w[2] != 0x64 {
				t.Errorf("swamp water % x, want the pack's 617b64", w)
			}
			late, _ := h.Attach()
			if len(ofType(mustDrain(t, late), frameBiomeTable)) != 1 {
				t.Error("a late viewer's head lacks the table")
			}
		})
	}
}
