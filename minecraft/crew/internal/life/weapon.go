package life

import (
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// materials ranks tool tiers; wooden and golden hit alike.
var materials = map[string]int{
	"wooden": 1, "golden": 1, "stone": 2, "copper": 2, "iron": 3, "diamond": 4, "netherite": 5,
}

const (
	swordBase = 100
	axeBase   = 50
	// A pickaxe, shovel or hoe still out-damages a bare fist.
	toolBase = 10
)

// weaponScore rates a stack as a weapon: swords beat axes beat other tools,
// better materials win within a class, and anything else scores zero.
func weaponScore(it belief.Item) int {
	mat, class, ok := strings.Cut(it.Name, "_")
	if !ok {
		return 0
	}
	tier := materials[mat]
	if tier == 0 {
		return 0
	}
	switch class {
	case "sword":
		return swordBase + tier
	case "axe":
		return axeBase + tier
	case "pickaxe", "shovel", "hoe":
		return toolBase + tier
	}
	return 0
}

const hotbarSlots = 9

// loadout picks the hotbar stack to fight with: the best weapon, or whatever
// is already in hand when there is none. The server only lets a unit select
// from the hotbar. held is the zero Item for an empty hand.
func loadout(s belief.Snapshot) (slot int, held belief.Item) {
	best := 0
	for _, it := range s.Inventory {
		if it.Slot >= hotbarSlots {
			continue
		}
		if sc := weaponScore(it); sc > best {
			best, slot, held = sc, it.Slot, it
		}
	}
	if best > 0 {
		return slot, held
	}
	if !s.HeldKnown {
		return 0, belief.Item{}
	}
	for _, it := range s.Inventory {
		if it.Slot == s.HeldSlot {
			return it.Slot, it
		}
	}
	return s.HeldSlot, belief.Item{}
}
