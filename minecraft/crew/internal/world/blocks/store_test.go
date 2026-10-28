package blocks

import (
	"os"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	stoneHash uint32 = 2150698529
)

type table map[uint32]string

func (t table) BlockName(h uint32) (string, bool) { n, ok := t[h]; return n, ok }

var names = table{airHash: "minecraft:air", stoneHash: "minecraft:stone", 7: "minecraft:chest"}

type sink struct{ sent []packet.Packet }

func (s *sink) WritePacket(p packet.Packet) error { s.sent = append(s.sent, p); return nil }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../subchunk/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func subChunkPacket(x, y, z int32, payload []byte) *packet.SubChunk {
	return &packet.SubChunk{
		Position: protocol.SubChunkPos{x, y, z},
		SubChunkEntries: []protocol.SubChunkEntry{{
			Result:     protocol.SubChunkResultSuccess,
			RawPayload: protocol.Option(payload),
		}},
	}
}

// uniformPayload is a version 8 sub-chunk with one layer holding a single
// block, which is how the server sends solid stone and all-air sections.
func uniformPayload(hash uint32) []byte {
	out := []byte{8, 1, 1} // version, one layer, header for zero bits
	// Palette of one zigzag varint32.
	v := uint32(hash) << 1
	if int32(hash) < 0 {
		v = ^v
	}
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func TestStoreAnswersBlocksFromSubChunks(t *testing.T) {
	payload := fixture(t, "lab_1layer_2bit.bin")
	sc, err := subchunk.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	// Name every palette entry after its own hash so each lookup is checkable.
	n := table{}
	for _, h := range sc.Layers[0].Palette {
		n[h] = "minecraft:b" + strings.Repeat("x", int(h%5))
	}
	s := New(n, 64)
	s.Apply(subChunkPacket(2, -1, 3, payload), &sink{})

	if len(sc.Layers[0].Palette) < 2 {
		t.Fatal("fixture needs two palette entries")
	}
	for _, l := range [][3]int{{0, 0, 0}, {5, 9, 3}, {15, 15, 15}} {
		want := n[sc.Layers[0].Block(l[0], l[1], l[2])][len("minecraft:"):]
		pos := protocol.BlockPos{2*16 + int32(l[0]), -1*16 + int32(l[1]), 3*16 + int32(l[2])}
		got, known := s.Block(pos)
		if !known || got != want {
			t.Errorf("Block(%v) = %q, %v; want %q", l, got, known, want)
		}
	}
	if _, known := s.Block(protocol.BlockPos{0, 0, 0}); known {
		t.Error("a block outside every received sub-chunk was reported as known")
	}
}

func TestUnmappedHashesAreUnknown(t *testing.T) {
	s := New(table{}, 8)
	s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
	if name, known := s.Block(protocol.BlockPos{1, 1, 1}); known {
		t.Errorf("unmapped hash answered %q as known", name)
	}
}

func TestEmptySubChunksAreAir(t *testing.T) {
	s := New(names, 8)
	s.Apply(&packet.SubChunk{Position: protocol.SubChunkPos{1, 0, 1}, SubChunkEntries: []protocol.SubChunkEntry{
		{Result: protocol.SubChunkResultSuccessAllAir},
		{Offset: protocol.SubChunkOffset{1, 0, 0}, Result: protocol.SubChunkResultSuccess, RawPayload: protocol.Option([]byte{8, 0})},
	}}, &sink{})
	for _, x := range []int32{16, 32} {
		if name, known := s.Block(protocol.BlockPos{x, 3, 16}); !known || name != "air" {
			t.Errorf("x=%d: %q, %v; want air", x, name, known)
		}
	}
}

func TestUpdateBlockOverwritesLayerZero(t *testing.T) {
	s := New(names, 8)
	s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
	pos := protocol.BlockPos{3, 4, 5}
	s.Apply(&packet.UpdateBlock{Position: pos, NewBlockRuntimeID: airHash, Layer: 0}, &sink{})
	if name, _ := s.Block(pos); name != "air" {
		t.Errorf("after layer 0 update: %q", name)
	}
	s.Apply(&packet.UpdateBlock{Position: pos, NewBlockRuntimeID: 7, Layer: 1}, &sink{})
	if name, _ := s.Block(pos); name != "air" {
		t.Errorf("a layer 1 update changed the block to %q", name)
	}
	if name, _ := s.Block(protocol.BlockPos{3, 4, 6}); name != "stone" {
		t.Errorf("neighbour = %q, want stone", name)
	}
	s.Apply(&packet.UpdateSubChunkBlocks{Blocks: []protocol.BlockChangeEntry{{BlockPos: protocol.BlockPos{9, 9, 9}, BlockRuntimeID: 7}}}, &sink{})
	if name, _ := s.Block(protocol.BlockPos{9, 9, 9}); name != "chest" {
		t.Errorf("after UpdateSubChunkBlocks: %q", name)
	}
}

func TestUpdateBlockInAnUnknownSubChunkIsIgnored(t *testing.T) {
	s := New(names, 8)
	s.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{3, 4, 5}, NewBlockRuntimeID: stoneHash}, &sink{})
	if _, known := s.Block(protocol.BlockPos{3, 4, 5}); known {
		t.Error("an update invented a sub-chunk")
	}
}

func TestChangeDimensionForgetsEverything(t *testing.T) {
	s := New(names, 8)
	s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
	s.Apply(&packet.ChangeDimension{Dimension: 1}, &sink{})
	if _, known := s.Block(protocol.BlockPos{1, 1, 1}); known {
		t.Error("blocks survived a dimension change")
	}
}

func TestResetForgetsEverything(t *testing.T) {
	s := New(names, 8)
	s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
	s.Reset()
	if _, known := s.Block(protocol.BlockPos{1, 1, 1}); known {
		t.Error("blocks survived a reset")
	}
}

func TestOtherDimensionsAreIgnored(t *testing.T) {
	s := New(names, 8)
	w := &sink{}
	p := subChunkPacket(0, 0, 0, uniformPayload(stoneHash))
	p.Dimension = 1
	s.Apply(p, w)
	s.Apply(&packet.LevelChunk{Dimension: 1, SubChunkLimit: protocol.Option(int32(9))}, w)
	if _, known := s.Block(protocol.BlockPos{1, 1, 1}); known || len(w.sent) != 0 {
		t.Errorf("another dimension reached the store: sent=%d", len(w.sent))
	}
}

func TestStoreEvictsTheFarthestSubChunkPastTheCap(t *testing.T) {
	s := New(names, 4)
	w := &sink{}
	// The latest LevelChunk is the reference point for what counts as far.
	s.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}}, w)
	for x := int32(0); x < 4; x++ {
		s.Apply(subChunkPacket(x, 0, 0, uniformPayload(stoneHash)), w)
	}
	s.Apply(subChunkPacket(-1, 0, 0, uniformPayload(stoneHash)), w)

	if _, known := s.Block(protocol.BlockPos{3 * 16, 0, 0}); known {
		t.Error("the farthest sub-chunk was kept")
	}
	for _, x := range []int32{-16, 0, 16, 32} {
		if _, known := s.Block(protocol.BlockPos{x, 0, 0}); !known {
			t.Errorf("sub-chunk at x=%d was evicted", x)
		}
	}
}

func TestLevelChunkWritesOneSubChunkRequest(t *testing.T) {
	s := New(names, 8)
	w := &sink{}
	s.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{3, -5}, SubChunkLimit: protocol.Option(int32(9))}, w)
	if len(w.sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(w.sent))
	}
	req := w.sent[0].(*packet.SubChunkRequest)
	if req.Position != (protocol.SubChunkPos{3, 0, -5}) || len(req.Offsets) != 9 ||
		req.Offsets[0] != (protocol.SubChunkOffset{0, -4, 0}) || req.Offsets[8] != (protocol.SubChunkOffset{0, 4, 0}) {
		t.Errorf("request = %+v", req)
	}
}

func TestLevelChunkWithoutRequestModeWritesNothing(t *testing.T) {
	s := New(names, 8)
	w := &sink{}
	s.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{1, 1}}, w)
	if len(w.sent) != 0 {
		t.Errorf("sent %d packets", len(w.sent))
	}
}

func TestBlockHashReportsWhatTheServerSent(t *testing.T) {
	s := New(names, 8)
	pos := protocol.BlockPos{3, 4, 5}
	if _, known := s.BlockHash(pos); known {
		t.Error("a hash was invented for an unknown sub-chunk")
	}
	s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
	if h, known := s.BlockHash(pos); !known || h != stoneHash {
		t.Errorf("hash = %d, %v; want stone", h, known)
	}
	s.Apply(&packet.UpdateBlock{Position: pos, NewBlockRuntimeID: 7}, &sink{})
	if h, _ := s.BlockHash(pos); h != 7 {
		t.Errorf("after update hash = %d, want 7", h)
	}
}

// The server announces chunks in no useful order, so the latest one can sit
// at the far edge of the view. Eviction has to measure from the point the
// server publishes chunks around, which is the unit, or the blocks under the
// unit are the first to go once the store is full.
func TestEvictionKeepsTheBlocksAroundThePublisherCentre(t *testing.T) {
	s := New(names, 48)
	w := &sink{}
	stone := uniformPayload(stoneHash)
	s.Apply(&packet.NetworkChunkPublisherUpdate{Position: protocol.BlockPos{8, 100, 8}, Radius: 128}, w)
	for _, c := range [][2]int32{{0, 0}, {5, 5}, {6, 6}} {
		s.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{c[0], c[1]}}, w)
		for y := int32(-4); y < 20; y++ {
			s.Apply(subChunkPacket(c[0], y, c[1], stone), w)
		}
	}
	if name, known := s.Block(protocol.BlockPos{8, 99, 8}); !known || name != "stone" {
		t.Fatalf("block under the unit = %q, known %v; want stone", name, known)
	}
}

type stateTable struct{ table }

func (s stateTable) BlockState(h uint32) (string, map[string]any, bool) {
	n, ok := s.table[h]
	return n, map[string]any{"half": "top"}, ok
}

func TestStateCarriesTheBlocksStatesWhenTheNameTableHasThem(t *testing.T) {
	pos := protocol.BlockPos{1, 2, 3}
	for _, tc := range []struct {
		names Names
		props map[string]any
	}{
		{stateTable{names}, map[string]any{"half": "top"}},
		{names, nil},
	} {
		s := New(tc.names, 4)
		s.Apply(subChunkPacket(0, 0, 0, uniformPayload(stoneHash)), &sink{})
		name, props, known := s.State(pos)
		if name != "stone" || !known || len(props) != len(tc.props) {
			t.Errorf("State = %q %v known=%v, want stone %v", name, props, known, tc.props)
		}
	}
	if _, _, known := New(names, 4).State(pos); known {
		t.Error("a block in an unreceived sub-chunk must be unknown")
	}
}

// A column's SubChunkLimit stops at its highest non-empty section; the server
// sends nothing above it, and a builder stamping on open ground works there.
func TestSectionsAboveTheAnnouncedLimitAreAir(t *testing.T) {
	s := New(names, 8)
	w := &sink{}
	s.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{0, 0}, SubChunkLimit: protocol.Option(int32(8))}, w)

	if name, known := s.Block(protocol.BlockPos{3, 64, 3}); !known || name != "air" {
		t.Errorf("above the limit = %q known=%v, want air", name, known)
	}
	if h, known := s.BlockHash(protocol.BlockPos{3, 200, 3}); !known || h != airHash {
		t.Errorf("hash above the limit = %d known=%v", h, known)
	}
	if _, known := s.Block(protocol.BlockPos{3, 10, 3}); known {
		t.Error("a section inside the limit that was never received must stay unknown")
	}
	if _, known := s.Block(protocol.BlockPos{40, 64, 3}); known {
		t.Error("another column was never announced")
	}

	s.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{3, 64, 3}, NewBlockRuntimeID: stoneHash}, w)
	if name, known := s.Block(protocol.BlockPos{3, 64, 3}); !known || name != "stone" {
		t.Errorf("a block placed above the limit = %q known=%v, want stone", name, known)
	}
	s.Reset()
	if _, known := s.Block(protocol.BlockPos{3, 64, 3}); known {
		t.Error("a reset must forget the announced columns")
	}
}
