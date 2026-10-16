package subchunk_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

// Fixtures are sub-chunks captured from the lab server, with the vertical
// index in the header zeroed so no position is recorded.
func labFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("testdata/lab_*.bin")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = b
	}
	return out
}

// dragonflyDecode reads a payload the way the camera prototype did: dragonfly
// only decodes sub-chunks inside a whole chunk, so a minimal biome section is
// appended after the payload.
func dragonflyDecode(payload []byte) (*chunk.Chunk, error) {
	buf := bytes.NewBuffer(append([]byte(nil), payload...))
	buf.Write([]byte{0x01, 0x00})
	buf.Write(bytes.Repeat([]byte{0xff}, 23))
	return chunk.NetworkDecodeBuffer(hashRegistry{}, buf, 1, worldRange)
}

func TestLabFixturesMatchDragonfly(t *testing.T) {
	for name, payload := range labFixtures(t) {
		t.Run(name, func(t *testing.T) {
			sc, err := subchunk.Decode(payload)
			if err != nil {
				t.Fatal(err)
			}
			if len(sc.Trailer) != 0 {
				t.Fatalf("fixture has %d trailing bytes", len(sc.Trailer))
			}
			ref, err := dragonflyDecode(payload)
			if err != nil {
				t.Fatal(err)
			}
			// Y was zeroed, so the reference holds the sub-chunk at index 4.
			rs := ref.Sub()[4]
			if len(rs.Layers()) != len(sc.Layers) {
				t.Fatalf("layers %d, dragonfly %d", len(sc.Layers), len(rs.Layers()))
			}
			for l := range sc.Layers {
				for x := 0; x < 16; x++ {
					for y := 0; y < 16; y++ {
						for z := 0; z < 16; z++ {
							if g, w := sc.Layers[l].Block(x, y, z), rs.Block(byte(x), byte(y), byte(z), uint8(l)); g != w {
								t.Fatalf("layer %d (%d,%d,%d) = %#x, dragonfly %#x", l, x, y, z, g, w)
							}
						}
					}
				}
			}
		})
	}
}

// Sub-chunk packets append the sub-chunk's block entities (chests, spawners,
// signs) after the block storages. The prototype appended its biome section
// after that, so dragonfly read the entity data as a biome storage and failed
// with "size=5": the first byte of a compound tag is 0x0a. About 0.4% of the
// lab's sub-chunks hold a block entity.
func TestBlockEntityTrailerBreaksWrapperButNotDecoder(t *testing.T) {
	for name, payload := range labFixtures(t) {
		nbt := []byte("\n\x00\x03\x12BlockEntityVersion\x00\x01\b\x02id\x05Chest\x03\x01x\x00\x03\x01y\x00\x03\x01z\x00\x00")
		withEntity := append(append([]byte(nil), payload...), nbt...)

		if _, err := dragonflyDecode(withEntity); err == nil {
			t.Fatalf("%s: wrapped dragonfly decode unexpectedly succeeded", name)
		}
		sc, err := subchunk.Decode(withEntity)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(sc.Trailer, nbt) {
			t.Fatalf("%s: trailer %q", name, sc.Trailer)
		}
		return
	}
}
