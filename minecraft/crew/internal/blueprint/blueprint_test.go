package blueprint

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

func validBlueprint() *Blueprint {
	b := &Blueprint{Name: "test", Kind: "house", Size: Size{2, 2, 1}, Palette: []Block{{Name: "stone"}, {Name: "torch"}}, Layers: []string{"00", "10"}, BuildOrder: []Position{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0}}, Materials: []Material{{Item: "stone", Count: 3}, {Item: "torch", Count: 1}}}
	return b
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Blueprint)
	}{
		{"palette bounds", func(b *Blueprint) { b.Layers[0] = "20" }},
		{"palette entry without name", func(b *Blueprint) { b.Palette[1].Name = "" }},
		{"layer cell count", func(b *Blueprint) { b.Layers[0] = "0" }},
		{"materials", func(b *Blueprint) { b.Materials[0].Count++ }},
		{"air only", func(b *Blueprint) {
			b.Palette = []Block{{Name: "air"}}
			b.Layers = []string{"00", "00"}
			b.Materials = nil
		}},
		{"axis limit", func(b *Blueprint) { b.Size.X = 49 }},
		{"support order", func(b *Blueprint) { b.BuildOrder[0], b.BuildOrder[2] = b.BuildOrder[2], b.BuildOrder[0] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := validBlueprint()
			tc.mutate(b)
			if err := b.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
		})
	}
	if err := validBlueprint().Validate(); err != nil {
		t.Fatal(err)
	}

	// Redstone lamps and redstone blocks are solid blocks and do not require support at ground level.
	bLamp := &Blueprint{
		Name:       "lamp_test",
		Kind:       "redstone",
		Size:       Size{1, 1, 1},
		Palette:    []Block{{Name: "redstone_lamp"}},
		Layers:     []string{"0"},
		BuildOrder: []Position{{0, 0, 0}},
		Materials:  []Material{{Item: "redstone_lamp", Count: 1}},
	}
	if err := bLamp.Validate(); err != nil {
		t.Fatalf("redstone_lamp at y=0 should not require support: %v", err)
	}
}

// build_order is optional: without it the order is derived bottom-up with
// solid blocks first, and the support check runs against that order.
func TestValidateDerivesBuildOrder(t *testing.T) {
	b := validBlueprint()
	b.BuildOrder = nil
	if err := b.Validate(); err != nil {
		t.Fatalf("blueprint without build_order should validate: %v", err)
	}
	order := b.PlacementOrder()
	want := []Position{{0, 0, 0}, {1, 0, 0}, {1, 1, 0}, {0, 1, 0}}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("derived order %v, want %v (torch after its support)", order, want)
	}

	// The support check still fires on the derived order: a torch in the
	// bottom layer has no support inside the blueprint.
	bottom := validBlueprint()
	bottom.BuildOrder = nil
	bottom.Layers = []string{"10", "00"}
	if err := bottom.Validate(); err == nil {
		t.Fatal("torch on the bottom layer should fail the support check")
	}

	// An explicit build_order overrides the derived order unchanged.
	explicit := []Position{{1, 1, 0}, {0, 1, 0}, {1, 0, 0}, {0, 0, 0}}
	b.BuildOrder = explicit
	if got := b.PlacementOrder(); !reflect.DeepEqual(got, explicit) {
		t.Fatalf("explicit build_order %v replaced by %v", explicit, got)
	}
}

// Blocks that attach, hang, sit or lean on a neighbour must be ordered
// after it; sea_lantern is solid despite the name.
func TestNeedsSupportAttachedBlocks(t *testing.T) {
	attached := []string{
		"torch", "redstone_torch", "soul_torch",
		"lantern", "soul_lantern", "oak_pressure_plate",
		"wooden_door", "iron_door", "ladder", "redstone_wire",
		"unpowered_repeater", "unpowered_comparator", "lever", "stone_button",
		"rail", "golden_rail", "moss_carpet", "oak_wall_sign",
		"vine", "twisting_vines", "oak_trapdoor",
	}
	for _, name := range attached {
		if !needsSupport(name) {
			t.Errorf("%s should need support", name)
		}
	}
	solid := []string{"sea_lantern", "redstone_lamp", "lit_redstone_lamp", "redstone_block", "stone", "oak_planks", "air"}
	for _, name := range solid {
		if needsSupport(name) {
			t.Errorf("%s should not need support", name)
		}
	}
}

func TestValidatorRepeaterWarning(t *testing.T) {
	b := validBlueprint()
	if warns := b.Warnings(); len(warns) != 0 {
		t.Fatalf("expected no warnings for validBlueprint, got: %v", warns)
	}

	b.Palette = append(b.Palette, Block{Name: "unpowered_repeater"})
	warns := b.Warnings()
	if len(warns) == 0 {
		t.Fatal("expected warning for unpowered_repeater in palette")
	}
	if !strings.Contains(warns[0], "repeater") {
		t.Fatalf("unexpected warning message: %s", warns[0])
	}
}

func TestLoadNormalizesNumericStates(t *testing.T) {
	// encoding/json decodes numbers as float64; Load must hand back ints so
	// rotation and command emission treat states as integers.
	doc := `{"name":"n","kind":"house","size":{"x":1,"y":2,"z":1},
		"palette":[{"name":"stone"},{"name":"wooden_door","states":{"direction":2,"open_bit":false,"upper_block_bit":false}}],
		"layers":["0","1"],"build_order":[{"x":0,"y":0,"z":0},{"x":0,"y":1,"z":0}],
		"materials":[{"item":"stone","count":1},{"item":"wooden_door","count":1}]}`
	path := filepath.Join(t.TempDir(), "numeric.json")
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Palette[1].States["direction"].(int); !ok {
		t.Fatalf("direction state is %T, want int", b.Palette[1].States["direction"])
	}
}

func TestImportLittleEndianRoundTrip(t *testing.T) {
	type entry struct {
		Name   string         `nbt:"name"`
		States map[string]any `nbt:"states"`
	}
	structure := struct {
		Size      []int32 `nbt:"size"`
		Structure struct {
			BlockIndices [][]int32 `nbt:"block_indices"`
			Palette      struct {
				Default struct {
					BlockPalette []entry `nbt:"block_palette"`
				} `nbt:"default"`
			} `nbt:"palette"`
		} `nbt:"structure"`
	}{Size: []int32{2, 1, 1}}
	structure.Structure.BlockIndices = [][]int32{{0, 1}}
	structure.Structure.Palette.Default.BlockPalette = []entry{{Name: "minecraft:air"}, {Name: "minecraft:stone"}}
	data, err := nbt.MarshalEncoding(structure, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sample.mcstructure")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := Import(path, "roundtrip", "infrastructure")
	if err != nil {
		t.Fatal(err)
	}
	if b.Size != (Size{2, 1, 1}) || b.Layers[0] != "01" || len(b.Materials) != 1 || b.Materials[0] != (Material{"stone", 1}) {
		t.Fatalf("unexpected imported structure: %#v", b)
	}
	// Imported JSON leaves build_order out; the order is derived on load.
	if len(b.BuildOrder) != 0 {
		t.Fatalf("import wrote a build_order (%d cells)", len(b.BuildOrder))
	}
}

func TestImportExport3DRoundTrip(t *testing.T) {
	// 3D test with non-trivial X, Y, Z dimensions.
	bp := &Blueprint{
		Name: "test3d",
		Kind: "house",
		Size: Size{2, 2, 2},
		Palette: []Block{
			{Name: "air"},
			{Name: "stone"},
			{Name: "torch"},
		},
		Layers: []string{
			"1111", // y=0: all stone
			"0200", // y=1: x=1,z=0 is torch, rest air
		},
		BuildOrder: []Position{
			{0, 0, 0}, {1, 0, 0}, {0, 0, 1}, {1, 0, 1},
			{0, 1, 0}, {0, 1, 1}, {1, 1, 1}, {1, 1, 0},
		},
		Materials: []Material{
			{Item: "stone", Count: 4},
			{Item: "torch", Count: 1},
		},
	}
	if err := bp.Validate(); err != nil {
		t.Fatalf("source blueprint invalid: %v", err)
	}

	path := filepath.Join(t.TempDir(), "test3d.mcstructure")
	if err := bp.Export(path); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	imported, err := Import(path, "test3d", "house")
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if imported.Size != bp.Size {
		t.Fatalf("size mismatch: got %v, want %v", imported.Size, bp.Size)
	}
	if !reflect.DeepEqual(imported.Layers, bp.Layers) {
		t.Fatalf("layers mismatch: got %v, want %v", imported.Layers, bp.Layers)
	}
	if !reflect.DeepEqual(imported.Materials, bp.Materials) {
		t.Fatalf("materials mismatch: got %v, want %v", imported.Materials, bp.Materials)
	}
}
