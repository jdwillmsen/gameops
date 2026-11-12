package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/inventory"
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
// chest, wooden and stone tools and a furnace. It reads the inventory and the
// world as they are when it is called, which the runner does as the task
// starts (see bootstrapPlan), so a unit that already holds logs or tools
// gathers and crafts only the difference.
func expandBootstrap(t Template, world skill.ChestWorld, snapshot belief.Snapshot, self func() belief.Snapshot, opts []skill.Option) []skill.Skill {
	inv := InventoryOf(snapshot)
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
	work := false
	add := func(s ...skill.Skill) {
		for _, sk := range s {
			work = work || sk.Name() != "near"
		}
		out = append(out, s...)
	}
	craftTo := func(item string, n int) {
		if n > 0 && inv[item] < n {
			add(skill.NewCraft(world, self, item, min(n, maxCraftCount)))
		}
	}
	find := func(block string) {
		if box != nil {
			add(skill.NewExplore(world, self, block, *box, accept))
		}
	}

	felled := short[logsPool]
	if felled > 0 {
		// Bare hands fell a tree, slowly; an axe on the hotbar is used, and
		// one in the pack cannot be held.
		tool := ""
		for _, it := range snapshot.Inventory {
			if strings.HasSuffix(it.Name, "_axe") && it.Slot < hotbarSlots {
				tool = "axe"
			}
		}
		find("oak_log")
		add(skill.NewFell(world, self, "oak_log", "oak_log", tool, felled, mineRadius, nil, opts...))
	}

	// The felled logs are oak, and a log makes planks of its own wood only, so
	// the planks follow the logs the unit will hold, not a species it guessed.
	for _, p := range plankCrafts(inv, felled, demand.Gross[planksPool]-inv.held(planksPool)) {
		craftTo(p.item, p.total)
	}
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
		add(&hotbarGate{self: self, tool: "pickaxe"})
		find("stone")
		add(skill.NewMineBlockWith(world, self, "stone", itemCobblestone, "pickaxe", n, mineRadius))
	}
	add(skill.NewNear(world, self, table, tableReach))
	for _, kind := range ToolKinds {
		craftTo(toolItem(ToolTiers[1], kind), 1)
	}
	craftTo(itemFurnace, 1)
	// A unit that already has all of it only walks to its table and back; a
	// repeat run does nothing at all.
	if !work {
		return []skill.Skill{settled{}}
	}
	return out
}

// settled is the whole of a bootstrap whose unit already has everything.
type settled struct{}

func (settled) Name() string                { return bootstrapTemplate }
func (settled) Ready(belief.Snapshot) error { return nil }

func (settled) Step(context.Context, skill.Actuator) (skill.Status, error) {
	return skill.Done, nil
}

// hotbarSlots is how many inventory slots the hotbar spans.
const hotbarSlots = 9

// hotbarGate fails a bootstrap early and plainly when the tool its next step
// swings is not on the hotbar. A freshly crafted tool lands in the first
// empty slot, the hotbar's before the pack's, so only a unit whose hotbar is
// full of kit can be without; moving a stack between slots is not something
// the actuator can do yet.
type hotbarGate struct {
	self func() belief.Snapshot
	tool string
}

func (g *hotbarGate) Name() string                { return "hotbar_gate" }
func (g *hotbarGate) Ready(belief.Snapshot) error { return nil }

func (g *hotbarGate) Step(context.Context, skill.Actuator) (skill.Status, error) {
	if _, ok := inventory.BestTool(g.self().Inventory, g.tool, true); !ok {
		return skill.Failed, fmt.Errorf("no %s on the hotbar: free a hotbar slot and run the bootstrap again", g.tool)
	}
	return skill.Done, nil
}

// standing says whether the world shows the block at the cell.
func standing(world skill.ChestWorld, cell protocol.BlockPos, block string) bool {
	if world == nil {
		return false
	}
	name, known := world.Block(cell)
	return known && name == block
}
