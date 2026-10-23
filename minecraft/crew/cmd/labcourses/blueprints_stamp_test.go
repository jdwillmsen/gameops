package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/blueprints"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
)

// A stamp plan is rendered with no server attached, so for every embedded
// blueprint and every rotation the rendering must succeed and every fill/setblock coordinate must land
// inside the rotated footprint at the origin. The check and marker
// positions stay inside it in x/z too: only the tick marker reads the
// world's build height at the origin corner, its one documented position
// outside the footprint's height range. Turning a course must not change
// the number of commands.
func TestBlueprintStampsAllRotations(t *testing.T) {
	entries, err := fs.ReadDir(blueprints.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			bp, err := blueprint.LoadFS(blueprints.Files, e.Name())
			if err != nil {
				t.Fatal(err)
			}
			var want int
			for _, rot := range []int{0, 90, 180, 270} {
				ox, oz := -1200, 2000
				areas, b := blueprintCommands(bp, ox, oz, rot)
				if len(areas) == 0 {
					t.Fatalf("rotation %d: no ticking areas", rot)
				}
				var out bytes.Buffer
				if err := render(&out, course{
					build: func(out *builder) { *out = b },
					areas: func() []box { return areas },
				}); err != nil {
					t.Fatalf("rotation %d: render failed: %v", rot, err)
				}

				w, d := bp.Size.X, bp.Size.Z
				if rot == 90 || rot == 270 {
					w, d = d, w
				}
				marker := fmt.Sprintf("testforblock %d 319 %d air", ox, oz)
				for _, cmd := range b.cmds {
					f := strings.Fields(cmd)
					switch f[0] {
					case "fill", "setblock", "testforblock":
					default:
						t.Errorf("rotation %d: unexpected command %q", rot, cmd)
						continue
					}
					if cmd == marker {
						continue
					}
					var coords [][3]int
					var x, y, z int
					mustScan(t, cmd, f[1:4], &x, &y, &z)
					coords = append(coords, [3]int{x, y, z})
					if f[0] == "fill" {
						var x1, y1, z1 int
						mustScan(t, cmd, f[4:7], &x1, &y1, &z1)
						coords = append(coords, [3]int{x1, y1, z1})
					}
					for _, c := range coords {
						if c[0] < ox || c[0] > ox+w-1 || c[2] < oz || c[2] > oz+d-1 ||
							c[1] < ground+1 || c[1] > ground+bp.Size.Y {
							t.Errorf("rotation %d: %q lies outside the rotated footprint %dx%d at (%d,%d)", rot, cmd, w, d, ox, oz)
							break
						}
					}
				}
				if want == 0 {
					want = len(b.cmds)
				} else if len(b.cmds) != want {
					t.Fatalf("rotation %d stamps %d commands, rotation 0 stamps %d", rot, len(b.cmds), want)
				}
			}
		})
	}
}
