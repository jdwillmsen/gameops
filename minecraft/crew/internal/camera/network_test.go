package camera

import (
	"testing"

	"github.com/df-mc/dragonfly/server/world"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func varint(v uint32) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// uniform is a version 8 sub-chunk whose single layer is one palette entry
// (zero bits per block), followed by trailer.
func uniform(hash uint32, trailer ...byte) []byte {
	zigzag := uint32(int32(hash)<<1) ^ uint32(int32(hash)>>31)
	p := append([]byte{8, 1, 1}, varint(zigzag)...)
	return append(p, trailer...)
}

func stoneHash(t *testing.T) uint32 {
	t.Helper()
	reg := world.DefaultBlockRegistry
	rid, ok := reg.StateToRuntimeID("minecraft:stone", map[string]any{"stone_type": "stone"})
	if !ok {
		t.Fatal("stone is not in the block table")
	}
	hash, _ := reg.RuntimeIDToHash(rid)
	return hash
}

// Hashes with no entry in the table: the server is newer than the table.
const unknownHash = 0xdeadbeef

func counter(version string) float64 {
	return testutil.ToFloat64(metricUnmapped.WithLabelValues(version))
}

func TestNetworkDecoderReadsASubChunkPayload(t *testing.T) {
	sec, err := NewNetworkDecoder(nil).SubChunk(uniform(stoneHash(t)))
	if err != nil {
		t.Fatalf("SubChunk: %v", err)
	}
	if name := sec.Palette[sec.Cells[(3*16+2)*16+1]]; name != "minecraft:stone" {
		t.Errorf("block = %q, want minecraft:stone", name)
	}
}

// Sub-chunk packets append block entity data after the last storage; it must
// not make the payload undecodable.
func TestNetworkDecoderToleratesATrailer(t *testing.T) {
	if _, err := NewNetworkDecoder(nil).SubChunk(uniform(stoneHash(t), 0x0a, 0x00, 0x00, 0x00)); err != nil {
		t.Fatalf("SubChunk with trailer: %v", err)
	}
}

func TestNetworkDecoderReadsALegacyChunk(t *testing.T) {
	stone := stoneHash(t)
	payload := append(uniform(stone), uniform(unknownHash)...)
	payload = append(payload, 0x01, 0x00) // biomes
	secs, rest, err := NewNetworkDecoder(nil).Chunk(payload, 2)
	if err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if string(rest) != "\x01\x00" {
		t.Errorf("rest = % x, want the biome bytes", rest)
	}
	if len(secs) != 2 || secs[0].Y != -4 || secs[1].Y != -3 {
		t.Fatalf("sections = %+v", secs)
	}
	if secs[0].Section.Palette[0] != "minecraft:stone" || secs[1].Section.Palette[0] != unmappedBlock {
		t.Errorf("palettes = %v, %v", secs[0].Section.Palette, secs[1].Section.Palette)
	}
}

func TestNetworkDecoderCountsUnmappedHashes(t *testing.T) {
	var warned []string
	dec := NewNetworkDecoder(func(v string) { warned = append(warned, v) })
	dec.BeginSession("test-count")
	if got := counter("test-count"); got != 0 {
		t.Fatalf("counter before any block = %v", got)
	}

	sec, err := dec.SubChunk(uniform(unknownHash))
	if err != nil {
		t.Fatal(err)
	}
	if sec.Palette[0] != unmappedBlock {
		t.Errorf("unknown hash named %q, want %q", sec.Palette[0], unmappedBlock)
	}
	if _, ok := dec.BlockName(unknownHash); ok {
		t.Error("an unknown hash resolved")
	}
	if got := counter("test-count"); got != 2 {
		t.Errorf("counter = %v, want 2", got)
	}
	if len(warned) != 1 || warned[0] != "test-count" {
		t.Errorf("warnings = %v, want exactly one for the version", warned)
	}

	dec.BeginSession("test-count")
	dec.BlockName(unknownHash)
	if len(warned) != 2 {
		t.Errorf("a new session warned %d times in total, want 2", len(warned))
	}
}

func TestNetworkDecoderMappedBlocksAreNotCounted(t *testing.T) {
	dec := NewNetworkDecoder(func(string) { t.Error("warned for a known block") })
	dec.BeginSession("test-clean")
	if _, err := dec.SubChunk(uniform(stoneHash(t))); err != nil {
		t.Fatal(err)
	}
	if name, ok := dec.BlockName(stoneHash(t)); !ok || name != "minecraft:stone" {
		t.Errorf("BlockName = %q, %v", name, ok)
	}
	if got := counter("test-clean"); got != 0 {
		t.Errorf("counter = %v, want 0", got)
	}
}

func TestNetworkDecoderRejectsGarbage(t *testing.T) {
	if _, err := NewNetworkDecoder(nil).SubChunk([]byte{0xff, 0xff}); err == nil {
		t.Error("garbage decoded without error")
	}
}
