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
	mineOreTemplate = "mine_ore"
	// An ore dive goes deeper and wider than a plain shaft: diamonds lie far
	// below the surface, so the stair and the box around it are longer.
	maxOreDepth        = 64
	maxOreBranchLength = 48
	maxOreSpan         = 160
	maxOreKinds        = 3
)

// shaftFields are the request fields every template that digs a shaft shares.
type shaftFields struct {
	Entrance *Chest `json:"entrance"`
	Facing   string `json:"facing"`
	Depth    int    `json:"depth"`
	Branches int    `json:"branches"`
	Length   int    `json:"branch_length"`
	Area     *struct {
		Min *Chest `json:"min"`
		Max *Chest `json:"max"`
	} `json:"area"`
}

// validate checks the shaft's shape and returns its area. The lab bound is not
// optional for a template that removes terrain.
func (f shaftFields) validate() (Area, error) {
	if f.Entrance == nil {
		return Area{}, errors.New("entrance is required")
	}
	if _, ok := facings[f.Facing]; !ok {
		return Area{}, errors.New("facing must be north, south, east or west")
	}
	if f.Depth < 1 || f.Depth > maxOreDepth {
		return Area{}, fmt.Errorf("depth must be 1 to %d", maxOreDepth)
	}
	if f.Branches < 0 || f.Branches > maxBranches {
		return Area{}, fmt.Errorf("branches must be 0 to %d", maxBranches)
	}
	if f.Length < 0 || f.Length > maxOreBranchLength || (f.Branches > 0) != (f.Length > 0) {
		return Area{}, fmt.Errorf("branch_length must be 1 to %d with branches, and absent without", maxOreBranchLength)
	}
	if f.Area == nil || f.Area.Min == nil || f.Area.Max == nil {
		return Area{}, errors.New("area with min and max is required")
	}
	a := Area{Min: *f.Area.Min, Max: *f.Area.Max}
	for _, ax := range [][2]int32{{a.Min.X, a.Max.X}, {a.Min.Y, a.Max.Y}, {a.Min.Z, a.Max.Z}} {
		if ax[0] > ax[1] {
			return Area{}, errors.New("area min must not exceed max")
		}
		// int64 so a span across the int32 range cannot wrap.
		if int64(ax[1])-int64(ax[0])+1 > maxOreSpan {
			return Area{}, fmt.Errorf("area must span at most %d blocks on each axis", maxOreSpan)
		}
	}
	return a, nil
}

// parseMineOre reads {"template":"mine_ore", ...the mine_shaft fields...,
// "ores":["coal","iron"], "torches":true, "goal":{"raw_iron":8}}. The shaft
// takes the ores it is asked for as it digs, lights its way if torches is set,
// and ends early once every item of goal has been gained.
func parseMineOre(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		shaftFields
		To      *Chest         `json:"to"`
		Ores    []string       `json:"ores"`
		Torches bool           `json:"torches"`
		Goal    map[string]int `json:"goal"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	a, err := raw.validate()
	if err != nil {
		return Template{}, err
	}
	if raw.To == nil {
		return Template{}, errors.New("to is required")
	}
	if len(raw.Ores) < 1 || len(raw.Ores) > maxOreKinds {
		return Template{}, fmt.Errorf("ores must list 1 to %d of %v", maxOreKinds, skill.OreNames())
	}
	drops := map[string]bool{}
	for i, name := range raw.Ores {
		kind, ok := skill.OreByName(name)
		if !ok {
			return Template{}, fmt.Errorf("ores must list only %v", skill.OreNames())
		}
		if slices.Contains(raw.Ores[:i], name) {
			return Template{}, errors.New("ores lists an ore twice")
		}
		drops[kind.Item] = true
	}
	for item, qty := range raw.Goal {
		if !drops[item] {
			return Template{}, errors.New("goal names an item no listed ore drops")
		}
		if qty < minQty || qty > maxQty {
			return Template{}, fmt.Errorf("qty must be %d to %d", minQty, maxQty)
		}
	}
	return Template{Template: raw.Template, Entrance: raw.Entrance, Facing: raw.Facing, Depth: raw.Depth,
		Branches: raw.Branches, BranchLength: raw.Length, Area: &a, To: raw.To,
		Ores: raw.Ores, Torches: raw.Torches, Items: raw.Goal}, nil
}

// oreKinds resolves the template's ore names. Parse has already vouched for
// them.
func oreKinds(names []string) []skill.OreKind {
	kinds := make([]skill.OreKind, 0, len(names))
	for _, n := range names {
		if k, ok := skill.OreByName(n); ok {
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// shaftSpec is the geometry of a dig the template describes.
func shaftSpec(t Template) skill.ShaftSpec {
	return skill.ShaftSpec{
		Entrance: protocol.BlockPos{t.Entrance.X, t.Entrance.Y, t.Entrance.Z},
		Dir:      facings[t.Facing],
		Depth:    t.Depth, Branches: t.Branches, BranchLength: t.BranchLength,
		Area:  skillArea(t.Area),
		Chest: protocol.BlockPos{t.To.X, t.To.Y, t.To.Z},
	}
}

func expandMineOre(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts []skill.Option) ([]skill.Skill, error) {
	spec := shaftSpec(t)
	spec.Ores, spec.Torches, spec.OreGoal = oreKinds(t.Ores), t.Torches, t.Items
	sk, err := skill.NewMineShaft(world, self, spec, opts...)
	if err != nil {
		return nil, err
	}
	return []skill.Skill{sk}, nil
}
