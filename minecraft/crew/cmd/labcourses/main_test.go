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
				case "setblock", "summon", "replaceitem":
					i := 1
					if f[0] != "setblock" {
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

func TestFieldsDoNotOverlapTheHub(t *testing.T) {
	hub := box{-61, -519, 51, -323}
	for name, r := range map[string]box{"grounds": groundsGrid.bounds(), "courses": courseGrid.bounds()} {
		if r.x0 <= hub.x1 && r.x1 >= hub.x0 && r.z0 <= hub.z1 && r.z1 >= hub.z0 {
			t.Errorf("%s %v overlaps the hub %v", name, r, hub)
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
