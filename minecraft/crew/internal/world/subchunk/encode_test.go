// The reference encoder and decoder come from dragonfly (MIT licence,
// Copyright (c) 2019 Dragonfly Tech). It is a test-only dependency: no
// dragonfly code is copied, and the production binary does not link it.
package subchunk_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// hashRegistry satisfies chunk.BlockRegistry without any block tables: the
// encoder writes palette values as given, so tests can use arbitrary hashes.
type hashRegistry struct{}

func (hashRegistry) BlockCount() int                                        { return 1 }
func (hashRegistry) AirRuntimeID() uint32                                   { return airHash }
func (hashRegistry) FilteringBlock(uint32) uint8                            { return 0 }
func (hashRegistry) LightBlock(uint32) uint8                                { return 0 }
func (hashRegistry) RandomTickBlock(uint32) bool                            { return false }
func (hashRegistry) NBTBlock(uint32) bool                                   { return false }
func (hashRegistry) LiquidDisplacingBlock(uint32) bool                      { return false }
func (hashRegistry) LiquidBlock(uint32) bool                                { return false }
func (hashRegistry) HashToRuntimeID(h uint32) (uint32, bool)                { return h, true }
func (hashRegistry) RuntimeIDToHash(r uint32) (uint32, bool)                { return r, true }
func (hashRegistry) StateToRuntimeID(string, map[string]any) (uint32, bool) { return 0, false }
func (hashRegistry) RuntimeIDToState(uint32) (string, map[string]any, bool) { return "", nil, false }

// airHash has the top bit set so that every palette entry in these tests
// exercises the sign handling of the varint encoding.
const airHash uint32 = 0xF0000001

var worldRange = cube.Range{-64, 319}

// blocks is a dense reference model of one layer in Bedrock's x, z, y order.
type blocks [4096]uint32

func idx(x, y, z int) int { return x<<8 | z<<4 | y }

// newEncoded builds a sub-chunk through dragonfly's own encoder from the
// reference layers and returns the network payload.
func newEncoded(t testing.TB, subY int, layers ...blocks) []byte {
	t.Helper()
	c := chunk.New(hashRegistry{}, worldRange)
	for l, b := range layers {
		for x := 0; x < 16; x++ {
			for y := 0; y < 16; y++ {
				for z := 0; z < 16; z++ {
					// SetBlock skips air on layers that do not exist yet, which
					// keeps the layer count equal to the reference.
					c.SetBlock(uint8(x), int16(subY*16+y), uint8(z), uint8(l), b[idx(x, y, z)])
				}
			}
		}
	}
	return chunk.EncodeSubChunk(c, chunk.NetworkEncoding, subY+4)
}

func filled(v uint32) blocks {
	var b blocks
	for i := range b {
		b[i] = v
	}
	return b
}

// scattered fills a layer with n distinct values (the first is air) placed at
// random positions.
func scattered(r *rand.Rand, n int) blocks {
	b := filled(airHash)
	for i := 1; i < n; i++ {
		v := airHash + uint32(i)*0x10001
		b[r.IntN(4096)] = v
	}
	// Guarantee every value occurs, so the palette really has n entries.
	for i := 1; i < n; i++ {
		b[i] = airHash + uint32(i)*0x10001
	}
	return b
}

func varint32(v int32) []byte {
	var buf bytes.Buffer
	_ = protocol.WriteVarint32(&buf, v)
	return buf.Bytes()
}
