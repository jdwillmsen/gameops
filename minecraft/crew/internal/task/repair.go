package task

import (
	"errors"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair/colosseum"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	repairTemplate = "repair_build"
	// planColosseum is the only plan a repair can be asked for.
	planColosseum = "colosseum"
	menderRole    = "mender"
)

// parseRepair reads {"template":"repair_build","plan":"colosseum","area":{...},"dry_run":bool}.
func parseRepair(body []byte) (Template, error) {
	var raw struct {
		Template string `json:"template"`
		Plan     string `json:"plan"`
		Area     *struct {
			Min *Chest `json:"min"`
			Max *Chest `json:"max"`
		} `json:"area"`
		DryRun bool `json:"dry_run"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	if raw.Plan != planColosseum {
		return Template{}, errors.New("plan must be colosseum")
	}
	if raw.Area == nil || raw.Area.Min == nil || raw.Area.Max == nil {
		return Template{}, errors.New("area with min and max is required")
	}
	a := Area{Min: *raw.Area.Min, Max: *raw.Area.Max}
	if err := a.validate(); err != nil {
		return Template{}, err
	}
	// The lab square the other templates use (x and z within 500) ends short
	// of the Colosseum, whose z runs to -864, so the arena's own footprint is
	// the bound here. The message names no coordinate: they are lab locations
	// and must stay out of logs.
	layout, err := colosseum.Layout()
	if err != nil {
		return Template{}, err
	}
	if !repair.InsideBox(repair.Footprint(layout), areaBox(a)) {
		return Template{}, errors.New("area is outside the Colosseum")
	}
	return Template{Template: raw.Template, Plan: raw.Plan, Area: &a, DryRun: raw.DryRun}, nil
}

func areaBox(a Area) repair.Box {
	return repair.Box{
		X0: int(a.Min.X), Y0: int(a.Min.Y), Z0: int(a.Min.Z),
		X1: int(a.Max.X), Y1: int(a.Max.Y), Z1: int(a.Max.Z),
	}
}

// expandRepair builds the repair skill; opts carry the guard and the logger.
func expandRepair(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts ...skill.Option) ([]skill.Skill, error) {
	sw, ok := world.(skill.RepairWorld)
	if !ok {
		return nil, errors.New("the world reports no block states")
	}
	if t.Plan != planColosseum || t.Area == nil {
		return nil, errors.New("unknown plan")
	}
	plan, err := colosseum.Plan()
	if err != nil {
		return nil, err
	}
	layout, err := colosseum.Layout()
	if err != nil {
		return nil, err
	}
	spec := skill.RepairSpec{
		Plan: plan, Layout: layout, DryRun: t.DryRun,
		Area: skill.Area{
			Min: protocol.BlockPos{t.Area.Min.X, t.Area.Min.Y, t.Area.Min.Z},
			Max: protocol.BlockPos{t.Area.Max.X, t.Area.Max.Y, t.Area.Max.Z},
		},
	}
	return []skill.Skill{skill.NewRepair(sw, self, spec, opts...)}, nil
}
