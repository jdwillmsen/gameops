package camera

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// restDecoder reports a fixed biome payload as what follows the sections, and
// counts the section decodes asked of it.
type restDecoder struct {
	*NetworkDecoder
	chunks int
	rest   []byte
}

func (d *restDecoder) Chunk(payload []byte, count int) ([]PlacedSection, []byte, error) {
	d.chunks++
	secs, _, err := d.NetworkDecoder.Chunk(payload, count)
	return secs, d.rest, err
}

// The biomes of a LevelChunk come from what the one section decode leaves
// behind. Reading them from the packet instead means decoding every section a
// second time on the unit's packet loop.
func TestLevelChunkReadsItsBiomesFromTheSingleDecode(t *testing.T) {
	dec := &restDecoder{NetworkDecoder: NewNetworkDecoder(nil), rest: uniformBiome(9)}
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{0, 1000, 0}, 0, 0, 0)
	h := New(Options{Decoder: dec, Self: tr, MaxViewers: 1})
	h.Spawn(self)

	pk := terrainChunk(t, 3, 4)
	h.Apply(pk, &sink{})

	if dec.chunks != 1 {
		t.Fatalf("%d section decodes for one LevelChunk, want 1", dec.chunks)
	}
	b := h.heldBiomes([2]int32{3, 4})
	if len(b) != 13 || binary.LittleEndian.Uint16(b[11:]) != 9 {
		t.Fatalf("biome frame % x, want the single biome 9 the decoder reported", b)
	}
}
