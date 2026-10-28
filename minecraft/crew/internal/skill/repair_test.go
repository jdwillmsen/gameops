package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
)

func planOf(t *testing.T, text string) []repair.Op {
	t.Helper()
	ops, err := repair.ParseOps(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func areaOf(x0, y0, z0, x1, y1, z1 int32) Area {
	return Area{Min: protocol.BlockPos{x0, y0, z0}, Max: protocol.BlockPos{x1, y1, z1}}
}

// damagedRow is a row of cobblestone on the rig's floor with one block
// missing at x 5 and a dirt intruder in the air above x 6.
func damagedRow(t *testing.T) (simWorld, RepairSpec) {
	w := flatWorld(12)
	w[protocol.BlockPos{4, 1, 4}] = "cobblestone"
	w[protocol.BlockPos{6, 1, 4}] = "cobblestone"
	w[protocol.BlockPos{6, 2, 4}] = "dirt"
	return w, RepairSpec{
		Plan: planOf(t, "fill 4 2 4 6 2 4 air\nfill 4 1 4 6 1 4 cobblestone"),
		Area: areaOf(4, 1, 4, 6, 2, 4),
	}
}

func TestRepairFixesAMissingBlockAndAnIntruder(t *testing.T) {
	w, spec := damagedRow(t)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	sk := NewRepair(stateWorld{w}, r.self, spec)
	if err := sk.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	st, err := r.run(sk, 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if n := w[protocol.BlockPos{5, 1, 4}]; n != "cobblestone" {
		t.Errorf("missing block is %q", n)
	}
	if n := w[protocol.BlockPos{6, 2, 4}]; n != "air" {
		t.Errorf("intruder is %q", n)
	}
	if r.breaks != 1 || r.placed != 1 {
		t.Errorf("broke %d, placed %d, want 1 and 1", r.breaks, r.placed)
	}
	text, ok := sk.(Reporter).Report()
	if !ok || !strings.HasPrefix(text, "repaired 2 cells") || !strings.Contains(text, "nothing left to repair") {
		t.Errorf("report = %q", text)
	}
	if got := sk.(Progresser).Progress(); got != 2 {
		t.Errorf("progress = %d, want 2", got)
	}
}

func TestRepairDryRunReportsTheTriageAndTouchesNothing(t *testing.T) {
	w, spec := damagedRow(t)
	spec.DryRun = true
	r := newRig(t, w, feetAt(1, 1, 2))
	sk := NewRepair(stateWorld{w}, r.self, spec)
	if err := sk.Ready(r.self()); err != nil {
		t.Errorf("a dry run needs no creative mode: %v", err)
	}
	st, err := r.run(sk, 10)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	text, _ := sk.(Reporter).Report()
	for _, want := range []string{"triage: 2 of", "intruder 1", "missing 1", "zones"} {
		if !strings.Contains(text, want) {
			t.Errorf("report %q lacks %q", text, want)
		}
	}
	if r.ticks != 0 || r.breaks != 0 || r.placed != 0 {
		t.Errorf("a dry run sent %d inputs, broke %d, placed %d", r.ticks, r.breaks, r.placed)
	}
	if len(text) > reportBytes {
		t.Errorf("report is %d bytes", len(text))
	}
}

func TestRepairNeedsCreativeUnlessDry(t *testing.T) {
	w, spec := damagedRow(t)
	sk := NewRepair(stateWorld{w}, nil, spec)
	if err := sk.Ready(belief.Snapshot{}); err == nil || !strings.Contains(err.Error(), "creative") {
		t.Errorf("survival: err = %v", err)
	}
	if err := sk.Ready(belief.Snapshot{Creative: true}); err != nil {
		t.Errorf("creative: err = %v", err)
	}
}

// Cells the unit was never sent are not damage and are not touched.
func TestRepairLeavesUnseenCellsAlone(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	spec := RepairSpec{Plan: planOf(t, "fill 20 1 20 22 1 20 cobblestone"), Area: areaOf(20, 1, 20, 22, 1, 20), DryRun: true}
	sk := NewRepair(stateWorld{w}, r.self, spec)
	if st, err := r.run(sk, 10); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if text, _ := sk.(Reporter).Report(); !strings.Contains(text, "0 of 0 scanned") || !strings.Contains(text, "3 not scanned") {
		t.Errorf("report = %q", text)
	}
}

func TestRepairRefusesDamageTooBigToWalk(t *testing.T) {
	w := flatWorld(20)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	spec := RepairSpec{Plan: planOf(t, "fill 0 1 0 19 6 19 cobblestone"), Area: areaOf(0, 1, 0, 19, 6, 19)}
	sk := NewRepair(stateWorld{w}, r.self, spec)
	st, err := r.run(sk, 10)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "arenarepair") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.ticks != 0 || r.placed != 0 {
		t.Errorf("sent %d inputs and placed %d before refusing", r.ticks, r.placed)
	}
}

func TestRepairRefusesMoreBlockTypesThanTheHotbarHolds(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	var plan []string
	for i, item := range []string{"dirt", "cobblestone", "oak_log", "gravel", "coal", "iron_axe", "wooden_sword", "iron_sword", "bread", "cooked_beef"} {
		plan = append(plan, "setblock "+string(rune('0'+i))+" 1 8 "+item)
	}
	spec := RepairSpec{Plan: planOf(t, strings.Join(plan, "\n")), Area: areaOf(0, 1, 8, 9, 1, 8)}
	st, err := r.run(NewRepair(stateWorld{w}, r.self, spec), 10)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "hotbar") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

// A block with no item, or the right block in another state, is left and
// counted rather than attempted.
func TestRepairCountsWhatItCannotPlace(t *testing.T) {
	w := flatWorld(12)
	w[protocol.BlockPos{5, 1, 5}] = "cobblestone"
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	spec := RepairSpec{
		Plan: planOf(t, "setblock 4 1 5 redstone_wire\nsetblock 5 1 5 cobblestone [\"wall_connection_type_north\"=\"tall\"]\nsetblock 7 1 7 cobblestone"),
		Area: areaOf(4, 1, 5, 7, 1, 7),
	}
	sk := NewRepair(stateWorld{w}, r.self, spec)
	if st, err := r.run(sk, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	text, _ := sk.(Reporter).Report()
	if !strings.HasPrefix(text, "repaired 1 cells") || !strings.Contains(text, "no item 1") || !strings.Contains(text, "states 1") {
		t.Errorf("report = %q", text)
	}
	if n := w[protocol.BlockPos{7, 1, 7}]; n != "cobblestone" {
		t.Errorf("placeable block is %q", n)
	}
}

// One cell nothing supports fails on its own and the rest is repaired.
func TestRepairSkipsACellItCannotReach(t *testing.T) {
	w, spec := damagedRow(t)
	spec.Plan = append(spec.Plan, planOf(t, "setblock 5 5 8 cobblestone")...)
	spec.Area = areaOf(4, 1, 4, 6, 5, 8)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	sk := NewRepair(stateWorld{w}, r.self, spec)
	if st, err := r.run(sk, 1500); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	text, _ := sk.(Reporter).Report()
	if !strings.HasPrefix(text, "repaired 2 cells, 1 failed") || !strings.Contains(text, "still damaged") {
		t.Errorf("report = %q", text)
	}
}

func TestRepairStopsForAHostileMobOrAVisitor(t *testing.T) {
	crew := []string{"Rusty"}
	for _, tc := range []struct {
		name string
		ent  belief.Entity
		want string
	}{
		{"zombie", belief.Entity{Type: "minecraft:zombie", Pos: mgl32.Vec3{6, 1, 6}}, "hostile mob"},
		{"visitor", belief.Entity{Type: "player", Player: true, Name: "Steve", Pos: mgl32.Vec3{3, 1, 3}}, "not crew"},
		{"crew", belief.Entity{Type: "player", Player: true, Name: "rusty", Pos: mgl32.Vec3{3, 1, 3}}, ""},
		{"far zombie", belief.Entity{Type: "minecraft:zombie", Pos: mgl32.Vec3{200, 1, 6}}, ""},
		{"cow", belief.Entity{Type: "minecraft:cow", Pos: mgl32.Vec3{3, 1, 3}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, spec := damagedRow(t)
			r := newRig(t, w, feetAt(1, 1, 2))
			r.goCreative()
			g := Guard{
				Each:   func(f func(belief.Entity)) { f(tc.ent) },
				Threat: func(typ string) bool { return strings.HasSuffix(typ, "zombie") },
				Crew:   crew,
			}
			sk := NewRepair(stateWorld{w}, r.self, spec, WithGuard(g))
			st, err := sk.Step(context.Background(), r)
			if tc.want == "" {
				if st == Failed {
					t.Fatalf("stopped for %s: %v", tc.name, err)
				}
				return
			}
			if st != Failed || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("status %v, err %v, want %q", st, err, tc.want)
			}
			if r.ticks != 0 {
				t.Errorf("sent %d inputs before stopping", r.ticks)
			}
		})
	}
}

// A fight in the middle leaves the cell as it was; the repair reads it again
// and still finishes.
func TestRepairResumesAfterTheUnitIsPulledAway(t *testing.T) {
	w, spec := damagedRow(t)
	r := newRig(t, w, feetAt(1, 1, 2))
	r.goCreative()
	sk := NewRepair(stateWorld{w}, r.self, spec)
	rs := sk.(Resumer)
	for i := 0; i < 40; i++ {
		if st, err := sk.Step(context.Background(), r); st != Running {
			t.Fatalf("step %d: %v %v", i, st, err)
		}
		if i%10 == 9 {
			rs.Resume()
		}
	}
	if st, err := r.run(sk, 800); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if w[protocol.BlockPos{5, 1, 4}] != "cobblestone" || w[protocol.BlockPos{6, 2, 4}] != "air" {
		t.Errorf("not repaired: %q %q", w[protocol.BlockPos{5, 1, 4}], w[protocol.BlockPos{6, 2, 4}])
	}
}

// A unit that never took the stock fails every placement the same way; the
// repair stops after a few instead of working through the whole area.
func TestRepairGivesUpAfterManyFailuresOnOneCause(t *testing.T) {
	w := flatWorld(12)
	r := newRig(t, w, feetAt(1, 1, 2))
	spec := RepairSpec{Plan: planOf(t, "fill 4 1 4 9 1 6 cobblestone"), Area: areaOf(4, 1, 4, 9, 1, 6)}
	st, err := r.run(NewRepair(stateWorld{w}, r.self, spec), 3000)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "giving up after 10 cells") || !strings.Contains(err.Error(), "no cobblestone") {
		t.Fatalf("status %v, err %v", st, err)
	}
}
