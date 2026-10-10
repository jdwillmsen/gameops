// Package structures finds the structures on the map: the ones the world
// has generated, read from the world's own records, and the ones its
// generator will build in chunks nobody has visited yet, worked out from
// the seed.
//
// The two are kept apart all the way to the page. A recorded structure is a
// fact about this world. A predicted one is a calculation, and it is only
// offered while the same calculation agrees with the facts the world holds.
package structures

import (
	"encoding/binary"
	"errors"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Kind is a structure type, by the name the page and the API use.
type Kind string

const (
	Fortress Kind = "fortress"
	WitchHut Kind = "witch_hut"
	Monument Kind = "monument"
	Outpost  Kind = "outpost"
)

// The server keeps, for each chunk, the boxes inside which a structure's
// own mobs spawn: record 57 of the chunk, "hardcoded spawn areas". The
// record is a 32-bit count followed by that many areas, each six 32-bit
// block coordinates (minimum x, y, z, then maximum, both inclusive) and one
// byte for the kind, all little-endian. An area is cut at the chunk's edge,
// so a structure is spread over the records of every chunk it touches.
const (
	// TagSpawnAreas is the record's tag in a chunk key.
	TagSpawnAreas = 57
	// TagFinalized is the record that says how far a chunk's generation
	// got; finalizedDone in it means the chunk is complete.
	TagFinalized  = 54
	finalizedDone = 2

	areaSize = 25
	// Far more than any chunk holds: the busiest in the FWB world has 20.
	// It bounds what one damaged count can make this allocate.
	maxAreasPerRecord = 1024
)

// The kind byte. 4 is not used by any current game version.
var kindOf = map[byte]Kind{1: Fortress, 2: WitchHut, 3: Monument, 5: Outpost}

// Box is a block-aligned box, both corners inclusive.
type Box struct {
	MinX int32 `json:"minX"`
	MinY int32 `json:"minY"`
	MinZ int32 `json:"minZ"`
	MaxX int32 `json:"maxX"`
	MaxY int32 `json:"maxY"`
	MaxZ int32 `json:"maxZ"`
}

func (b Box) union(o Box) Box {
	return Box{
		min(b.MinX, o.MinX), min(b.MinY, o.MinY), min(b.MinZ, o.MinZ),
		max(b.MaxX, o.MaxX), max(b.MaxY, o.MaxY), max(b.MaxZ, o.MaxZ),
	}
}

// within reports whether b lies inside the columns from (minX, minZ) to
// (maxX, maxZ); height is not compared.
func (b Box) within(minX, minZ, maxX, maxZ int32) bool {
	return b.MinX >= minX && b.MaxX <= maxX && b.MinZ >= minZ && b.MaxZ <= maxZ
}

type piece struct {
	kind Kind
	box  Box
	// variant is which of several a kind that comes in several is.
	variant string
}

var errRecord = errors.New("spawn area record is not a count followed by that many areas")

// decode reads one chunk's spawn areas. A record whose length disagrees
// with its count is refused whole, since nothing says where its areas
// start. Within a sound record, an area this map cannot place is left out
// and counted: one of a kind it does not know (a later game version's), or
// one whose box is inside out or outside its own chunk, which only damage
// produces and which would otherwise be drawn across the map.
func decode(at chunks.Pos, value []byte) (pieces []piece, unknown, malformed int, err error) {
	if len(value) < 4 {
		return nil, 0, 0, errRecord
	}
	n := binary.LittleEndian.Uint32(value)
	if n > maxAreasPerRecord || len(value) != 4+int(n)*areaSize {
		return nil, 0, 0, errRecord
	}
	for area := value[4:]; len(area) > 0; area = area[areaSize:] {
		at32 := func(i int) int32 { return int32(binary.LittleEndian.Uint32(area[i*4:])) }
		box := Box{at32(0), at32(1), at32(2), at32(3), at32(4), at32(5)}
		kind, known := kindOf[area[24]]
		switch {
		case !known:
			unknown++
		case box.MinY > box.MaxY || box.MinX > box.MaxX || box.MinZ > box.MaxZ ||
			!box.within(at.X*16, at.Z*16, at.X*16+15, at.Z*16+15):
			malformed++
		default:
			pieces = append(pieces, piece{kind: kind, box: box})
		}
	}
	return pieces, unknown, malformed, nil
}
