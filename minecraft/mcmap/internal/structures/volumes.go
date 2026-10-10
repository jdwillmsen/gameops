package structures

import (
	"encoding/binary"
	"errors"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Newer versions of the game keep a second record of where structures are:
// record 119 of a chunk, the boxes of every structure piece that reaches
// into it, under the structure's own name. It is written for the kinds the
// game builds from data files (trial chambers, trail ruins, abandoned
// camps) and for the old scattered ones (igloos, both pyramids, swamp
// huts), beside the fortresses, monuments and outposts record 57 already
// has. No village, stronghold, mineshaft, shipwreck, ruin, portal, bastion,
// ancient city or end city is in it, and neither is a chunk the game last
// wrote before it kept the record.
//
// The record is little-endian throughout:
//
//	u32 version, 1
//	u32 names, then for each: u32 handle, u16 length, the name
//	u32 boxes, then for each: u32 handle, six i32 (minimum x, y, z, maximum)
//	u32 entries, then for each: u32 box, u32 name, u32 whole
//	u32 entries, then for each: u32 box, u32 name, i32, u32 whole
//
// Names and boxes share one run of handles. The first list of entries is
// the data-file kinds' and the second the old kinds'; each entry says which
// structure a box belongs to. whole is 1 on the box round the whole
// structure and 0 on a piece of it, both cut at the chunk's edge. An old
// kind's whole box stands at the height the structure was first given, and
// its pieces at the height it was built at.
const (
	// TagVolumes is the record's tag in a chunk key.
	TagVolumes = 119

	volumesVersion = 1
	// More than any chunk holds: the busiest in the FWB world has 3 names
	// and 114 boxes. They bound what one damaged count can ask for.
	maxVolumeNames = 64
	maxVolumeBoxes = 4096
	maxVolumeName  = 128
)

const (
	AbandonedCamp Kind = "abandoned_camp"
	DesertPyramid Kind = "desert_pyramid"
	Igloo         Kind = "igloo"
	JungleTemple  Kind = "jungle_temple"
	TrailRuins    Kind = "trail_ruins"
)

// volumeKinds is the kinds taken from this record, by the game's name. A
// trial chamber is in it too and is left to its blocks, which every
// version of the game leaves.
var volumeKinds = map[string]Kind{
	"desert_pyramid":   DesertPyramid,
	"fortress":         Fortress,
	"igloo":            Igloo,
	"jungle_pyramid":   JungleTemple,
	"monument":         Monument,
	"pillager_outpost": Outpost,
	"swamp_hut":        WitchHut,
	"trail_ruins":      TrailRuins,
}

// volumesElsewhere is the kinds in this record that are read from somewhere
// else, so that leaving them out here is not a kind going unknown.
var volumesElsewhere = map[string]bool{"trial_chambers": true}

// campPrefix starts the name of every abandoned camp, which goes on to say
// which biome's camp it is.
const campPrefix = "abandoned_camp"

var errVolumes = errors.New("volume record is not a version, names, boxes and two lists of entries")

type volumeReader struct {
	b   []byte
	bad bool
}

func (r *volumeReader) u32() uint32 {
	if len(r.b) < 4 {
		r.bad = true
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

// rawVolume is one entry of the record as written: a box, the game's name
// for the structure it is part of, and which sort of box it is.
type rawVolume struct {
	name             string
	box              Box
	whole, scattered bool
}

// readVolumes reads every entry of one chunk's record. A record that is not
// laid out as above is refused whole; an entry naming a box or a structure
// the record does not hold is left out and counted.
func readVolumes(value []byte) (entries []rawVolume, malformed int, err error) {
	r := &volumeReader{b: value}
	if r.u32() != volumesVersion {
		return nil, 0, errVolumes
	}
	n := r.u32()
	if r.bad || n > maxVolumeNames {
		return nil, 0, errVolumes
	}
	names := make(map[uint32]string, n)
	for range n {
		handle := r.u32()
		if len(r.b) < 2 {
			return nil, 0, errVolumes
		}
		size := int(binary.LittleEndian.Uint16(r.b))
		if size > maxVolumeName || len(r.b) < 2+size {
			return nil, 0, errVolumes
		}
		names[handle] = cleanID(string(r.b[2 : 2+size]))
		r.b = r.b[2+size:]
	}
	n = r.u32()
	if r.bad || n > maxVolumeBoxes || len(r.b) < int(n)*28 {
		return nil, 0, errVolumes
	}
	boxes := make(map[uint32]Box, n)
	for range n {
		handle := r.u32()
		at32 := func() int32 { return int32(r.u32()) }
		boxes[handle] = Box{at32(), at32(), at32(), at32(), at32(), at32()}
	}
	for _, scattered := range []bool{false, true} {
		n = r.u32()
		size := 12
		if scattered {
			size = 16
		}
		if r.bad || n > maxVolumeBoxes || len(r.b) < int(n)*size {
			return nil, 0, errVolumes
		}
		for range n {
			boxHandle, nameHandle := r.u32(), r.u32()
			if scattered {
				r.u32()
			}
			whole := r.u32()
			box, hasBox := boxes[boxHandle]
			name, hasName := names[nameHandle]
			if !hasBox || !hasName || whole > 1 {
				malformed++
				continue
			}
			entries = append(entries, rawVolume{name, box, whole == 1, scattered})
		}
	}
	if r.bad || len(r.b) != 0 {
		return nil, 0, errVolumes
	}
	return entries, malformed, nil
}

// decodeVolumes reads one chunk's volumes of the kinds kept here, as the
// boxes a structure is put together from. An old kind is kept as its
// pieces, which stand where it was built; a data-file kind as the box
// round the whole of it, which its pieces all lie inside. Within a sound
// record, a box of a kind this map does not know is left out and counted,
// and so is one that is inside out or outside its own chunk; the game
// writes an empty box for a piece that builds nothing, which is passed
// over.
func decodeVolumes(at chunks.Pos, value []byte) (found []piece, unknown, malformed int, err error) {
	entries, malformed, err := readVolumes(value)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, e := range entries {
		kind, taken := volumeKinds[e.name]
		variant := ""
		if rest, camp := strings.CutPrefix(e.name, campPrefix); camp && !taken {
			kind, taken, variant = AbandonedCamp, true, strings.TrimPrefix(rest, "_")
		}
		empty := e.box.MinX == e.box.MaxX+1 && e.box.MinY == e.box.MaxY+1 && e.box.MinZ == e.box.MaxZ+1
		switch {
		case volumesElsewhere[e.name] || (taken && (empty || e.whole == e.scattered)):
		case !taken:
			unknown++
		case e.box.MinY > e.box.MaxY || e.box.MinX > e.box.MaxX || e.box.MinZ > e.box.MaxZ ||
			!e.box.within(at.X*16, at.Z*16, at.X*16+15, at.Z*16+15):
			malformed++
		default:
			found = append(found, piece{kind, e.box, variant})
		}
	}
	return found, unknown, malformed, nil
}
