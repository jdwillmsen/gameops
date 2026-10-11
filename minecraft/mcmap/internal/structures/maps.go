package structures

import "github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"

// The game's own explorer maps say where it worked a structure out to be.
// Each map is one record under map_, an NBT compound whose decorations are
// the marks drawn on it; a mark for a structure holds, in key, the block
// the game found the structure at, and in data the mark's type, which for
// a woodland explorer map is the mansion's. The game finds that block from
// the seed, without generating anything, so a map can point into country
// nobody has been to, and a map made before a world was given another
// seed points at a mansion that will never be built.
//
// So a map's target is not a structure. It is the game's own answer to
// the question a rule answers, and is what a rule for a kind the world
// has built none of can be checked against.
var mapPrefix = []byte("map_")

const (
	// maxMapTargets is how many places the maps of one world are kept as
	// pointing to. The FWB world's 55,870 maps point to 56.
	maxMapTargets = 4096
	// markMansion is the type of a woodland mansion's mark.
	markMansion = 14
)

// markKinds is the kinds a map's mark is read for, by its type.
var markKinds = map[int64]Kind{markMansion: Mansion}

// target is the chunk a map says a structure of a kind is in.
type target struct {
	kind Kind
	dim  chunks.Dimension
	site Site
}

// mapRecord keeps where one map points. A map that does not parse, or that
// names a dimension there is none of, is passed over and counted.
func (c *contents) mapRecord(v []byte) {
	if len(v) > maxContentRecord {
		c.stats.Skipped++
		return
	}
	var (
		dim    int64
		marks  []byte
		hasDim bool
	)
	if _, err := fieldsAt(v, func(field []byte, tag byte, payload []byte) {
		switch string(field) {
		case "dimension":
			dim, hasDim = wholeOf(tag, payload)
		case "decorations":
			if tag == tagList {
				marks = payload
			}
		}
	}); err != nil {
		c.stats.Skipped++
		return
	}
	// Nearly every map is one a player drew and points nowhere. Its
	// picture is skipped over by its length, so such a map costs the dozen
	// tags it has.
	d := chunks.Dimension(dim)
	if marks == nil {
		return
	}
	// A map that does not say which dimension it is of is not taken for
	// the overworld's: where it points is then somewhere in none.
	if !hasDim || dim < 0 || dim > int64(chunks.End) {
		if len(marks) >= 5 && marks[0] == tagCompound {
			c.stats.Skipped++
		}
		return
	}
	if _, err := eachCompound(tagList, marks, func(mark []byte) error {
		var (
			sort                int64
			x, z                int32
			hasSort, hasX, hasZ bool
		)
		err := eachField(mark, func(part []byte, tag byte, payload []byte) error {
			if tag != tagCompound {
				return nil
			}
			return eachField(payload, func(field []byte, tag byte, payload []byte) error {
				switch string(part) + "." + string(field) {
				case "data.type":
					sort, hasSort = wholeOf(tag, payload)
				case "key.blockX":
					x, hasX = intOf(tag, payload)
				case "key.blockZ":
					z, hasZ = intOf(tag, payload)
				}
				return nil
			})
		})
		if kind, read := markKinds[sort]; err == nil && read && hasSort && hasX && hasZ {
			c.point(target{kind, d, Site{x >> 4, z >> 4}})
		}
		return err
	}); err != nil {
		c.stats.Skipped++
	}
}

func (c *contents) point(t target) {
	if _, held := c.targets[t]; held {
		return
	}
	if t.site.ChunkX < -maxCoordinate/16 || t.site.ChunkX > maxCoordinate/16 || t.site.ChunkZ < -maxCoordinate/16 || t.site.ChunkZ > maxCoordinate/16 {
		c.stats.Skipped++
		return
	}
	if len(c.targets) >= maxMapTargets {
		c.stats.TargetsOver++
		return
	}
	c.targets[t] = struct{}{}
}
