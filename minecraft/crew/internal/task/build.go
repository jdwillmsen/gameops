package task

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/blueprints"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const (
	buildTemplate = "build_blueprint"
	// builderRole is the one role that takes build_blueprint.
	builderRole = "builder"
	// maxBuildSpan bounds a stamp on every axis, inside what a blueprint
	// itself allows.
	maxBuildSpan = 64
)

// ErrWrongRole says the task is for a unit with another role.
var ErrWrongRole = errors.New("this unit's role does not take this task")

var blueprintName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// SetRole tells the runner the unit's role, which decides the templates it
// takes: build_blueprint only on a builder, repair_build only on a mender.
func (r *Runner) SetRole(role string) {
	r.mu.Lock()
	r.role = role
	r.mu.Unlock()
}

// admits must be called with mu held.
func (r *Runner) admits(t Template) error {
	switch t.Template {
	case buildTemplate:
		if r.role != builderRole {
			return ErrWrongRole
		}
	case repairTemplate:
		if r.role != menderRole {
			return ErrWrongRole
		}
	}
	return nil
}

func loadBlueprint(name string) (*blueprint.Blueprint, error) {
	if !blueprintName.MatchString(name) {
		return nil, errors.New("unknown blueprint")
	}
	bp, err := blueprint.LoadFS(blueprints.Files, name+".json")
	if err != nil {
		return nil, errors.New("unknown blueprint")
	}
	return bp, nil
}

// parseBuild reads {"template":"build_blueprint","blueprint":"<name>",
// "at":{x,y,z},"rotation":0,"dry_run":false}. At is the footprint's minimum
// corner after rotation. The qty is the blocks to stamp.
func parseBuild(body []byte) (Template, error) {
	var raw struct {
		Template  string `json:"template"`
		Blueprint string `json:"blueprint"`
		At        *Chest `json:"at"`
		Rotation  int    `json:"rotation"`
		DryRun    bool   `json:"dry_run"`
	}
	if err := decodeStrict(body, &raw); err != nil {
		return Template{}, err
	}
	bp, err := loadBlueprint(raw.Blueprint)
	if err != nil {
		return Template{}, err
	}
	if raw.At == nil {
		return Template{}, errors.New("at is required")
	}
	if raw.Rotation != 0 && raw.Rotation != 90 && raw.Rotation != 180 && raw.Rotation != 270 {
		return Template{}, errors.New("rotation must be 0, 90, 180 or 270")
	}
	w, d := bp.Footprint(raw.Rotation)
	if w > maxBuildSpan || d > maxBuildSpan || bp.Size.Y > maxBuildSpan {
		return Template{}, fmt.Errorf("blueprint must span at most %d blocks on each axis", maxBuildSpan)
	}
	// Both corners are checked, and the messages name no coordinate: they
	// are lab locations and must stay out of logs.
	far := Chest{X: raw.At.X + int32(w) - 1, Y: raw.At.Y + int32(bp.Size.Y) - 1, Z: raw.At.Z + int32(d) - 1}
	if !raw.At.inLab() || !far.inLab() {
		return Template{}, errors.New("blueprint does not fit the allowed area")
	}
	return Template{Template: raw.Template, Blueprint: raw.Blueprint, At: raw.At, Rotation: raw.Rotation,
		DryRun: raw.DryRun, Qty: len(bp.Cells(raw.Rotation))}, nil
}

func expandBuild(t Template, world skill.ChestWorld, self func() belief.Snapshot, opts []skill.Option) ([]skill.Skill, error) {
	bp, err := loadBlueprint(t.Blueprint)
	if err != nil {
		return nil, err
	}
	min := protocol.BlockPos{t.At.X, t.At.Y, t.At.Z}
	return []skill.Skill{skill.NewBuild(world, self, skill.BuildSpec{
		Cells:  skill.BlueprintCells(bp, min, t.Rotation),
		DryRun: t.DryRun,
	}, opts...)}, nil
}

// noteBuild copies a stamp's report onto the task record and says whether it
// changed.
func noteBuild(e *entry, sk skill.Skill) bool {
	br, ok := sk.(skill.BuildReporter)
	if !ok {
		return false
	}
	rep := br.Report()
	if e.Build != nil && reflect.DeepEqual(*e.Build, rep) {
		return false
	}
	e.Build = &rep
	return true
}
