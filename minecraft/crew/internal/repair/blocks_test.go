package repair

import (
	"reflect"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type fakeSource map[protocol.BlockPos]struct {
	name  string
	props map[string]any
}

func (f fakeSource) State(p protocol.BlockPos) (string, map[string]any, bool) {
	b, ok := f[p]
	return b.name, b.props, ok
}

func (f fakeSource) set(x, y, z int32, name string, props map[string]any) {
	f[protocol.BlockPos{x, y, z}] = struct {
		name  string
		props map[string]any
	}{name, props}
}

func TestBlocksReaderLeavesUnseenCellsUnknown(t *testing.T) {
	src := fakeSource{}
	src.set(0, 0, 0, "minecraft:stone", nil)
	pal := NewPalette()
	g, err := NewBlocksReader(src, nil).Read(Box{X0: 0, Y0: 0, Z0: 0, X1: 1, Y1: 0, Z1: 0}, pal)
	if err != nil {
		t.Fatal(err)
	}
	if pal.Name(g.At(0, 0, 0)) != "stone" {
		t.Errorf("seen cell = %q", pal.Name(g.At(0, 0, 0)))
	}
	if g.At(1, 0, 0) != Unknown {
		t.Errorf("unseen cell = %d, want Unknown", g.At(1, 0, 0))
	}
}

// A lit lamp, a toggled door and a lever thrown the other way are the arena
// in use, not damage; a different block or a different facing is.
func TestBlocksReaderFoldsStatesLikeTheWorldReader(t *testing.T) {
	plan := ops(t, "setblock 0 0 0 redstone_lamp\nsetblock 1 0 0 lever [\"lever_direction\"=\"north\",\"open_bit\"=false]\nsetblock 2 0 0 stone\nsetblock 3 0 0 lantern [\"hanging\"=true]")
	src := fakeSource{}
	src.set(0, 0, 0, "minecraft:lit_redstone_lamp", nil)
	src.set(1, 0, 0, "minecraft:lever", map[string]any{"lever_direction": "north", "open_bit": true})
	src.set(2, 0, 0, "minecraft:air", nil)
	src.set(3, 0, 0, "minecraft:lantern", map[string]any{"hanging": false})
	box := Box{X0: 0, Y0: 0, Z0: 0, X1: 3, Y1: 0, Z1: 0}
	d, err := Diff(plan, NewBlocksReader(src, plan), box, testLayout(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{KindMissing: 1, KindReplaced: 1}
	if d.Cells != 2 || !reflect.DeepEqual(d.ByKind, want) {
		t.Errorf("damage %d cells %v, want 2 cells %v", d.Cells, d.ByKind, want)
	}
}

func TestClipOpsKeepsTheOverrideOrder(t *testing.T) {
	plan := ops(t, "fill 0 0 0 9 0 0 stone\nfill 4 0 0 6 0 0 sand\nfill 20 0 0 22 0 0 glass")
	box := Box{X0: 3, Y0: 0, Z0: 0, X1: 7, Y1: 0, Z1: 0}
	clipped := ClipOps(plan, box)
	if len(clipped) != 2 {
		t.Fatalf("clipped to %d ops: %+v", len(clipped), clipped)
	}
	pal := NewPalette()
	whole, part := NewGrid(box), NewGrid(box)
	Paint(whole, pal, plan)
	Paint(part, pal, clipped)
	if !reflect.DeepEqual(whole.Cells, part.Cells) {
		t.Errorf("clipped plan paints %v, whole plan paints %v", part.Cells, whole.Cells)
	}
}

func TestOrderRectsClearsFirstAndLeavesLateBlocksLast(t *testing.T) {
	in := []Rect{
		{Box: Box{X0: 0, Y0: 5, Z0: 0, X1: 0, Y1: 5, Z1: 0}, Block: "lantern"},
		{Box: Box{X0: 0, Y0: 9, Z0: 0, X1: 0, Y1: 9, Z1: 0}, Block: "stone"},
		{Box: Box{X0: 0, Y0: 7, Z0: 0, X1: 0, Y1: 7, Z1: 0}, Block: "air"},
		{Box: Box{X0: 0, Y0: 1, Z0: 0, X1: 0, Y1: 1, Z1: 0}, Block: "stone"},
	}
	var got []string
	for _, r := range OrderRects(in) {
		got = append(got, r.Block)
	}
	if want := []string{"air", "stone", "stone", "lantern"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
	if in[0].Block != "lantern" {
		t.Error("OrderRects reordered its input")
	}
}
