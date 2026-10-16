package camera

import (
	"testing"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

// Round-trips real network bytes through the dragonfly decoder, so a library
// bump that changes the wire format fails here and not on a live server.
func TestDragonflyDecoderReadsASubChunkPayload(t *testing.T) {
	dec := NewDragonflyDecoder()
	reg := world.DefaultBlockRegistry
	stone, ok := reg.StateToRuntimeID("minecraft:stone", map[string]any{"stone_type": "stone"})
	if !ok {
		t.Fatal("stone is not in the block table")
	}
	// The server puts network block hashes where dragonfly expects runtime IDs.
	hash, _ := reg.RuntimeIDToHash(stone)
	c := chunk.New(reg, cube.Range{minY, maxY})
	c.SetBlock(1, 0, 2, 0, hash) // y=0 lives in sub-chunk index 4
	payload := chunk.EncodeSubChunk(c, chunk.NetworkEncoding, 4)

	sec, err := dec.SubChunk(payload)
	if err != nil {
		t.Fatalf("SubChunk: %v", err)
	}
	name := sec.Palette[sec.Cells[(0*16+2)*16+1]]
	if name != "minecraft:stone" {
		t.Errorf("block = %q, want minecraft:stone", name)
	}
	if other := sec.Palette[sec.Cells[0]]; other == "minecraft:stone" {
		t.Error("an untouched cell decoded as stone")
	}
}

func TestDragonflyDecoderResolvesBlockUpdateHashes(t *testing.T) {
	dec := NewDragonflyDecoder()
	reg := world.DefaultBlockRegistry
	rid, _ := reg.StateToRuntimeID("minecraft:stone", map[string]any{"stone_type": "stone"})
	hash, _ := reg.RuntimeIDToHash(rid)
	if name, ok := dec.BlockName(hash); !ok || name != "minecraft:stone" {
		t.Errorf("BlockName = %q, %v", name, ok)
	}
	if _, ok := dec.BlockName(0xdeadbeef); ok {
		t.Error("an unknown hash resolved")
	}
}

func TestDragonflyDecoderRejectsGarbage(t *testing.T) {
	if _, err := NewDragonflyDecoder().SubChunk([]byte{0xff, 0xff}); err == nil {
		t.Error("garbage decoded without error")
	}
}
