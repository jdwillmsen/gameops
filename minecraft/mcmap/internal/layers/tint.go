package layers

import (
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Biomes is where the biome of a column is looked up: the reading of the
// world's biomes, which is the one of the surface and so is right for what
// the game tints, grass, foliage and water, and says nothing of the caves
// under it.
type Biomes interface {
	At(d chunks.Dimension, x, z int32) (biomes.Biome, bool)
}

// A biome's colours for what the game tints. The colour a block has in the
// table is the one it has in a plains biome, and a biome's own is laid on
// it as the ratio of the two, per channel.
type palette struct{ grass, foliage, water [3]uint8 }

func hex(v uint32) [3]uint8 { return rgbOf(v) }

var plains = palette{hex(0x91bd59), hex(0x77ab2f), hex(0x44aff5)}

// biomePalettes are matched against a biome's name in order, and the first
// whose word the name holds is the biome's. The numbers are this service's
// own reading of how each climate is coloured in the game.
var biomePalettes = []struct {
	word string
	palette
}{
	{"mangrove", palette{hex(0x8cb23c), hex(0x8db127), hex(0x4c6559)}},
	{"swamp", palette{hex(0x6a7039), hex(0x6a7039), hex(0x4c6559)}},
	{"dark_forest", palette{hex(0x507a32), hex(0x507a32), hex(0x1e97f2)}},
	{"roofed_forest", palette{hex(0x507a32), hex(0x507a32), hex(0x1e97f2)}},
	{"pale_garden", palette{hex(0x778272), hex(0x878d76), hex(0x1e97f2)}},
	{"birch", palette{hex(0x88bb67), hex(0x6ba941), hex(0x1e97f2)}},
	{"flower_forest", palette{hex(0x79c05a), hex(0x59ae30), hex(0x1e97f2)}},
	{"forest", palette{hex(0x79c05a), hex(0x59ae30), hex(0x1e97f2)}},
	{"cherry", palette{hex(0xb6db61), hex(0xb6db61), hex(0x5db7ef)}},
	{"jungle", palette{hex(0x59c93c), hex(0x30bb0b), hex(0x14a2c5)}},
	{"taiga", palette{hex(0x86b783), hex(0x68a464), hex(0x287082)}},
	{"grove", palette{hex(0x80b497), hex(0x60a17b), hex(0x14559b)}},
	{"ice", palette{hex(0x80b497), hex(0x60a17b), hex(0x14559b)}},
	{"frozen", palette{hex(0x80b497), hex(0x60a17b), hex(0x2570b5)}},
	{"snowy", palette{hex(0x80b497), hex(0x60a17b), hex(0x14559b)}},
	{"cold_ocean", palette{hex(0x8eb971), hex(0x71a74d), hex(0x2080c9)}},
	{"cold", palette{hex(0x8eb971), hex(0x71a74d), hex(0x2080c9)}},
	{"warm_ocean", palette{hex(0x8eb971), hex(0x71a74d), hex(0x02b0e5)}},
	{"lukewarm", palette{hex(0x8eb971), hex(0x71a74d), hex(0x0d96db)}},
	{"ocean", palette{hex(0x8eb971), hex(0x71a74d), hex(0x1787d4)}},
	{"river", palette{hex(0x8eb971), hex(0x71a74d), hex(0x0084ff)}},
	{"beach", palette{hex(0x91bd59), hex(0x77ab2f), hex(0x157cab)}},
	{"desert", palette{hex(0xbfb755), hex(0xaea42a), hex(0x32a598)}},
	{"savanna", palette{hex(0xbfb755), hex(0xaea42a), hex(0x2c8b9c)}},
	{"mesa", palette{hex(0x90814d), hex(0x9e814d), hex(0x4e7f81)}},
	{"badlands", palette{hex(0x90814d), hex(0x9e814d), hex(0x4e7f81)}},
	{"mushroom", palette{hex(0x55c93f), hex(0x2bbb0f), hex(0x8a8997)}},
	{"extreme_hills", palette{hex(0x8ab689), hex(0x6da36b), hex(0x007bf7)}},
	{"hills", palette{hex(0x8ab689), hex(0x6da36b), hex(0x007bf7)}},
	{"peaks", palette{hex(0x80b497), hex(0x60a17b), hex(0x007bf7)}},
	{"meadow", palette{hex(0x83bb6d), hex(0x63a948), hex(0x0e4ecf)}},
	{"hell", palette{hex(0xbfb755), hex(0xaea42a), hex(0x905957)}},
}

func paletteOf(biome string) palette {
	for _, p := range biomePalettes {
		if strings.Contains(biome, p.word) {
			return p.palette
		}
	}
	return plains
}

// tinter looks up the palette of the column at a block position, keeping
// the palette of each biome it has met.
type tinter struct {
	biomes Biomes
	dim    chunks.Dimension
	known  map[uint32]palette
}

func newTinter(b Biomes, d chunks.Dimension) *tinter {
	if b == nil {
		return nil
	}
	return &tinter{biomes: b, dim: d, known: map[uint32]palette{}}
}

// at is the palette of the column, or plains where no biome is stored.
func (t *tinter) at(x, z int32) *palette {
	if t == nil {
		return &plains
	}
	b, ok := t.biomes.At(t.dim, x, z)
	if !ok {
		return &plains
	}
	p, met := t.known[b.ID]
	if !met {
		p = paletteOf(b.Name)
		t.known[b.ID] = p
	}
	return &p
}

// tinted lays a palette's colour on a block's, by what the block is tinted
// by: the ratio of the palette's colour to plains', per channel, which
// leaves a plains column as the table has it.
func tinted(rgb [3]uint8, f Flags, p *palette) [3]uint8 {
	var tint, ref [3]uint8
	switch {
	case f&Water != 0:
		tint, ref = p.water, plains.water
	case f&TintGrass != 0:
		tint, ref = p.grass, plains.grass
	case f&TintFoliage != 0:
		tint, ref = p.foliage, plains.foliage
	default:
		return rgb
	}
	var out [3]uint8
	for i := range out {
		out[i] = uint8(min(int(rgb[i])*int(tint[i])/max(int(ref[i]), 1), 255))
	}
	return out
}
