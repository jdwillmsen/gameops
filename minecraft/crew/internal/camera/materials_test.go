package camera

import (
	"fmt"
	"strconv"
	"testing"
)

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

// The byte-wide table sent every block after the 255th as one material, which
// drew all of them as the same texture.
func TestTableFedMoreNamesThanAByteHoldsKeepsThemDistinct(t *testing.T) {
	tab := newMaterialTable()
	seen := map[uint16]string{}
	for i := 0; i < 600; i++ {
		name := fmt.Sprintf("minecraft:block_%d", i)
		id, fresh := tab.id(name)
		if id == 0 || !fresh {
			t.Fatalf("%s: id %d fresh %v", name, id, fresh)
		}
		if other, dup := seen[id]; dup {
			t.Fatalf("%s and %s share id %d", name, other, id)
		}
		seen[id] = name
	}
	for _, m := range tab.all() {
		if m.Name != "block_"+strconv.Itoa(int(m.ID)-1) {
			t.Fatalf("id %d is %q", m.ID, m.Name)
		}
	}
	if id, fresh := tab.id("minecraft:block_300"); id != 301 || fresh {
		t.Errorf("a later lookup of block_300 = %d fresh %v, want 301 not fresh", id, fresh)
	}
}

func TestFullTableLeavesNewBlocksUndrawnInsteadOfAliased(t *testing.T) {
	tab := newMaterialTable()
	for i := 0; i < maxMaterials; i++ {
		if id, _ := tab.id("minecraft:b" + strconv.Itoa(i)); id != uint16(i+1) {
			t.Fatalf("block %d got id %d", i, id)
		}
	}
	if id, fresh := tab.id("minecraft:one_more"); id != 0 || fresh {
		t.Errorf("past the last id = %d fresh %v, want 0 not fresh", id, fresh)
	}
	if id, _ := tab.id("minecraft:b0"); id != 1 {
		t.Errorf("a known block lost its id: %d", id)
	}
}

func TestFromReturnsOnlyWhatIsNew(t *testing.T) {
	tab := newMaterialTable()
	for _, n := range []string{"stone", "dirt", "sand"} {
		tab.id("minecraft:" + n)
	}
	if got := tab.from(1); len(got) != 2 || got[0].Name != "dirt" || got[1].ID != 3 {
		t.Errorf("from(1) = %v", got)
	}
	if got := tab.from(3); got != nil {
		t.Errorf("from(3) = %v, want nothing", got)
	}
}

func TestNotCubeHidesChainsAndLanternsButNotTheSeaLantern(t *testing.T) {
	for name, want := range map[string]bool{
		"iron_chain": true, "copper_chain": true, "copper_lantern": true, "soul_lantern": true,
		"trip_wire": true, "sea_lantern": false, "chain_command_block": false,
	} {
		if got := notCube(name); got != want {
			t.Errorf("notCube(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPistonHelpersAreNotDrawn(t *testing.T) {
	tbl := newMaterialTable()
	for _, n := range []string{"moving_block", "piston_arm_collision", "sticky_piston_arm_collision"} {
		if id, _ := tbl.id("minecraft:" + n); id != 0 {
			t.Errorf("%s drew as material %d", n, id)
		}
	}
}
