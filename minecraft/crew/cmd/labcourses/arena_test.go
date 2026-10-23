package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The reviewed build is 3524 commands: 2 ticking-area lines are extra and are
// printed by render, not collected by the builder. Pinning the count catches
// an accidental per-block command creeping into a ring or a pen.
const wantArenaCommands = 3524

var arenaCommandShape = regexp.MustCompile(`^(fill( -?\d+){6}|setblock( -?\d+){3}) \S+$`)

func buildArenaCommands() builder {
	var b builder
	buildArena(&b)
	return b
}

func TestArenaCommandCount(t *testing.T) {
	b := buildArenaCommands()
	if len(b.cmds) != wantArenaCommands {
		t.Fatalf("%d commands, want %d", len(b.cmds), wantArenaCommands)
	}
}

// Every command must be fill or setblock, use whole numbers, stay under the
// fill and world-height limits, and land inside the declared ticking areas.
func TestArenaCommandsAreValidAndBounded(t *testing.T) {
	b := buildArenaCommands()
	areas := arenaAreas()
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
	inArena := func(x, z int) bool {
		return x >= arenaBounds.x0 && x <= arenaBounds.x1 && z >= arenaBounds.z0 && z <= arenaBounds.z1
	}
	for _, cmd := range b.cmds {
		if !arenaCommandShape.MatchString(cmd) {
			t.Errorf("%q is not a fill or setblock command with integer coordinates", cmd)
			continue
		}
		f := strings.Fields(cmd)
		if strings.Contains(cmd, "light_block") {
			t.Errorf("%q: the arena must be lit by sea lanterns, not light blocks", cmd)
		}
		var x0, y0, z0 int
		var x1, y1, z1 int
		if f[0] == "fill" {
			mustScan(t, cmd, f[1:7], &x0, &y0, &z0, &x1, &y1, &z1)
			if x0 > x1 || y0 > y1 || z0 > z1 {
				t.Errorf("%q: corners are not ordered", cmd)
			}
			if v := (x1 - x0 + 1) * (y1 - y0 + 1) * (z1 - z0 + 1); v > maxFill {
				t.Errorf("%q: %d blocks, limit %d", cmd, v, maxFill)
			}
		} else {
			mustScan(t, cmd, f[1:4], &x0, &y0, &z0)
			x1, y1, z1 = x0, y0, z0
		}
		if y0 < -64 || y1 > 319 {
			t.Errorf("%q: outside the world's height", cmd)
		}
		if !inArena(x0, z0) || !inArena(x1, z1) {
			t.Errorf("%q: outside the arena bounds %v", cmd, arenaBounds)
		}
		for _, p := range [][2]int{{x0, z0}, {x1, z1}, {x0, z1}, {x1, z0}} {
			if !loaded(p[0], p[1]) {
				t.Errorf("%q: corner %v outside every ticking area", cmd, p)
				break
			}
		}
	}
}

func TestArenaIsDeterministic(t *testing.T) {
	a, b := buildArenaCommands(), buildArenaCommands()
	if !reflect.DeepEqual(a.cmds, b.cmds) {
		t.Fatal("two arena builds differ")
	}
	one, err := json.Marshal(arenaLayout())
	if err != nil {
		t.Fatal(err)
	}
	two, err := json.Marshal(arenaLayout())
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Error("two layout documents differ")
	}
}

// The layout is the director's contract: each gate box in it must be the
// exact iron-bar box the build places, so clearing the listed box opens that
// pen and nothing else.
func TestArenaLayoutMatchesBuild(t *testing.T) {
	b := buildArenaCommands()
	placed := map[string]int{}
	for _, cmd := range b.cmds {
		placed[cmd]++
	}
	doc := arenaLayout()
	if len(doc.Pens) != arenaPenCount {
		t.Fatalf("%d pens, want %d", len(doc.Pens), arenaPenCount)
	}
	gates := map[string]bool{}
	for _, p := range doc.Pens {
		if p.AngleDeg < 0 || p.AngleDeg >= 360 {
			t.Errorf("pen %d angle %d out of range", p.ID, p.AngleDeg)
		}
		if p.Spawn.Y != ground+1 {
			t.Errorf("pen %d spawn y = %d, want %d", p.ID, p.Spawn.Y, ground+1)
		}
		d := arenaD2(p.Spawn.X, p.Spawn.Z)
		if d <= arenaFloorR*arenaFloorR || d >= arenaWallOuter*arenaWallOuter {
			t.Errorf("pen %d spawn (%d,%d) is not under the stands", p.ID, p.Spawn.X, p.Spawn.Z)
		}
		key := fmt.Sprintf("%v", p.Gate)
		if gates[key] {
			t.Errorf("pen %d shares a gate box with another pen", p.ID)
		}
		gates[key] = true
		cmd := fmt.Sprintf("fill %d %d %d %d %d %d iron_bars",
			p.Gate.X0, p.Gate.Y0, p.Gate.Z0, p.Gate.X1, p.Gate.Y1, p.Gate.Z1)
		if p.Open != openCommand(box3{p.Gate.X0, p.Gate.Y0, p.Gate.Z0, p.Gate.X1, p.Gate.Y1, p.Gate.Z1}) {
			t.Errorf("pen %d open command %q does not clear its gate", p.ID, p.Open)
		}
		if placed[cmd] != 1 {
			t.Errorf("pen %d: gate command %q appears %d times in the build", p.ID, cmd, placed[cmd])
		}
	}
	if len(doc.ArchedGates) != 4 {
		t.Fatalf("%d arched gates, want 4", len(doc.ArchedGates))
	}
	names := map[string]bool{}
	for _, g := range doc.ArchedGates {
		names[g.Name] = true
		if g.Opening.Y1 <= arenaCorridorY {
			t.Errorf("%s gate is not arched above the corridor", g.Name)
		}
		width := min(g.Corridor.X1-g.Corridor.X0+1, g.Corridor.Z1-g.Corridor.Z0+1)
		if width != 2*arenaCorridorHalf+1 {
			t.Errorf("%s corridor is %d blocks wide, want %d", g.Name, width, 2*arenaCorridorHalf+1)
		}
	}
	for _, want := range []string{"north", "east", "south", "west"} {
		if !names[want] {
			t.Errorf("no %s arched gate", want)
		}
	}
	start := doc.StartGate
	startCmd := fmt.Sprintf("fill %d %d %d %d %d %d iron_bars",
		start.Gate.X0, start.Gate.Y0, start.Gate.Z0, start.Gate.X1, start.Gate.Y1, start.Gate.Z1)
	if placed[startCmd] != 1 {
		t.Errorf("start gate command %q appears %d times", startCmd, placed[startCmd])
	}
	if start.Open != openCommand(box3{start.Gate.X0, start.Gate.Y0, start.Gate.Z0, start.Gate.X1, start.Gate.Y1, start.Gate.Z1}) {
		t.Errorf("start gate open command %q does not clear its gate", start.Open)
	}
	if b := arenaStartGate(); start.Gate.Z0 != arenaCZ-arenaTier1Inner || start.Gate.X0 != -arenaCorridorHalf || start.Gate.X1 != arenaCorridorHalf {
		t.Errorf("start gate %v is not on the north side at the floor edge", b)
	}
}

func TestArenaUsesSeaLanterns(t *testing.T) {
	b := buildArenaCommands()
	lanterns := 0
	for _, cmd := range b.cmds {
		if strings.Contains(cmd, "sea_lantern") {
			lanterns++
		}
	}
	// Twelve pen ceilings plus the two rings of wall lanterns.
	if lanterns < 36 {
		t.Errorf("%d sea lantern commands, want at least 36", lanterns)
	}
}
