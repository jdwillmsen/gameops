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
	w := bp.Size.X
	d := bp.Size.Z
	if rotation == 90 || rotation == 270 {
		w, d = d, w
	}
	area := box{ox, oz, ox + w - 1, oz + d - 1}
	b.fill(area.x0, ground+1, area.z0, area.x1, ground+bp.Size.Y, area.z1, "air")
	for _, p := range bp.BuildOrder {
		idx := strings.IndexByte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_", bp.Layers[p.Y][p.Z*bp.Size.X+p.X])
		block := bp.Palette[idx]
		if block.Name == "air" || block.States["upper_block_bit"] == true {
			continue
		}
		x, z := rotate(p.X, p.Z, bp.Size.X, bp.Size.Z, rotation)
		states := rotateStates(block.States, rotation)
		name := block.Name
		if name == "door" || strings.HasSuffix(name, "_door") {
			b.raw("setblock %d %d %d %s%s", ox+x, ground+p.Y+1, oz+z, name, formatStates(states))
			continue
		}
		if name == "redstone_wire" {
			b.raw("setblock %d %d %d %s%s", ox+x, ground+p.Y+1, oz+z, name, formatStates(states))
			continue
		}
		b.raw("fill %d %d %d %d %d %d %s%s", ox+x, ground+p.Y+1, oz+z, ox+x, ground+p.Y+1, oz+z, name, formatStates(states))
	}
	for _, check := range bp.Checks {
		x, z := rotate(check.Set.X, check.Set.Z, bp.Size.X, bp.Size.Z, rotation)
		block := check.Block
		b.raw("setblock %d %d %d %s%s", ox+x, ground+check.Set.Y+1, oz+z, block.Name, formatStates(rotateStates(block.States, rotation)))
		// Allow redstone propagation across ticks before testing the expected block state.
		for i := 0; i < 5; i++ {
			b.raw("testforblock %d 319 %d air", ox, oz)
		}
		x, z = rotate(check.Expect.X, check.Expect.Z, bp.Size.X, bp.Size.Z, rotation)
		b.raw("testforblock %d %d %d %s%s", ox+x, ground+check.Expect.Y+1, oz+z, check.Expected.Name, formatStates(rotateStates(check.Expected.States, rotation)))
	}
	return tickingAreas(area), b
}

func rotate(x, z, w, d, r int) (int, int) {
	switch r % 360 {
	case 90:
		return d - 1 - z, x
	case 180:
		return w - 1 - x, d - 1 - z
	case 270:
		return z, w - 1 - x
	default:
		return x, z
	}
}

func rotateStates(in map[string]any, r int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	turns := (r % 360) / 90
	if turns == 0 {
		return out
	}
	for k, v := range out {
		baseKey := strings.TrimPrefix(k, "minecraft:")
		switch baseKey {
		case "facing_direction", "direction", "weirdo_direction", "cardinal_direction", "lever_direction", "rail_direction", "pillar_axis":
			out[k] = rotateState(baseKey, v, turns)
		}
	}
	return out
}

func rotateState(key string, v any, n int) any {
	if s, ok := v.(string); ok {
		switch key {
		case "pillar_axis":
			if n%2 == 1 && (s == "x" || s == "z") {
				if s == "x" {
					return "z"
				}
				return "x"
			}
			return s
		case "cardinal_direction":
			arr := []string{"south", "west", "north", "east"}
			return arr[(indexString(arr, s)+n)%4]
		case "lever_direction":
			switch s {
			case "up_north_south":
				if n%2 == 1 {
					return "up_east_west"
				}
				return s
			case "up_east_west":
				if n%2 == 1 {
					return "up_north_south"
				}
				return s
			case "down_north_south":
				if n%2 == 1 {
					return "down_east_west"
				}
				return s
			case "down_east_west":
				if n%2 == 1 {
					return "down_north_south"
				}
				return s
			default:
				wallCycle := []string{"north", "east", "south", "west"}
				for i, d := range wallCycle {
					if d == s {
						return wallCycle[(i+n)%4]
					}
				}
				return s
			}
		case "rail_direction":
			return rotateRail(s, n)
		}
		return s
	}
	if num, ok := v.(int32); ok {
		v = int(num)
	}
	if num, ok := v.(int); ok {
		switch key {
		case "facing_direction":
			cycle := []int{2, 5, 3, 4}
			for i, direction := range cycle {
				if direction == num {
					return cycle[(i+n)%4]
				}
			}
			return num
		case "direction":
			return (num + n) % 4
		case "weirdo_direction":
			cycle := []int{0, 2, 1, 3}
			for i, d := range cycle {
				if d == num {
					return cycle[(i+n)%4]
				}
			}
			return num
		}
	}
	return v
}

func rotateRail(s string, n int) string {
	if s == "north_south" || s == "east_west" {
		if n%2 == 1 {
			if s == "north_south" {
				return "east_west"
			}
			return "north_south"
		}
		return s
	}
	ascCycle := []string{"ascending_north", "ascending_east", "ascending_south", "ascending_west"}
	for i, d := range ascCycle {
		if d == s {
			return ascCycle[(i+n)%4]
		}
	}
	curveCycle := []string{"south_east", "south_west", "north_west", "north_east"}
	for i, d := range curveCycle {
		if d == s {
			return curveCycle[(i+n)%4]
		}
	}
	return s
}

func indexString(arr []string, v string) int {
	for i, s := range arr {
		if s == v {
			return i
		}
	}
	return 0
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
