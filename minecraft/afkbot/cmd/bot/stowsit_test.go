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

func TestSeatMountsNearestCushion(t *testing.T) {
	s, _, p, l := seatFixture()
	addActor(l, 1, 101, seatKind, mgl32.Vec3{11, 64, 10})
	addActor(l, 2, 102, seatKind, mgl32.Vec3{10.2, 64, 10})
	addActor(l, 3, 103, "minecraft:cow", mgl32.Vec3{10, 64, 10})
	addActor(l, 4, 104, seatKind, mgl32.Vec3{20, 64, 10})
	w := &sink{}
	s.step(w, p, l, 3)
	if len(w.got) != 1 {
		t.Fatalf("sent %d", len(w.got))
	}
	d := w.got[0].(*packet.InventoryTransaction).TransactionData.(*protocol.UseItemOnEntityTransactionData)
	if d.TargetEntityRuntimeID != 102 || d.ActionType != protocol.UseItemOnEntityActionInteract ||
		d.HotBarSlot != 3 || d.Position != (mgl32.Vec3{9, 64, 9}) || d.HeldItem.StackNetworkID != 0 {
		t.Fatalf("data = %+v", d)
	}
}

func TestSeatMissingRetriesThenGivesUp(t *testing.T) {
	s, now, p, l := seatFixture()
	w := &sink{}
	for i := 0; i < 20; i++ {
		s.step(w, p, l, 0)
		s.step(w, p, l, 0)
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
	s.step(w, p, l, 0)
	s.step(w, p, l, 0)
	if len(w.got) != 1 {
		t.Fatalf("not rate limited: %d", len(w.got))
	}
	*now = now.Add(seatEvery)
	s.step(w, p, l, 0)
	if len(w.got) != 2 {
		t.Fatalf("no retry: %d", len(w.got))
	}
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiderEntityUniqueID: 7, RiddenEntityUniqueID: 1, Type: protocol.EntityLinkRider}}, 1, 7)
	*now = now.Add(seatEvery)
	s.step(w, p, l, 0)
	if len(w.got) != 2 {
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
