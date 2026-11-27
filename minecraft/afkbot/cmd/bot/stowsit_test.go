package main

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/logging"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type sink struct{ got []packet.Packet }

func (s *sink) WritePacket(p packet.Packet) error { s.got = append(s.got, p); return nil }

func item(id int32) protocol.ItemInstance {
	return protocol.ItemInstance{StackNetworkID: id, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: id}, Count: 1}}
}

func content(items ...protocol.ItemInstance) *packet.InventoryContent {
	return &packet.InventoryContent{WindowID: protocol.WindowIDInventory, Content: items}
}

func newTestStower() (*stower, *time.Time) {
	now := time.Unix(1000, 0)
	s := newStower(logging.New("error"))
	s.now = func() time.Time { return now }
	return s, &now
}

func TestStowPicksLowestEmptyHotbarSlot(t *testing.T) {
	s, _ := newTestStower()
	w := &sink{}
	s.handle(content(item(1), item(2), protocol.ItemInstance{}, item(3), protocol.ItemInstance{}), w, 5)
	if len(w.got) != 1 {
		t.Fatalf("sent %d packets", len(w.got))
	}
	m := w.got[0].(*packet.MobEquipment)
	if m.EntityRuntimeID != 5 || m.HotBarSlot != 2 || m.InventorySlot != 2 || m.WindowID != protocol.WindowIDInventory || m.NewItem.Stack.ItemType.NetworkID != 0 {
		t.Fatalf("packet = %+v", m)
	}
}

func TestStowTreatsMissingContentAsEmpty(t *testing.T) {
	s, _ := newTestStower()
	w := &sink{}
	s.handle(content(item(1)), w, 5)
	if m := w.got[0].(*packet.MobEquipment); m.HotBarSlot != 1 {
		t.Fatalf("slot = %d", m.HotBarSlot)
	}
}

func TestStowFullHotbarSendsNothing(t *testing.T) {
	s, _ := newTestStower()
	w := &sink{}
	items := make([]protocol.ItemInstance, hotbarSize)
	for i := range items {
		items[i] = item(int32(i + 1))
	}
	s.handle(content(items...), w, 5)
	if len(w.got) != 0 {
		t.Fatalf("sent %v with a full hotbar", w.got)
	}
}

func TestStowWaitsForInventory(t *testing.T) {
	s, _ := newTestStower()
	w := &sink{}
	s.handle(&packet.MobEquipment{EntityRuntimeID: 5, NewItem: item(1)}, w, 5)
	if len(w.got) != 0 {
		t.Fatal("sent before the inventory was known")
	}
	s.handle(content(), w, 5)
	if len(w.got) != 1 {
		t.Fatal("did not stow once the inventory arrived")
	}
}

func TestStowSlotUpdateAndOtherWindowsIgnored(t *testing.T) {
	s, now := newTestStower()
	w := &sink{}
	items := make([]protocol.ItemInstance, hotbarSize)
	for i := range items {
		items[i] = item(int32(i + 1))
	}
	s.handle(&packet.InventoryContent{WindowID: 7, Content: nil}, w, 5)
	if s.known {
		t.Fatal("another window counted as the inventory")
	}
	s.handle(content(items...), w, 5)
	s.handle(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 4}, w, 5)
	if len(w.got) != 0 {
		t.Fatal("slot update alone must not re-run a finished stow")
	}
	*now = now.Add(stowEvery)
	s.handle(&packet.MobEquipment{EntityRuntimeID: 5, NewItem: item(2), HotBarSlot: 1}, w, 5)
	if m := w.got[0].(*packet.MobEquipment); m.HotBarSlot != 4 {
		t.Fatalf("slot = %d, want the slot just emptied", m.HotBarSlot)
	}
}

func TestStowRerunsRateLimited(t *testing.T) {
	s, now := newTestStower()
	w := &sink{}
	s.handle(content(item(1)), w, 5)
	s.handle(&packet.MobEquipment{EntityRuntimeID: 9, NewItem: item(1)}, w, 5)
	s.handle(&packet.MobEquipment{EntityRuntimeID: 5, NewItem: protocol.ItemInstance{}, HotBarSlot: 1}, w, 5)
	if len(w.got) != 1 {
		t.Fatalf("other player or empty selection triggered a send: %d", len(w.got))
	}
	s.handle(&packet.MobEquipment{EntityRuntimeID: 5, NewItem: item(1), HotBarSlot: 0}, w, 5)
	if len(w.got) != 1 {
		t.Fatal("re-run was not rate limited")
	}
	*now = now.Add(stowEvery)
	s.handle(&packet.InventorySlot{WindowID: protocol.WindowIDInventory, Slot: 8}, w, 5)
	if len(w.got) != 2 {
		t.Fatalf("pending re-run not sent after the interval: %d", len(w.got))
	}
}

func seatFixture() (*seater, *time.Time, *pose, *poseLog) {
	now := time.Unix(1000, 0)
	s := newSeater(logging.New("error"), [3]float32{10, 64, 10})
	s.now = func() time.Time { return now }
	p := newPose()
	p.set(mgl32.Vec3{9, 64, 9}, 0)
	l := newPoseLog(logging.New("error"))
	return s, &now, p, l
}

func addActor(l *poseLog, uid int64, rid uint64, kind string, pos mgl32.Vec3) {
	l.event(&packet.AddActor{EntityUniqueID: uid, EntityRuntimeID: rid, EntityType: kind, Position: pos}, 1, 1)
}

func TestSeatLooksAtTheNearestCushionThenUsesIt(t *testing.T) {
	s, now, p, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{11, 64, 10})
	addActor(l, 2, 102, seatKind, mgl32.Vec3{10.2, 64, 10})
	addActor(l, 3, 103, "minecraft:cow", mgl32.Vec3{10, 64, 10})
	addActor(l, 4, 104, seatKind, mgl32.Vec3{20, 64, 10})
	w := &sink{}
	s.step(w, p, l, 7, 3, true)
	if len(w.got) != 0 || p.aim == nil {
		t.Fatalf("clicked before looking: sent=%d aim=%v", len(w.got), p.aim)
	}
	if want := lookAt(mgl32.Vec3{9, 64, 9}, mgl32.Vec3{10.2, 64 + seatAimHeight, 10}); *p.aim != want {
		t.Fatalf("aim = %v, want %v", *p.aim, want)
	}
	*now = now.Add(seatAimFor / 2)
	s.step(w, p, l, 7, 3, true)
	if len(w.got) != 0 {
		t.Fatal("clicked before the look reached the server")
	}
	*now = now.Add(seatAimFor)
	s.step(w, p, l, 7, 3, true)
	if len(w.got) != 2 {
		t.Fatalf("sent %d", len(w.got))
	}
	d := w.got[0].(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemOnEntityTransactionData)
	if d.TargetEntityRuntimeID != 102 || d.ActionType != protocol.UseItemOnEntityActionInteract ||
		d.HotBarSlot != 3 || d.Position != (mgl32.Vec3{9, 64, 9}) || d.HeldItem.StackNetworkID != 0 ||
		d.ClickedPosition != (mgl32.Vec3{0, 1, 0}) {
		t.Fatalf("data = %+v", d)
	}
	if a, ok := w.got[1].(*packet.Animate); !ok || a.ActionType != packet.AnimateActionSwingArm || a.EntityRuntimeID != 7 {
		t.Fatalf("no arm swing after the interact: %#v", w.got[1])
	}
	if p.aim != nil {
		t.Fatal("look not given back after the click")
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
		if abs32(got[0]-c.pitch) > 0.01 || abs32(got[1]-c.yaw) > 0.01 {
			t.Errorf("lookAt(%v) = %v, want {%v %v}", c.target, got, c.pitch, c.yaw)
		}
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func TestSeatGivesTheLookBackWhenSeatedMidAim(t *testing.T) {
	s, _, p, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{10, 64, 10})
	w := &sink{}
	s.step(w, p, l, 7, 0, true)
	p.setVehicle(1)
	s.step(w, p, l, 7, 0, true)
	if p.aim != nil || s.aiming != nil || len(w.got) != 0 {
		t.Fatalf("aim=%v aiming=%v sent=%d", p.aim, s.aiming, len(w.got))
	}
}

func TestSeatMissingRetriesThenGivesUp(t *testing.T) {
	s, now, p, l := seatFixture()
	w := &sink{}
	for i := 0; i < 20; i++ {
		s.step(w, p, l, 7, 0, true)
		s.step(w, p, l, 7, 0, true)
		*now = now.Add(seatEvery)
	}
	if s.tries != seatMaxTries || len(w.got) != 0 {
		t.Fatalf("tries=%d sent=%d", s.tries, len(w.got))
	}
}

func TestSeatRetriesEveryIntervalUntilRiding(t *testing.T) {
	s, now, p, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{10, 64, 10})
	w := &sink{}
	try := func() {
		s.step(w, p, l, 7, 0, true)
		*now = now.Add(seatAimFor)
		s.step(w, p, l, 7, 0, true)
		s.step(w, p, l, 7, 0, true)
	}
	try()
	if len(w.got) != 2 {
		t.Fatalf("not rate limited: %d", len(w.got))
	}
	*now = now.Add(seatEvery)
	try()
	if len(w.got) != 4 {
		t.Fatalf("no retry: %d", len(w.got))
	}
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiderEntityUniqueID: 7, RiddenEntityUniqueID: 1, Type: protocol.EntityLinkRider}}, 1, 7)
	*now = now.Add(seatEvery)
	try()
	if len(w.got) != 4 {
		t.Fatal("kept mounting while riding")
	}
}

func TestSeatActorsRemovedAndBounded(t *testing.T) {
	_, _, _, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{})
	l.event(&packet.RemoveActor{EntityUniqueID: 1}, 1, 1)
	if len(l.seats) != 0 {
		t.Fatal("removed actor kept")
	}
	for i := 0; i < maxPoseKinds+10; i++ {
		addActor(l, int64(i), uint64(i), seatKind, mgl32.Vec3{})
	}
	if len(l.seats) > maxPoseKinds {
		t.Fatalf("seats = %d", len(l.seats))
	}
}

func TestSeatWaitsForAKnownEmptyHandAndForTheLiving(t *testing.T) {
	s, _, p, l := seatFixture()
	addActor(l, 9, 9, seatKind, mgl32.Vec3{10, 64, 10})
	r := recorder{got: make(chan *packet.PlayerAuthInput, 4)}
	s.step(r, p, l, 7, 0, false)
	p.setPaused(true)
	s.step(r, p, l, 7, 0, true)
	if s.tries != 0 {
		t.Fatalf("tried to mount with a held item or while dead: tries=%d", s.tries)
	}
	p.setPaused(false)
	p.setVehicle(5)
	s.tries = 4
	s.step(r, p, l, 7, 0, true)
	if s.tries != 0 {
		t.Fatalf("tries not reset once seated: %d", s.tries)
	}
}

func TestSeatAimsAgainAfterAMoveMidAim(t *testing.T) {
	s, now, p, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{10, 64, 10})
	w := &sink{}
	s.step(w, p, l, 7, 0, true)
	*now = now.Add(seatAimFor)
	p.set(mgl32.Vec3{11, 64, 10}, 0)
	s.step(w, p, l, 7, 0, true)
	if len(w.got) != 0 {
		t.Fatal("clicked with a look worked out from the old position")
	}
	if want := lookAt(mgl32.Vec3{11, 64, 10}, mgl32.Vec3{10, 64 + seatAimHeight, 10}); p.aim == nil || *p.aim != want {
		t.Fatalf("aim = %v, want %v", p.aim, want)
	}
	*now = now.Add(seatAimFor)
	s.step(w, p, l, 7, 0, true)
	if len(w.got) != 2 {
		t.Fatalf("sent %d after re-aiming", len(w.got))
	}
}

func TestSeatLinksOfOtherRidersAreSeenWithoutTouchingTheBotsRide(t *testing.T) {
	_, _, p, l := seatFixture()
	l.event(&packet.AddActor{EntityUniqueID: 1, EntityRuntimeID: 101, EntityType: seatKind,
		EntityLinks: []protocol.EntityLink{{RiddenEntityUniqueID: 1, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRider}}}, 1, 7)
	l.event(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: 1, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRemove}}, 1, 7)
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: 1, RiderEntityUniqueID: 42, Type: protocol.EntityLinkRider}}, 1, 7)
	if _, _, _, v := p.get(); v != 0 || len(l.seats) != 1 {
		t.Fatalf("vehicle=%d seats=%d", v, len(l.seats))
	}
}
