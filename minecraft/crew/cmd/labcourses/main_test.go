package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/blueprints"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
)

// Every command must land in a loaded chunk, stay under the fill limit, and
// fit the server's ten ticking areas of at most 100 chunks each.
func TestCoursesFitServerLimits(t *testing.T) {
	for name, c := range courses {
		t.Run(name, func(t *testing.T) {
			var b builder
			c.build(&b)
			areas := c.areas()
			if len(areas) > 10 {
				t.Fatalf("%d ticking areas, server allows 10", len(areas))
			}
			for _, a := range areas {
				if n := ((a.x1 >> 4) - (a.x0 >> 4) + 1) * ((a.z1 >> 4) - (a.z0 >> 4) + 1); n > maxAreaChunks {
					t.Errorf("area %v covers %d chunks", a, n)
				}
			}
			loaded := func(x, z int) bool {
				for _, a := range areas {
					if x >= a.x0 && x <= a.x1 && z >= a.z0 && z <= a.z1 {
						return true
					}
				}
				return false
			}
			for _, cmd := range b.cmds {
				f := strings.Fields(cmd)
				var x0, y0, z0, x1, y1, z1 int
				switch f[0] {
				case "fill":
					mustScan(t, cmd, f[1:7], &x0, &y0, &z0, &x1, &y1, &z1)
					if v := (x1 - x0 + 1) * (y1 - y0 + 1) * (z1 - z0 + 1); v > maxFill {
						t.Errorf("%s: %d blocks, limit %d", cmd, v, maxFill)
					}
				case "setblock", "summon", "replaceitem", "place":
					i := 1
					switch {
					case f[0] == "replaceitem" && f[1] == "entity":
						if !strings.HasPrefix(f[2], "@e[type=armor_stand,") {
							t.Errorf("%s: replaceitem must target an armor stand", cmd)
						}
						continue
					case f[0] == "place":
						if f[1] != "feature" {
							t.Errorf("%s: only place feature is used", cmd)
						}
						i = 3
					case f[0] != "setblock":
						i = 2
					}
					mustScan(t, cmd, f[i:i+3], &x0, &y0, &z0)
					x1, y1, z1 = x0, y0, z0
				case "kill":
					if !strings.HasPrefix(f[1], "@e[type=") {
						t.Errorf("%s: kill must target a mob type, never players", cmd)
					}
					continue
				default:
					t.Errorf("unexpected command %q", cmd)
					continue
				}
				if y0 < -64 || y1 > 319 {
					t.Errorf("%s: outside the world's height", cmd)
				}
				for _, p := range [][2]int{{x0, z0}, {x1, z1}, {x0, z1}, {x1, z0}} {
					if !loaded(p[0], p[1]) {
						t.Errorf("%s: corner %v outside every ticking area", cmd, p)
						break
					}
				}
			}
		})
	}
}

func TestBlueprintsFitServerLimits(t *testing.T) {
	for _, name := range []string{"small_house", "redstone_sample"} {
		t.Run(name, func(t *testing.T) {
			// Stamping goes through the embedded copy, the way the
			// nightly job's image reads it.
			bp, err := blueprint.LoadFS(blueprints.Files, name+".json")
			if err != nil {
				t.Fatal(err)
			}
			areas, b := blueprintCommands(bp, 0, -800, 90)
			if len(areas) > 10 {
				t.Fatalf("%d ticking areas, server allows 10", len(areas))
			}
			for _, a := range areas {
				if n := ((a.x1 >> 4) - (a.x0 >> 4) + 1) * ((a.z1 >> 4) - (a.z0 >> 4) + 1); n > maxAreaChunks {
					t.Errorf("area %v covers %d chunks", a, n)
				}
			}
			for _, cmd := range b.cmds {
				f := strings.Fields(cmd)
				var x, y, z int
				if f[0] != "fill" && f[0] != "setblock" && f[0] != "testforblock" {
					t.Fatalf("unexpected command %q", cmd)
				}
				mustScan(t, cmd, f[1:4], &x, &y, &z)
				if y < -64 || y > 319 {
					t.Errorf("%s: outside world height", cmd)
				}
				loaded := false
				for _, a := range areas {
					if x >= a.x0 && x <= a.x1 && z >= a.z0 && z <= a.z1 {
						loaded = true
						break
					}
				}
				if !loaded {
					t.Errorf("%s: outside every ticking area", cmd)
				}
			}
		})
	}
}

func mustScan(t *testing.T, cmd string, fields []string, dst ...*int) {
	t.Helper()
	for i, s := range fields {
		if _, err := fmt.Sscan(s, dst[i]); err != nil {
			t.Fatalf("%s: field %q is not a number", cmd, s)
		}
	}
}

// The fields and the hub must not overlap each other, and every field must
// stay inside the flat square: outside it is natural terrain.
func TestFieldsDoNotOverlap(t *testing.T) {
	boxes := map[string]box{
		"hub":     {-61, -519, 51, -323},
		"grounds": groundsGrid.bounds(),
		"courses": courseGrid.bounds(),
		"trees":   treesGrid.bounds(),
		"works":   worksGrid.bounds(),
		"arena":   arenaBounds,
	}
	square := box{-500, -900, 499, 99}
	for a, r := range boxes {
		if r.x0 < square.x0 || r.x1 > square.x1 || r.z0 < square.z0 || r.z1 > square.z1 {
			t.Errorf("%s %v leaves the flat square %v", a, r, square)
		}
		for b, s := range boxes {
			if a < b && r.x0 <= s.x1 && r.x1 >= s.x0 && r.z0 <= s.z1 && r.z1 >= s.z0 {
				t.Errorf("%s %v overlaps %s %v", a, r, b, s)
			}
		}
	}
}

func TestFillSplitsLargeBoxes(t *testing.T) {
	var b builder
	b.fill(0, 0, 0, 299, 9, 299, "air")
	total := 0
	for _, cmd := range b.cmds {
		var x0, y0, z0, x1, y1, z1 int
		if _, err := fmt.Sscanf(cmd, "fill %d %d %d %d %d %d air", &x0, &y0, &z0, &x1, &y1, &z1); err != nil {
			t.Fatal(cmd, err)
		}
		v := (x1 - x0 + 1) * (y1 - y0 + 1) * (z1 - z0 + 1)
		if v > maxFill {
			t.Errorf("%s: %d blocks", cmd, v)
		}
		total += v
	}
	if total != 300*10*300 {
		t.Errorf("split fills cover %d blocks, want %d", total, 300*10*300)
	}
}

// Rotation must reach states loaded from disk, not just in-memory maps:
// encoding/json decodes numbers as float64, and a state that misses the
// numeric cases in rotateState would silently keep its old facing.
func TestBlueprintRotationThroughLoad(t *testing.T) {
	bp, err := blueprint.LoadFS(blueprints.Files, "small_house.json")
	if err != nil {
		t.Fatal(err)
	}
	var door map[string]any
	for _, blk := range bp.Palette {
		if blk.Name == "wooden_door" && blk.States["upper_block_bit"] == false {
			door = blk.States
		}
	}
	if door == nil {
		t.Fatal("small_house has no lower wooden_door palette entry")
	}
	rotated := blueprint.RotateStates(door, 90)
	if got := rotated["direction"]; got != 1 {
		t.Fatalf("door direction after 90° = %v (%T), want 1", got, got)
	}
	_, out := blueprintCommands(bp, 100, -500, 90)
	var placed []string
	for _, cmd := range out.cmds {
		if strings.Contains(cmd, "wooden_door") {
			placed = append(placed, cmd)
		}
	}
	if len(placed) != 1 || !strings.Contains(placed[0], `"direction"=1`) {
		t.Fatalf("stamped door command missing rotated direction: %v", placed)
	}
}
