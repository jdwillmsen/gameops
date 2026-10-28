package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
)

func printBlueprint(bp *blueprint.Blueprint, ox, oz, rotation int) {
	areas, b := blueprintCommands(bp, ox, oz, rotation)
	// Blueprint plans flow through render() so fields and blueprints keep
	// one printer and one output format.
	err := render(os.Stdout, course{
		build: func(out *builder) { *out = b },
		areas: func() []box { return areas },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "labcourses:", err)
		os.Exit(1)
	}
}

func blueprintCommands(bp *blueprint.Blueprint, ox, oz, rotation int) ([]box, builder) {
	var b builder
	w, d := bp.Footprint(rotation)
	area := box{ox, oz, ox + w - 1, oz + d - 1}
	b.fill(area.x0, ground+1, area.z0, area.x1, ground+bp.Size.Y, area.z1, "air")
	for _, c := range bp.Cells(rotation) {
		name := c.Block.Name
		if name == "door" || strings.HasSuffix(name, "_door") || name == "redstone_wire" {
			b.raw("setblock %d %d %d %s%s", ox+c.Pos.X, ground+c.Pos.Y+1, oz+c.Pos.Z, name, formatStates(c.Block.States))
			continue
		}
		b.raw("fill %d %d %d %d %d %d %s%s", ox+c.Pos.X, ground+c.Pos.Y+1, oz+c.Pos.Z, ox+c.Pos.X, ground+c.Pos.Y+1, oz+c.Pos.Z, name, formatStates(c.Block.States))
	}
	for _, check := range bp.Checks {
		x, z := blueprint.Rotate(check.Set.X, check.Set.Z, bp.Size.X, bp.Size.Z, rotation)
		block := check.Block
		b.raw("setblock %d %d %d %s%s", ox+x, ground+check.Set.Y+1, oz+z, block.Name, formatStates(blueprint.RotateStates(block.States, rotation)))
		// Allow redstone propagation across ticks before testing the expected block state.
		for i := 0; i < 5; i++ {
			b.raw("testforblock %d 319 %d air", ox, oz)
		}
		x, z = blueprint.Rotate(check.Expect.X, check.Expect.Z, bp.Size.X, bp.Size.Z, rotation)
		b.raw("testforblock %d %d %d %s%s", ox+x, ground+check.Expect.Y+1, oz+z, check.Expected.Name, formatStates(blueprint.RotateStates(check.Expected.States, rotation)))
	}
	return tickingAreas(area), b
}

func formatStates(states map[string]any) string {
	if len(states) == 0 {
		return ""
	}
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	v := make([]string, 0, len(keys))
	for _, k := range keys {
		value := fmt.Sprint(states[k])
		if _, ok := states[k].(string); ok {
			value = strconv.Quote(value)
		}
		v = append(v, "\""+k+"\"="+value)
	}
	return " [" + strings.Join(v, ",") + "]"
}
