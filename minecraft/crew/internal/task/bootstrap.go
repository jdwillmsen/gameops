package task

import (
	"errors"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	bootstrapTemplate = "bootstrap"
	// tableReach is how close to its table the unit stands to craft: inside
	// the craft skill's own click range with room to drift.
	tableReach = 2.5
)

// parseBootstrap reads {"template":"bootstrap","table":{x,y,z}} with an
// optional "chest" cell and an optional "search" box. The table and chest
// cells are where the unit puts what it crafts; both must be free air.
func parseBootstrap(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Table    *Chest `json:"table"`
		Chest    *Chest `json:"chest"`
		Search   *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"search"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Table == nil {
		return Template{}, errors.New("table is required")
	}
	// The messages name no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Table.inLab() || (raw.Chest != nil && !raw.Chest.inLab()) {
		return Template{}, errors.New("table and chest must be inside the allowed area")
	}
	if raw.Chest != nil && *raw.Chest == *raw.Table {
		return Template{}, errors.New("table and chest must be different cells")
	}
	t := Template{Template: raw.Template, Block: raw.Table, To: raw.Chest}
	if raw.Search != nil {
		if raw.Search.Min == nil || raw.Search.Max == nil {
			return Template{}, errors.New("search needs min and max")
		}
		s := SearchBox{Min: *raw.Search.Min, Max: *raw.Search.Max}
		if err := s.validate(); err != nil {
			return Template{}, err
		}
		t.Search = &s
	}
	return t, nil
}

// bootstrapTargets is everything a unit has after bootstrapping that it may
// still be missing: the table and chest (unless one already stands), both
// tool sets and a furnace.
func bootstrapTargets(tableStands, chestStands, hasChest bool) map[string]int {
	targets := map[string]int{itemFurnace: 1}
	if !tableStands {
		targets[itemTable] = 1
	}
	if hasChest && !chestStands {
		targets[itemChest] = 1
	}
	for _, tier := range ToolTiers[:2] {
		for _, kind := range ToolKinds {
			targets[toolItem(tier, kind)] = 1
		}
	}
	return targets
}

// expandBootstrap turns a unit holding anything into one with a table, a
// chest, wooden and stone tools and a furnace. How much to gather is worked
// out when the task starts, from what the unit holds then, so a unit that
// already holds logs or tools gathers and crafts only the difference.
func expandBootstrap(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts []skill.Option) []skill.Skill {
	inv := InventoryOf(self())
	table := protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}
	var chest protocol.BlockPos
	if t.To != nil {
		chest = protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
	}
	tableStands := standing(world, table, itemTable)
	chestStands := t.To != nil && standing(world, chest, itemChest)

	targets := bootstrapTargets(tableStands, chestStands, t.To != nil)
	demand := DemandFor(targets, inv)
	short := demand.Short(inv)

	var box *skill.Area
	var accept *skill.Area
	if t.Search != nil {
		b := skill.Area{
			Min: protocol.BlockPos{t.Search.Min.X, t.Search.Min.Y, t.Search.Min.Z},
			Max: protocol.BlockPos{t.Search.Max.X, t.Search.Max.Y, t.Search.Max.Z},
		}
		box = &b
	}
	var out []skill.Skill
	add := func(s ...skill.Skill) { out = append(out, s...) }
	craftTo := func(item string, n int) {
		if n > 0 {
			add(skill.NewCraft(world, self, item, min(n, maxCraftCount)))
		}
	}
	find := func(block string) {
		if box != nil {
			add(skill.NewExplore(world, self, block, *box, accept))
		}
	}

	if n := short[logsPool]; n > 0 {
		// Bare hands fell a tree, slowly; an axe the unit holds is used.
		tool := ""
		for name := range inv {
			if strings.HasSuffix(name, "_axe") {
				tool = "axe"
			}
		}
		find("oak_log")
		add(skill.NewFell(world, self, "oak_log", "oak_log", tool, n, mineRadius, nil, opts...))
	}

	planks := plankItem(inv)
	// Planks of another wood the unit already holds pay for part of the
	// demand, but the craft skill counts only the item it is asked for.
	otherPlanks := inv.held(planksPool) - inv[planks]
	craftTo(planks, demand.Gross[planksPool]-otherPlanks)
	craftTo(itemStick, demand.Gross[itemStick])
	if !tableStands {
		craftTo(itemTable, 1)
		add(skill.NewPlaceBlock(world, self, table, itemTable, opts...))
	}
	// Everything that needs a table is crafted while standing beside it, the
	// chest and the wooden tools before the unit leaves for stone.
	add(skill.NewNear(world, self, table, tableReach))
	if t.To != nil && !chestStands {
		craftTo(itemChest, 1)
	}
	for _, kind := range ToolKinds {
		craftTo(toolItem(ToolTiers[0], kind), 1)
	}
	if t.To != nil && !chestStands {
		add(skill.NewPlaceBlock(world, self, chest, itemChest, opts...))
	}

	if n := short[itemCobblestone]; n > 0 {
		find("stone")
		add(skill.NewMineBlockWith(world, self, "stone", itemCobblestone, "pickaxe", n, mineRadius))
	}
	add(skill.NewNear(world, self, table, tableReach))
	for _, kind := range ToolKinds {
		craftTo(toolItem(ToolTiers[1], kind), 1)
	}
	craftTo(itemFurnace, 1)
	return out
}

// standing says whether the world shows the block at the cell.
func standing(world skill.ChestWorld, cell protocol.BlockPos, block string) bool {
	if world == nil {
		return false
	}
	name, known := world.Block(cell)
	return known && name == block
}
