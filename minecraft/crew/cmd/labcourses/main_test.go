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

func TestRotateStates(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		rot      int
		expected map[string]any
	}{
		{
			name:     "cardinal and minecraft:cardinal",
			input:    map[string]any{"cardinal_direction": "north", "minecraft:cardinal_direction": "south"},
			rot:      90,
			expected: map[string]any{"cardinal_direction": "east", "minecraft:cardinal_direction": "west"},
		},
		{
			name:     "facing direction",
			input:    map[string]any{"facing_direction": 2}, // north
			rot:      90,
			expected: map[string]any{"facing_direction": 5}, // east
		},
		{
			name:     "weirdo direction",
			input:    map[string]any{"weirdo_direction": 0}, // east
			rot:      90,
			expected: map[string]any{"weirdo_direction": 2}, // south
		},
		{
			name:     "weirdo direction 180",
			input:    map[string]any{"weirdo_direction": 0}, // east
			rot:      180,
			expected: map[string]any{"weirdo_direction": 1}, // west
		},
		{
			name:     "lever floor and wall",
			input:    map[string]any{"lever_direction": "up_north_south"},
			rot:      90,
			expected: map[string]any{"lever_direction": "up_east_west"},
		},
		{
			name:     "lever wall 180",
			input:    map[string]any{"lever_direction": "north"},
			rot:      180,
			expected: map[string]any{"lever_direction": "south"},
		},
		{
			name:     "rail flat and curved",
			input:    map[string]any{"rail_direction": "north_south"},
			rot:      90,
			expected: map[string]any{"rail_direction": "east_west"},
		},
		{
			name:     "rail ascending",
			input:    map[string]any{"rail_direction": "ascending_north"},
			rot:      90,
			expected: map[string]any{"rail_direction": "ascending_east"},
		},
		{
			name:     "rail curved 180",
			input:    map[string]any{"rail_direction": "south_east"},
			rot:      180,
			expected: map[string]any{"rail_direction": "north_west"},
		},
		{
			name:     "pillar axis",
			input:    map[string]any{"pillar_axis": "x"},
			rot:      90,
			expected: map[string]any{"pillar_axis": "z"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := rotateStates(tc.input, tc.rot)
			for k, want := range tc.expected {
				if got := res[k]; got != want {
					t.Errorf("key %q = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// Bedrock's rail_direction is a number 0-9, the encoding the works field
// and imported structures use. Every value must move clockwise, where one
// quarter turn maps east to south.
func TestRotateRailInt(t *testing.T) {
	tests := []struct {
		start, turn, want int
	}{
		// 0 north-south and 1 east-west swap on odd turns
		{0, 0, 0}, {0, 1, 1}, {0, 2, 0}, {0, 3, 1},
		{1, 0, 1}, {1, 1, 0}, {1, 2, 1}, {1, 3, 0},
		// 2-5 ascending east, west, north, south
		{2, 0, 2}, {2, 1, 5}, {2, 2, 3}, {2, 3, 4},
		{3, 0, 3}, {3, 1, 4}, {3, 2, 2}, {3, 3, 5},
		{4, 0, 4}, {4, 1, 2}, {4, 2, 5}, {4, 3, 3},
		{5, 0, 5}, {5, 1, 3}, {5, 2, 4}, {5, 3, 2},
		// 6-9 curves south-east, south-west, north-west, north-east
		{6, 0, 6}, {6, 1, 7}, {6, 2, 8}, {6, 3, 9},
		{7, 0, 7}, {7, 1, 8}, {7, 2, 9}, {7, 3, 6},
		{8, 0, 8}, {8, 1, 9}, {8, 2, 6}, {8, 3, 7},
		{9, 0, 9}, {9, 1, 6}, {9, 2, 7}, {9, 3, 8},
	}
	for _, tc := range tests {
		if got := rotateRailInt(tc.start, tc.turn); got != tc.want {
			t.Errorf("rotateRailInt(%d, %d turn) = %d, want %d", tc.start, tc.turn, got, tc.want)
		}
	}
}

// Rotating a full turn must land every rotated state key back on its
// starting value, in both the numeric and string encodings.
func TestRotateStatesFourTurnsIdentity(t *testing.T) {
	values := map[string][]any{
		"facing_direction":   {0, 1, 2, 3, 4, 5},
		"direction":          {0, 1, 2, 3},
		"weirdo_direction":   {0, 1, 2, 3},
		"cardinal_direction": {"north", "east", "south", "west"},
		"lever_direction": {"up_north_south", "up_east_west", "down_north_south",
			"down_east_west", "north", "east", "south", "west"},
		"rail_direction": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			"north_south", "east_west",
			"ascending_north", "ascending_east", "ascending_south", "ascending_west",
			"south_east", "south_west", "north_west", "north_east"},
		"pillar_axis": {"x", "y", "z"},
	}
	for key, vals := range values {
		for _, v := range vals {
			cur := v
			for range 4 {
				cur = rotateState(key, cur, 1)
			}
			if cur != v {
				t.Errorf("%s %v: four quarter turns ended at %v", key, v, cur)
			}
		}
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
	rotated := rotateStates(door, 90)
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
