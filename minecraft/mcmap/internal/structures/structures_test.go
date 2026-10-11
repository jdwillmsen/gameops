package structures

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Every position in this package's tests is made up. Where a structure is
// follows from the seed, so a real structure's box, a real chunk's number
// or a record cut from a real world is a clue to the seed of the world it
// came from, and a public repository is no place for one. A fixture is
// built here, by the helpers below, in the layout the game writes (game
// 1.26.52.3), with small round numbers that stand nowhere near anything.
//
// The two records most tests start from: the eight areas a fortress has in
// one nether chunk, and the one area of a witch hut.
var (
	fortressChunk = chunks.Pos{Dim: chunks.Nether, X: 2, Z: -3}
	hutChunk      = chunks.Pos{Dim: chunks.Overworld, X: -4, Z: 5}

	fortressAreas = []Box{
		{40, 50, -48, 44, 54, -45}, {32, 60, -40, 35, 66, -36}, {36, 60, -40, 39, 66, -36}, {44, 50, -44, 47, 54, -41},
		{40, 60, -40, 43, 66, -36}, {40, 60, -47, 43, 66, -42}, {45, 60, -47, 47, 66, -42}, {45, 60, -40, 47, 66, -36},
	}
	// fortressBox is the one box those areas join into.
	fortressBox = Box{32, 50, -48, 47, 66, -36}
	hutBox      = Box{-64, 70, 80, -57, 75, 86}

	fortressRecord = func() []byte {
		var areas []any
		for _, box := range fortressAreas {
			areas = append(areas, box, fortressByte)
		}
		return record(areas...)
	}()
	hutRecord = record(hutBox, hutByte)
)

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

func TestDecode_ReadsRecordsAsTheGameWritesThem(t *testing.T) {
	got, unknown, malformed, err := decode(fortressChunk, fortressRecord)
	if err != nil || unknown != 0 || malformed != 0 {
		t.Fatalf("fortress record: unknown %d, malformed %d, err %v", unknown, malformed, err)
	}
	if len(got) != len(fortressAreas) {
		t.Fatalf("fortress record gave %d areas, want %d", len(got), len(fortressAreas))
	}
	for i, want := range fortressAreas {
		if got[i] != (piece{kind: Fortress, box: want}) {
			t.Errorf("area %d = %+v, want %+v", i, got[i], want)
		}
	}

	got, _, _, err = decode(hutChunk, hutRecord)
	if want := []piece{{kind: WitchHut, box: hutBox}}; err != nil || !reflect.DeepEqual(got, want) {
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
	whole := fortressRecord
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
		if got, _, _, err := decode(fortressChunk, value); err == nil {
			t.Errorf("%s: decoded %d areas, want a refusal", name, len(got))
		}
	}
	if got, _, _, err := decode(fortressChunk, []byte{0, 0, 0, 0}); err != nil || len(got) != 0 {
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
		if want := []piece{{kind: Outpost, box: good}}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: kept %+v, want only the good area", name, got)
		}
	}
}

func TestAssemble_JoinsAStructureCutAtChunkEdges(t *testing.T) {
	// The corner of a monument's footprint as the four chunks it lies in
	// would record it, and a second monument well away.
	pieces := []piece{
		{kind: Monument, box: Box{75, 39, 107, 79, 61, 111}},
		{kind: Monument, box: Box{80, 39, 107, 95, 61, 111}},
		{kind: Monument, box: Box{75, 39, 112, 79, 61, 127}},
		{kind: Monument, box: Box{80, 39, 112, 95, 61, 127}},
		{kind: Monument, box: Box{800, 39, 640, 815, 61, 655}},
	}
	got := assemble(pieces)
	want := []Structure{
		{Kind: Monument, Box: Box{75, 39, 107, 95, 61, 127}, Areas: 4},
		{Kind: Monument, Box: Box{800, 39, 640, 815, 61, 655}, Areas: 1},
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
		"two kinds": {[]piece{{kind: Monument, box: Box{0, 39, 0, 15, 61, 15}}, {kind: WitchHut, box: Box{16, 64, 0, 22, 70, 8}}}, 2},
		// One block of open ground between two outposts' areas.
		"a gap": {[]piece{{kind: WitchHut, box: Box{0, 64, 0, 6, 70, 8}}, {kind: WitchHut, box: Box{8, 64, 0, 14, 70, 8}}}, 2},
		// An outpost's tents stand apart from its tower, and the next
		// outpost is hundreds of blocks off.
		"an outpost's tent": {[]piece{{kind: Outpost, box: Box{0, 64, 0, 15, 85, 15}}, {kind: Outpost, box: Box{60, 64, 0, 64, 68, 4}}}, 1},
		"the next outpost":  {[]piece{{kind: Outpost, box: Box{0, 64, 0, 15, 85, 15}}, {kind: Outpost, box: Box{65, 64, 0, 79, 85, 15}}}, 2},
		// Fortress rooms are recorded with ground between them.
		"fortress rooms near": {[]piece{{kind: Fortress, box: Box{0, 48, 0, 4, 57, 4}}, {kind: Fortress, box: Box{37, 48, 0, 41, 57, 4}}}, 1},
		// Parts of one fortress with the country between them not
		// generated are one fortress, as far as its corridors run.
		"fortress parts apart": {[]piece{{kind: Fortress, box: Box{0, 48, 0, 4, 57, 4}}, {kind: Fortress, box: Box{5 + fortressJoin, 48, 0, 9 + fortressJoin, 57, 4}}}, 1},
		"fortress rooms far":   {[]piece{{kind: Fortress, box: Box{0, 48, 0, 4, 57, 4}}, {kind: Fortress, box: Box{6 + fortressJoin, 48, 0, 10 + fortressJoin, 57, 4}}}, 2},
		// Joined through a third that reaches both.
		"a chain": {[]piece{{kind: Fortress, box: Box{0, 48, 0, 4, 57, 4}}, {kind: Fortress, box: Box{60, 48, 0, 64, 57, 4}}, {kind: Fortress, box: Box{30, 48, 0, 34, 57, 4}}}, 1},
		// Diagonal neighbours touch at a corner.
		"a corner": {[]piece{{kind: Monument, box: Box{0, 39, 0, 15, 61, 15}}, {kind: Monument, box: Box{16, 39, 16, 31, 61, 31}}}, 1},
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
		{kind: Outpost, box: Box{800, 64, 0, 815, 85, 15}},
		{kind: Monument, box: Box{0, 39, 0, 15, 61, 15}},
		{kind: Monument, box: Box{16, 39, 0, 31, 61, 15}},
		{kind: Outpost, box: Box{-800, 64, 0, -785, 85, 15}},
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
