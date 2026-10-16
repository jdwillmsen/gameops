package camera

import "testing"

// A flat-colour page has no shape to draw them with, so it must be able to
// tell non-cubes from cubes; a textured page needs them to have an id at all.
func TestNonCubeBlocksGetAnIdFlaggedSoFlatPagesCanSkipThem(t *testing.T) {
	tab := newMaterialTable()
	for _, name := range []string{"minecraft:short_grass", "minecraft:oak_stairs", "minecraft:torch"} {
		id, fresh := tab.id(name)
		if id == 0 || !fresh {
			t.Errorf("%s: id %d fresh %v; want a new id", name, id, fresh)
		}
	}
	for _, m := range tab.all() {
		if !m.NonCube {
			t.Errorf("%s is not flagged non-cube", m.Name)
		}
	}
	tab.id("minecraft:stone")
	tab.id("minecraft:grass_block")
	for _, m := range tab.all()[3:] {
		if m.NonCube {
			t.Errorf("%s is flagged non-cube", m.Name)
		}
	}
}

func TestAirIsStillNotDrawn(t *testing.T) {
	if id, _ := newMaterialTable().id("minecraft:air"); id != 0 {
		t.Errorf("air id = %d", id)
	}
}
