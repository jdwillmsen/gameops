package layers

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// hill is solid to y 9 with a grassy top, a cave from y 0 to 4 in the
// columns with x from 4 to 8, a pool in the cave and, over the rest, a
// plant growing on the top.
func hill(x, y, z int) string {
	switch {
	case x >= 4 && x <= 8 && y >= 0 && y <= 4:
		if x == 8 && y == 0 {
			return "water"
		}
		return "air"
	case y < 9:
		return "stone"
	case y == 9:
		return "grass_block"
	case y == 10 && x == 12:
		return "short_grass"
	}
	return "air"
}

func column(t *testing.T, world blockFunc) (*Column, []Block) {
	t.Helper()
	f := newFake(t)
	pos := chunks.Pos{Dim: chunks.Overworld}
	f.chunk(pos, 31, world)
	r := f.reader()
	col := new(Column)
	if found, err := r.Read(pos, col); !found || err != nil {
		t.Fatalf("found %v, error %v", found, err)
	}
	return col, r.Registry.Blocks()
}

func named(blocks []Block, s Sight) string { return blocks[s.ID].Name }

func TestSee_ACutShowsTheHighestBlockAtOrBelowIt(t *testing.T) {
	col, blocks := column(t, hill)
	spec := Spec{Dim: chunks.Overworld, Cut: 20, Thickness: 32}
	for _, c := range []struct {
		name  string
		x     int
		cut   int
		block string
		y     int
		kind  Kind
	}{
		{"the top of the ground, past a plant", 12, 20, "grass_block", 9, Ground},
		{"a cave's pit from a cut over the ground", 5, 20, "grass_block", 9, Ground},
		{"stone where the cut runs through rock", 2, 6, "stone", 6, Ground},
		{"a cave's floor where the cut runs through it", 5, 3, "stone", -1, Ground},
		{"a pool in a cave, whose bed shows through", 8, 3, "water", 0, Ground},
	} {
		s := spec
		s.Cut = c.cut
		got := col.see(blocks, c.x, 5, s)
		if got.Kind != c.kind || named(blocks, got) != c.block || got.Y != c.y {
			t.Errorf("%s: sees %s at %d, kind %d; want %s at %d", c.name, named(blocks, got), got.Y, got.Kind, c.block, c.y)
		}
	}
	empty, known := column(t, all("air"))
	if got := empty.see(known, 5, 5, Spec{Dim: chunks.Overworld, Cut: 3, Thickness: 3}); got.Kind != Nothing {
		t.Errorf("a band of nothing but air shows %+v", got)
	}
	// The pool is drawn over what is under it, one block of water deep.
	got := col.see(blocks, 8, 5, Spec{Dim: chunks.Overworld, Cut: 3, Thickness: 32})
	if got.Depth != 1 || blocks[got.Bed].Name != "stone" {
		t.Errorf("the pool is %d deep over %s", got.Depth, blocks[got.Bed].Name)
	}
}

func TestSee_ABandStopsTheLookingAtItsBottom(t *testing.T) {
	col, blocks := column(t, hill)
	// Cut at 3 with a band of two: y 3 and 2 are both cave air.
	if got := col.see(blocks, 5, 5, Spec{Dim: chunks.Overworld, Cut: 3, Thickness: 2}); got.Kind != Nothing {
		t.Errorf("a band of two blocks of air shows %s at %d", named(blocks, got), got.Y)
	}
	if got := col.see(blocks, 5, 5, Spec{Dim: chunks.Overworld, Cut: 3, Thickness: 5}); named(blocks, got) != "stone" || got.Y != -1 {
		t.Errorf("a band to the floor shows %s at %d", named(blocks, got), got.Y)
	}
}

func TestSee_AFloorIsTheGroundOfTheHighestSpaceWithARoofOverIt(t *testing.T) {
	col, blocks := column(t, hill)
	spec := Spec{Dim: chunks.Overworld, Cut: 20, Thickness: 32, Style: Floor}
	for _, c := range []struct {
		name  string
		x     int
		cut   int
		block string
		y     int
		kind  Kind
	}{
		{"a cave's floor", 5, 20, "stone", -1, Ground},
		{"rock with no space in the band", 2, 20, "grass_block", 9, Ground},
		{"rock under a roof with no space in the band", 2, 6, "stone", 6, Mass},
		{"open ground, which has no roof", 12, 20, "grass_block", 9, Ground},
		{"a pool in a cave, which is the floor and is drawn as water", 8, 20, "water", 0, Ground},
	} {
		s := spec
		s.Cut = c.cut
		got := col.see(blocks, c.x, 5, s)
		if got.Kind != c.kind || named(blocks, got) != c.block || got.Y != c.y {
			t.Errorf("%s: sees %s at %d, kind %d; want %s at %d, kind %d", c.name, named(blocks, got), got.Y, got.Kind, c.block, c.y, c.kind)
		}
	}
}

func TestSee_AFloorHigherThanTheCutIsNotSeen(t *testing.T) {
	// Two caves one over the other, with the lower floor at 1 and the
	// upper at 21. A floor is the ground of a space, so a cut at the
	// space's own height finds it and a cut at the ground's does not.
	two := func(x, y, z int) string {
		switch {
		case y >= 2 && y <= 6, y >= 22 && y <= 26:
			return "air"
		case y < 30:
			return "stone"
		}
		return "air"
	}
	col, blocks := column(t, two)
	for _, c := range []struct{ cut, y int }{{28, 21}, {22, 21}, {21, 1}, {10, 1}} {
		got := col.see(blocks, 3, 3, Spec{Dim: chunks.Overworld, Cut: c.cut, Thickness: 64, Style: Floor})
		if got.Kind != Ground || got.Y != c.y {
			t.Errorf("a cut at %d sees a floor at %d, kind %d; want %d", c.cut, got.Y, got.Kind, c.y)
		}
	}
	// A band that does not reach the lower floor does not find it.
	got := col.see(blocks, 3, 3, Spec{Dim: chunks.Overworld, Cut: 10, Thickness: 4, Style: Floor})
	if got.Kind != Mass {
		t.Errorf("a band over solid stone is kind %d", got.Kind)
	}
}

func TestSee_ACeilingOfLeavesIsARoofAndAPlantIsNot(t *testing.T) {
	// A floor at y 0 under a canopy at 4, and the same under a plant.
	canopy := func(x, y, z int) string {
		switch {
		case y < 0:
			return "dirt"
		case y == 4 && x < 8:
			return "oak_leaves"
		case y == 4:
			return "short_grass"
		}
		return "air"
	}
	col, blocks := column(t, canopy)
	spec := Spec{Dim: chunks.Overworld, Cut: 8, Thickness: 16, Style: Floor}
	if got := col.see(blocks, 3, 3, spec); got.Kind != Ground || got.Y != -1 {
		t.Errorf("under leaves: kind %d at %d", got.Kind, got.Y)
	}
	// Open to the sky through a plant: ground in the Cut's sense, since
	// there is no space to be enclosed in.
	if got := col.see(blocks, 12, 3, spec); got.Kind != Ground || got.Y != -1 {
		t.Errorf("under a plant: kind %d at %d", got.Kind, got.Y)
	}
}

func TestSpec_Valid(t *testing.T) {
	for _, c := range []struct {
		spec Spec
		ok   bool
	}{
		{Spec{Dim: chunks.Overworld, Cut: 0, Thickness: 16}, true},
		{Spec{Dim: chunks.Overworld, Cut: -64, Thickness: 1}, true},
		{Spec{Dim: chunks.Overworld, Cut: 319, Thickness: 384}, true},
		{Spec{Dim: chunks.Nether, Cut: 100, Thickness: 101, Style: Floor}, true},
		{Spec{Dim: chunks.Nether, Cut: 128, Thickness: 16}, false},
		{Spec{Dim: chunks.Overworld, Cut: -65, Thickness: 16}, false},
		{Spec{Dim: chunks.Overworld, Cut: 0, Thickness: 0}, false},
		{Spec{Dim: chunks.Nether, Cut: 0, Thickness: 129}, false},
		{Spec{Dim: chunks.Dimension(7), Cut: 0, Thickness: 16}, false},
		{Spec{Dim: chunks.End, Cut: 0, Thickness: 16, Style: 9}, false},
	} {
		if err := c.spec.Valid(); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.spec, err)
		}
	}
}
