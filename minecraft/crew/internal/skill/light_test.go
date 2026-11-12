package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// corridor is a sealed stone box with a one-cell-high tunnel along x from 0 to
// 30 at y=5, z=5.
func corridor() simWorld {
	w := simWorld{}
	w.fill("stone", -2, 2, 2, 34, 8, 8)
	w.fill("air", 0, 5, 5, 30, 5, 5)
	return w
}

func TestBlockLightFollowsTheTunnelAndStopsAtStone(t *testing.T) {
	cases := []struct {
		name   string
		source string
		at     protocol.BlockPos
		probe  protocol.BlockPos
		want   int
	}{
		{"dark tunnel", "", protocol.BlockPos{0, 5, 5}, protocol.BlockPos{10, 5, 5}, 0},
		{"torch cell itself", "torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{10, 5, 5}, 14},
		{"one block from a torch", "torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{11, 5, 5}, 13},
		{"six blocks from a torch is still 8", "torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{16, 5, 5}, 8},
		{"seven blocks from a torch is 7", "torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{17, 5, 5}, 7},
		{"fourteen blocks reaches 0", "torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{24, 5, 5}, 0},
		{"lava lights what is next to it", "lava", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{11, 5, 5}, 14},
		{"soul torch is dimmer", "soul_torch", protocol.BlockPos{10, 5, 5}, protocol.BlockPos{11, 5, 5}, 9},
	}
	for _, c := range cases {
		w := corridor()
		if c.source != "" {
			w[c.at] = c.source
		}
		if got := blockLight(w, c.probe); got != c.want {
			t.Errorf("%s: light = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBlockLightDoesNotPassThroughStone(t *testing.T) {
	w := corridor()
	w[protocol.BlockPos{10, 5, 5}] = "torch"
	// A cell on the other side of a wall, closer than the torch's reach.
	w[protocol.BlockPos{12, 5, 5}] = "stone"
	if got := blockLight(w, protocol.BlockPos{14, 5, 5}); got != 0 {
		t.Errorf("light behind a wall = %d, want 0", got)
	}
}

func TestBlockLightCountsASolidSourceBeside(t *testing.T) {
	w := corridor()
	w[protocol.BlockPos{10, 5, 5}] = "glowstone"
	if got := blockLight(w, protocol.BlockPos{9, 5, 5}); got != 14 {
		t.Errorf("light beside glowstone = %d, want 14", got)
	}
}

func TestBlockLightTreatsUnknownCellsAsDark(t *testing.T) {
	w := corridor()
	w[protocol.BlockPos{10, 5, 5}] = "torch"
	delete(w, protocol.BlockPos{12, 5, 5})
	if got := blockLight(w, protocol.BlockPos{13, 5, 5}); got != 0 {
		t.Errorf("light past an unloaded cell = %d, want 0", got)
	}
}

func TestPickaxeTierRanksMaterials(t *testing.T) {
	for name, want := range map[string]int{
		"wooden_pickaxe": 0, "golden_pickaxe": 0, "stone_pickaxe": 1, "copper_pickaxe": 1,
		"iron_pickaxe": 2, "diamond_pickaxe": 3, "netherite_pickaxe": 3,
	} {
		if got, ok := pickaxeTier(name); !ok || got != want {
			t.Errorf("%s: tier %d ok=%v, want %d", name, got, ok, want)
		}
	}
	if _, ok := pickaxeTier("iron_axe"); ok {
		t.Error("an axe has a pickaxe tier")
	}
}

func TestBestPickaxeTakesTheHighestTierOnTheHotbar(t *testing.T) {
	snap := belief.Snapshot{Inventory: []belief.Item{
		{Name: "wooden_pickaxe", Slot: 0, Count: 1},
		{Name: "iron_pickaxe", Slot: 20, Count: 1}, // not on the hotbar: cannot be selected
		{Name: "stone_pickaxe", Slot: 3, Count: 1},
	}}
	it, tier, ok := bestPickaxe(snap)
	if !ok || it.Name != "stone_pickaxe" || tier != 1 {
		t.Errorf("best = %q tier %d ok=%v, want the stone pickaxe", it.Name, tier, ok)
	}
	if _, _, ok := bestPickaxe(belief.Snapshot{}); ok {
		t.Error("a pickaxe was found in an empty inventory")
	}
}

func TestOreKinds(t *testing.T) {
	for _, n := range OreNames() {
		k, ok := OreByName(n)
		if !ok || k.Name != n || k.Item == "" || len(k.Blocks) != 2 {
			t.Errorf("%s: %+v ok=%v", n, k, ok)
		}
	}
	if _, ok := OreByName("gold"); ok {
		t.Error("gold is not a known ore")
	}
	// The ladder: each ore needs the tool the ore before it makes.
	coal, iron, diamond := OreCoal, OreIron, OreDiamond
	if !(coal.Tier < iron.Tier && iron.Tier < diamond.Tier) {
		t.Errorf("tiers %d %d %d do not rise", coal.Tier, iron.Tier, diamond.Tier)
	}
	if tier, _ := pickaxeTier("stone_pickaxe"); tier < iron.Tier {
		t.Error("a stone pickaxe cannot take iron")
	}
	if tier, _ := pickaxeTier("iron_pickaxe"); tier < diamond.Tier {
		t.Error("an iron pickaxe cannot take diamond")
	}
	if tier, _ := pickaxeTier("stone_pickaxe"); tier >= diamond.Tier {
		t.Error("a stone pickaxe takes diamond")
	}
}
