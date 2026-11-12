// Package inventory decides what a unit keeps, drops and stores. It is pure:
// a snapshot's stacks and a Policy go in, a Plan comes out, so every rule is
// table-testable without a world. The Life reflex carries the plan out.
package inventory

import (
	"cmp"
	"slices"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	// Slots is the main inventory with the hotbar.
	Slots = 36
	// HotbarSlots are the first of them, the only ones a unit can hold.
	HotbarSlots = 9
	// stackSize is assumed for every item; the server's table carries no limit.
	stackSize = 64
)

// Policy is what a unit holds on to. Zero values keep nothing of that class.
type Policy struct {
	// FullSlots: a deposit trip starts when this many slots or fewer are free.
	FullSlots int
	// KeepFood is how many food items stay, richest first.
	KeepFood int
	// KeepBlocks is how many of each building block stay.
	KeepBlocks int
	// KeepFiller is how many of each filler block (dirt, cobblestone) stay;
	// the rest is dropped.
	KeepFiller int
	// SpareTools is how many tools of a kind stay beyond the best one.
	SpareTools int
}

// Default is the policy a unit runs with unless the environment says otherwise.
func Default() Policy {
	return Policy{FullSlots: 4, KeepFood: 24, KeepBlocks: 128, KeepFiller: 64, SpareTools: 1}
}

// Move is a quantity of one item, by name; the stacks it comes from are chosen
// at the time it is carried out.
type Move struct {
	Name  string
	Count int
}

// Plan is what a snapshot's inventory needs.
type Plan struct {
	// Drops is junk and filler beyond the reserve, to throw away.
	Drops []Move
	// Deposits is the surplus to store, ordered by the slots it frees.
	Deposits []Move
	// Free is how many slots are empty.
	Free int
	// Freed is how many slots storing each deposit empties.
	Freed map[string]int
}

const (
	// MaxTripMoves bounds the items one trip stores: each is a chest visit.
	MaxTripMoves = 6
	// TripFree is how many slots beyond FullSlots a trip aims to free, so the
	// next trip is not due a minute later.
	TripFree = 8
)

// Trip picks the deposits for one visit to the chest, the ones that free the
// most slots first, stopping once the inventory has room again.
func (pol Policy) Trip(p Plan) []Move {
	free := p.Free
	var out []Move
	for _, m := range p.Deposits {
		if len(out) == MaxTripMoves || free >= pol.FullSlots+TripFree {
			break
		}
		out = append(out, m)
		free += p.Freed[m.Name]
	}
	return out
}

// Full says the inventory is down to the policy's reserve of free slots.
func (p Plan) Full(pol Policy) bool { return p.Free <= pol.FullSlots }

type class uint8

const (
	// other is loot with no rule: stored when the inventory fills.
	other class = iota
	junk
	filler
	build
	food
	tool
	armour
	// protected is never stored or dropped: gear for the job and for survival.
	protected
)

var junkNames = map[string]bool{
	"rotten_flesh": true, "spider_eye": true, "poisonous_potato": true, "pufferfish": true,
}

var fillerNames = map[string]bool{
	"dirt": true, "cobblestone": true, "cobbled_deepslate": true, "gravel": true,
	"andesite": true, "diorite": true, "granite": true, "tuff": true, "netherrack": true,
}

var buildNames = map[string]bool{
	"stone": true, "stone_bricks": true, "glass": true, "sand": true, "sandstone": true, "bricks": true,
}

var protectedNames = map[string]bool{
	"torch": true, "crafting_table": true, "chest": true, "furnace": true, "shield": true, "bow": true,
	"arrow": true, "bucket": true, "water_bucket": true, "shears": true, "flint_and_steel": true,
	"fishing_rod": true, "ender_pearl": true, "totem_of_undying": true, "elytra": true,
}

// ToolKinds are the tools a unit keeps the best of.
var ToolKinds = []string{"pickaxe", "axe", "shovel", "hoe", "sword"}

// ArmourPieces are the four worn pieces.
var ArmourPieces = []string{"helmet", "chestplate", "leggings", "boots"}

// materials ranks tool and armour materials, best first.
var materials = []string{"netherite", "diamond", "iron", "stone", "chainmail", "copper", "golden", "leather", "wooden"}

// foods are what a unit eats and the points each restores. The life reflex's
// eating table must agree; a test there keeps the two in step.
var foods = map[string]int{
	"cooked_beef": 8, "cooked_porkchop": 8, "pumpkin_pie": 8,
	"cooked_mutton": 6, "cooked_salmon": 6, "cooked_chicken": 6,
	"bread": 5, "baked_potato": 5, "cooked_cod": 5,
	"apple": 4, "carrot": 3, "melon_slice": 2, "cookie": 2, "sweet_berries": 2,
}

// FoodPoints is what eating the item restores, zero for a non-food.
func FoodPoints(name string) int { return foods[name] }

// Foods lists the food names for tests.
func Foods() map[string]int { return foods }

// IsJunk says an item is dropped whatever the unit is doing.
func IsJunk(name string) bool { return junkNames[name] }

func classify(name string) class {
	switch {
	case junkNames[name]:
		return junk
	case fillerNames[name]:
		return filler
	case foods[name] > 0:
		return food
	case ToolKind(name) != "":
		return tool
	case ArmourPiece(name) != "":
		return armour
	case protectedNames[name], strings.HasSuffix(name, "_sapling"), strings.HasSuffix(name, "_bed"):
		return protected
	case buildNames[name], strings.HasSuffix(name, "_planks"), strings.HasSuffix(name, "_log"):
		return build
	}
	return other
}

// ToolKind names the kind of tool an item is, empty for anything else.
func ToolKind(name string) string {
	for _, k := range ToolKinds {
		if strings.HasSuffix(name, "_"+k) {
			return k
		}
	}
	return ""
}

// ArmourPiece names the worn slot an item fits, empty for anything else.
func ArmourPiece(name string) string {
	for _, p := range ArmourPieces {
		if strings.HasSuffix(name, "_"+p) {
			return p
		}
	}
	return ""
}

// materialRank is 0 for the best material; an unknown one ranks last.
func materialRank(name string) int {
	for i, m := range materials {
		if strings.HasPrefix(name, m+"_") {
			return i
		}
	}
	return len(materials)
}

func durability(it belief.Item) int {
	if it.DurabilityPct == nil {
		return 100
	}
	return *it.DurabilityPct
}

// better orders gear: best material, then most durability, then lowest slot,
// so the choice is the same on every run.
func better(a, b belief.Item) int {
	return cmp.Or(
		cmp.Compare(materialRank(a.Name), materialRank(b.Name)),
		cmp.Compare(durability(b), durability(a)),
		cmp.Compare(a.Slot, b.Slot),
	)
}

// BestTool is the best stack of a tool kind ("pickaxe", which matches the
// item itself and any "<material>_pickaxe") in the inventory, by material,
// then durability, then lowest slot. hotbarOnly restricts it to what the unit
// can hold.
func BestTool(inv []belief.Item, kind string, hotbarOnly bool) (belief.Item, bool) {
	var best belief.Item
	found := false
	for _, it := range inv {
		if it.Count < 1 || (hotbarOnly && it.Slot >= HotbarSlots) {
			continue
		}
		if it.Name != kind && !strings.HasSuffix(it.Name, "_"+kind) {
			continue
		}
		if !found || better(it, best) < 0 {
			best, found = it, true
		}
	}
	return best, found
}

// ToolFor names the tool kind that breaks a block fastest, empty when no
// tool helps.
func ToolFor(block string) string {
	switch {
	case block == "dirt" || block == "grass_block" || block == "sand" || block == "gravel" ||
		block == "clay" || block == "snow" || block == "soul_sand" || block == "podzol" || block == "mud":
		return "shovel"
	case strings.HasSuffix(block, "_planks") || strings.HasSuffix(block, "_log") || strings.HasSuffix(block, "_wood") ||
		block == "crafting_table" || block == "chest" || block == "bookshelf":
		return "axe"
	case strings.HasSuffix(block, "_leaves") || block == "hay_block" || block == "nether_wart_block":
		return "hoe"
	case block == "web" || block == "cobweb" || block == "bamboo":
		return "sword"
	case block == "air" || block == "water" || block == "":
		return ""
	}
	return "pickaxe"
}

// Plan works out what to drop and what to store. Armour in the worn slots is
// not in the inventory and never touched.
func (pol Policy) Plan(inv []belief.Item) Plan {
	have := map[string]int{}
	slots := map[string]int{}
	names := []string{}
	for _, it := range inv {
		if it.Count < 1 {
			continue
		}
		if _, seen := have[it.Name]; !seen {
			names = append(names, it.Name)
		}
		have[it.Name] += it.Count
		slots[it.Name]++
	}
	slices.Sort(names)

	freed := map[string]int{}
	plan := Plan{Free: Slots - len(inv), Freed: freed}
	var surplus []Move
	store := func(name string, n int) {
		if n < 1 {
			return
		}
		surplus = append(surplus, Move{Name: name, Count: n})
		left := have[name] - n
		freed[name] = max(0, slots[name]-(left+stackSize-1)/stackSize)
	}

	for _, name := range names {
		switch classify(name) {
		case junk:
			plan.Drops = append(plan.Drops, Move{name, have[name]})
		case filler:
			if n := have[name] - pol.KeepFiller; n > 0 {
				plan.Drops = append(plan.Drops, Move{name, n})
			}
		case build:
			store(name, have[name]-pol.KeepBlocks)
		case other:
			store(name, have[name])
		}
	}
	for _, m := range pol.surplusFood(inv) {
		store(m.Name, m.Count)
	}
	for _, m := range pol.surplusGear(inv) {
		store(m.Name, m.Count)
	}

	// Most slots freed first, then by name: the biggest wins come before the
	// trip is cut short.
	slices.SortFunc(surplus, func(a, b Move) int {
		return cmp.Or(cmp.Compare(freed[b.Name], freed[a.Name]), cmp.Compare(a.Name, b.Name))
	})
	plan.Deposits = surplus
	return plan
}

// surplusFood is the food beyond what the policy keeps, the poorest first.
// Only stacks in the main inventory are stored: a unit eats from its hotbar,
// and nothing moves food there.
func (pol Policy) surplusFood(inv []belief.Item) []Move {
	var stacks []belief.Item
	total := 0
	for _, it := range inv {
		if foods[it.Name] > 0 && it.Count > 0 {
			total += it.Count
			if it.Slot >= HotbarSlots {
				stacks = append(stacks, it)
			}
		}
	}
	excess := total - pol.KeepFood
	if excess <= 0 {
		return nil
	}
	slices.SortFunc(stacks, func(a, b belief.Item) int {
		return cmp.Or(cmp.Compare(foods[a.Name], foods[b.Name]), cmp.Compare(a.Name, b.Name), cmp.Compare(b.Slot, a.Slot))
	})
	byName := map[string]int{}
	var order []string
	for _, st := range stacks {
		if excess == 0 {
			break
		}
		n := min(st.Count, excess)
		if _, ok := byName[st.Name]; !ok {
			order = append(order, st.Name)
		}
		byName[st.Name] += n
		excess -= n
	}
	out := make([]Move, 0, len(order))
	for _, name := range order {
		out = append(out, Move{name, byName[name]})
	}
	return out
}

// surplusGear is every tool beyond the best of its kind and its spares, and
// every armour piece beyond the best of its slot. A worn piece is not in the
// inventory, so a carried one is the unit's spare or a replacement.
//
// A deposit takes an item by name, so a name is stored only when every stack
// of it is surplus (two iron axes, one kept, stay), and a hotbar stack is never
// stored: the unit holds what is on the hotbar and nothing moves a tool there.
func (pol Policy) surplusGear(inv []belief.Item) []Move {
	surplus := map[string]int{}
	kept := map[string]bool{}
	var order []string
	group := func(keep int, key func(string) string) {
		by := map[string][]belief.Item{}
		var keys []string
		for _, it := range inv {
			k := key(it.Name)
			if k == "" || it.Count < 1 {
				continue
			}
			if _, ok := by[k]; !ok {
				keys = append(keys, k)
			}
			by[k] = append(by[k], it)
		}
		slices.Sort(keys)
		for _, k := range keys {
			items := by[k]
			slices.SortFunc(items, better)
			for i, it := range items {
				if i < min(keep, len(items)) || it.Slot < HotbarSlots {
					kept[it.Name] = true
					continue
				}
				if _, ok := surplus[it.Name]; !ok {
					order = append(order, it.Name)
				}
				surplus[it.Name] += it.Count
			}
		}
	}
	group(1+max(0, pol.SpareTools), ToolKind)
	group(1, ArmourPiece)
	var out []Move
	for _, name := range order {
		if !kept[name] {
			out = append(out, Move{name, surplus[name]})
		}
	}
	return out
}

// Take is part of a stack to move.
type Take struct {
	Item  belief.Item
	Count int
}

// Stacks picks the stacks a Move comes from: the highest slots first, so the
// hotbar the unit works from is the last to thin out.
func Stacks(inv []belief.Item, m Move) []Take {
	var from []belief.Item
	for _, it := range inv {
		if it.Name == m.Name && it.Count > 0 {
			from = append(from, it)
		}
	}
	slices.SortFunc(from, func(a, b belief.Item) int {
		return cmp.Compare(b.Slot, a.Slot)
	})
	var out []Take
	left := m.Count
	for _, it := range from {
		if left == 0 {
			break
		}
		n := min(it.Count, left)
		out = append(out, Take{Item: it, Count: n})
		left -= n
	}
	return out
}
