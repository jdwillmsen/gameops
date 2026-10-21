package act

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Item IDs of the lab server that recorded the fixtures.
const (
	cobblestone = 4
	woodenPick  = 312
	shieldID    = 358
)

type sink struct {
	mu  sync.Mutex
	got []packet.Packet
	err error
}

func (s *sink) WritePacket(pk packet.Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, pk)
	return nil
}

func (s *sink) inputs(t *testing.T) []*packet.PlayerAuthInput {
	t.Helper()
	var out []*packet.PlayerAuthInput
	for _, pk := range s.got {
		if in, ok := pk.(*packet.PlayerAuthInput); ok {
			out = append(out, in)
		}
	}
	return out
}

func (s *sink) last(t *testing.T) packet.Packet {
	t.Helper()
	if len(s.got) == 0 {
		t.Fatal("nothing was sent")
	}
	return s.got[len(s.got)-1]
}

// world is a fake solid map: every block at or below floor, plus extras.
type world struct {
	floor int32
	extra map[protocol.BlockPos]bool
}

type namedBlocks map[protocol.BlockPos]string

func (b namedBlocks) Block(p protocol.BlockPos) (string, bool) {
	n, ok := b[p]
	return n, ok
}

type correctionLog struct {
	events []map[string]any
}

func (l *correctionLog) Info(event string, fields map[string]any) {
	if event == "movement_corrected" {
		l.events = append(l.events, fields)
	}
}

func (w world) Solid(p protocol.BlockPos) bool { return p[1] <= w.floor || w.extra[p] }

// flat has ground whose top face is y=0, so a unit standing on it has feet at 0.
func flat() world { return world{floor: -1} }

func newAct(w Solid, start Pose) (*Actuator, *sink) {
	s := &sink{}
	start.OnGround = true
	return New(s, 7, start, w), s
}

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.5f, want %.5f", name, got, want)
	}
}

func hasFlag(in *packet.PlayerAuthInput, f int) bool { return in.InputData.Load(f) }

// labPackets decodes a fixture: a uint32 little-endian length, then the packet
// header and payload as the server sent them.
func labPackets(t *testing.T, name string) []packet.Packet {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".bin")
	if err != nil {
		t.Fatal(err)
	}
	pool := packet.NewServerPool()
	var out []packet.Packet
	for len(b) > 0 {
		n := binary.LittleEndian.Uint32(b)
		// bytes.Buffer, not Reader: a Reader fails on every empty item.
		body := bytes.NewBuffer(b[4 : 4+n])
		b = b[4+n:]
		var h packet.Header
		if err := h.Read(body); err != nil {
			t.Fatal(err)
		}
		pk := pool[h.PacketID]()
		pk.Marshal(protocol.NewReader(body, shieldID, false))
		out = append(out, pk)
	}
	return out
}

func TestTickSendsOneInputWithIncreasingTick(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	for i := 0; i < 5; i++ {
		if err := a.Tick(Input{Forward: float32(i % 2)}); err != nil {
			t.Fatal(err)
		}
	}
	ins := s.inputs(t)
	if len(ins) != 5 || len(s.got) != 5 {
		t.Fatalf("sent %d packets, %d inputs, want 5 of each", len(s.got), len(ins))
	}
	for i := 1; i < len(ins); i++ {
		if ins[i].Tick <= ins[i-1].Tick {
			t.Errorf("tick %d after %d", ins[i].Tick, ins[i-1].Tick)
		}
	}
}

func TestInputUsesTheRecordedFieldValues(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{10, 0, 20}, Yaw: 90, Pitch: 5})
	if err := a.Tick(Input{Forward: 1, Yaw: 90, Pitch: 5}); err != nil {
		t.Fatal(err)
	}
	in := s.inputs(t)[0]
	if in.InputMode != packet.InputModeMouse || in.PlayMode != packet.PlayModeNormal || in.InteractionModel != packet.InteractionModelClassic {
		t.Errorf("modes = %d/%d/%d", in.InputMode, in.PlayMode, in.InteractionModel)
	}
	if in.HeadYaw != 90 || in.InteractYaw != 90 || in.InteractPitch != 5 || in.Yaw != 90 || in.Pitch != 5 {
		t.Errorf("rotation not carried: %+v", in)
	}
	want := mgl32.Vec2{0, 1}
	if in.MoveVector != want || in.RawMoveVector != want || in.AnalogueMoveVector != want {
		t.Errorf("move vectors = %v %v %v", in.MoveVector, in.RawMoveVector, in.AnalogueMoveVector)
	}
	for _, f := range []int{packet.InputFlagUp, packet.InputFlagBlockBreakingDelayEnabled, packet.InputFlagVerticalCollision} {
		if !hasFlag(in, f) {
			t.Errorf("flag %d not set", f)
		}
	}
	// Position is the eye, one tick of forward at yaw 90 (toward -X) ahead.
	near(t, "eye y", float64(in.Position.Y()), 1.62, 1e-4)
	near(t, "x", float64(in.Position.X()), 10-0.098, 1e-4)
	near(t, "z", float64(in.Position.Z()), 20, 1e-4)
	near(t, "delta y", float64(in.Delta.Y()), -0.0784, 1e-5)
}

func TestWalkingAdvancesByTheSpikeStep(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	for i := 0; i < 40; i++ {
		if err := a.Tick(Input{Forward: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ins := s.inputs(t)
	want := []float64{0.098, 0.1515, 0.1807, 0.1966}
	for i, w := range want {
		near(t, "step", float64(ins[i].Delta.Z()), w, 5e-4)
	}
	near(t, "steady step", float64(ins[39].Delta.Z()), 0.2159, 5e-4)
	// Yaw 0 faces +Z, and a wall-less flat world never moves Y.
	if p := a.Pose(); p.Feet.Y() != 0 || !p.OnGround || p.Feet.Z() < 7 {
		t.Errorf("pose = %+v", p)
	}
}

func TestYawPicksTheForwardDirection(t *testing.T) {
	for _, c := range []struct {
		yaw    float32
		dx, dz float64
	}{{0, 0, 1}, {-90, 1, 0}, {90, -1, 0}, {180, 0, -1}} {
		a, s := newAct(flat(), Pose{})
		if err := a.Tick(Input{Forward: 1, Yaw: c.yaw}); err != nil {
			t.Fatal(err)
		}
		d := s.inputs(t)[0].Delta
		near(t, "dx", float64(d.X()), c.dx*0.098, 1e-5)
		near(t, "dz", float64(d.Z()), c.dz*0.098, 1e-5)
	}
}

func TestIdleTicksSendTheDecayingCarry(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	for i := 0; i < 5; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	for i := 0; i < 7; i++ {
		if err := a.Tick(Input{}); err != nil {
			t.Fatal(err)
		}
	}
	ins := s.inputs(t)
	if len(ins) != 12 {
		t.Fatalf("idle ticks must still send an input, got %d", len(ins))
	}
	for i := 6; i < 12; i++ {
		prev, cur := float64(ins[i-1].Delta.Z()), float64(ins[i].Delta.Z())
		near(t, "decay", cur, prev*0.546, 1e-4)
		if hasFlag(ins[i], packet.InputFlagUp) {
			t.Error("idle tick carries the Up flag")
		}
	}
	if d := float64(ins[11].Delta.Z()); d > 0.01 {
		t.Errorf("carry still %.4f after six idle ticks", d)
	}
}

func TestJumpRisesOneBlockAndLands(t *testing.T) {
	// A one-block step whose face is 1.0 ahead of the unit's centre.
	w := flat()
	w.extra = map[protocol.BlockPos]bool{}
	for x := int32(-2); x <= 2; x++ {
		for z := int32(1); z <= 3; z++ {
			w.extra[protocol.BlockPos{x, 0, z}] = true
		}
	}
	a, s := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.0}})
	if err := a.Tick(Input{Forward: 1, Jump: true}); err != nil {
		t.Fatal(err)
	}
	first := s.inputs(t)[0]
	for _, f := range []int{packet.InputFlagJumping, packet.InputFlagStartJumping, packet.InputFlagJumpDown} {
		if !hasFlag(first, f) {
			t.Errorf("takeoff flag %d missing", f)
		}
	}
	if hasFlag(first, packet.InputFlagVerticalCollision) {
		t.Error("takeoff tick must not report a vertical collision")
	}
	near(t, "takeoff dy", float64(first.Delta.Y()), 0.42, 1e-5)

	for i := 0; i < 30 && !a.Pose().OnGround; i++ {
		if err := a.Tick(Input{Forward: 1}); err != nil {
			t.Fatal(err)
		}
	}
	p := a.Pose()
	if !p.OnGround || p.Feet.Y() != 1 {
		t.Fatalf("pose after the jump = %+v, want on the step at y=1", p)
	}
	ins := s.inputs(t)
	wantDY := []float64{0.42, 0.3332, 0.2481, 0.1648, 0.0831, 0.0030, -0.0754}
	for i, d := range wantDY {
		if i >= len(ins) {
			break
		}
		near(t, "dy", float64(ins[i].Delta.Y()), d, 6e-4)
	}
	land := ins[len(ins)-1]
	if !hasFlag(land, packet.InputFlagVerticalCollision) || !hasFlag(land, packet.InputFlagJumping) {
		t.Error("landing tick must carry VerticalCollision")
	}
	if land.Delta.Y() >= 0 {
		t.Errorf("landing dy = %v, want a clamped fall", land.Delta.Y())
	}
	for _, in := range ins[1 : len(ins)-1] {
		if hasFlag(in, packet.InputFlagVerticalCollision) {
			t.Error("airborne tick carries VerticalCollision")
		}
	}
}

func TestWallStopsTheUnitAtTheHitboxEdge(t *testing.T) {
	w := flat()
	w.extra = map[protocol.BlockPos]bool{{0, 0, 3}: true, {0, 1, 3}: true}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	for i := 0; i < 40; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	near(t, "z", float64(a.Pose().Feet.Z()), 3-0.3, 1e-3)
}

func TestWalkingOffAnEdgeFalls(t *testing.T) {
	// A ledge: the raised floor ends at z=2, leaving a 1-block drop.
	w := world{floor: -2, extra: map[protocol.BlockPos]bool{}}
	for x := int32(-2); x <= 2; x++ {
		for z := int32(-3); z < 2; z++ {
			w.extra[protocol.BlockPos{x, -1, z}] = true
		}
	}
	a, _ := newAct(w, Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}})
	for i := 0; i < 40; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	if p := a.Pose(); p.Feet.Y() != -1 || !p.OnGround {
		t.Errorf("pose = %+v, want landed one block down", p)
	}
}

func TestCorrectionResetsThePose(t *testing.T) {
	before := testutil.ToFloat64(metricCorrections)
	a, s := newAct(flat(), Pose{})
	for i := 0; i < 5; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{4, 1.62 + 0, 9},
		Delta:    mgl32.Vec3{0, -0.0784, 0.05},
		Rotation: mgl32.Vec2{3, 40},
		OnGround: true,
	})
	if got := testutil.ToFloat64(metricCorrections) - before; got != 1 {
		t.Errorf("corrections counted = %v, want 1", got)
	}
	p := a.Pose()
	if p.Feet != (mgl32.Vec3{4, 0, 9}) || !p.OnGround {
		t.Fatalf("pose = %+v", p)
	}
	if p.Pitch != 3 || p.Yaw != 40 {
		t.Errorf("rotation = %v/%v, want 3/40", p.Pitch, p.Yaw)
	}
	// The correction's horizontal delta is the new carry.
	_ = a.Tick(Input{})
	d := s.inputs(t)[5].Delta
	near(t, "carry z", float64(d.Z()), 0.05, 1e-5)
}

func TestCorrectionLogDescribesPredictedMovementAndNearbyBlocks(t *testing.T) {
	blocks := namedBlocks{
		{1, -1, 2}: "stone", {1, 0, 2}: "air", {0, 0, 2}: "oak_planks",
	}
	tests := []struct {
		name  string
		shift mgl32.Vec3
		axis  string
		sign  string
		dist  float64
	}{
		{name: "x positive", shift: mgl32.Vec3{0.5, 0, 0}, axis: "x", sign: "+", dist: 0.5},
		{name: "y negative", shift: mgl32.Vec3{0, -0.75, 0}, axis: "y", sign: "-", dist: 0.75},
		{name: "z negative", shift: mgl32.Vec3{0.25, 0.1, -0.5}, axis: "z", sign: "-", dist: 0.57},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := Pose{Feet: mgl32.Vec3{1.5, 0, 2.5}, Yaw: 90, OnGround: true}
			a, _ := newAct(flat(), start)
			log := &correctionLog{}
			a.SetCorrectionDiagnostics(log, blocks)
			a.SetSkill("mine")
			when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
			a.now = func() time.Time { return when }
			corrected := start.Feet.Add(tt.shift).Add(mgl32.Vec3{0, eyeHeight, 0})
			a.Apply(&packet.CorrectPlayerMovePrediction{Position: corrected, OnGround: true})
			if len(log.events) != 1 {
				t.Fatalf("logged events = %d, want 1", len(log.events))
			}
			want := map[string]any{
				"skill": "mine", "on_ground": true, "step": false,
				"vy": 0.0, "dist": tt.dist, "axis": tt.axis, "axis_sign": tt.sign,
				"below": "stone", "feet": "air", "ahead": "oak_planks", "suppressed": uint64(0),
				"tick_found": false,
			}
			for key, value := range want {
				if got := log.events[0][key]; got != value {
					t.Errorf("field %q = %#v, want %#v", key, got, value)
				}
			}
		})
	}
}

func TestCorrectionLogComparesWithTheCorrectedTicksInput(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{0.5, 0, 0.5}, OnGround: true})
	log := &correctionLog{}
	a.SetCorrectionDiagnostics(log, nil)
	a.SetSkill("mine")
	for i := 0; i < 3; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	a.SetSkill("build")
	for i := 0; i < 4; i++ {
		_ = a.Tick(Input{Forward: 1})
	}
	sent := s.inputs(t)[2]
	a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: sent.Position.Add(mgl32.Vec3{0, 0.3, 0}),
		Tick:     sent.Tick,
		OnGround: true,
	})
	if len(log.events) != 1 {
		t.Fatalf("logged events = %d, want 1", len(log.events))
	}
	want := map[string]any{
		"tick_found": true, "skill": "mine", "dist": 0.3, "axis": "y", "axis_sign": "+",
	}
	for key, value := range want {
		if got := log.events[0][key]; got != value {
			t.Errorf("field %q = %#v, want %#v", key, got, value)
		}
	}
}

func TestCorrectionLogFallsBackWhenTheTickIsNoLongerHeld(t *testing.T) {
	a, s := newAct(flat(), Pose{OnGround: true})
	log := &correctionLog{}
	a.SetCorrectionDiagnostics(log, nil)
	for i := 0; i < sentHistory+1; i++ {
		_ = a.Tick(Input{})
	}
	a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{0, eyeHeight, 0},
		Tick:     s.inputs(t)[0].Tick,
		OnGround: true,
	})
	if got := log.events[0]["tick_found"]; got != false {
		t.Errorf("tick_found = %#v, want false", got)
	}
}

func TestCorrectionLogRateLimitReportsSuppressedCorrections(t *testing.T) {
	a, _ := newAct(flat(), Pose{OnGround: true})
	log := &correctionLog{}
	a.SetCorrectionDiagnostics(log, nil)
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return when }
	correction := &packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{0, eyeHeight, 0}, OnGround: true}
	a.Apply(correction)
	a.Apply(correction)
	a.Apply(correction)
	if len(log.events) != 1 {
		t.Fatalf("logged events before limit expires = %d, want 1", len(log.events))
	}
	when = when.Add(10 * time.Second)
	a.Apply(correction)
	if len(log.events) != 2 {
		t.Fatalf("logged events after limit expires = %d, want 2", len(log.events))
	}
	if got := log.events[1]["suppressed"]; got != uint64(2) {
		t.Errorf("suppressed = %#v, want 2", got)
	}
}

func TestOwnMovePlayerAdoptsThePositionAndOthersAreIgnored(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	a.Apply(&packet.MovePlayer{EntityRuntimeID: 99, Position: mgl32.Vec3{50, 51.62, 50}})
	if a.Pose().Feet != (mgl32.Vec3{}) {
		t.Fatal("another entity's move was adopted")
	}
	a.Apply(&packet.MovePlayer{EntityRuntimeID: 7, Position: mgl32.Vec3{5, 1.62, 6}, Yaw: 12, Pitch: 3, OnGround: true})
	p := a.Pose()
	if p.Feet != (mgl32.Vec3{5, 0, 6}) || p.Yaw != 12 || p.Pitch != 3 {
		t.Errorf("pose = %+v", p)
	}
}

func TestBreakSendsTheRecordedActionOrder(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	pos := protocol.BlockPos{3, 0, 4}
	for _, ph := range []BreakPhase{BreakStart, BreakContinue, BreakContinue, BreakAbort} {
		if err := a.Tick(Input{Break: &BreakAction{Pos: pos, Face: 2, Phase: ph}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Tick(Input{}); err != nil {
		t.Fatal(err)
	}
	want := []int32{protocol.PlayerActionStartBreak, protocol.PlayerActionContinueDestroyBlock, protocol.PlayerActionContinueDestroyBlock, protocol.PlayerActionAbortBreak}
	ins := s.inputs(t)
	for i, w := range want {
		acts, ok := ins[i].BlockActions.Value()
		if !hasFlag(ins[i], packet.InputFlagPerformBlockActions) || !ok || len(acts) != 1 {
			t.Fatalf("input %d: flag/actions wrong: %+v", i, ins[i])
		}
		if acts[0].Action != w || acts[0].BlockPos != pos || acts[0].Face != 2 {
			t.Errorf("input %d action = %+v, want %d", i, acts[0], w)
		}
	}
	if hasFlag(ins[4], packet.InputFlagPerformBlockActions) {
		t.Error("a tick without a break still performs block actions")
	}
	if _, ok := ins[4].BlockActions.Value(); ok {
		t.Error("a tick without a break still carries actions")
	}
}

func TestRecordedBreakRepliesDoNotMoveTheUnit(t *testing.T) {
	a, _ := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 0, 2}})
	for _, pk := range labPackets(t, "break_stone") {
		a.Apply(pk)
	}
	if a.Pose().Feet != (mgl32.Vec3{1, 0, 2}) {
		t.Error("block and inventory replies changed the pose")
	}
	if open, _, _ := a.Container(); open {
		t.Error("a break opened a container")
	}
}

func TestSelectHotbarSendsMobEquipment(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:wooden_pickaxe", RuntimeID: woodenPick}})
	if err := a.SelectHotbar(0, beliefItem(0, "wooden_pickaxe", 1, 77)); err != nil {
		t.Fatal(err)
	}
	me, ok := s.last(t).(*packet.MobEquipment)
	if !ok {
		t.Fatalf("sent %T", s.last(t))
	}
	if me.EntityRuntimeID != 7 || me.HotBarSlot != 0 || me.InventorySlot != 0 || me.WindowID != 0 {
		t.Errorf("equipment = %+v", me)
	}
	if me.NewItem.Stack.NetworkID != woodenPick || me.NewItem.StackNetworkID != 77 || me.NewItem.Stack.Count != 1 {
		t.Errorf("item = %+v", me.NewItem)
	}
	if err := a.SelectHotbar(0, beliefItem(0, "mystery", 1, 5)); err == nil {
		t.Error("an item the server never listed was sent")
	}
	if err := a.SelectHotbar(12, beliefItem(12, "wooden_pickaxe", 1, 5)); err == nil {
		t.Error("a non-hotbar slot was accepted")
	}
}

func TestEndLoadingScreenSendsTheGateRelease(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	if err := a.EndLoadingScreen(); err != nil {
		t.Fatal(err)
	}
	ls, ok := s.last(t).(*packet.ServerBoundLoadingScreen)
	if !ok || ls.Type != packet.LoadingScreenTypeEnd {
		t.Fatalf("sent %#v", s.last(t))
	}
	if _, set := ls.LoadingScreenID.Value(); set {
		t.Error("the release must not carry a screen ID")
	}
}

func TestTickPropagatesWriteErrorsWithoutAdvancing(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	s.err = os.ErrClosed
	if err := a.Tick(Input{Forward: 1}); err == nil {
		t.Fatal("write error swallowed")
	}
	if a.Pose().Feet != (mgl32.Vec3{}) {
		t.Error("pose advanced on a packet that never left")
	}
}

func TestOwnKnockbackBecomesVelocityAndOthersAreIgnored(t *testing.T) {
	a, _ := newAct(flat(), Pose{Feet: mgl32.Vec3{0, 0, 0}})
	a.Apply(&packet.SetActorMotion{EntityRuntimeID: 99, Velocity: mgl32.Vec3{1, 1, 1}})
	if p := a.Pose(); p.Carry != (mgl32.Vec2{}) || !p.OnGround {
		t.Fatalf("another entity's motion moved the unit: %+v", p)
	}
	a.Apply(&packet.SetActorMotion{EntityRuntimeID: 7, Velocity: mgl32.Vec3{0.3, 0.36, -0.2}})
	p := a.Pose()
	if p.OnGround || p.Carry != (mgl32.Vec2{0.3, -0.2}) {
		t.Fatalf("pose after knockback = %+v, want airborne with the horizontal carry", p)
	}
	before := p.Feet
	if err := a.Tick(Input{}); err != nil {
		t.Fatal(err)
	}
	after := a.Pose()
	if after.Feet.X() <= before.X() || after.Feet.Y() <= before.Y() || after.Feet.Z() >= before.Z() {
		t.Errorf("feet %v -> %v: the knockback did not carry the unit", before, after.Feet)
	}
}

func TestHorizontalKnockbackKeepsAGroundedUnitOnTheGround(t *testing.T) {
	a, _ := newAct(flat(), Pose{})
	a.Apply(&packet.SetActorMotion{EntityRuntimeID: 7, Velocity: mgl32.Vec3{0.2, 0, 0}})
	if p := a.Pose(); !p.OnGround || p.Carry.X() != 0.2 {
		t.Fatalf("pose = %+v, want grounded with carry 0.2", p)
	}
}

func TestAttackSendsTheHitAndTheSwing(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{4, 0, 4}})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:iron_sword", RuntimeID: 300}})
	sword := beliefItem(2, "iron_sword", 1, 9)
	if err := a.Attack(55, 2, sword); err != nil {
		t.Fatal(err)
	}
	if len(s.got) != 2 {
		t.Fatalf("sent %d packets, want the transaction and the swing", len(s.got))
	}
	tx, ok := s.got[0].(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("first packet is %T", s.got[0])
	}
	d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData)
	if !ok || d.TargetEntityRuntimeID != 55 || d.ActionType != protocol.UseItemOnEntityActionAttack || d.HotBarSlot != 2 || d.HeldItem.Stack.NetworkID != 300 {
		t.Errorf("transaction = %+v", d)
	}
	if got := d.Position; got != (mgl32.Vec3{4, eyeHeight, 4}) {
		t.Errorf("position = %v, want the eye", got)
	}
	sw, ok := s.got[1].(*packet.Animate)
	if !ok || sw.ActionType != packet.AnimateActionSwingArm || sw.EntityRuntimeID != 7 {
		t.Errorf("second packet = %+v", s.got[1])
	}
	if err := a.Attack(55, 9, beliefItem(9, "iron_sword", 1, 9)); err == nil {
		t.Error("a slot off the hotbar was accepted")
	}
}

func TestAttackWithAnEmptyHandNeedsNoItemTable(t *testing.T) {
	a, s := newAct(flat(), Pose{})
	if err := a.Attack(1, 0, beliefItem(0, "", 0, 0)); err != nil || len(s.got) != 2 {
		t.Fatalf("err %v, %d packets", err, len(s.got))
	}
}

func TestUseItemClicksAirWithTheHeldStack(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 0, 2}})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:bread", RuntimeID: 340}})
	if err := a.UseItem(3, beliefItem(3, "bread", 5, 88)); err != nil {
		t.Fatal(err)
	}
	tx, ok := s.last(t).(*packet.InventoryTransaction)
	if !ok {
		t.Fatalf("sent %T", s.last(t))
	}
	d, ok := tx.TransactionData.(*protocol.UseItemTransactionData)
	if !ok {
		t.Fatalf("transaction data %T", tx.TransactionData)
	}
	if d.ActionType != protocol.UseItemActionClickAir || d.TriggerType != protocol.TriggerTypePlayerInput || d.HotBarSlot != 3 || d.Hand != protocol.HandSlotMainHand {
		t.Errorf("data = %+v", d)
	}
	if d.HeldItem.Stack.NetworkID != 340 || d.HeldItem.StackNetworkID != 88 || d.HeldItem.Stack.Count != 5 {
		t.Errorf("held = %+v", d.HeldItem)
	}
	if err := a.UseItem(12, beliefItem(12, "bread", 1, 1)); err == nil {
		t.Error("a non-hotbar slot was accepted")
	}
	if err := a.UseItem(0, beliefItem(0, "mystery", 1, 1)); err == nil {
		t.Error("an item the server never listed was sent")
	}
}

func TestReleaseItemSendsAReleaseTransaction(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 0, 2}})
	a.ItemNames([]protocol.ItemEntry{{Name: "minecraft:bread", RuntimeID: 340}})
	if err := a.ReleaseItem(3, beliefItem(3, "bread", 5, 88)); err != nil {
		t.Fatal(err)
	}
	tx := s.last(t).(*packet.InventoryTransaction)
	d, ok := tx.TransactionData.(*protocol.ReleaseItemTransactionData)
	if !ok {
		t.Fatalf("transaction data %T", tx.TransactionData)
	}
	if d.HotBarSlot != 3 || d.HeldItem.Stack.NetworkID != 340 || d.HeadPosition.Y() < 1.6 {
		t.Errorf("data = %+v", d)
	}
}

func TestStartUsingRidesOnlyTheInputThatAsksForIt(t *testing.T) {
	a, s := newAct(flat(), Pose{Feet: mgl32.Vec3{1, 0, 2}})
	if err := a.Tick(Input{StartUsing: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(Input{}); err != nil {
		t.Fatal(err)
	}
	in := s.inputs(t)
	if !in[0].InputData.Load(packet.InputFlagStartUsingItem) {
		t.Error("the asking input lacks StartUsingItem")
	}
	if in[1].InputData.Load(packet.InputFlagStartUsingItem) {
		t.Error("the flag stayed on for the next tick")
	}
}
