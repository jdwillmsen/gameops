package life

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const selfID = 1

var itemTable = []protocol.ItemEntry{
	{Name: "minecraft:wooden_sword", RuntimeID: 300},
	{Name: "minecraft:iron_sword", RuntimeID: 301},
	{Name: "minecraft:iron_axe", RuntimeID: 302},
	{Name: "minecraft:diamond_sword", RuntimeID: 303},
	{Name: "minecraft:wooden_pickaxe", RuntimeID: 312},
	{Name: "minecraft:dirt", RuntimeID: 3},
}

// world is a known block map: dirt at y=0, air above, bounded by size.
type world struct {
	size  int32
	over  map[protocol.BlockPos]string
	other string // the floor outside the island, when island is set
}

func (w *world) Block(p protocol.BlockPos) (string, bool) {
	if b, ok := w.over[p]; ok {
		return b, true
	}
	if p.X() < 0 || p.Z() < 0 || p.X() >= w.size || p.Z() >= w.size || p.Y() < -2 || p.Y() > 8 {
		return "", false
	}
	switch {
	case p.Y() <= 0:
		return "dirt", true
	}
	return "air", true
}

type sink struct {
	got     []packet.Packet
	tracker *belief.Tracker
}

// WritePacket records the held slot as the session's writer does, because the
// server never echoes a client's own selection back.
func (s *sink) WritePacket(pk packet.Packet) error {
	s.got = append(s.got, pk)
	if m, ok := pk.(*packet.MobEquipment); ok && s.tracker != nil {
		s.tracker.SetHeld(int(m.HotBarSlot))
	}
	return nil
}

func (s *sink) inputs() []*packet.PlayerAuthInput {
	var out []*packet.PlayerAuthInput
	for _, pk := range s.got {
		if in, ok := pk.(*packet.PlayerAuthInput); ok {
			out = append(out, in)
		}
	}
	return out
}

func (s *sink) attacks() []*protocol.UseItemOnEntityTransactionData {
	var out []*protocol.UseItemOnEntityTransactionData
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

type logged struct {
	event  string
	fields map[string]any
}

type capture struct{ lines []logged }

func (c *capture) Info(event string, f map[string]any) { c.lines = append(c.lines, logged{event, f}) }

type rig struct {
	t       *testing.T
	w       *world
	out     *sink
	a       *act.Actuator
	tracker *belief.Tracker
	l       *Life
	log     *capture
	ticks   int
}

func newRig(t *testing.T, cfg Config, feet mgl32.Vec3) *rig {
	t.Helper()
	w := &world{size: 40, over: map[protocol.BlockPos]string{}}
	out := &sink{}
	tracker := belief.New(time.Now)
	tracker.Spawn(selfID, feet.Add(mgl32.Vec3{0, 1.62, 0}), 0, 0, 0)
	tracker.ItemNames(itemTable)
	out.tracker = tracker
	a := act.New(out, selfID, act.Pose{Feet: feet, OnGround: true}, path.NewSolid(w))
	a.ItemNames(itemTable)
	log := &capture{}
	l := New(cfg, w, tracker.Snapshot, log, rand.New(rand.NewSource(7)))
	l.Spawn(selfID)
	return &rig{t: t, w: w, out: out, a: a, tracker: tracker, l: l, log: log}
}

var defaults = Config{Wander: true, Radius: 8, Defend: true}

// step is one runner tick: the life decides, then sends the tick's one input.
func (r *rig) step() (engaged bool) {
	r.t.Helper()
	snap := r.tracker.Snapshot()
	before := len(r.out.inputs())
	var err error
	if r.l.Engaged(r.a.Pose(), snap) {
		engaged = true
		err = r.l.Fight(context.Background(), r.a, snap)
	} else {
		err = r.l.Idle(context.Background(), r.a)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	if n := len(r.out.inputs()) - before; n != 1 {
		r.t.Fatalf("tick %d sent %d inputs, want exactly 1", r.ticks, n)
	}
	r.ticks++
	return engaged
}

func (r *rig) run(n int) {
	r.t.Helper()
	for i := 0; i < n; i++ {
		r.step()
	}
}

func (r *rig) spawn(id uint64, typ string, at mgl32.Vec3) {
	r.l.Apply(&packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at})
}

func (r *rig) player(id uint64, at mgl32.Vec3) {
	r.l.Apply(&packet.AddPlayer{EntityRuntimeID: id, AbilityData: protocol.AbilityData{EntityUniqueID: int64(id)}, Position: at})
}

func (r *rig) moveTo(id uint64, at mgl32.Vec3) {
	r.l.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: id, Position: at})
}

func (r *rig) hurt() {
	r.l.Apply(&packet.ActorEvent{EntityRuntimeID: selfID, EventType: packet.ActorEventHurt})
}

func (r *rig) give(slot int, item int32, count int) {
	r.tracker.Apply(&packet.InventorySlot{WindowID: 0, Slot: uint32(slot), NewItem: protocol.ItemInstance{
		StackNetworkID: int32(500 + slot),
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: uint16(count)},
	}})
}

func (r *rig) setHealth(h float32) {
	pk := &packet.UpdateAttributes{EntityRuntimeID: selfID, Attributes: []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: h}}}}
	r.tracker.Apply(pk)
	r.l.Apply(pk)
}

func (r *rig) feet() mgl32.Vec3 { return r.a.Pose().Feet }

func dist(a, b mgl32.Vec3) float64 { return horizontal(a, b) }

var start = mgl32.Vec3{20.5, 1, 20.5}

func TestIdleSendsOneInputPerTickAndWandersInsideTheRadius(t *testing.T) {
	r := newRig(t, defaults, start)
	moved, maxFar := false, 0.0
	yaws := map[float32]bool{}
	for i := 0; i < 3000; i++ {
		r.step()
		d := dist(r.feet(), start)
		moved = moved || d > 1
		maxFar = math.Max(maxFar, d)
		yaws[r.a.Pose().Yaw] = true
	}
	if len(r.out.inputs()) != 3000 || len(r.out.got) != 3000 {
		t.Fatalf("sent %d packets, %d inputs, want 3000 inputs and nothing else", len(r.out.got), len(r.out.inputs()))
	}
	if !moved {
		t.Error("the unit never left the spot it went idle at")
	}
	if maxFar > 8.6 {
		t.Errorf("wandered %.1f blocks from the start, want at most the radius of 8", maxFar)
	}
	if len(yaws) < 5 {
		t.Errorf("only %d distinct headings: the unit should look around", len(yaws))
	}
	for _, in := range r.out.inputs() {
		if _, ok := in.BlockActions.Value(); ok {
			t.Fatal("an idle input carried a block action")
		}
	}
}

func TestIdleWithWanderOffStandsStillButKeepsTicking(t *testing.T) {
	r := newRig(t, Config{Wander: false, Radius: 8, Defend: true}, start)
	r.run(400)
	if len(r.out.inputs()) != 400 {
		t.Fatalf("inputs = %d, want 400", len(r.out.inputs()))
	}
	if d := dist(r.feet(), start); d > 0.01 {
		t.Errorf("moved %.2f blocks with wandering off", d)
	}
}

func TestIdleNeverWalksOntoWaterOrOffALedge(t *testing.T) {
	r := newRig(t, defaults, start)
	// A 7x7 island of dirt in water, and a ledge: the world drops away to
	// the south.
	for x := int32(0); x < 40; x++ {
		for z := int32(0); z < 40; z++ {
			if x < 17 || x > 23 || z < 17 || z > 23 {
				r.w.over[protocol.BlockPos{x, 0, z}] = "water"
			}
		}
	}
	for x := int32(17); x <= 23; x++ {
		for y := int32(-2); y <= 0; y++ {
			r.w.over[protocol.BlockPos{x, y, 24}] = "air"
		}
	}
	for i := 0; i < 3000; i++ {
		r.step()
		f := r.feet()
		if f.X() < 16.9 || f.X() > 24.1 || f.Z() < 16.9 || f.Z() > 24.1 || f.Y() < 0.9 {
			t.Fatalf("tick %d: left the island or fell: %v", i, f)
		}
	}
}

func TestIdleStaysWithinRadiusWhenWallForcesDetour(t *testing.T) {
	r := newRig(t, defaults, start)
	r.step()
	r.a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{26.5, 2.62, 20.5},
		OnGround: true,
	})
	for z := int32(19); z <= 21; z++ {
		for y := int32(1); y <= 2; y++ {
			r.w.over[protocol.BlockPos{27, y, z}] = "stone"
		}
	}
	for i := 0; i < 3000; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 8.6 {
			t.Fatalf("tick %d: wandered %.2f blocks from idle anchor, want at most 8.6", i, d)
		}
	}
}

func TestIdleStopsWalkAfterCorrectionLeavesRadiusOrHeightBand(t *testing.T) {
	for _, tc := range []struct {
		name string
		feet mgl32.Vec3
	}{
		{name: "radius", feet: mgl32.Vec3{30, 1, 20.5}},
		{name: "height", feet: mgl32.Vec3{20.5, 4, 20.5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, defaults, start)
			for i := 0; i < 3000 && r.l.idle.walk == nil; i++ {
				r.step()
			}
			if r.l.idle.walk == nil {
				t.Fatal("never started an idle walk")
			}
			r.a.Apply(&packet.CorrectPlayerMovePrediction{
				Position: tc.feet.Add(mgl32.Vec3{0, 1.62, 0}),
				OnGround: true,
			})
			r.step()
			if r.l.idle.walk != nil {
				t.Fatal("kept navigating after correction left idle bounds")
			}
		})
	}
}

func TestIdleReanchorsAfterCorrectionOutsideRadius(t *testing.T) {
	r := newRig(t, defaults, start)
	r.step()
	r.a.Apply(&packet.CorrectPlayerMovePrediction{
		Position: mgl32.Vec3{30, 2.62, 20.5},
		OnGround: true,
	})
	r.step()
	if want := (protocol.BlockPos{30, 1, 20}); r.l.idle.anchor != want {
		t.Fatalf("anchor = %v after correction, want %v", r.l.idle.anchor, want)
	}
	moved := false
	for i := 0; i < 3000; i++ {
		r.step()
		if d := dist(r.feet(), mgl32.Vec3{30.5, 1, 20.5}); d > 1 {
			moved = true
		}
		if d := dist(r.feet(), mgl32.Vec3{30.5, 1, 20.5}); d > 8.6 {
			t.Fatalf("tick %d: wandered %.2f blocks from corrected position", i, d)
		}
	}
	if !moved {
		t.Fatal("unit did not resume wandering from its corrected position")
	}
}

func TestTaskStartCancelsTheWalk(t *testing.T) {
	r := newRig(t, defaults, start)
	for i := 0; i < 3000 && r.l.idle.walk == nil; i++ {
		r.step()
	}
	if r.l.idle.walk == nil {
		t.Fatal("never started a walk")
	}
	r.l.Cancel()
	if r.l.idle.walk != nil || r.l.idle.anchored {
		t.Error("Cancel left the walk or the anchor in place")
	}
}

func TestStaleWalkIsDroppedAfterAGap(t *testing.T) {
	r := newRig(t, defaults, start)
	for i := 0; i < 3000 && r.l.idle.walk == nil; i++ {
		r.step()
	}
	for i := 0; i < 10; i++ { // a fight had the unit
		r.l.tick++
	}
	r.step()
	if r.l.idle.walk != nil {
		t.Error("a walk planned before the gap was resumed")
	}
}

func TestZombieInRangeIsFoughtOnTheAttackCadenceWithTheBestWeapon(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(0, 3, 10)
	r.give(2, 300, 1) // wooden sword
	r.give(4, 303, 1) // diamond sword
	r.give(5, 302, 1) // iron axe
	r.give(20, 303, 1)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21.5, 1, 20.5})

	for i := 0; i < 60; i++ {
		if !r.step() {
			t.Fatalf("tick %d: not engaged with a zombie one block away", i)
		}
	}
	hits := r.out.attacks()
	if len(hits) != 6 {
		t.Fatalf("%d attacks in 60 ticks, want one every 10", len(hits))
	}
	for _, h := range hits {
		if h.TargetEntityRuntimeID != 50 || h.ActionType != protocol.UseItemOnEntityActionAttack {
			t.Errorf("attack = %+v", h)
		}
		if h.HotBarSlot != 4 || h.HeldItem.Stack.NetworkID != 303 {
			t.Errorf("attacked with slot %d item %d, want the diamond sword in slot 4", h.HotBarSlot, h.HeldItem.Stack.NetworkID)
		}
	}
	var selected int
	for _, pk := range r.out.got {
		if m, ok := pk.(*packet.MobEquipment); ok {
			selected++
			if m.HotBarSlot != 4 {
				t.Errorf("selected slot %d, want 4", m.HotBarSlot)
			}
		}
	}
	if selected != 1 {
		t.Errorf("selected the weapon %d times, want once", selected)
	}
	if got := r.log.lines[0]; got.event != "defend_started" || got.fields["entity"] != "zombie" {
		t.Errorf("first log line = %+v", got)
	}
}

func TestSwordBeatsAxeBeatsNothingAndAFistIsTheLastResort(t *testing.T) {
	cases := []struct {
		name  string
		items map[int]int32
		slot  int
		item  int32
	}{
		{"sword over axe", map[int]int32{1: 302, 6: 300}, 6, 300},
		{"axe over a pickaxe", map[int]int32{1: 312, 2: 302}, 2, 302},
		{"better sword wins", map[int]int32{3: 300, 4: 301}, 4, 301},
		{"fist", map[int]int32{1: 3}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, defaults, start)
			for slot, item := range c.items {
				r.give(slot, item, 1)
			}
			r.spawn(50, "minecraft:zombie", mgl32.Vec3{21, 1, 20.5})
			r.run(5)
			hits := r.out.attacks()
			if len(hits) == 0 {
				t.Fatal("no attack")
			}
			if int(hits[0].HotBarSlot) != c.slot || hits[0].HeldItem.Stack.NetworkID != c.item {
				t.Errorf("attacked with slot %d item %d, want slot %d item %d", hits[0].HotBarSlot, hits[0].HeldItem.Stack.NetworkID, c.slot, c.item)
			}
		})
	}
}

func TestAZombieAtRangeIsApproachedThenFought(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{25.5, 1, 20.5})
	for i := 0; i < 80 && len(r.out.attacks()) == 0; i++ {
		r.step()
	}
	if len(r.out.attacks()) == 0 {
		t.Fatalf("never reached the zombie; feet %v", r.feet())
	}
	if d := dist(r.feet(), mgl32.Vec3{25.5, 1, 20.5}); d > meleeReach {
		t.Errorf("attacked from %.1f blocks away, past reach", d)
	}
}

func TestAMobOutOfRangeOrOnAnotherFloorIsLeftAlone(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{20.5, 1, 30})
	r.spawn(51, "minecraft:zombie", mgl32.Vec3{22, 9, 20.5})
	for i := 0; i < 5; i++ {
		if r.step() {
			t.Fatal("engaged with a mob that is far or a floor away")
		}
	}
}

func TestPlayersAndPassiveMobsAreNeverAttacked(t *testing.T) {
	r := newRig(t, defaults, start)
	r.player(60, mgl32.Vec3{21, 1, 20.5})
	r.spawn(61, "minecraft:cow", mgl32.Vec3{20, 1, 20.5})
	r.spawn(62, "minecraft:villager_v2", mgl32.Vec3{20.5, 1, 21})
	r.spawn(63, "minecraft:item", mgl32.Vec3{20.5, 1, 19.5})
	for i := 0; i < 100; i++ {
		if r.step() {
			t.Fatalf("tick %d: engaged with a player or a passive mob", i)
		}
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks on players or passive mobs", n)
	}
}

func TestAPlayerHittingTheUnitIsFacedAndSteppedAwayFromNotHit(t *testing.T) {
	r := newRig(t, defaults, start)
	r.player(60, mgl32.Vec3{21.5, 1, 20.5})
	r.hurt()
	engaged := 0
	var endPose act.Pose
	for i := 0; i < 40; i++ {
		if r.step() {
			engaged++
			endPose = r.a.Pose()
		}
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks on a player", n)
	}
	if engaged == 0 {
		t.Fatal("a blow from a player was ignored")
	}
	// Bedrock yaw 0 looks along +Z and -90 along +X, so the view is
	// (-sin yaw, cos yaw); when the step back ends it must point at the player.
	rad := float64(endPose.Yaw) * math.Pi / 180
	toPlayer := mgl32.Vec2{21.5 - endPose.Feet.X(), 20.5 - endPose.Feet.Z()}.Normalize()
	cos := -math.Sin(rad)*float64(toPlayer.X()) + math.Cos(rad)*float64(toPlayer.Y())
	if off := math.Acos(math.Max(-1, math.Min(1, cos))) * 180 / math.Pi; off > 20 {
		t.Errorf("faces %.0f degrees away from the player when the step back ends (yaw %.0f)", off, endPose.Yaw)
	}
	if d := dist(r.feet(), mgl32.Vec3{21.5, 1, 20.5}); d < 2.5 {
		t.Errorf("only %.1f blocks from the player after stepping back", d)
	}
	if r.log.lines[len(r.log.lines)-1].event == "" {
		t.Error("no log line")
	}
	for _, l := range r.log.lines {
		if l.event == "defend_started" && l.fields["mode"] != "step_back" {
			t.Errorf("defend_started mode = %v, want step_back", l.fields["mode"])
		}
	}
}

func TestBeingHurtTurnsTowardTheNearestHostileMob(t *testing.T) {
	r := newRig(t, defaults, start)
	// A skeleton to the east, out of the 6-block threat range, shoots.
	r.spawn(70, "minecraft:skeleton", mgl32.Vec3{29.5, 1, 20.5})
	r.hurt()
	r.step()
	yaw := r.a.Pose().Yaw
	if math.Abs(float64(yaw)+90) > 15 { // facing +X is a yaw of -90
		t.Errorf("yaw = %.0f after a hit from the east, want about -90", yaw)
	}
}

func TestAHealthDropAloneCountsAsBeingHurt(t *testing.T) {
	r := newRig(t, defaults, start)
	r.setHealth(20)
	r.spawn(70, "minecraft:skeleton", mgl32.Vec3{29.5, 1, 20.5})
	r.step()
	if len(r.log.lines) != 0 {
		t.Fatalf("logged %+v with nothing wrong", r.log.lines)
	}
	r.setHealth(17)
	r.step()
	found := false
	for _, l := range r.log.lines {
		found = found || (l.event == "hurt_by" && l.fields["entity"] == "skeleton")
	}
	if !found {
		t.Errorf("log = %+v, want hurt_by skeleton", r.log.lines)
	}
}

func TestAFallWithNothingNearbyDoesNotEngage(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(61, "minecraft:cow", mgl32.Vec3{21, 1, 20.5})
	r.hurt()
	if r.step() {
		t.Error("engaged after damage no hostile mob or player could have dealt")
	}
}

func TestLowHealthRetreatsInsteadOfFighting(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(0, 301, 1)
	r.setHealth(5)
	z := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(50, "minecraft:zombie", z)
	d0 := dist(r.feet(), z)
	for i := 0; i < 40; i++ {
		r.step()
		z = z.Add(mgl32.Vec3{-0.05, 0, 0}) // a zombie trailing the unit
		r.moveTo(50, z)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks at 5 health, want a retreat", n)
	}
	if d := dist(r.feet(), z); d <= d0 {
		t.Errorf("distance to the zombie went from %.1f to %.1f, want the unit to gain ground", d0, d)
	}
	if r.feet().X() >= start.X() {
		t.Errorf("unit went toward the zombie: %v", r.feet())
	}
}

func TestACornerdUnitFightsEvenAtLowHealth(t *testing.T) {
	r := newRig(t, defaults, start)
	for x := int32(0); x < 40; x++ {
		for z := int32(0); z < 40; z++ {
			if x != 20 || z != 20 {
				r.w.over[protocol.BlockPos{x, 0, z}] = "lava"
			}
		}
	}
	r.setHealth(3)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21, 1, 20.5})
	r.run(30)
	if len(r.out.attacks()) == 0 {
		t.Error("a unit with no way out never fought back")
	}
}

func TestCreepersAreBackedAwayFromNotMeleed(t *testing.T) {
	r := newRig(t, defaults, start)
	r.give(0, 301, 1)
	c := mgl32.Vec3{22.5, 1, 20.5}
	r.spawn(80, "minecraft:creeper", c)
	for i := 0; i < 40; i++ {
		r.step()
		c = c.Add(mgl32.Vec3{-0.03, 0, 0})
		r.moveTo(80, c)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks on a creeper", n)
	}
	if d := dist(r.feet(), c); d < 3 {
		t.Errorf("%.1f blocks from the creeper after backing away", d)
	}
}

func TestACreeperFarOffIsNotAThreat(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(80, "minecraft:creeper", mgl32.Vec3{27, 1, 20.5})
	if r.step() {
		t.Error("engaged with a creeper beyond its blast range")
	}
}

func TestAnEndermanIsLeftAloneUntilItHurtsTheUnit(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(90, "minecraft:enderman", mgl32.Vec3{22.5, 1, 20.5})
	if r.step() {
		t.Fatal("engaged with an enderman that had done nothing")
	}
	r.hurt()
	if !r.step() {
		t.Fatal("not engaged with the enderman that hurt the unit")
	}
	r.run(30)
	if len(r.out.attacks()) == 0 {
		t.Error("never fought the enderman that attacked")
	}
}

func TestAMobTheUnitCannotReachIsGivenUpOn(t *testing.T) {
	r := newRig(t, defaults, start)
	// Walled in: the zombie stands in a sealed cell five blocks away.
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			for y := int32(1); y <= 2; y++ {
				if dx != 0 || dz != 0 {
					r.w.over[protocol.BlockPos{25 + dx, y, 20 + dz}] = "stone"
				}
			}
		}
	}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			r.w.over[protocol.BlockPos{25 + dx, 3, 20 + dz}] = "stone"
		}
	}
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{25.5, 1, 20.5})
	engaged := 0
	for i := 0; i < 100; i++ {
		if r.step() {
			engaged++
		}
	}
	if engaged > graceTicks+10 {
		t.Errorf("engaged for %d of 100 ticks with a mob it cannot reach", engaged)
	}
}

func TestAdjacentAttackingZombieRemainsThreatAfterFightLimit(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(52, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.l.engaged = true
	r.l.target, _ = r.l.ents.Get(52)
	r.l.fightTicks[52] = fightLimit
	r.hurt()
	if !r.step() {
		t.Fatal("stopped defending an adjacent zombie at fight limit")
	}
	if _, ignored := r.l.ignore[52]; ignored {
		t.Fatal("gave up on a zombie already within melee reach")
	}
	r.l.ignore[52] = r.l.tick + ignoreTicks
	for i := 0; i < 20; i++ {
		r.hurt()
		if !r.step() {
			t.Fatalf("stopped defending adjacent zombie at tick %d", i)
		}
	}
	if len(r.out.attacks()) == 0 {
		t.Fatal("never struck adjacent zombie despite the ignore entry")
	}
}

func TestNewZombieGetsFreshFightLimitAfterRecentAttacker(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(54, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.hurt()
	r.step()
	if r.l.target.ID != 54 {
		t.Fatalf("first target = %d, want 54", r.l.target.ID)
	}
	r.l.fightTicks[54] = fightLimit + 20
	r.hurt()
	r.step()
	if _, ignored := r.l.ignore[54]; ignored {
		t.Fatal("recent attacker was given up while still in melee reach")
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 54})
	r.spawn(55, "minecraft:zombie", mgl32.Vec3{23.5, 1, 20.5})
	if !r.step() {
		t.Fatal("did not engage the fresh zombie")
	}
	if r.l.target.ID != 55 || r.l.fightTicks[55] != 1 {
		t.Fatalf("new target=%d fight ticks=%d, want target 55 at tick 1", r.l.target.ID, r.l.fightTicks[55])
	}
	if _, ignored := r.l.ignore[55]; ignored {
		t.Fatal("fresh zombie inherited the previous target's fight limit")
	}
}

func TestInReachZombieThatNeverAttackedIsGivenUpAfterFightLimit(t *testing.T) {
	r := newRig(t, defaults, start)
	for y := int32(1); y <= 2; y++ {
		r.w.over[protocol.BlockPos{21, y, 20}] = "stone"
	}
	r.spawn(53, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.l.engaged = true
	r.l.target, _ = r.l.ents.Get(53)
	r.l.fightTicks[53] = fightLimit
	if !r.step() {
		t.Fatal("did not process the in-reach zombie before giving up")
	}
	if r.l.attacker != 0 {
		t.Fatalf("unexpected attacker %d for a zombie that never hurt the unit", r.l.attacker)
	}
	if r.l.ignore[53] <= r.l.tick {
		t.Fatal("did not ignore the in-reach zombie after the fight limit")
	}
	if _, ok := r.l.fightTicks[53]; ok {
		t.Fatal("kept per-mob fight ticks after giving up")
	}
	for i := 0; i < graceTicks+5; i++ {
		r.step()
	}
	if r.l.engaged {
		t.Fatal("kept the task preempted by a non-attacking zombie after giving up")
	}
	r.l.fightTicks[99] = 1
	r.l.Reset()
	if len(r.l.fightTicks) != 0 {
		t.Fatal("kept per-mob fight ticks after Reset")
	}
}

func TestAlternatingUnreachableZombiesAccumulateFightTicksIndependently(t *testing.T) {
	r := newRig(t, defaults, start)
	for y := int32(1); y <= 2; y++ {
		r.w.over[protocol.BlockPos{21, y, 20}] = "stone"
	}
	a := mgl32.Vec3{22.5, 1, 20.5}
	b := mgl32.Vec3{22.5, 1, 21.5}
	r.spawn(56, "minecraft:zombie", a)
	r.spawn(57, "minecraft:zombie", b)
	for i := 0; i < 2*fightLimit+10; i++ {
		if i%2 == 0 {
			r.moveTo(56, a)
			r.moveTo(57, b)
		} else {
			r.moveTo(56, b)
			r.moveTo(57, a)
		}
		r.step()
		if r.l.ignore[56] > r.l.tick && r.l.ignore[57] > r.l.tick {
			return
		}
	}
	t.Fatalf("alternating mobs were not both given up within %d ticks: fight ticks=%v ignore=%v", 2*fightLimit+10, r.l.fightTicks, r.l.ignore)
}

func TestIgnoredPillarSkeletonDoesNotRestartFightAfterEachArrow(t *testing.T) {
	r := newRig(t, defaults, start)
	for y := int32(1); y <= 3; y++ {
		r.w.over[protocol.BlockPos{30, y, 20}] = "stone"
	}
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{30.5, 4, 20.5})
	active := 0
	for i := 0; i < 100; i++ {
		r.hurt()
		if r.step() {
			active++
		}
	}
	if active > graceTicks+10 {
		t.Errorf("engaged for %d of 100 ticks despite the unreachable mob ignore", active)
	}
}

func TestPlayerHitOutranksNearbyWolfsNeutralAttribution(t *testing.T) {
	r := newRig(t, defaults, start)
	r.player(71, mgl32.Vec3{21.5, 1, 20.5})
	r.spawn(72, "minecraft:wolf", mgl32.Vec3{24.5, 1, 20.5})
	r.hurt()
	r.step()
	if r.l.attacker != 71 || !r.l.attackerPlayer {
		t.Fatalf("attributed hit to %d (player=%v), want the nearby player", r.l.attacker, r.l.attackerPlayer)
	}
	if len(r.out.attacks()) != 0 {
		t.Fatalf("attacked the wolf after a player hit")
	}
	if r.l.mode != modeStepBack {
		t.Errorf("mode = %v, want step back", r.l.mode)
	}
}

func TestEnvironmentalDamageDoesNotProvokeDistantGolem(t *testing.T) {
	r := newRig(t, defaults, start)
	r.setHealth(20)
	r.spawn(73, "minecraft:iron_golem", mgl32.Vec3{24.5, 1, 20.5})
	r.setHealth(17)
	if r.step() {
		t.Fatal("engaged with a golem outside melee reach after an environmental health drop")
	}
	if r.l.attacker != 0 || len(r.out.attacks()) != 0 {
		t.Fatalf("attributed or attacked golem: attacker=%d attacks=%d", r.l.attacker, len(r.out.attacks()))
	}
}

func TestDefendOffNeverEngages(t *testing.T) {
	r := newRig(t, Config{Wander: true, Radius: 8, Defend: false}, start)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21, 1, 20.5})
	r.hurt()
	for i := 0; i < 20; i++ {
		if r.step() {
			t.Fatal("engaged with defence off")
		}
	}
	if len(r.out.attacks()) != 0 {
		t.Error("attacked with defence off")
	}
}

func TestTheFightEndsAfterTheGraceWhenTheMobIsGone(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21, 1, 20.5})
	r.run(5)
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	still := 0
	for i := 0; i < 60; i++ {
		if r.step() {
			still++
		}
	}
	if still < graceTicks-2 || still > graceTicks+2 {
		t.Errorf("engaged for %d ticks after the mob vanished, want about %d", still, graceTicks)
	}
	last := r.log.lines[len(r.log.lines)-1]
	if last.event != "defend_ended" {
		t.Errorf("last log = %+v, want defend_ended", last)
	}
}

func TestNoLogLineCarriesAPosition(t *testing.T) {
	r := newRig(t, defaults, start)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21, 1, 20.5})
	r.hurt()
	r.run(40)
	for _, l := range r.log.lines {
		for k := range l.fields {
			switch k {
			case "entity", "mode", "ticks":
			default:
				t.Errorf("%s logged field %q: only types, modes and counts belong in the log", l.event, k)
			}
		}
	}
}
