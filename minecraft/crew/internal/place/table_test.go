package place

import (
	"bytes"
	"reflect"
	"testing"
)

func synthetic() *Table {
	return &Table{
		Header: Header{Format: FormatVersion, ServerVersion: "9.9.9.9"},
		Blocks: map[string]*Block{
			"oak_stairs": {
				Item:    "minecraft:oak_stairs",
				Derived: []string{"shape"},
				States: []Entry{
					{State: map[string]any{"facing": "north", "upside_down_bit": false}, Recipe: Recipe{Support: "bottom", Face: "top", Hit: [3]float32{0.5, 1, 0.5}, Yaw: 180}},
					{State: map[string]any{"facing": "north", "upside_down_bit": true}, Recipe: Recipe{Support: "bottom", Face: "top", Hit: [3]float32{0.5, 1, 0.5}, Yaw: 180, Pitch: -60}},
					{State: map[string]any{"facing": "north", "upside_down_bit": true}, Recipe: Recipe{Support: "east", Face: "west", Hit: [3]float32{0, 0.75, 0.5}, Yaw: 180}},
					{State: map[string]any{"facing": 2, "upside_down_bit": true}, Recipe: Recipe{Support: "top", Face: "bottom", Hit: [3]float32{0.5, 0, 0.5}, Yaw: 180}},
				},
				Unreachable: []map[string]any{{"facing": "up"}},
			},
		},
	}
}

func TestReadRejectsACorruptGzipTrailer(t *testing.T) {
	var buf bytes.Buffer
	if err := synthetic().Write(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("intact table: %v", err)
	}
	bad := buf.Bytes()
	bad[len(bad)-8] ^= 0xff // first CRC byte of the trailer
	if _, err := Read(bytes.NewReader(bad)); err == nil {
		t.Error("a table with a bad CRC read fine")
	}
}

func TestLookupPicksCheapestRecipe(t *testing.T) {
	got, ok := synthetic().Lookup("oak_stairs", map[string]any{"facing": "north", "upside_down_bit": true})
	if !ok {
		t.Fatal("not found")
	}
	// The side click with an off-centre hit costs 2; the pitched top click costs 1.
	if got.Face != "top" || got.Pitch != -60 {
		t.Fatalf("got %+v, want the pitched top click", got)
	}
}

func TestLookupAcceptsPartialAndNamespacedWants(t *testing.T) {
	tb := synthetic()
	if _, ok := tb.Lookup("minecraft:oak_stairs", map[string]any{"facing": "north"}); !ok {
		t.Fatal("partial want with namespace not found")
	}
	if r, ok := tb.Lookup("oak_stairs", nil); !ok || r.Face != "top" {
		t.Fatalf("empty want: %+v %v", r, ok)
	}
}

func TestLookupComparesAcrossIntegerTypes(t *testing.T) {
	tb := synthetic()
	for _, v := range []any{2, int32(2), uint8(2), int64(2), float64(2)} {
		r, ok := tb.Lookup("oak_stairs", map[string]any{"facing": v})
		if !ok || r.Face != "bottom" {
			t.Errorf("want %T(2): %+v %v", v, r, ok)
		}
	}
	if _, ok := tb.Lookup("oak_stairs", map[string]any{"facing": 3}); ok {
		t.Error("3 should not match")
	}
}

func TestLookupIgnoresDerivedKeys(t *testing.T) {
	_, ok := synthetic().Lookup("oak_stairs", map[string]any{"facing": "north", "shape": "outer_left"})
	if !ok {
		t.Fatal("a derived key must not block a lookup")
	}
}

func TestLookupMisses(t *testing.T) {
	tb := synthetic()
	cases := []struct {
		block string
		want  map[string]any
	}{
		{"nope", nil},
		{"oak_stairs", map[string]any{"facing": "up"}},
		{"oak_stairs", map[string]any{"missing_key": true}},
	}
	for _, c := range cases {
		if _, ok := tb.Lookup(c.block, c.want); ok {
			t.Errorf("%s %v should miss", c.block, c.want)
		}
	}
	if !tb.Unreachable("oak_stairs", map[string]any{"facing": "up"}) {
		t.Error("a known block with an unseen state is unreachable")
	}
	if tb.Unreachable("nope", nil) {
		t.Error("an unknown block is not 'unreachable', it is unknown")
	}
}

func TestDerivedReturnsACopy(t *testing.T) {
	tb := synthetic()
	d := tb.Derived("oak_stairs")
	if !reflect.DeepEqual(d, []string{"shape"}) {
		t.Fatalf("got %v", d)
	}
	d[0] = "changed"
	if tb.Derived("minecraft:oak_stairs")[0] != "shape" {
		t.Fatal("Derived must not expose the table's slice")
	}
	if tb.Derived("nope") != nil {
		t.Fatal("unknown block has no derived list")
	}
}

func TestWriteReadRoundTripIsDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := synthetic().Write(&a); err != nil {
		t.Fatal(err)
	}
	if err := synthetic().Write(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("equal tables must encode to equal bytes")
	}
	back, err := Read(bytes.NewReader(a.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	// JSON gives numbers back as float64; lookups must not care.
	if r, ok := back.Lookup("oak_stairs", map[string]any{"facing": 2}); !ok || r.Face != "bottom" {
		t.Fatalf("after round trip: %+v %v", r, ok)
	}
}

func TestReadRejectsOtherFormats(t *testing.T) {
	tb := synthetic()
	tb.Header.Format = 99
	var buf bytes.Buffer
	if err := tb.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf); err == nil {
		t.Fatal("format 99 must be refused")
	}
}

func TestKeyIgnoresTypeAndSkippedKeys(t *testing.T) {
	a := Key(map[string]any{"a": int32(1), "b": "x", "c": true}, map[string]bool{"c": true})
	b := Key(map[string]any{"b": "x", "a": float64(1)}, nil)
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestVersionOrder(t *testing.T) {
	if !versionLess("1.26.9.1", "1.26.60.6") || versionLess("1.26.60.6", "1.26.9.1") {
		t.Fatal("versions compare numerically")
	}
}

func TestCostPrefersTopClickOverSneak(t *testing.T) {
	top := Recipe{Face: "top", Hit: [3]float32{0.5, 1, 0.5}}
	sneak := Recipe{Face: "top", Hit: [3]float32{0.5, 1, 0.5}, Sneak: true}
	side := Recipe{Face: "east", Hit: [3]float32{1, 0.5, 0.5}}
	if !(top.Cost() < side.Cost() && side.Cost() < sneak.Cost()) {
		t.Fatalf("costs %v %v %v", top.Cost(), side.Cost(), sneak.Cost())
	}
}
