package repair

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

type entry struct {
	name   string
	states map[string]any
}

// encodeSub writes a version 9 sub-chunk with one layer of 4-bit indexes;
// pick returns the palette index of a cell.
func encodeSub(t *testing.T, sy int8, pal []entry, pick func(x, y, z int) int) []byte {
	t.Helper()
	return encodeBits(t, sy, 4, pal, pick)
}

// encodeBits is encodeSub at any index width. Width 0 is a uniform slice: the
// palette's one entry follows the header with no count and no index words.
func encodeBits(t *testing.T, sy int8, bits int, pal []entry, pick func(x, y, z int) int) []byte {
	t.Helper()
	out := []byte{9, 1, byte(sy), byte(bits << 1)}
	if bits != 0 {
		per := 32 / bits
		words := make([]uint32, (cellsPerSubChunk+per-1)/per)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				for y := 0; y < 16; y++ {
					i := x<<8 | z<<4 | y
					words[i/per] |= uint32(pick(x, y, z)) << (i % per * bits)
				}
			}
		}
		for _, w := range words {
			out = binary.LittleEndian.AppendUint32(out, w)
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(len(pal)))
	}
	for _, e := range pal {
		var buf bytes.Buffer
		m := map[string]any{"name": e.name, "states": e.states, "version": int32(18153475)}
		if err := nbt.NewEncoderWithEncoding(&buf, nbt.LittleEndian).Encode(m); err != nil {
			t.Fatal(err)
		}
		out = append(out, buf.Bytes()...)
	}
	return out
}

func putChunk(t *testing.T, db *leveldb.DB, cx, cz int) {
	t.Helper()
	if err := db.Put(chunkKey(cx, cz, tagChunkVersion, 0), []byte{40}, nil); err != nil {
		t.Fatal(err)
	}
}

func putSubChunk(t *testing.T, db *leveldb.DB, cx, cz int, sy int8, raw []byte) {
	t.Helper()
	if err := db.Put(chunkKey(cx, cz, tagSubChunk, sy), raw, nil); err != nil {
		t.Fatal(err)
	}
}

func openFixture(t *testing.T) (*leveldb.DB, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, dir
}

func TestWorldReaderDecodesAndMatchesThePlansSpelling(t *testing.T) {
	db, dir := openFixture(t)
	// Chunk -19,-48 holds x -304..-289, z -768..-753. Slice 4 is y 64..79:
	// y 64 is sand, y 65 a lantern the plan names by hanging only, y 66 a
	// lit lamp, y 67 a stair the plan wants facing 2 but faces 1.
	putChunk(t, db, -19, -48)
	putSubChunk(t, db, -19, -48, 4, encodeSub(t, 4,
		[]entry{
			{"minecraft:air", nil},
			{"minecraft:sand", nil},
			{"minecraft:lantern", map[string]any{"hanging": uint8(1), "extra": int32(7)}},
			{"minecraft:lit_redstone_lamp", nil},
			{"minecraft:stone_brick_stairs", map[string]any{"weirdo_direction": int32(1), "upside_down_bit": uint8(0)}},
		},
		func(x, y, z int) int {
			if x != 0 || z != 0 {
				return 0
			}
			switch y {
			case 0:
				return 1
			case 1:
				return 2
			case 2:
				return 3
			case 3:
				return 4
			}
			return 0
		}))
	db.Close()

	plan := ops(t, "fill -304 64 -768 -304 64 -768 sand\n"+
		"setblock -304 65 -768 lantern [\"hanging\"=true]\n"+
		"setblock -304 66 -768 redstone_lamp\n"+
		"setblock -304 67 -768 stone_brick_stairs [\"upside_down_bit\"=false,\"weirdo_direction\"=2]\n")
	r, err := OpenWorld(dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pal := NewPalette()
	g, err := r.Read(Box{X0: -304, X1: -303, Y0: 64, Y1: 68, Z0: -768, Z1: -768}, pal)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sand", `lantern ["hanging"=true]`, "redstone_lamp", `stone_brick_stairs ["upside_down_bit"=false,"weirdo_direction"=1]`, "air"}
	for i, w := range want {
		if got := pal.Name(g.At(-304, 64+i, -768)); got != w {
			t.Errorf("y %d: got %q, want %q", 64+i, got, w)
		}
	}
	if got := pal.Name(g.At(-303, 64, -768)); got != "air" {
		t.Errorf("neighbour cell: got %q", got)
	}
}

func TestWorldReaderChunkStates(t *testing.T) {
	db, dir := openFixture(t)
	putChunk(t, db, 0, 0) // exists, no slices: all air
	db.Close()
	r, err := OpenWorld(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pal := NewPalette()
	g, err := r.Read(Box{X0: 0, X1: 17, Y0: 36, Y1: 40, Z0: 0, Z1: 0}, pal)
	if err != nil {
		t.Fatal(err)
	}
	if pal.Name(g.At(3, 38, 0)) != "air" {
		t.Errorf("absent slice in an existing chunk should be air")
	}
	if g.At(17, 38, 0) != Unknown {
		t.Errorf("a cell in a chunk that was never generated must stay unknown")
	}
}

func TestWorldReaderRejectsWhatItCannotRead(t *testing.T) {
	db, dir := openFixture(t)
	putChunk(t, db, 0, 0)
	putSubChunk(t, db, 0, 0, 3, []byte{1, 0, 0})
	db.Close()
	r, err := OpenWorld(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Read(Box{X0: 0, X1: 0, Y0: 48, Y1: 48, Z0: 0, Z1: 0}, NewPalette()); err == nil {
		t.Fatal("an unsupported sub-chunk version must be an error, not air")
	}
	if _, err := OpenWorld(filepath.Join(t.TempDir(), "nothing"), nil); err == nil {
		t.Fatal("a missing db must be an error")
	}
}

func TestCanonicalToleratesDynamicState(t *testing.T) {
	r := NewWorldReader(nil, ops(t, `setblock 0 40 0 lever ["lever_direction"="up_east_west","open_bit"=false]`+"\n"+
		`setblock 0 41 0 unpowered_repeater ["minecraft:cardinal_direction"="west","repeater_delay"=0]`))
	if got := r.canonical("minecraft:lever", map[string]string{"lever_direction": `"up_east_west"`, "open_bit": "true"}); got != `lever ["lever_direction"="up_east_west","open_bit"=false]` {
		t.Errorf("a toggled lever is not damage, got %q", got)
	}
	if got := r.canonical("powered_repeater", map[string]string{"minecraft:cardinal_direction": `"west"`, "repeater_delay": "0"}); got != `unpowered_repeater ["minecraft:cardinal_direction"="west","repeater_delay"=0]` {
		t.Errorf("a powered repeater is its unpowered plan, got %q", got)
	}
	if got := r.canonical("lever", map[string]string{"lever_direction": `"north"`, "open_bit": "false"}); got != `lever ["lever_direction"="north","open_bit"=false]` {
		t.Errorf("a lever facing the wrong way must differ from the plan, got %q", got)
	}
}

func TestSplitBlock(t *testing.T) {
	name, st := splitBlock(`iron_door ["door_hinge_bit"=true,"minecraft:cardinal_direction"="north,x"]`)
	if name != "iron_door" || st["door_hinge_bit"] != "true" || st["minecraft:cardinal_direction"] != `"north,x"` {
		t.Errorf("got %q %v", name, st)
	}
}

// A full pass over a database: damage the world, plan, and the plan names the
// damage and nothing else.
func TestWorldScanFindsDamage(t *testing.T) {
	l := testLayout(t)
	db, dir := openFixture(t)
	// The plan: a one-cell wall column at x -304, z -768, y 64..66 in stone
	// bricks, with air at y 67. The world lost its middle block and has a
	// dirt block on top.
	putChunk(t, db, -19, -48)
	putSubChunk(t, db, -19, -48, 4, encodeSub(t, 4,
		[]entry{{"minecraft:air", nil}, {"minecraft:stone_bricks", nil}, {"minecraft:dirt", nil}},
		func(x, y, z int) int {
			if x != 0 || z != 0 {
				return 0
			}
			switch y {
			case 0, 2:
				return 1
			case 3:
				return 2
			}
			return 0
		}))
	db.Close()
	plan := ops(t, "fill -304 64 -768 -304 66 -768 stone_bricks\nsetblock -304 67 -768 air\n")
	r, err := OpenWorld(dir, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	d, p, rep, err := Compute(plan, r, l)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cells != 2 || rep.ByKind[KindMissing] != 1 || rep.ByKind[KindIntruder] != 1 {
		t.Fatalf("damage %d, kinds %v", d.Cells, rep.ByKind)
	}
	if len(p.Repairs) != 2 || p.Repairs[0] != "setblock -304 67 -768 air" || p.Repairs[1] != "setblock -304 65 -768 stone_bricks" {
		t.Errorf("repairs: %v", p.Repairs)
	}
}

func TestWorldReaderEveryPaletteWidth(t *testing.T) {
	for _, bits := range []int{1, 2, 3, 4, 5, 6, 8, 16} {
		n := min(1<<bits, 37)
		pal := make([]entry, n)
		for i := range pal {
			pal[i] = entry{name: "minecraft:block_" + strconv.Itoa(i)}
		}
		pick := func(x, y, z int) int { return (x*7 + y*3 + z) % n }
		db, dir := openFixture(t)
		putChunk(t, db, 0, 0)
		putSubChunk(t, db, 0, 0, 1, encodeBits(t, 1, bits, pal, pick))
		db.Close()
		r, err := OpenWorld(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		pl := NewPalette()
		g, err := r.Read(Box{X0: 0, X1: 15, Y0: 16, Y1: 31, Z0: 0, Z1: 15}, pl)
		r.Close()
		if err != nil {
			t.Fatalf("%d bits: %v", bits, err)
		}
		for x := 0; x < 16; x += 5 {
			for y := 0; y < 16; y += 3 {
				for z := 0; z < 16; z += 4 {
					if got, want := pl.Name(g.At(x, 16+y, z)), "block_"+strconv.Itoa(pick(x, y, z)); got != want {
						t.Fatalf("%d bits at %d,%d,%d: %s, want %s", bits, x, y, z, got, want)
					}
				}
			}
		}
	}
}

func TestWorldReaderZeroBitAndNegativeSlices(t *testing.T) {
	db, dir := openFixture(t)
	putChunk(t, db, -1, -1)
	// Slice -1 is y -16..-1, uniform deepslate with no index words; slice -4
	// is y -64..-49, bedrock in the bottom layer only.
	putSubChunk(t, db, -1, -1, -1, encodeBits(t, -1, 0, []entry{{"minecraft:deepslate", map[string]any{"pillar_axis": "y"}}}, nil))
	putSubChunk(t, db, -1, -1, -4, encodeSub(t, -4, []entry{{"minecraft:stone", nil}, {"minecraft:bedrock", nil}},
		func(x, y, z int) int {
			if y == 0 {
				return 1
			}
			return 0
		}))
	db.Close()
	r, err := OpenWorld(dir, ops(t, `setblock -1 -1 -1 deepslate ["pillar_axis"="y"]`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pal := NewPalette()
	g, err := r.Read(Box{X0: -16, X1: -1, Y0: -64, Y1: 0, Z0: -16, Z1: -1}, pal)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, y, z int
		want    string
	}{
		{-1, -1, -1, `deepslate ["pillar_axis"="y"]`},
		{-16, -16, -16, `deepslate ["pillar_axis"="y"]`},
		{-8, -64, -8, "bedrock"},
		{-8, -63, -8, "stone"},
		{-8, -49, -8, "stone"},
		{-8, -32, -8, "air"}, // slices -3 and -2 are absent
		{-8, 0, -8, "air"},
	} {
		if got := pal.Name(g.At(c.x, c.y, c.z)); got != c.want {
			t.Errorf("%d,%d,%d: %q, want %q", c.x, c.y, c.z, got, c.want)
		}
	}
}

// testdata/gate-db.tar holds the chunk records (version and sub-chunks only,
// no entities or player data) of chunks -16,-47 and -15,-47 of the lab world,
// which contain pen gate 1 and the stands behind it. The gate was open when it
// was taken. Slices -4..4 are present, so the negative slices are real ones.
func openGateFixture(t *testing.T, plan []Op) *WorldReader {
	t.Helper()
	f, err := os.Open("testdata/gate-db.tar")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		if err := os.WriteFile(filepath.Join(dir, h.Name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := OpenWorld(filepath.Join(dir, "db"), plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestWorldReaderRealChunks(t *testing.T) {
	r := openGateFixture(t, nil)
	pal := NewPalette()
	g, err := r.Read(Box{X0: -250, X1: -230, Y0: -64, Y1: 120, Z0: -752, Z1: -737}, pal)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, y, z int
		want    string
	}{
		{-241, 64, -747, "air"}, // the open gate
		{-241, 63, -747, "stone_bricks"},
		{-241, 67, -747, "cut_sandstone"},
		{-231, 70, -740, "sandstone"},
		{-241, 49, -747, "stone"},
		{-241, 100, -747, "air"},
		{-241, -1, -747, `deepslate ["pillar_axis"="y"]`},
		{-241, -64, -747, `bedrock ["infiniburn_bit"=false]`},
	} {
		if got := pal.Name(g.At(c.x, c.y, c.z)); got != c.want {
			t.Errorf("%d,%d,%d: %q, want %q", c.x, c.y, c.z, got, c.want)
		}
	}
	// Chunk -16,-48 is not in the fixture, so its cells are unknown.
	edge, err := r.Read(Box{X0: -241, X1: -241, Y0: 64, Y1: 64, Z0: -753, Z1: -752}, NewPalette())
	if err != nil {
		t.Fatal(err)
	}
	if edge.At(-241, 64, -753) != Unknown || edge.At(-241, 64, -752) == Unknown {
		t.Errorf("chunk edge: %v", edge.Cells)
	}
}

func TestRealGateDamageIsFound(t *testing.T) {
	l := testLayout(t)
	plan := ops(t, "fill -241 64 -748 -241 66 -746 iron_bars\n")
	r := openGateFixture(t, plan)
	d, p, rep, err := Compute(plan, r, l)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cells != 9 || rep.ByKind[KindMissing] != 9 {
		t.Fatalf("cells %d zones %v kinds %v", d.Cells, rep.ByZone, rep.ByKind)
	}
	if len(p.Repairs) != 1 || p.Repairs[0] != "fill -241 64 -748 -241 66 -746 iron_bars" {
		t.Errorf("repairs %v", p.Repairs)
	}
}
