package camera

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// terrainChunk is a legacy LevelChunk packet for a hilly overworld column: 24
// sections of stone, dirt, grass and water under an air sky, then the biome
// storages and the border byte, like a vanilla server sends on join.
func terrainChunk(tb testing.TB, cx, cz int32) *packet.LevelChunk {
	tb.Helper()
	finalizeRegistry.Do(world.DefaultBlockRegistry.Finalize)
	reg := world.DefaultBlockRegistry
	rid := func(name string, props map[string]any) uint32 {
		id, ok := reg.StateToRuntimeID(name, props)
		if !ok {
			tb.Fatalf("block %s unknown", name)
		}
		return id
	}
	stone := rid("minecraft:stone", map[string]any{"stone_type": "stone"})
	dirt := rid("minecraft:dirt", map[string]any{"dirt_type": "normal"})
	grass := rid("minecraft:grass_block", map[string]any{})
	water := rid("minecraft:water", map[string]any{"liquid_depth": int32(0)})
	bedrock := rid("minecraft:bedrock", map[string]any{"infiniburn_bit": false})

	c := chunk.New(reg, cube.Range{-64, 319})
	for x := uint8(0); x < 16; x++ {
		for z := uint8(0); z < 16; z++ {
			wx, wz := float64(int32(x)+cx*16), float64(int32(z)+cz*16)
			h := int16(64 + 10*math.Sin(wx/9) + 6*math.Cos(wz/7))
			for y := int16(-64); y <= h; y++ {
				b := stone
				switch {
				case y == -64:
					b = bedrock
				case y == h:
					b = grass
				case y > h-4:
					b = dirt
				}
				c.SetBlock(x, y, z, 0, b)
			}
			for y := h + 1; y <= 62; y++ {
				c.SetBlock(x, y, z, 0, water)
			}
			biome := uint32(1 + (int(wx)/48+int(wz)/48)&3)
			for y := int16(-64); y < 320; y += 4 {
				c.SetBiome(x, y, z, biome)
			}
		}
	}
	data := chunk.Encode(c, chunk.NetworkEncoding)
	var payload []byte
	for _, s := range data.SubChunks {
		payload = append(payload, s...)
	}
	payload = append(payload, data.Biomes...)
	payload = append(payload, 0)
	return &packet.LevelChunk{
		Position:      protocol.ChunkPos{cx, cz},
		SubChunkCount: uint32(len(data.SubChunks)),
		RawPayload:    payload,
	}
}

func benchHub(b *testing.B, viewers int) *Hub {
	b.Helper()
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{0, 1000, 0}, 0, 0, 0)
	h := New(Options{Decoder: NewNetworkDecoder(nil), Self: tr, MaxViewers: 4, QueueLimit: 1 << 20, Tick: time.Hour})
	h.Spawn(self)
	for i := 0; i < viewers; i++ {
		v, err := h.Attach()
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(v.Close)
	}
	return h
}

// BenchmarkLevelChunk is what the unit's packet loop pays for each chunk of
// the burst a client at view distance 8 receives on join.
func BenchmarkLevelChunk(b *testing.B) {
	for _, viewers := range []int{0, 1} {
		b.Run(fmt.Sprintf("viewers%d", viewers), func(b *testing.B) {
			const grid = 17 // (2*8+1)^2 = 289 chunks
			chunks := make([]*packet.LevelChunk, grid*grid)
			for i := range chunks {
				chunks[i] = terrainChunk(b, int32(i%grid), int32(i/grid))
			}
			h := benchHub(b, viewers)
			w := &sink{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.Apply(chunks[i%len(chunks)], w)
			}
		})
	}
}

// BenchmarkSetTime is the world clock packet, which a server sends every few
// seconds and the unit must read between two warrior decisions.
func BenchmarkSetTime(b *testing.B) {
	for _, viewers := range []int{0, 1} {
		b.Run(fmt.Sprintf("viewers%d", viewers), func(b *testing.B) {
			h := benchHub(b, viewers)
			w := &sink{}
			pk := &packet.SetTime{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pk.Time = int32(i % 24000)
				h.Apply(pk, w)
			}
		})
	}
}
