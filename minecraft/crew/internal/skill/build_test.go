package skill

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
)

var topTorch = map[string]any{"torch_facing_direction": "top"}

// stamp is a 3x3 plank floor with a glass block on each corner and a torch
// in the middle, one layer up from the origin.
func stamp(ox, oy, oz int32) []BuildCell {
	var cells []BuildCell
	for z := int32(0); z < 3; z++ {
		for x := int32(0); x < 3; x++ {
			cells = append(cells, BuildCell{Pos: protocol.BlockPos{ox + x, oy, oz + z}, Block: "oak_planks"})
		}
	}
	for z := int32(0); z < 3; z += 2 {
		for x := int32(0); x < 3; x += 2 {
			cells = append(cells, BuildCell{Pos: protocol.BlockPos{ox + x, oy + 1, oz + z}, Block: "glass"})
		}
	}
	return append(cells, BuildCell{Pos: protocol.BlockPos{ox + 1, oy + 1, oz + 1}, Block: "torch", States: topTorch})
}

func creativeRig(t *testing.T, w simWorld, feet ...float32) *rig {
	t.Helper()
	r := newRig(t, w, feetAt(feet[0], feet[1], feet[2]))
	r.creative = true
	return r
}

// stateSim is a world that can read states back; props decides what a block
// reports.
type stateSim struct {
	simWorld
	props func(name string) map[string]any
}

func (s stateSim) State(p protocol.BlockPos) (string, map[string]any, bool) {
	n, known := s.Block(p)
	return n, s.props(n), known
}

func TestBuildStampsInOrderAndReportsWhatItCannotPlace(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(w, r.self, BuildSpec{Cells: stamp(5, 1, 5)})
	st, err := r.run(b, 4000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if got := w.count("oak_planks"); got != 9 {
		t.Errorf("%d planks, want 9", got)
	}
	if got := w.count("glass"); got != 4 {
		t.Errorf("%d glass, want 4", got)
	}
	// The world cannot read states back, so a torch could not be confirmed
	// and is left unplaced.
	if got := w.count("torch"); got != 0 {
		t.Errorf("%d torches placed without a way to verify their state", got)
	}
	rep := b.Report()
	if rep.Total != 14 || rep.Placed != 13 || rep.SkippedState["torch"] != 1 || b.Progress() != 13 {
		t.Errorf("report %+v, progress %d", rep, b.Progress())
	}
}

func TestBuildRunTwiceBuildsOnce(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 1, 1, 1)
	if st, err := r.run(NewBuild(w, r.self, BuildSpec{Cells: stamp(5, 1, 5)}), 4000); st != Done || err != nil {
		t.Fatalf("first run: %v, %v", st, err)
	}
	clicks := r.placeClicks
	b := NewBuild(w, r.self, BuildSpec{Cells: stamp(5, 1, 5)})
	if st, err := r.run(b, 4000); st != Done || err != nil {
		t.Fatalf("second run: %v, %v", st, err)
	}
	if r.placeClicks != clicks || r.breaks != 0 {
		t.Errorf("second run clicked %d more times and broke %d blocks", r.placeClicks-clicks, r.breaks)
	}
	if rep := b.Report(); rep.Placed != 0 || rep.AlreadyCorrect != 13 {
		t.Errorf("report %+v", rep)
	}
}

func TestBuildClearsAWrongBlockBeforePlacing(t *testing.T) {
	w := flatWorld(14)
	w[protocol.BlockPos{6, 1, 5}] = "cobblestone"
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(w, r.self, BuildSpec{Cells: stamp(5, 1, 5)})
	if st, err := r.run(b, 4000); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if w[protocol.BlockPos{6, 1, 5}] != "oak_planks" || w.count("cobblestone") != 0 {
		t.Errorf("cell holds %q", w[protocol.BlockPos{6, 1, 5}])
	}
	if rep := b.Report(); rep.Cleared != 1 || r.breaks != 1 {
		t.Errorf("cleared %d, broke %d", rep.Cleared, r.breaks)
	}
}

func TestBuildDryRunPlacesNothing(t *testing.T) {
	w := flatWorld(14)
	w[protocol.BlockPos{5, 1, 5}] = "oak_planks"
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(w, r.self, BuildSpec{Cells: stamp(5, 1, 5), DryRun: true})
	if st, err := r.run(b, 10); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	rep := b.Report()
	if rep.Total != 14 || rep.AlreadyCorrect != 1 || rep.ToPlace != 12 || rep.Placed != 0 || rep.SkippedState["torch"] != 1 {
		t.Errorf("report %+v", rep)
	}
	if r.placeClicks != 0 || w.count("oak_planks") != 1 {
		t.Errorf("dry run touched the world: %d clicks", r.placeClicks)
	}
}

func TestBuildStepsOffTheCellItMustFill(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 5, 1, 5)
	cells := []BuildCell{{Pos: protocol.BlockPos{5, 1, 5}, Block: "oak_planks"}}
	b := NewBuild(w, r.self, BuildSpec{Cells: cells})
	if st, err := r.run(b, 400); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if w[cells[0].Pos] != "oak_planks" {
		t.Errorf("cell holds %q", w[cells[0].Pos])
	}
}

// A block listed before the one that supports it is put off until the
// support stands.
func TestBuildRetriesAnUnsupportedCellAfterTheRest(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 1, 1, 1)
	cells := []BuildCell{
		{Pos: protocol.BlockPos{8, 2, 8}, Block: "glass"},
		{Pos: protocol.BlockPos{8, 1, 8}, Block: "oak_planks"},
	}
	b := NewBuild(w, r.self, BuildSpec{Cells: cells})
	if st, err := r.run(b, 800); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if w[cells[0].Pos] != "glass" {
		t.Errorf("glass cell holds %q", w[cells[0].Pos])
	}
}

func TestBuildFailsOnACellNothingCanSupport(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(w, r.self, BuildSpec{Cells: []BuildCell{{Pos: protocol.BlockPos{8, 4, 8}, Block: "glass"}}})
	st, err := r.run(b, 800)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "glass") {
		t.Fatalf("status %v, err %v", st, err)
	}
	if strings.Contains(err.Error(), "8") {
		t.Errorf("error names a coordinate: %v", err)
	}
}

func TestBuildPlacesAClickDerivedStateWhenItCanReadItBack(t *testing.T) {
	w := flatWorld(14)
	sw := stateSim{simWorld: w, props: func(n string) map[string]any {
		if n == "torch" {
			return topTorch
		}
		return map[string]any{}
	}}
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(sw, r.self, BuildSpec{Cells: stamp(5, 1, 5)})
	if st, err := r.run(b, 4000); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if w.count("torch") != 1 || b.Report().Placed != 14 || len(b.Report().SkippedState) != 0 {
		t.Errorf("report %+v, torches %d", b.Report(), w.count("torch"))
	}
}

// A block that lands with other states than asked is taken away again, not
// left standing wrong.
func TestBuildBreaksABlockWhoseStateCameOutWrong(t *testing.T) {
	w := flatWorld(14)
	sw := stateSim{simWorld: w, props: func(n string) map[string]any {
		if n == "torch" {
			return map[string]any{"torch_facing_direction": "north"}
		}
		return map[string]any{}
	}}
	r := creativeRig(t, w, 1, 1, 1)
	b := NewBuild(sw, r.self, BuildSpec{Cells: stamp(5, 1, 5)})
	if st, err := r.run(b, 4000); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if w.count("torch") != 0 || b.Report().SkippedState["torch"] != 1 || b.Report().Placed != 13 {
		t.Errorf("report %+v, torches %d", b.Report(), w.count("torch"))
	}
}

func TestBuildLeavesStatesItCannotChooseAlone(t *testing.T) {
	w := flatWorld(14)
	r := creativeRig(t, w, 1, 1, 1)
	door := map[string]any{"direction": 1, "open_bit": false, "upper_block_bit": false}
	b := NewBuild(w, r.self, BuildSpec{Cells: []BuildCell{
		{Pos: protocol.BlockPos{5, 1, 5}, Block: "oak_planks"},
		{Pos: protocol.BlockPos{6, 1, 5}, Block: "oak_door", States: door},
	}})
	if st, err := r.run(b, 800); st != Done || err != nil {
		t.Fatalf("%v, %v", st, err)
	}
	if rep := b.Report(); rep.SkippedState["oak_door"] != 1 || rep.Placed != 1 {
		t.Errorf("report %+v", rep)
	}
}

func TestBuildReadyNeedsCreativeUnlessItIsADryRun(t *testing.T) {
	w := flatWorld(14)
	real := NewBuild(w, nil, BuildSpec{Cells: stamp(5, 1, 5)})
	if err := real.Ready(belief.Snapshot{}); err == nil || !strings.Contains(err.Error(), "creative") {
		t.Errorf("survival: err = %v", err)
	}
	if err := real.Ready(belief.Snapshot{Creative: true}); err != nil {
		t.Errorf("creative: err = %v", err)
	}
	dry := NewBuild(w, nil, BuildSpec{Cells: stamp(5, 1, 5), DryRun: true})
	if err := dry.Ready(belief.Snapshot{}); err != nil {
		t.Errorf("dry run: err = %v", err)
	}
}

// The house is stamped by a creative unit at each rotation. Its door and
// torches carry states the unit cannot choose or read back here, so they are
// reported and the rest stands.
func TestBuildStampsTheSmallHouse(t *testing.T) {
	bp, err := blueprint.Load("../../blueprints/small_house.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, rot := range []int{0, 90} {
		w := flatWorld(14)
		r := creativeRig(t, w, 0, 1, 0)
		b := NewBuild(w, r.self, BuildSpec{Cells: BlueprintCells(bp, protocol.BlockPos{3, 1, 3}, rot)})
		if st, err := r.run(b, 30000); st != Done || err != nil {
			t.Fatalf("rotation %d: %v, %v", rot, st, err)
		}
		rep := b.Report()
		if got := w.count("oak_planks"); got != 160 {
			t.Errorf("rotation %d: %d planks, want 160", rot, got)
		}
		if got := w.count("glass"); got != 8 {
			t.Errorf("rotation %d: %d glass, want 8", rot, got)
		}
		if rep.Total != 173 || rep.Placed != 168 || rep.SkippedState["torch"] != 4 || rep.SkippedState["wooden_door"] != 1 {
			t.Errorf("rotation %d: report %+v", rot, rep)
		}
	}
}

func TestBuildPlacesMoreBlockTypesThanTheHotbarHolds(t *testing.T) {
	w := flatWorld(14)
	r := newRig(t, w, feetAt(1, 1, 1))
	r.goCreative()
	names := []string{"dirt", "cobblestone", "oak_planks", "glass", "stone", "sand", "brick_block", "white_wool", "gold_block", "iron_block", "diamond_block", "emerald_block"}
	var cells []BuildCell
	for i, n := range names {
		cells = append(cells, BuildCell{Pos: protocol.BlockPos{int32(5 + i%6), 1, int32(5 + i/6)}, Block: n})
	}
	b := NewBuild(w, r.self, BuildSpec{Cells: cells})
	st, err := r.run(b, 6000)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	for _, c := range cells {
		if got, _ := w.Block(c.Pos); got != c.Block {
			t.Errorf("%v is %q, want %q", c.Pos, got, c.Block)
		}
	}
}
