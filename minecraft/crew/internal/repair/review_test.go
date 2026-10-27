package repair

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// Lines copied from the generator's clear.cmds and build.cmds.
const generatorLines = `fill -420 64 -864 -389 94 -833 air
fill -293 52 -812 -293 52 -812 lantern ["hanging"=true]
fill -345 50 -752 -345 50 -752 iron_door ["door_hinge_bit"=false,"open_bit"=false,"upper_block_bit"=false,"minecraft:cardinal_direction"="east"]
fill -300 50 -763 -300 50 -763 lever ["lever_direction"="up_east_west","open_bit"=false]
fill -291 50 -809 -291 50 -809 chest ["minecraft:cardinal_direction"="west"]
fill -300 40 -800 -290 45 -790 stone_bricks
`

func TestParseOpsGeneratorLines(t *testing.T) {
	got := ops(t, generatorLines)
	want := []string{
		"air", `lantern ["hanging"=true]`,
		`iron_door ["door_hinge_bit"=false,"open_bit"=false,"upper_block_bit"=false,"minecraft:cardinal_direction"="east"]`,
		`lever ["lever_direction"="up_east_west","open_bit"=false]`,
		`chest ["minecraft:cardinal_direction"="west"]`, "stone_bricks",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ops, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Block != want[i] {
			t.Errorf("op %d block = %q, want %q", i, got[i].Block, want[i])
		}
	}
	if got[0].Box != (Box{X0: -420, Y0: 64, Z0: -864, X1: -389, Y1: 94, Z1: -833}) {
		t.Errorf("clear box = %+v", got[0].Box)
	}
}

func TestNormalizeScanSpelling(t *testing.T) {
	for in, want := range map[string]string{
		`minecraft:lantern["hanging"=true]`:                          `lantern ["hanging"=true]`,
		`lantern ["hanging"=true]`:                                   `lantern ["hanging"=true]`,
		`lever ["lever_direction"="up_east_west", "open_bit"=false]`: `lever ["lever_direction"="up_east_west","open_bit"=false]`,
		"minecraft:cave_air":                                         "air",
		"void_air":                                                   "air",
	} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAirVariants(t *testing.T) {
	for _, b := range []string{"air", "cave_air", "void_air"} {
		if !IsAir(b) {
			t.Errorf("IsAir(%q) = false", b)
		}
	}
	if IsAir("stone") || IsAir("airlock") {
		t.Error("non-air treated as air")
	}
	if k := kindOf("stone", "cave_air"); k != KindMissing {
		t.Errorf("cave_air found under stone = %q, want missing", k)
	}
	if k := kindOf("void_air", "stone"); k != KindIntruder {
		t.Errorf("stone found where void_air planned = %q, want intruder", k)
	}
	// A scan reporting cave_air must not count as damage against planned air.
	b := Box{X0: 0, Y0: 0, Z0: 0, X1: 1, Y1: 0, Z1: 0}
	plan := ops(t, "fill 0 0 0 1 0 0 air")
	live := OpsReader{Ops: ops(t, "fill 0 0 0 1 0 0 cave_air")}
	d, err := Diff(plan, live, b, arena.Layout{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Cells != 0 {
		t.Errorf("cave_air vs air counted %d damaged cells", d.Cells)
	}
}

func TestIsLateMatchesNameOnly(t *testing.T) {
	if IsLate(`stone_bricks ["vine_bit"=true]`) {
		t.Error("state text made a solid block late")
	}
	if !IsLate(`lantern ["hanging"=true]`) || !IsLate("redstone_wire") {
		t.Error("late blocks not recognised")
	}
}

func TestHalveOversizedSingleLayer(t *testing.T) {
	b := Box{X0: 0, Y0: 5, Z0: 0, X1: 299, Y1: 5, Z1: 299}
	parts := split(b)
	if len(parts) < 3 {
		t.Fatalf("a %d-cell layer split into %d pieces", volume(b), len(parts))
	}
	total := 0
	for _, p := range parts {
		if v := volume(p); v > maxFill {
			t.Errorf("piece %+v holds %d blocks, over %d", p, v, maxFill)
		}
		total += volume(p)
	}
	if total != volume(b) {
		t.Errorf("pieces cover %d cells, want %d", total, volume(b))
	}
}

func TestApplySweepsEveryStaleAreaName(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxTickingAreas; i++ {
		want := fmt.Sprintf("tickingarea remove crew-repair-%d", i)
		found := false
		for _, c := range f.Cmds {
			found = found || c == want
		}
		if !found {
			t.Errorf("stale area %d not removed", i)
		}
	}
}

func TestSetupFailureIsRefusedNotAborted(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	idle := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "tickingarea add") {
			return "Too many ticking areas", nil
		}
		return idle(cmd)
	}
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err == nil {
		t.Fatal("expected an error")
	}
	if r.Status == StatusAborted || r.RepairsRun != 0 {
		t.Errorf("status %q after %d repairs; no repair ran so the arena is untouched", r.Status, r.RepairsRun)
	}
}
