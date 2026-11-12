package task

import (
	"errors"
	"fmt"
	"slices"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	ironAgeTemplate = "iron_age"
	itemCoal        = "coal"
	itemRawIron     = "raw_iron"
	itemTorch       = "torch"
	// A torch craft makes four torches from one coal and one stick.
	torchesPerCraft = 4
	// smeltsPerCoal is how many items a coal smelts.
	smeltsPerCoal = 8
	// One torch lights about three stair steps, and one about six tunnel cells.
	stepsPerTorch  = 3
	tunnelPerTorch = 6
	spareTorches   = 2
)

// parseIronAge reads {"template":"iron_age", "table":{...}, "furnace":{...},
// "chest":{...}, ...the mine_shaft fields...} with an optional "search" box.
// It takes a unit with a stone pickaxe and a furnace to a full set of iron
// tools; see expandIronAge for the chain.
func parseIronAge(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		shaftFields
		Table   *Chest `json:"table"`
		Furnace *Chest `json:"furnace"`
		Chest   *Chest `json:"chest"`
		Search  *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"search"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Table == nil || raw.Furnace == nil || raw.Chest == nil {
		return Template{}, errors.New("table, furnace and chest are required")
	}
	// The messages name no coordinate: they are lab locations and must stay
	// out of logs.
	if !raw.Table.inLab() || !raw.Furnace.inLab() || !raw.Chest.inLab() {
		return Template{}, errors.New("table, furnace and chest must be inside the allowed area")
	}
	if *raw.Table == *raw.Furnace || *raw.Table == *raw.Chest || *raw.Furnace == *raw.Chest {
		return Template{}, errors.New("table, furnace and chest must be different cells")
	}
	a, err := raw.validate()
	if err != nil {
		return Template{}, err
	}
	t := Template{Template: raw.Template, Block: raw.Table, Furnace: raw.Furnace, To: raw.Chest,
		Entrance: raw.Entrance, Facing: raw.Facing, Depth: raw.Depth, Branches: raw.Branches,
		BranchLength: raw.Length, Area: &a}
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

// ironPlan is what an iron age run has to fetch and make, worked out from the
// inventory the unit starts with.
type ironPlan struct {
	// ingots to smelt, and raw iron still to mine for them.
	ingots, rawIron int
	// coal to mine: fuel for the smelt and for the torches.
	coal int
	// torchCrafts is the number of torch crafts, each four torches.
	torchCrafts int
	demand      Demand
	short       map[string]int
}

func ironTargets() map[string]int {
	targets := map[string]int{}
	for _, kind := range ToolKinds {
		targets[toolItem("iron", kind)] = 1
	}
	return targets
}

// torchesFor is how many torches lighting a dive needs: a few for every step
// of the stair and every cell of its branches.
func torchesFor(depth, branches, length int) int {
	return depth/stepsPerTorch + branches*length/tunnelPerTorch + spareTorches
}

// planIronAge prices the whole chain. Everything the unit already holds is
// credited at each step, so a run is the difference between what the unit has
// and what the iron tools cost.
func planIronAge(inv Inventory, depth, branches, length int) ironPlan {
	var p ironPlan
	targets := ironTargets()
	p.demand = DemandFor(targets, inv)
	p.ingots = p.demand.Short(inv)[itemIronIngot]
	p.rawIron = max(p.ingots-inv[itemRawIron], 0)
	if p.rawIron > 0 {
		wanted := torchesFor(depth, branches, length) - inv[itemTorch]
		p.torchCrafts = max((wanted+torchesPerCraft-1)/torchesPerCraft, 0)
	}
	if p.torchCrafts > 0 {
		// The torches' handles are sticks, priced with the tools'.
		targets[itemStick] = p.torchCrafts
		p.demand = DemandFor(targets, inv)
	}
	fuel := (p.ingots + smeltsPerCoal - 1) / smeltsPerCoal
	p.coal = max(fuel+p.torchCrafts-inv[itemCoal]-inv["charcoal"], 0)
	p.short = p.demand.Short(inv)
	delete(p.short, itemIronIngot)
	return p
}

// expandIronAge is the chain from stone to iron tools:
//
//  1. fell the logs the iron tools' handles and the torches need (searching for
//     a tree first when search is set), and craft the planks and sticks;
//  2. dive for coal if there is not enough for the smelt and the torches, and
//     take it out of the chest the dive deposits into;
//  3. craft the torches;
//  4. dive for iron with torches and take the raw iron out of the chest;
//  5. smelt it in the furnace, setting the carried one down if the cell is
//     empty;
//  6. stand beside the table and craft the iron pickaxe, axe and sword.
//
// The quantities are worked out when the task starts, from what the unit holds
// then, so a unit that has coal or ingots or a tool already skips that step.
// Both dives take the same stair, so the second walks the part the first dug.
func expandIronAge(t Template, world skill.ChestWorld, s belief.Snapshot, self func() belief.Snapshot, opts []skill.Option) ([]skill.Skill, error) {
	inv := InventoryOf(s)
	table := protocol.BlockPos{t.Block.X, t.Block.Y, t.Block.Z}
	furnace := protocol.BlockPos{t.Furnace.X, t.Furnace.Y, t.Furnace.Z}
	chest := protocol.BlockPos{t.To.X, t.To.Y, t.To.Z}
	plan := planIronAge(inv, t.Depth, t.Branches, t.BranchLength)

	// Coal breaks to any pickaxe, iron ore needs a stone one. Check before
	// the first dive: a wooden pickaxe would otherwise dig the coal dive and
	// the iron dive for nothing and fail at the chest.
	if plan.rawIron > 0 || plan.coal > 0 {
		need := 0
		if plan.rawIron > 0 {
			need = 1
		}
		if heldTier(inv, "pickaxe") < need {
			return nil, fmt.Errorf("no %s or better in inventory", toolItem(ToolTiers[need], "pickaxe"))
		}
	}

	var out []skill.Skill
	add := func(s ...skill.Skill) { out = append(out, s...) }
	// A craft is queued only for what the unit does not already hold.
	craftTo := func(item string, n int) {
		if n > inv.held(item) {
			add(skill.NewCraft(world, self, item, min(n, maxCraftCount)))
		}
	}

	// Stop before setting out for what the run cannot do without.
	need := map[string]int{}
	if !standing(world, furnace, "furnace") && !standing(world, furnace, "lit_furnace") {
		need[itemFurnace] = 1
	}
	if !standing(world, table, itemTable) {
		need[itemTable] = 1
	}
	if len(need) > 0 && plan.ingots > 0 {
		add(skill.NewRequire(self, need))
	}
	if !standing(world, table, itemTable) && inv[itemTable] > 0 {
		add(skill.NewPlaceBlock(world, self, table, itemTable, opts...))
	}

	if n := plan.short[logsPool]; n > 0 {
		tool := ""
		if heldTier(inv, "axe") >= 0 {
			tool = "axe"
		}
		if t.Search != nil {
			box := skill.Area{
				Min: protocol.BlockPos{t.Search.Min.X, t.Search.Min.Y, t.Search.Min.Z},
				Max: protocol.BlockPos{t.Search.Max.X, t.Search.Max.Y, t.Search.Max.Z},
			}
			add(skill.NewExplore(world, self, "oak_log", box, nil))
		}
		add(skill.NewFell(world, self, "oak_log", "oak_log", tool, n, mineRadius, nil, opts...))
	}
	// Planks per wood, from the logs the unit will hold: the felled ones are
	// oak, and a log makes planks of its own wood only.
	for _, p := range plankCrafts(inv, plan.short[logsPool], plan.demand.Gross[planksPool]-inv.held(planksPool)) {
		craftTo(p.item, p.total)
	}
	craftTo(itemStick, plan.demand.Gross[itemStick])

	dive := func(ores []string, goal map[string]int, torches bool) error {
		sub := t
		sub.Ores, sub.Torches, sub.Items = ores, torches, goal
		sk, err := expandMineOre(sub, world, self, opts)
		add(sk...)
		return err
	}
	if plan.coal > 0 {
		if err := dive([]string{"coal"}, map[string]int{itemCoal: plan.coal}, false); err != nil {
			return nil, err
		}
		add(skill.NewWithdrawChest(world, self, chest, map[string]int{itemCoal: plan.coal}, opts...))
	}
	craftTo(itemTorch, inv[itemTorch]+plan.torchCrafts*torchesPerCraft)
	if plan.rawIron > 0 {
		if err := dive([]string{"iron"}, map[string]int{itemRawIron: plan.rawIron}, true); err != nil {
			return nil, err
		}
		add(skill.NewWithdrawChest(world, self, chest, map[string]int{itemRawIron: plan.rawIron}, opts...))
	}
	var toCraft []string
	handles := 0
	for _, kind := range ToolKinds {
		if tool := toolItem("iron", kind); inv[tool] == 0 {
			toCraft = append(toCraft, tool)
			for _, in := range chainRecipes[tool].in {
				if in.item == itemStick {
					handles += in.n
				}
			}
		}
	}
	if plan.ingots > 0 {
		// Short of coal, the smelt falls back on wood, but not on the sticks
		// the iron tools are about to need.
		sm, err := skill.NewSmelt(world, self, furnace, itemRawIron, plan.ingots,
			append(slices.Clone(opts), skill.Keep(map[string]int{itemStick: handles}))...)
		if err != nil {
			return nil, err
		}
		add(sm)
	}
	if len(toCraft) > 0 {
		add(skill.NewNear(world, self, table, tableReach))
	}
	for _, tool := range toCraft {
		craftTo(tool, 1)
	}
	if len(out) == 0 {
		return nil, errors.New("the unit already holds the iron tools")
	}
	return out, nil
}
