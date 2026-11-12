package task

import (
	"errors"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	enchantingTemplate = "enchanting_table"
	itemEnchanting     = "enchanting_table"
	itemObsidian       = "obsidian"
	itemDiamond        = "diamond"
	itemBook           = "book"
)

// EnchantingTableCost is what the enchanting table is crafted from. It is the
// ladder's long-range goal: obsidian takes a diamond pickaxe and a pool of
// water or lava, and a book takes paper and leather, neither of which the
// unit can fetch yet, so the template only crafts what the unit brings.
var EnchantingTableCost = map[string]int{itemObsidian: 4, itemDiamond: 2, itemBook: 1}

// expandProgression expands the templates of the stone-to-enchanting ladder.
// handled is false for any other template.
func expandProgression(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts []skill.Option) (sk []skill.Skill, handled bool, err error) {
	switch {
	case t.Template == mineOreTemplate && t.Entrance != nil && t.Area != nil && t.To != nil:
		sk, err = expandMineOre(t, world, self, opts)
	case t.Template == smeltTemplate && t.Block != nil:
		sk, err = expandSmelt(t, world, self, opts)
	case t.Template == ironAgeTemplate && t.Block != nil && t.Furnace != nil && t.To != nil && t.Area != nil && t.Entrance != nil:
		// Priced from what the unit holds when the task starts, not when it
		// was submitted behind other work.
		sk = []skill.Skill{skill.NewLazy(ironAgeTemplate, self, func(s belief.Snapshot) ([]skill.Skill, error) {
			return expandIronAge(t, world, s, self, opts)
		})}
	case t.Template == enchantingTemplate && t.Block != nil:
		sk = []skill.Skill{skill.NewLazy(enchantingTemplate, self, func(s belief.Snapshot) ([]skill.Skill, error) {
			return expandEnchanting(t, world, s, self, opts), nil
		})}
	default:
		return nil, false, nil
	}
	return sk, true, err
}

// parseEnchanting reads {"template":"enchanting_table","table":{x,y,z}} with an
// optional "at" cell to set the finished table down in.
func parseEnchanting(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Table    *Chest `json:"table"`
		At       *Chest `json:"at"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Table == nil {
		return Template{}, errors.New("table is required")
	}
	// The message names no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Table.inLab() || (raw.At != nil && !raw.At.inLab()) {
		return Template{}, errors.New("table and at must be inside the allowed area")
	}
	if raw.At != nil && *raw.At == *raw.Table {
		return Template{}, errors.New("table and at must be different cells")
	}
	return Template{Template: raw.Template, Block: raw.Table, At: raw.At}, nil
}

// expandEnchanting crafts an enchanting table beside the crafting table, from
// the obsidian, diamonds and book the unit holds, and places it at "at" if one
// is given. It stops before walking anywhere when an ingredient is missing.
func expandEnchanting(t Template, world skill.ChestWorld, s belief.Snapshot, self func() belief.Snapshot, opts []skill.Option) []skill.Skill {
	table := protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}
	out := []skill.Skill{}
	if InventoryOf(s)[itemEnchanting] == 0 {
		out = append(out,
			skill.NewRequire(self, EnchantingTableCost),
			skill.NewNear(world, self, table, tableReach),
			skill.NewCraft(world, self, itemEnchanting, 1))
	}
	if t.At != nil {
		out = append(out, skill.NewPlaceBlock(world, self, protocol.BlockPos{t.At.X, t.At.Y, t.At.Z}, itemEnchanting, opts...))
	}
	return out
}

// Stage is one rung of the progression ladder: a goal the unit holds or does
// not, and the template that works toward it.
type Stage struct {
	Name string
	// Template is the task that advances the stage, or "" when none exists yet.
	Template string
	// Done says the unit has what the stage is for.
	Done func(Inventory) bool
}

// coalStock is the coal a unit keeps for torches and the next smelt.
const coalStock = 8

func holdsAny(inv Inventory, items ...string) bool {
	for _, it := range items {
		if inv[it] > 0 {
			return true
		}
	}
	return false
}

func holdsIronTools(inv Inventory) bool {
	return inv["iron_pickaxe"] > 0 && inv["iron_axe"] > 0 && inv["iron_sword"] > 0
}

// Ladder is the stone-to-enchanting progression in order. A unit is at the
// first stage it has not finished.
var Ladder = []Stage{
	{"stone_tools", bootstrapTemplate, func(inv Inventory) bool {
		return holdsAny(inv, "stone_pickaxe", "iron_pickaxe")
	}},
	// Coal is spent on the way to iron, so a unit with iron tools is past it.
	{"coal", mineOreTemplate, func(inv Inventory) bool {
		return inv[itemCoal]+inv["charcoal"] >= coalStock || holdsIronTools(inv)
	}},
	{"iron_tools", ironAgeTemplate, holdsIronTools},
	{"diamonds", mineOreTemplate, func(inv Inventory) bool {
		return inv[itemDiamond] >= EnchantingTableCost[itemDiamond] || inv[itemEnchanting] > 0
	}},
	{"obsidian", "", func(inv Inventory) bool {
		return inv[itemObsidian] >= EnchantingTableCost[itemObsidian] || inv[itemEnchanting] > 0
	}},
	{"book", "", func(inv Inventory) bool {
		return inv[itemBook] >= EnchantingTableCost[itemBook] || inv[itemEnchanting] > 0
	}},
	{"enchanting_table", enchantingTemplate, func(inv Inventory) bool { return inv[itemEnchanting] > 0 }},
}

// NextStage is the first stage the inventory has not finished, or false when
// the unit has climbed the whole ladder.
func NextStage(s belief.Snapshot) (Stage, bool) {
	inv := InventoryOf(s)
	if inv[itemEnchanting] > 0 {
		return Stage{}, false
	}
	for _, st := range Ladder {
		if !st.Done(inv) {
			return st, true
		}
	}
	return Stage{}, false
}
