package main

import (
	"fmt"
	"strings"
	"testing"
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
