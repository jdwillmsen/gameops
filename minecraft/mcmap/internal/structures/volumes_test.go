package structures

import (
	"context"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// volumeEntry is one box of a volume record: the structure it is named
// for, whether it is the box round the whole of it, and which of the
// record's two lists it is entered in.
type volumeEntry struct {
	name      string
	box       Box
	whole     bool
	scattered bool
}

// volumes builds a chunk's record 119 as the game writes it: names and
// boxes numbered in one run, then the data-file kinds' entries and the old
// kinds' entries.
func volumes(entries ...volumeEntry) []byte {
	u32 := func(b []byte, v uint32) []byte { return binary.LittleEndian.AppendUint32(b, v) }
	names := map[string]uint32{}
	var order []string
	for _, e := range entries {
		if _, seen := names[e.name]; !seen {
			// Every other handle, as the game leaves gaps between them.
			names[e.name] = uint32(len(names) * 2)
			order = append(order, e.name)
		}
	}
	out := u32(u32(nil, volumesVersion), uint32(len(order)))
	for _, name := range order {
		out = u32(out, names[name])
		out = binary.LittleEndian.AppendUint16(out, uint16(len(name)))
		out = append(out, name...)
	}
	out = u32(out, uint32(len(entries)))
	for i, e := range entries {
		out = u32(out, uint32(1000+i))
		for _, v := range []int32{e.box.MinX, e.box.MinY, e.box.MinZ, e.box.MaxX, e.box.MaxY, e.box.MaxZ} {
			out = u32(out, uint32(v))
		}
	}
	for _, scattered := range []bool{false, true} {
		n := 0
		for _, e := range entries {
			if e.scattered == scattered {
				n++
			}
		}
		out = u32(out, uint32(n))
		for i, e := range entries {
			if e.scattered != scattered {
				continue
			}
			out = u32(u32(out, uint32(1000+i)), names[e.name])
			if scattered {
				out = u32(out, ^uint32(2))
			}
			whole := uint32(0)
			if e.whole {
				whole = 1
			}
			out = u32(out, whole)
		}
	}
	return out
}

var volumeChunk = chunks.Pos{Dim: chunks.Overworld, X: 5, Z: -3}

// inChunk is a box inside volumeChunk, by its corners within the chunk.
func inChunk(x0, y0, z0, x1, y1, z1 int32) Box {
	return Box{80 + x0, y0, -48 + z0, 80 + x1, y1, -48 + z1}
}

func TestVolumes_KeepsEachKindAsTheBoxesThatSayWhereItStands(t *testing.T) {
	ruins, ruinsPiece := inChunk(2, 61, 7, 15, 82, 15), inChunk(5, 76, 11, 9, 80, 15)
	igloo, iglooFirst := inChunk(0, 69, 0, 6, 73, 7), inChunk(0, 64, 0, 6, 68, 7)
	camp := inChunk(6, 63, 0, 15, 70, 8)
	got, unknown, malformed, err := decodeVolumes(volumeChunk, volumes(
		// A data-file kind: the box round the whole of it is kept, and the
		// pieces inside that are not.
		volumeEntry{name: "minecraft:trail_ruins", box: ruinsPiece},
		volumeEntry{name: "minecraft:trail_ruins", box: ruins, whole: true},
		// An old kind: its piece stands where it was built, and the box
		// round it at the height it was first given.
		volumeEntry{name: "minecraft:igloo", box: igloo, scattered: true},
		volumeEntry{name: "minecraft:igloo", box: iglooFirst, whole: true, scattered: true},
		// A camp is recorded under its biome's name.
		volumeEntry{name: "minecraft:abandoned_camp_mesa_plateau_stone", box: camp, whole: true},
		// A piece that builds nothing is written as a box inside out by one.
		volumeEntry{name: "minecraft:abandoned_camp_mesa_plateau_stone", box: Box{95, 70, -43, 94, 69, -44}},
		// A chamber is read from its blocks, and is nothing unknown.
		volumeEntry{name: "minecraft:trial_chambers", box: inChunk(0, -30, 0, 15, 10, 15), whole: true},
		// A later version's kind, a box that is another chunk's, and one
		// that is inside out by more than the game ever writes.
		volumeEntry{name: "minecraft:sky_palace", box: inChunk(0, 200, 0, 15, 210, 15), whole: true},
		volumeEntry{name: "minecraft:monument", box: Box{0, 39, 0, 15, 61, 15}, scattered: true},
		volumeEntry{name: "minecraft:monument", box: Box{90, 61, -40, 85, 39, -45}, scattered: true},
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []piece{
		{TrailRuins, ruins, ""},
		{AbandonedCamp, camp, "mesa_plateau_stone"},
		{Igloo, igloo, ""},
	}
	if !reflect.DeepEqual(got, want) || unknown != 1 || malformed != 2 {
		t.Errorf("got %+v, %d unknown, %d malformed; want %+v, 1 and 2", got, unknown, malformed, want)
	}
}

func TestVolumes_KnowEveryKindTheRecordIsReadFor(t *testing.T) {
	for name, kind := range map[string]Kind{
		"fortress": Fortress, "monument": Monument, "pillager_outpost": Outpost, "swamp_hut": WitchHut,
		"desert_pyramid": DesertPyramid, "jungle_pyramid": JungleTemple, "igloo": Igloo,
	} {
		got, _, _, err := decodeVolumes(volumeChunk, volumes(volumeEntry{name: "minecraft:" + name, box: inChunk(0, 64, 0, 6, 70, 8), scattered: true}))
		if err != nil || len(got) != 1 || got[0].kind != kind {
			t.Errorf("%s read as %+v, %v; want one %s", name, got, err, kind)
		}
	}
}

// Nothing says where the rest of a damaged record starts, and a count that
// does not match what follows would otherwise be boxes made of whatever
// bytes came next.
func TestVolumes_RefuseARecordThatIsNotWhole(t *testing.T) {
	sound := volumes(
		volumeEntry{name: "minecraft:trail_ruins", box: inChunk(2, 61, 7, 15, 82, 15), whole: true},
		volumeEntry{name: "minecraft:swamp_hut", box: inChunk(0, 64, 0, 6, 70, 8), scattered: true},
	)
	if got, _, _, err := decodeVolumes(volumeChunk, sound); err != nil || len(got) != 2 {
		t.Fatalf("the sound record: %+v, %v", got, err)
	}
	for cut := range len(sound) {
		if got, _, _, err := decodeVolumes(volumeChunk, sound[:cut]); err == nil {
			t.Errorf("cut to %d of %d bytes: read as %+v", cut, len(sound), got)
		}
	}
	damaged := map[string][]byte{
		"a byte too many":    append(append([]byte{}, sound...), 0),
		"another version":    append([]byte{2, 0, 0, 0}, sound[4:]...),
		"names past the end": append([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0x7f}, sound[8:]...),
	}
	for name, value := range damaged {
		if got, _, _, err := decodeVolumes(volumeChunk, value); err == nil {
			t.Errorf("%s: read as %+v", name, got)
		}
	}
	// And whatever a byte is changed to, the worst is a record refused.
	for i := range sound {
		for _, v := range []byte{0, 1, 0x7f, 0x80, 0xff} {
			changed := append([]byte{}, sound...)
			changed[i] = v
			got, _, _, err := decodeVolumes(volumeChunk, changed)
			for _, p := range got {
				if err == nil && !p.box.within(80, -48, 95, -33) {
					t.Fatalf("byte %d set to %d: a box outside its chunk was kept: %+v", i, v, p)
				}
			}
		}
	}
}

// An entry is a box and a name by their handles; one that names neither of
// the record's own is left out and counted, and the rest stand.
func TestVolumes_LeaveOutAnEntryThatNamesNothing(t *testing.T) {
	value := volumes(
		volumeEntry{name: "minecraft:igloo", box: inChunk(0, 64, 0, 6, 68, 7), scattered: true},
		volumeEntry{name: "minecraft:desert_pyramid", box: inChunk(0, 62, 0, 15, 76, 15), scattered: true},
	)
	// The last entry's box handle is its first four bytes, sixteen from the end.
	binary.LittleEndian.PutUint32(value[len(value)-16:], 77)
	got, unknown, malformed, err := decodeVolumes(volumeChunk, value)
	if err != nil || len(got) != 1 || got[0].kind != Igloo || unknown != 0 || malformed != 1 {
		t.Errorf("got %+v, %d unknown, %d malformed, %v; want the igloo and one malformed", got, unknown, malformed, err)
	}
}

// A newer game writes a monument or an outpost to this record and not to
// the spawn areas, and writes kinds the spawn areas never held.
func TestTake_ReadsTheStructuresOnlyTheNewerRecordHolds(t *testing.T) {
	w := newWorld(t)
	pyramid := Box{320, 62, 640, 340, 76, 660}
	for cx := int32(20); cx <= 21; cx++ {
		for cz := int32(40); cz <= 41; cz++ {
			at := chunks.Pos{Dim: chunks.Overworld, X: cx, Z: cz}
			part := Box{max(pyramid.MinX, cx*16), 62, max(pyramid.MinZ, cz*16), min(pyramid.MaxX, cx*16+15), 76, min(pyramid.MaxZ, cz*16+15)}
			w.put(at, TagVolumes, volumes(volumeEntry{name: "minecraft:desert_pyramid", box: part, scattered: true}))
		}
	}
	w.put(chunks.Pos{Dim: chunks.Overworld, X: -9, Z: 2}, TagVolumes, volumes(
		volumeEntry{name: "minecraft:abandoned_camp_taiga", box: Box{-138, 70, 37, -129, 78, 47}, whole: true},
		volumeEntry{name: "minecraft:monument", box: Box{-144, 39, 32, -129, 61, 47}, scattered: true}))
	w.put(chunks.Pos{Dim: chunks.Nether, X: 3, Z: 3}, TagVolumes, volumes(volumeEntry{name: "minecraft:fortress", box: Box{50, 48, 50, 55, 57, 60}, scattered: true}))
	w.put(chunks.Pos{Dim: chunks.Overworld, X: 60, Z: 60}, TagVolumes, []byte{1, 0, 0})
	got, err := surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	want := []Structure{
		{Kind: DesertPyramid, Box: pyramid, Areas: 4},
		{Kind: Monument, Box: Box{-144, 39, 32, -129, 61, 47}, Areas: 1},
		{Kind: AbandonedCamp, Box: Box{-138, 70, 37, -129, 78, 47}, Areas: 1, Variant: "taiga"},
	}
	if !reflect.DeepEqual(got.Layers[chunks.Overworld].Recorded, want) {
		t.Errorf("overworld = %+v\nwant %+v", got.Layers[chunks.Overworld].Recorded, want)
	}
	if nether := got.Layers[chunks.Nether].Recorded; len(nether) != 1 || nether[0].Kind != Fortress {
		t.Errorf("nether = %+v", nether)
	}
	if got.Areas != 7 || got.Malformed != 1 {
		t.Errorf("%d boxes read and %d malformed, want 7 and the one record that is not whole", got.Areas, got.Malformed)
	}
}
