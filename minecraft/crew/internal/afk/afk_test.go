package afk

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

var _ task.Reflex = (*Reflex)(nil)

// fakeActuator records what the reflex asks of the unit. The embedded
// interface is nil: a call the reflex must never make panics.
type fakeActuator struct {
	skill.Actuator
	pose     act.Pose
	riding   int64
	ticks    []act.Input
	selects  []int
	interact []interaction
}

type interaction struct {
	target uint64
	slot   int
	held   belief.Item
}

func (f *fakeActuator) Pose() act.Pose { return f.pose }
func (f *fakeActuator) Riding() int64  { return f.riding }
func (f *fakeActuator) Tick(in act.Input) error {
	f.ticks = append(f.ticks, in)
	return nil
}
func (f *fakeActuator) SelectHotbar(slot int, item belief.Item) error {
	if item != (belief.Item{}) {
		return fmt.Errorf("stow selected %+v, want an empty hand", item)
	}
	f.selects = append(f.selects, slot)
	return nil
}
func (f *fakeActuator) InteractEntity(target uint64, slot int, held belief.Item) error {
	f.interact = append(f.interact, interaction{target, slot, held})
	return nil
}

type recLog struct{ lines []string }

func (l *recLog) Info(e string, f map[string]any) {
	l.lines = append(l.lines, fmt.Sprintf("%s %v", e, f))
}
func (l *recLog) Warn(e string, f map[string]any) {
	l.lines = append(l.lines, fmt.Sprintf("%s %v", e, f))
}

func (l *recLog) count(event string) int {
	n := 0
	for _, line := range l.lines {
		if strings.HasPrefix(line, event+" ") {
			n++
		}
	}
	return n
}

type fixture struct {
	r    *Reflex
	a    *fakeActuator
	log  *recLog
	now  *time.Time
	snap *belief.Snapshot
	ents map[uint64]belief.Entity
}

const selfRuntime, selfUnique = 7, -7

func newFixture(cfg Config) *fixture {
	now := time.Unix(1000, 0)
	f := &fixture{
		a:    &fakeActuator{pose: act.Pose{Feet: mgl32.Vec3{9, 64, 9}, Yaw: 33, Pitch: 5}},
		log:  &recLog{},
		now:  &now,
		snap: &belief.Snapshot{Spawned: true},
		ents: map[uint64]belief.Entity{},
	}
	f.r = New(cfg, func() belief.Snapshot { return *f.snap }, func(fn func(belief.Entity)) {
		for _, e := range f.ents {
			fn(e)
		}
	}, f.log)
	f.r.now = func() time.Time { return *f.now }
	f.r.Begin(selfRuntime, selfUnique)
	return f
}

func (f *fixture) idle(t *testing.T) {
	t.Helper()
	before := len(f.a.ticks)
	if err := f.r.Idle(context.Background(), f.a); err != nil {
		t.Fatal(err)
	}
	if got := len(f.a.ticks) - before; got != 1 {
		t.Fatalf("Idle sent %d inputs, want exactly one", got)
	}
}

func (f *fixture) advance(d time.Duration) { *f.now = f.now.Add(d) }

func (f *fixture) cushion(uid int64, rid uint64, pos mgl32.Vec3) {
	f.ents[rid] = belief.Entity{ID: rid, UniqueID: uid, Type: seatKind, Pos: pos}
}

func item(id int32) protocol.ItemInstance {
	return protocol.ItemInstance{StackNetworkID: id, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 1}}
}

func content(items ...protocol.ItemInstance) *packet.InventoryContent {
	return &packet.InventoryContent{WindowID: protocol.WindowIDInventory, Content: items}
}

func fullHotbar() *packet.InventoryContent {
	items := make([]protocol.ItemInstance, hotbarSize)
	for i := range items {
		items[i] = item(int32(i + 1))
	}
	return content(items...)
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

// The reflex is what makes an idle unit unable to fight: nothing it says can
// put the runner on the fighting, swimming or tending path.
func TestReflexNeverEngagesSwimsOrTends(t *testing.T) {
	f := newFixture(Config{})
	threatened := belief.Snapshot{Spawned: true, Health: 1, HealthKnown: true, Hunger: 0, HungerKnown: true}
	if f.r.Engaged(f.a.pose, threatened) || f.r.Swimming(f.a.pose) || f.r.Tending(threatened, false) || f.r.Tending(threatened, true) {
		t.Fatal("the idle reflex engaged, swam or tended")
	}
	for name, call := range map[string]func() error{
		"Fight": func() error { return f.r.Fight(context.Background(), f.a, threatened) },
		"Tend":  func() error { return f.r.Tend(context.Background(), f.a, threatened) },
		"Swim":  func() error { return f.r.Swim(context.Background(), f.a) },
	} {
		before := len(f.a.ticks)
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(f.a.ticks) != before+1 || f.a.ticks[len(f.a.ticks)-1] != (act.Input{Yaw: 33, Pitch: 5}) {
			t.Errorf("%s did not send one still input: %+v", name, f.a.ticks)
		}
	}
}

func TestIdleHoldsTheCurrentRotationWithoutAFacing(t *testing.T) {
	f := newFixture(Config{})
	f.idle(t)
	if got := f.a.ticks[0]; got != (act.Input{Yaw: 33, Pitch: 5}) {
		t.Errorf("input = %+v, want the pose's rotation and no movement", got)
	}
}

func TestIdleHoldsTheFacingAndThePitch(t *testing.T) {
	f := newFixture(Config{Facing: 270, HasFacing: true})
	f.idle(t)
	f.a.pose.Yaw = 10 // the server turned the unit
	f.idle(t)
	for _, in := range f.a.ticks {
		if in != (act.Input{Yaw: 270, Pitch: 5}) {
			t.Errorf("input = %+v, want yaw 270 at the held pitch", in)
		}
	}
}

func TestStowPicksLowestEmptyHotbarSlot(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(content(item(1), item(2), protocol.ItemInstance{}, item(3), protocol.ItemInstance{}))
	f.idle(t)
	if len(f.a.selects) != 1 || f.a.selects[0] != 2 {
		t.Fatalf("selects = %v, want [2]", f.a.selects)
	}
	if f.log.count("stow_sent") != 1 {
		t.Errorf("log = %v", f.log.lines)
	}
}

func TestStowTreatsMissingContentAsEmpty(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(content(item(1)))
	f.idle(t)
	if len(f.a.selects) != 1 || f.a.selects[0] != 1 {
		t.Fatalf("selects = %v, want [1]", f.a.selects)
	}
}

func TestStowFullHotbarSendsNothing(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(fullHotbar())
	f.idle(t)
	if len(f.a.selects) != 0 {
		t.Fatalf("selected %v with a full hotbar", f.a.selects)
	}
	if f.log.count("stow_no_empty_slot") != 1 {
		t.Errorf("log = %v", f.log.lines)
	}
	// A slot that frees later is tried at once the interval allows.
	f.r.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 6})
	f.advance(stowEvery)
	f.idle(t)
	if len(f.a.selects) != 1 || f.a.selects[0] != 6 {
		t.Fatalf("selects = %v, want the freed slot 6", f.a.selects)
	}
}

func TestStowWaitsForInventory(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(&packet.MobEquipment{EntityRuntimeID: selfRuntime, NewItem: item(1)})
	f.idle(t)
	if len(f.a.selects) != 0 {
		t.Fatal("stowed before the inventory was known")
	}
	f.r.Apply(content())
	f.idle(t)
	if len(f.a.selects) != 1 {
		t.Fatal("did not stow once the inventory arrived")
	}
}

func TestStowSlotUpdateAndOtherWindowsIgnored(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(&packet.InventoryContent{WindowID: 7})
	f.idle(t)
	if len(f.a.selects) != 0 {
		t.Fatal("another window counted as the inventory")
	}
	items := fullHotbar()
	items.Content[4] = protocol.ItemInstance{}
	f.r.Apply(items)
	f.idle(t)
	f.r.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 4, NewItem: item(9)})
	f.r.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 2})
	f.advance(stowEvery)
	f.idle(t)
	if len(f.a.selects) != 1 {
		t.Fatalf("a slot update alone re-ran a finished stow: %v", f.a.selects)
	}
	// The server puts an item in the unit's hand: stow again, to the slot
	// that is empty now.
	f.r.Apply(&packet.MobEquipment{EntityRuntimeID: selfRuntime, NewItem: item(9), HotBarSlot: 4})
	f.idle(t)
	if len(f.a.selects) != 2 || f.a.selects[1] != 2 {
		t.Fatalf("selects = %v, want a re-stow to slot 2", f.a.selects)
	}
}

func TestStowRerunsRateLimited(t *testing.T) {
	f := newFixture(Config{Stow: true})
	f.r.Apply(content(item(1)))
	f.idle(t)
	f.r.Apply(&packet.MobEquipment{EntityRuntimeID: 9, NewItem: item(1)})
	f.r.Apply(&packet.MobEquipment{EntityRuntimeID: selfRuntime, HotBarSlot: 1})
	f.idle(t)
	if len(f.a.selects) != 1 {
		t.Fatalf("another player or an empty selection triggered a stow: %v", f.a.selects)
	}
	f.r.Apply(&packet.MobEquipment{EntityRuntimeID: selfRuntime, NewItem: item(1), HotBarSlot: 0})
	f.idle(t)
	if len(f.a.selects) != 1 {
		t.Fatal("re-stow was not rate limited")
	}
	f.advance(stowEvery)
	f.idle(t)
	if len(f.a.selects) != 2 {
		t.Fatalf("pending re-stow not sent after the interval: %v", f.a.selects)
	}
}

// seatFixture is a unit at feet {9,64,9} with an empty hand in slot 3 and a
// seat configured at {10,64,10}.
func seatFixture(t *testing.T) *fixture {
	f := newFixture(Config{Stow: true, Seat: [3]float32{10, 64, 10}, HasSeat: true})
	f.r.Apply(content(item(1), item(2), item(3)))
	f.idle(t)
	if len(f.a.selects) != 1 || f.a.selects[0] != 3 {
		t.Fatalf("stow = %v, want slot 3", f.a.selects)
	}
	// The stow's tick also spent a seat try on an empty world.
	f.r.tries, f.r.seatLast = 0, time.Time{}
	f.a.ticks, f.log.lines = nil, nil
	return f
}

func eye(feet mgl32.Vec3) mgl32.Vec3 { return feet.Add(mgl32.Vec3{0, eyeHeight, 0}) }

func TestSeatLooksAtTheNearestCushionThenUsesIt(t *testing.T) {
	f := newFixture(Config{Stow: true, Seat: [3]float32{10, 64, 10}, HasSeat: true, Facing: 90, HasFacing: true})
	f.r.Apply(content(item(1), item(2), item(3)))
	f.cushion(1, 101, mgl32.Vec3{11, 64, 10})
	f.cushion(2, 102, mgl32.Vec3{10.2, 64, 10})
	f.ents[103] = belief.Entity{ID: 103, UniqueID: 3, Type: "minecraft:cow", Pos: mgl32.Vec3{10, 64, 10}}
	f.cushion(4, 104, mgl32.Vec3{20, 64, 10})

	f.idle(t)
	if len(f.a.interact) != 0 {
		t.Fatal("clicked before looking")
	}
	want := lookAt(eye(mgl32.Vec3{9, 64, 9}), mgl32.Vec3{10.2, 64 + seatAimHeight, 10})
	if got := f.a.ticks[0]; got.Pitch != want[0] || got.Yaw != want[1] {
		t.Fatalf("input = %+v, want the look %v", got, want)
	}
	f.advance(seatAimFor / 2)
	f.idle(t)
	if len(f.a.interact) != 0 {
		t.Fatal("clicked before the look reached the server")
	}
	f.advance(seatAimFor)
	f.idle(t)
	if len(f.a.interact) != 1 {
		t.Fatalf("interactions = %v", f.a.interact)
	}
	if got := f.a.interact[0]; got != (interaction{target: 102, slot: 3}) {
		t.Errorf("interaction = %+v, want cushion 102 from the empty slot 3", got)
	}
	if got := f.a.ticks[len(f.a.ticks)-1]; got.Yaw != 90 || got.Pitch != 5 {
		t.Errorf("input after the click = %+v, want the facing back", got)
	}
	if f.log.count("seat_found") != 1 || f.log.count("seat_mount_sent") != 1 {
		t.Errorf("log = %v", f.log.lines)
	}
}

// Crew's pose is the feet, where afkbot's position was the eye. A look worked
// out from the feet points 1.6 blocks under the cushion's middle, and the
// server refuses a click on what the unit is not looking at.
func TestSeatAimsFromTheEyeNotTheFeet(t *testing.T) {
	f := seatFixture(t)
	f.a.pose.Feet = mgl32.Vec3{10, 64, 8}
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	f.idle(t)
	// From the eye at y 65.62 to the cushion's middle at 64.125, 2 blocks
	// south: down by atan(1.495/2).
	got := f.a.ticks[0]
	if !near(got.Pitch, 36.78) || !near(got.Yaw, 0) {
		t.Errorf("look = pitch %v yaw %v, want pitch 36.78 yaw 0", got.Pitch, got.Yaw)
	}
}

func TestLookAtStraightDownAndAlongTheAxes(t *testing.T) {
	for _, c := range []struct {
		target     mgl32.Vec3
		pitch, yaw float32
	}{
		{mgl32.Vec3{0, -1, 0}, 90, 0},
		{mgl32.Vec3{0, 0, 1}, 0, 0},
		{mgl32.Vec3{-1, 0, 0}, 0, 90},
		{mgl32.Vec3{0, 0, -1}, 0, 180},
		{mgl32.Vec3{1, 0, 0}, 0, 270},
	} {
		got := lookAt(mgl32.Vec3{}, c.target)
		if !near(got[0], c.pitch) || !near(got[1], c.yaw) {
			t.Errorf("lookAt(%v) = %v, want {%v %v}", c.target, got, c.pitch, c.yaw)
		}
	}
}

func TestSeatGivesTheLookBackWhenSeatedMidAim(t *testing.T) {
	f := seatFixture(t)
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	f.idle(t)
	f.snap.Vehicle = 1
	f.idle(t)
	if f.r.aiming != nil || len(f.a.interact) != 0 {
		t.Fatalf("aiming=%v interactions=%v", f.r.aiming, f.a.interact)
	}
	if got := f.a.ticks[1]; got != (act.Input{Yaw: 33, Pitch: 5}) {
		t.Errorf("input = %+v, want the held look", got)
	}
}

func TestSeatMissingRetriesThenGivesUp(t *testing.T) {
	f := seatFixture(t)
	for i := 0; i < 20; i++ {
		f.idle(t)
		f.idle(t)
		f.advance(seatEvery)
	}
	if f.r.tries != seatMaxTries || len(f.a.interact) != 0 {
		t.Fatalf("tries=%d interactions=%d", f.r.tries, len(f.a.interact))
	}
	if n := f.log.count("seat_missing"); n != seatMaxTries {
		t.Errorf("seat_missing logged %d times, want %d", n, seatMaxTries)
	}
}

func TestSeatRetriesEveryIntervalUntilRiding(t *testing.T) {
	f := seatFixture(t)
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	try := func() {
		f.idle(t)
		f.advance(seatAimFor)
		f.idle(t)
		f.idle(t)
	}
	try()
	if len(f.a.interact) != 1 {
		t.Fatalf("not rate limited: %d", len(f.a.interact))
	}
	f.advance(seatEvery)
	try()
	if len(f.a.interact) != 2 {
		t.Fatalf("no retry: %d", len(f.a.interact))
	}
	// The actuator sent its last input seated, before belief says so.
	f.a.riding = 1
	f.advance(seatEvery)
	try()
	if len(f.a.interact) != 2 {
		t.Fatal("kept mounting while riding")
	}
}

func TestSeatWaitsForAKnownEmptyHandAndForTheLiving(t *testing.T) {
	f := newFixture(Config{Seat: [3]float32{10, 64, 10}, HasSeat: true})
	f.cushion(9, 9, mgl32.Vec3{10, 64, 10})
	f.idle(t) // the inventory is unknown
	f.r.Apply(fullHotbar())
	f.idle(t) // the hand holds an item
	f.r.Apply(content())
	f.snap.Dead = true
	f.idle(t)
	if f.r.tries != 0 {
		t.Fatalf("tried to mount with a held item or while dead: tries=%d", f.r.tries)
	}
	f.snap.Dead = false
	f.snap.Vehicle = 5
	f.r.tries = 4
	f.idle(t)
	if f.r.tries != 0 {
		t.Fatalf("tries not reset once seated: %d", f.r.tries)
	}
}

func TestSeatAimsAgainAfterAMoveMidAim(t *testing.T) {
	f := seatFixture(t)
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	f.idle(t)
	f.advance(seatAimFor)
	f.a.pose.Feet = mgl32.Vec3{11, 64, 10}
	f.idle(t)
	if len(f.a.interact) != 0 {
		t.Fatal("clicked with a look worked out from the old position")
	}
	want := lookAt(eye(mgl32.Vec3{11, 64, 10}), mgl32.Vec3{10, 64 + seatAimHeight, 10})
	if got := f.a.ticks[1]; got.Pitch != want[0] || got.Yaw != want[1] {
		t.Fatalf("input = %+v, want the look %v", got, want)
	}
	f.advance(seatAimFor)
	f.idle(t)
	if len(f.a.interact) != 1 {
		t.Fatalf("interactions = %d after re-aiming, want 1", len(f.a.interact))
	}
}

func TestResetGivesTheSeatFreshTries(t *testing.T) {
	f := seatFixture(t)
	for i := 0; i < seatMaxTries; i++ {
		f.idle(t)
		f.advance(seatEvery)
	}
	if f.r.tries != seatMaxTries {
		t.Fatalf("tries = %d", f.r.tries)
	}
	f.r.Reset()
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	f.idle(t)
	if f.r.tries != 1 || f.r.aiming == nil {
		t.Fatalf("after Reset: tries=%d aiming=%v, want a fresh try", f.r.tries, f.r.aiming)
	}
}

// A new session's server sends the hotbar again; until it does, the old one
// says nothing about the hand.
func TestEndForgetsTheHotbar(t *testing.T) {
	f := seatFixture(t)
	f.r.End()
	f.r.Reset()
	f.cushion(1, 101, mgl32.Vec3{10, 64, 10})
	f.idle(t)
	if f.r.tries != 0 || len(f.a.selects) != 1 {
		t.Fatalf("acted on the last session's hotbar: tries=%d selects=%v", f.r.tries, f.a.selects)
	}
}

func TestRideLinkHoldsBackRepeats(t *testing.T) {
	f := newFixture(Config{})
	f.r.Apply(&packet.AddActor{EntityUniqueID: 1, EntityRuntimeID: 101, EntityType: seatKind})
	link := &packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: 1, RiderEntityUniqueID: selfUnique, Type: protocol.EntityLinkRider}}
	for i := 0; i < 3; i++ {
		f.r.Apply(link)
	}
	f.advance(logEvery)
	f.r.Apply(link)
	if n := f.log.count("ride_link"); n != 2 {
		t.Fatalf("ride_link logged %d times, want 2: %v", n, f.log.lines)
	}
	last := f.log.lines[len(f.log.lines)-1]
	if !strings.Contains(last, "suppressed:2") || !strings.Contains(last, "ridden_kind:"+seatKind) {
		t.Errorf("line = %s, want the held-back count and what is ridden", last)
	}
}

func TestSeatLinksOfOtherRidersAreLogged(t *testing.T) {
	f := newFixture(Config{})
	f.r.Apply(&packet.AddActor{EntityUniqueID: 1, EntityRuntimeID: 101, EntityType: seatKind,
		EntityLinks: []protocol.EntityLink{{RiddenEntityUniqueID: 1, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRider}}})
	f.r.Apply(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: 1, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRemove}})
	f.r.Apply(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: 2, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRider}})
	if n := f.log.count("seat_link"); n != 2 {
		t.Fatalf("seat_link logged %d times, want 2: %v", n, f.log.lines)
	}
	if f.log.count("ride_link") != 0 {
		t.Error("another rider's link logged as the unit's ride")
	}
}

func TestActorTablesAreRemovedAndBounded(t *testing.T) {
	f := newFixture(Config{})
	f.r.Apply(&packet.AddActor{EntityUniqueID: 1, EntityType: seatKind})
	f.r.Apply(&packet.RemoveActor{EntityUniqueID: 1})
	if len(f.r.seats) != 0 || len(f.r.kinds) != 0 {
		t.Fatal("removed actor kept")
	}
	for i := 0; i < maxKinds+10; i++ {
		f.r.Apply(&packet.AddActor{EntityUniqueID: int64(i), EntityType: seatKind})
	}
	if len(f.r.seats) > maxKinds || len(f.r.kinds) > maxKinds {
		t.Fatalf("seats = %d kinds = %d", len(f.r.seats), len(f.r.kinds))
	}
}

func TestChunkRadiusGrantedIsLogged(t *testing.T) {
	f := newFixture(Config{})
	f.r.Apply(&packet.ChunkRadiusUpdated{ChunkRadius: 12})
	if len(f.log.lines) != 1 || f.log.lines[0] != "chunk_radius_granted map[radius:12]" {
		t.Errorf("log = %v", f.log.lines)
	}
}
