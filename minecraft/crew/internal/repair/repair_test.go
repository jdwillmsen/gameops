package repair

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

const layoutPath = "../../lab/colosseum/layout.json"

func testLayout(t *testing.T) arena.Layout {
	t.Helper()
	l, err := arena.LoadLayout(layoutPath)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func ops(t *testing.T, text string) []Op {
	t.Helper()
	o, err := ParseOps(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// arenaPlan is a small plan inside the real footprint: a floor, a wall with a
// lantern on it, and air above the floor.
func arenaPlan(t *testing.T, l arena.Layout) []Op {
	c := l.Center
	return ops(t, strings.Join([]string{
		"fill -310 64 -770 -290 90 -750 air",
		"fill -310 63 -770 -290 63 -750 sand",
		"fill -310 64 -770 -310 70 -750 stone_bricks",
		`fill -309 66 -760 -309 66 -760 lantern ["hanging"=false]`,
		"setblock " + itoa(c.X) + " 64 " + itoa(c.Z) + " gold_block",
	}, "\n"))
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestParseOps(t *testing.T) {
	got := ops(t, "# area 0 0 15 15\nkill @e[type=armor_stand]\nfill 5 70 3 1 64 3 minecraft:lantern [\"hanging\"=true]\nsetblock 1 2 3 stone\n\n")
	if len(got) != 2 {
		t.Fatalf("got %d ops, want 2: %+v", len(got), got)
	}
	if got[0].Box != (Box{X0: 1, Y0: 64, Z0: 3, X1: 5, Y1: 70, Z1: 3}) || got[0].Block != `lantern ["hanging"=true]` {
		t.Errorf("fill parsed as %+v", got[0])
	}
	if got[1].Box.X0 != 1 || got[1].Box.Z1 != 3 || got[1].Block != "stone" {
		t.Errorf("setblock parsed as %+v", got[1])
	}
	for _, bad := range []string{"fill 1 2 3 4 5", "fill a 2 3 4 5 6 stone", "fill 1 2 3 4 5 6 stone replace", "setblock 1 2 3"} {
		if _, err := ParseOps(strings.NewReader(bad)); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
}

func TestFootprintCoversTheLayout(t *testing.T) {
	l := testLayout(t)
	fp := Footprint(l)
	for _, a := range l.Areas {
		if a.X0 < fp.X0 || a.X1 > fp.X1 || a.Z0 < fp.Z0 || a.Z1 > fp.Z1 {
			t.Errorf("area %+v outside footprint %+v", a, fp)
		}
	}
	for _, p := range l.Pens {
		if !contains(fp, Box(p.Gate)) {
			t.Errorf("pen %d gate outside footprint", p.ID)
		}
	}
	if !contains(fp, Box(l.StartGate.Gate)) {
		t.Error("start gate outside footprint")
	}
	if fp.X1-fp.X0 < 2*l.OuterRadius || fp.Z1-fp.Z0 < 2*l.OuterRadius {
		t.Errorf("footprint %+v does not hold the outer wall radius %d", fp, l.OuterRadius)
	}
}

func TestNoDamageNoCommands(t *testing.T) {
	l := testLayout(t)
	plan := arenaPlan(t, l)
	d, p, r, err := Compute(plan, OpsReader{Ops: plan}, l)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cells != 0 || len(p.Commands()) != 0 || r.Damaged != 0 {
		t.Fatalf("intact arena gave %d damaged cells, %d commands", d.Cells, len(p.Commands()))
	}
	if d.Scanned == 0 {
		t.Fatal("nothing was scanned")
	}
}

func damaged(plan []Op, extra string, t *testing.T) []Op {
	return append(append([]Op{}, plan...), ops(t, extra)...)
}

func TestRepairRestoresAndIsIdempotent(t *testing.T) {
	l := testLayout(t)
	plan := arenaPlan(t, l)
	world := damaged(plan, strings.Join([]string{
		"fill -310 64 -765 -310 68 -763 air",   // hole in the wall
		"fill -300 64 -760 -299 65 -760 tnt",   // intruder in the air
		"setblock -300 63 -755 dirt",           // floor replaced
		"setblock -309 66 -760 air",            // lantern gone
		"fill -310 64 -770 -310 64 -770 stone", // wall block swapped
	}, "\n"), t)

	d, p, r, err := Compute(plan, OpsReader{Ops: world}, l)
	if err != nil {
		t.Fatal(err)
	}
	wantCells := 3*5 + 4 + 1 + 1 + 1
	if d.Cells != wantCells {
		t.Fatalf("damaged cells = %d, want %d", d.Cells, wantCells)
	}
	if d.ByKind[KindMissing] != 16 || d.ByKind[KindIntruder] != 4 || d.ByKind[KindReplaced] != 2 {
		t.Errorf("kinds = %v", d.ByKind)
	}
	if r.ByZone["floor"] != wantCells {
		t.Errorf("zones = %v", r.ByZone)
	}

	// Run the plan against the simulated world: the arena must match again,
	// and running it a second time must change nothing.
	fixed := world
	for round := 0; round < 2; round++ {
		for _, cmd := range p.Repairs {
			fixed = append(fixed, ops(t, cmd)...)
		}
		d2, p2, _, err := Compute(plan, OpsReader{Ops: fixed}, l)
		if err != nil {
			t.Fatal(err)
		}
		if d2.Cells != 0 || len(p2.Repairs) != 0 {
			t.Fatalf("round %d: %d cells still damaged, %d commands left", round, d2.Cells, len(p2.Repairs))
		}
	}
}

func TestRepairOrder(t *testing.T) {
	l := testLayout(t)
	plan := arenaPlan(t, l)
	world := damaged(plan, strings.Join([]string{
		"fill -310 64 -765 -310 68 -763 air",
		"setblock -300 80 -760 tnt",
		"setblock -309 66 -760 air",
	}, "\n"), t)
	_, p, _, err := Compute(plan, OpsReader{Ops: world}, l)
	if err != nil {
		t.Fatal(err)
	}
	stage := func(cmd string) int {
		switch {
		case strings.HasSuffix(cmd, " air"):
			return 0
		case strings.Contains(cmd, "lantern"):
			return 2
		}
		return 1
	}
	last := 0
	for _, cmd := range p.Repairs {
		if s := stage(cmd); s < last {
			t.Fatalf("stage %d command %q after a stage %d command in %v", s, cmd, last, p.Repairs)
		} else {
			last = s
		}
	}
	if last != 2 || stage(p.Repairs[0]) != 0 {
		t.Fatalf("expected air first and lantern last, got %v", p.Repairs)
	}
}

func TestUnknownCellsAreLeftAlone(t *testing.T) {
	l := testLayout(t)
	plan := arenaPlan(t, l)
	// The scan only vouches for part of the floor.
	scan := ops(t, "fill -310 63 -770 -300 63 -750 sand\nsetblock -305 63 -760 dirt")
	d, _, _, err := Compute(plan, OpsReader{Ops: scan}, l)
	if err != nil {
		t.Fatal(err)
	}
	if d.Cells != 1 {
		t.Fatalf("damaged = %d, want only the one scanned bad cell", d.Cells)
	}
	if d.Unscanned == 0 {
		t.Error("unscanned cells were not counted")
	}
}

func TestOutsideFootprintRefused(t *testing.T) {
	l := testLayout(t)
	plan := ops(t, "setblock 0 64 0 stone")
	if _, _, _, err := Compute(plan, OpsReader{}, l); err == nil || !strings.Contains(err.Error(), "footprint") {
		t.Fatalf("plan outside the footprint gave %v", err)
	}
	if _, err := BuildPlan([]Rect{{Box: Box{X0: 0, Y0: 64, Z0: 0, X1: 0, Y1: 64, Z1: 0}, Block: "stone"}}, Footprint(l)); err == nil {
		t.Fatal("BuildPlan accepted a rect outside the footprint")
	}
}

func TestEveryCommandStaysInsideFootprint(t *testing.T) {
	l := testLayout(t)
	fp := Footprint(l)
	plan := ops(t, "fill -420 36 -864 -181 40 -653 stone")
	_, p, _, err := Compute(plan, OpsReader{Ops: ops(t, "fill -420 36 -864 -181 40 -653 dirt")}, l)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range p.Repairs {
		for _, o := range ops(t, cmd) {
			if !contains(fp, o.Box) {
				t.Errorf("%q leaves the footprint", cmd)
			}
			if v := volume(o.Box); v > maxFill {
				t.Errorf("%q covers %d blocks, over the fill limit", cmd, v)
			}
		}
	}
	if len(p.Setup) == 0 || len(p.Setup) != len(p.Teardown) || len(p.Setup) > maxTickingAreas {
		t.Errorf("ticking areas: setup %v teardown %v", p.Setup, p.Teardown)
	}
}

func volume(b Box) int { dx, dy, dz := dims(b); return dx * dy * dz }

func TestReportText(t *testing.T) {
	l := testLayout(t)
	plan := arenaPlan(t, l)
	world := damaged(plan, "setblock -300 63 -755 dirt", t)
	d, p, r, err := Compute(plan, OpsReader{Ops: world}, l)
	if err != nil || d.Cells != 1 {
		t.Fatalf("cells %d err %v", d.Cells, err)
	}
	r.Dry, r.Status = true, StatusDryRun
	var sb strings.Builder
	r.Write(&sb)
	for _, want := range []string{"dry run", "1 damaged", "replaced", "floor", "dirt -> sand"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, sb.String())
		}
	}
	if r.Commands != len(p.Commands()) {
		t.Error("report command count disagrees with the plan")
	}
}

// --- Apply ---------------------------------------------------------------

func quiet() Options { return Options{Sleep: func(time.Duration) {}, Batch: 2} }

// idleServer answers the list and fight counts for an empty, fight-free lab.
func idleServer(f *arena.FakeConsole) {
	f.Respond = func(cmd string) (string, error) {
		switch {
		case cmd == "list":
			return "There are 0/10 players online:\n", nil
		case strings.HasPrefix(cmd, "testfor"):
			return "No targets matched selector", nil
		}
		return "Successfully filled 1 block(s)", nil
	}
}

var samplePlan = Plan{
	Setup:    []string{"tickingarea add 0 0 0 0 0 0 crew-repair-0"},
	Repairs:  []string{"fill 1 1 1 1 1 1 air", "fill 2 2 2 2 2 2 stone", "fill 3 3 3 3 3 3 stone", "fill 4 4 4 4 4 4 stone", "fill 5 5 5 5 5 5 stone"},
	Teardown: []string{"tickingarea remove crew-repair-0"},
}

func TestDryRunSendsNothing(t *testing.T) {
	f := &arena.FakeConsole{}
	var r Report
	if err := Apply(f, samplePlan, &r, Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.Cmds) != 0 || r.Executed != 0 || !r.Dry {
		t.Fatalf("dry run sent %v, executed %d", f.Cmds, r.Executed)
	}
}

func TestApplySendsInOrderAndTearsDown(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err != nil {
		t.Fatal(err)
	}
	if r.Executed != len(samplePlan.Commands()) {
		t.Fatalf("executed %d, want %d", r.Executed, len(samplePlan.Commands()))
	}
	if f.Cmds[len(f.Cmds)-1] != samplePlan.Teardown[0] {
		t.Errorf("last command %q is not the teardown", f.Cmds[len(f.Cmds)-1])
	}
	if f.Count("testfor") < 4 {
		t.Errorf("fight was checked %d times, want before, after the quiet period, and between batches", f.Count("testfor"))
	}
}

func TestApplyRefusesDuringFight(t *testing.T) {
	f := &arena.FakeConsole{}
	f.Respond = func(cmd string) (string, error) {
		if cmd == "list" {
			return "There are 0/10 players online:\n", nil
		}
		return "Found Zombie, Zombie", nil
	}
	var r Report
	err := Apply(f, samplePlan, &r, quiet())
	if !errors.Is(err, ErrFightActive) {
		t.Fatalf("err = %v, want ErrFightActive", err)
	}
	if f.Count("fill") != 0 || f.Count("tickingarea") != 0 {
		t.Fatalf("commands sent despite the fight: %v", f.Cmds)
	}
}

func TestApplyStopsWhenAFightStartsMidRun(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	idle := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor") && f.Count("fill") >= 2 {
			return "Found Zombie", nil
		}
		return idle(cmd)
	}
	var r Report
	err := Apply(f, samplePlan, &r, quiet())
	if !errors.Is(err, ErrFightActive) {
		t.Fatalf("err = %v, want ErrFightActive", err)
	}
	if f.Count("fill") != 2 {
		t.Errorf("sent %d fills, want it to stop at the batch edge after 2", f.Count("fill"))
	}
	if f.Cmds[len(f.Cmds)-1] != samplePlan.Teardown[0] {
		t.Error("ticking area was not removed after the abort")
	}
}

func TestApplyRefusesOtherPlayers(t *testing.T) {
	f := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		return "There are 1/10 players online:\nSteve", nil
	}}
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err == nil {
		t.Fatal("ran with a stranger online")
	}
	if f.Count("fill") != 0 {
		t.Fatal("commands sent with a stranger online")
	}
}

func TestApplyStopsOnServerRefusal(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	idle := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "fill 2") {
			return "Cannot place blocks outside of the world", nil
		}
		return idle(cmd)
	}
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err == nil || !strings.Contains(err.Error(), "outside of the world") {
		t.Fatalf("err = %v", err)
	}
	if f.Count("fill 3") != 0 {
		t.Error("kept going after the server refused a command")
	}
}

func TestStatusReflectsHowTheRunEnded(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	var ok Report
	if err := Apply(f, samplePlan, &ok, quiet()); err != nil || ok.Status != StatusApplied || ok.Error != "" {
		t.Fatalf("clean run: err %v status %q error %q", err, ok.Status, ok.Error)
	}

	var dry Report
	_ = Apply(&arena.FakeConsole{}, samplePlan, &dry, Options{DryRun: true})
	if dry.Status != StatusDryRun {
		t.Errorf("dry status = %q", dry.Status)
	}

	busy := &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "list" {
			return "There are 0/10 players online:\n", nil
		}
		return "Found Zombie", nil
	}}
	var refused Report
	_ = Apply(busy, samplePlan, &refused, quiet())
	if refused.Status != StatusRefused || refused.Error == "" {
		t.Errorf("refused: status %q error %q", refused.Status, refused.Error)
	}

	g := &arena.FakeConsole{}
	idleServer(g)
	idle := g.Respond
	g.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "fill 3") {
			return "Cannot place blocks outside of the world", nil
		}
		return idle(cmd)
	}
	var aborted Report
	_ = Apply(g, samplePlan, &aborted, quiet())
	if aborted.Status != StatusAborted || aborted.Error == "" {
		t.Errorf("aborted: status %q error %q", aborted.Status, aborted.Error)
	}
	var sb strings.Builder
	aborted.Write(&sb)
	if strings.Contains(sb.String(), "(applied)") || !strings.Contains(sb.String(), "ABORTED") || !strings.Contains(sb.String(), "error:") {
		t.Errorf("aborted report text:\n%s", sb.String())
	}
}

func TestApplyRechecksPlayersBetweenBatches(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	idle := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if cmd == "list" && f.Count("fill") >= 2 {
			return "There are 1/10 players online:\nSteve", nil
		}
		return idle(cmd)
	}
	var r Report
	err := Apply(f, samplePlan, &r, quiet())
	if err == nil || errors.Is(err, ErrFightActive) {
		t.Fatalf("err = %v, want a not-alone refusal", err)
	}
	if f.Count("fill") != 2 || f.Count("tickingarea remove crew-repair-0") < 2 {
		t.Errorf("fills %d, teardown missing: %v", f.Count("fill"), f.Cmds)
	}
	if r.Status != StatusAborted {
		t.Errorf("status = %q", r.Status)
	}
}

func TestApplyWaitsForChunksAndClearsStaleAreas(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	var slept []time.Duration
	var order []string
	o := quiet()
	o.LoadWait = 7 * time.Second
	o.Sleep = func(d time.Duration) {
		slept = append(slept, d)
		order = append(order, "sleep")
	}
	inner := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "tickingarea") || strings.HasPrefix(cmd, "fill") {
			order = append(order, strings.Fields(cmd)[0]+" "+strings.Fields(cmd)[1])
		}
		return inner(cmd)
	}
	var r Report
	if err := Apply(f, samplePlan, &r, o); err != nil {
		t.Fatal(err)
	}
	want := append(strings.Split(strings.Repeat("tickingarea remove,", maxTickingAreas), ",")[:maxTickingAreas], "tickingarea add", "sleep", "fill 1")
	// order starts after the quiet-period sleep.
	got := order[1 : 1+len(want)]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want prefix %v", order, want)
		}
	}
	if len(slept) != 2 || slept[1] != 7*time.Second {
		t.Errorf("sleeps = %v, want quiet then the 7s load wait", slept)
	}
}

func TestApplyFailsWhenAreaAlreadyExists(t *testing.T) {
	f := &arena.FakeConsole{}
	idleServer(f)
	idle := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "tickingarea add") {
			return "A ticking area with that name already exists", nil
		}
		return idle(cmd)
	}
	var r Report
	if err := Apply(f, samplePlan, &r, quiet()); err == nil {
		t.Fatal("run continued past a rejected ticking area")
	}
	if f.Count("fill") != 0 {
		t.Error("fills sent without loaded chunks")
	}
}
