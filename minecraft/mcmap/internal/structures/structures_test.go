package structures

import (
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Two records exactly as the FWB world held them on 2026-10-05 (game
// 1.26.52.3): the eight fortress areas of nether chunk 7, -72, and the one
// witch hut of overworld chunk -76, 107. Only these two structures are
// quoted: every recorded position narrows down the seed that produced it.
const (
	realFortressRecord = "08000000" +
		"7d000000330000008ffbffff7f000000390000008ffbffff01" +
		"70000000420000008cfbffff72000000480000008ffbffff01" +
		"73000000420000008cfbffff77000000480000008ffbffff01" +
		"7d000000330000008afbffff7f000000390000008efbffff01" +
		"78000000420000008cfbffff7c000000480000008ffbffff01" +
		"780000004200000087fbffff7c000000480000008bfbffff01" +
		"7d0000004200000087fbffff7f000000480000008bfbffff01" +
		"7d000000420000008cfbffff7f000000480000008ffbffff01"
	realHutRecord = "0100000040fbffff55000000b006000046fbffff5b000000b806000002"
)

var (
	realFortressChunk = chunks.Pos{Dim: chunks.Nether, X: 7, Z: -72}
	realHutChunk      = chunks.Pos{Dim: chunks.Overworld, X: -76, Z: 107}
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// record builds a spawn area record from boxes and kind bytes.
func record(areas ...any) []byte {
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(areas)/2))
	for i := 0; i < len(areas); i += 2 {
		b := areas[i].(Box)
		for _, v := range []int32{b.MinX, b.MinY, b.MinZ, b.MaxX, b.MaxY, b.MaxZ} {
			out = binary.LittleEndian.AppendUint32(out, uint32(v))
		}
		out = append(out, areas[i+1].(byte))
	}
	return out
}

const (
	fortressByte byte = 1
	hutByte      byte = 2
	monumentByte byte = 3
	outpostByte  byte = 5
)

func TestDecode_RealRecords(t *testing.T) {
	got, unknown, malformed, err := decode(realFortressChunk, unhex(t, realFortressRecord))
	if err != nil || unknown != 0 || malformed != 0 {
		t.Fatalf("fortress record: unknown %d, malformed %d, err %v", unknown, malformed, err)
	}
	if len(got) != 8 {
		t.Fatalf("fortress record gave %d areas, want 8", len(got))
	}
	if want := (piece{Fortress, Box{125, 51, -1137, 127, 57, -1137}}); got[0] != want {
		t.Errorf("first area = %+v, want %+v", got[0], want)
	}
	if want := (piece{Fortress, Box{125, 66, -1140, 127, 72, -1137}}); got[7] != want {
		t.Errorf("last area = %+v, want %+v", got[7], want)
	}

	got, _, _, err = decode(realHutChunk, unhex(t, realHutRecord))
	if want := []piece{{WitchHut, Box{-1216, 85, 1712, -1210, 91, 1720}}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("hut record = %+v, %v; want %+v", got, err, want)
	}
}

func TestDecode_KnowsEveryKindTheGameRecords(t *testing.T) {
	at := chunks.Pos{X: 1, Z: 1}
	box := Box{16, 60, 16, 31, 70, 31}
	got, unknown, malformed, err := decode(at, record(box, fortressByte, box, hutByte, box, monumentByte, box, outpostByte))
	if err != nil || unknown != 0 || malformed != 0 {
		t.Fatalf("unknown %d, malformed %d, err %v", unknown, malformed, err)
	}
	var kinds []Kind
	for _, p := range got {
		kinds = append(kinds, p.kind)
	}
	if want := []Kind{Fortress, WitchHut, Monument, Outpost}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
}

// A record whose length and count disagree gives no way to find its areas,
// so nothing is taken from it.
func TestDecode_RefusesARecordThatIsNotWhole(t *testing.T) {
	whole := unhex(t, realFortressRecord)
	huge := binary.LittleEndian.AppendUint32(nil, maxAreasPerRecord+1)
	huge = append(huge, make([]byte, (maxAreasPerRecord+1)*areaSize)...)
	for name, value := range map[string][]byte{
		"empty":            {},
		"short count":      whole[:3],
		"cut mid-area":     whole[:len(whole)-1],
		"one area missing": whole[:len(whole)-areaSize],
		"trailing byte":    append(append([]byte{}, whole...), 0),
		"count of -1":      {0xff, 0xff, 0xff, 0xff},
		"over the bound":   huge,
	} {
		if got, _, _, err := decode(realFortressChunk, value); err == nil {
			t.Errorf("%s: decoded %d areas, want a refusal", name, len(got))
		}
	}
	if got, _, _, err := decode(realFortressChunk, []byte{0, 0, 0, 0}); err != nil || len(got) != 0 {
		t.Errorf("a record of no areas: %d areas, %v", len(got), err)
	}
}

// One bad area does not cost the chunk its good ones, and is never drawn.
func TestDecode_LeavesOutAnAreaItCannotPlace(t *testing.T) {
	at := chunks.Pos{X: 1, Z: -1}
	good := Box{16, 60, -16, 31, 70, -1}
	for name, c := range map[string]struct {
		box                Box
		kind               byte
		unknown, malformed int
	}{
		"a kind no version uses":  {good, 4, 1, 0},
		"a kind from the future":  {good, 9, 1, 0},
		"inside out across x":     {Box{31, 60, -16, 16, 70, -1}, monumentByte, 0, 1},
		"inside out in height":    {Box{16, 70, -16, 31, 60, -1}, monumentByte, 0, 1},
		"outside its chunk":       {Box{16, 60, -16, 32, 70, -1}, monumentByte, 0, 1},
		"across the whole world":  {Box{-30_000_000, 0, -30_000_000, 30_000_000, 255, 30_000_000}, monumentByte, 0, 1},
		"in another chunk wholly": {Box{160, 60, 160, 170, 70, 170}, monumentByte, 0, 1},
	} {
		got, unknown, malformed, err := decode(at, record(good, outpostByte, c.box, c.kind))
		if err != nil || unknown != c.unknown || malformed != c.malformed {
			t.Errorf("%s: unknown %d, malformed %d, err %v; want %d, %d", name, unknown, malformed, err, c.unknown, c.malformed)
		}
		if want := []piece{{Outpost, good}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: kept %+v, want only the good area", name, got)
		}
	}
}

func TestAssemble_JoinsAStructureCutAtChunkEdges(t *testing.T) {
	// A monument's footprint, 27 to 84 on both axes, as four chunks of it
	// would record it, and a second monument well away.
	pieces := []piece{
		{Monument, Box{27, 39, 27, 31, 61, 31}},
		{Monument, Box{32, 39, 27, 47, 61, 31}},
		{Monument, Box{27, 39, 32, 31, 61, 47}},
		{Monument, Box{32, 39, 32, 47, 61, 47}},
		{Monument, Box{512, 39, 512, 527, 61, 527}},
	}
	got := assemble(pieces)
	want := []Structure{
		{Monument, Box{27, 39, 27, 47, 61, 47}, 4, nil, 0},
		{Monument, Box{512, 39, 512, 527, 61, 527}, 1, nil, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("assemble = %+v\nwant %+v", got, want)
	}
}

func TestAssemble_KeepsApartWhatIsNotOneStructure(t *testing.T) {
	for name, c := range map[string]struct {
		pieces []piece
		want   int
	}{
		// Touching, but a hut is not part of a monument.
		"two kinds": {[]piece{{Monument, Box{0, 39, 0, 15, 61, 15}}, {WitchHut, Box{16, 64, 0, 22, 70, 8}}}, 2},
		// One block of open ground between two outposts' areas.
		"a gap": {[]piece{{Outpost, Box{0, 64, 0, 15, 85, 15}}, {Outpost, Box{17, 64, 0, 31, 85, 15}}}, 2},
		// Fortress rooms are recorded with ground between them.
		"fortress rooms near": {[]piece{{Fortress, Box{0, 48, 0, 4, 57, 4}}, {Fortress, Box{37, 48, 0, 41, 57, 4}}}, 1},
		"fortress rooms far":  {[]piece{{Fortress, Box{0, 48, 0, 4, 57, 4}}, {Fortress, Box{38, 48, 0, 42, 57, 4}}}, 2},
		// Joined through a third that reaches both.
		"a chain": {[]piece{{Fortress, Box{0, 48, 0, 4, 57, 4}}, {Fortress, Box{60, 48, 0, 64, 57, 4}}, {Fortress, Box{30, 48, 0, 34, 57, 4}}}, 1},
		// Diagonal neighbours touch at a corner.
		"a corner": {[]piece{{Monument, Box{0, 39, 0, 15, 61, 15}}, {Monument, Box{16, 39, 16, 31, 61, 31}}}, 1},
		"nothing":  {nil, 0},
	} {
		if got := assemble(c.pieces); len(got) != c.want {
			t.Errorf("%s: %d structures, want %d: %+v", name, len(got), c.want, got)
		}
	}
}

// The order decides which structures a full layer keeps, and what the page
// is sent must not reshuffle between two surveys of the same world.
func TestAssemble_ListsTheLargestFirstInAStableOrder(t *testing.T) {
	pieces := []piece{
		{Outpost, Box{800, 64, 0, 815, 85, 15}},
		{Monument, Box{0, 39, 0, 15, 61, 15}},
		{Monument, Box{16, 39, 0, 31, 61, 15}},
		{Outpost, Box{-800, 64, 0, -785, 85, 15}},
	}
	first := assemble(pieces)
	if first[0].Kind != Monument || first[1].MinX != -800 || first[2].MinX != 800 {
		t.Errorf("order = %+v", first)
	}
	for range 20 {
		if again := assemble(pieces); !reflect.DeepEqual(again, first) {
			t.Fatalf("order changed between runs:\n%+v\n%+v", first, again)
		}
	}
}
