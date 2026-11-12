package belief

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func dropPacket(id uint64, item int32, count uint16, at mgl32.Vec3) *packet.AddItemActor {
	return &packet.AddItemActor{
		EntityUniqueID:  int64(id),
		EntityRuntimeID: id,
		Item:            protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: count}},
		Position:        at,
	}
}

func clocked() (*Entities, *time.Time) {
	e := NewEntities()
	now := time.Unix(1000, 0)
	e.now = func() time.Time { return now }
	return e, &now
}

func TestDroppedItemsAreTrackedApartFromEntities(t *testing.T) {
	e, _ := clocked()
	e.Apply(dropPacket(9, 302, 1, mgl32.Vec3{10, 64, 10}))
	e.Each(func(Entity) { t.Error("an item entity showed up among the entities") })
	got := e.DroppedNear(mgl32.Vec3{12, 64, 10}, 6)
	if len(got) != 1 || got[0].Item != 302 || got[0].Count != 1 || got[0].Pos != (mgl32.Vec3{10, 64, 10}) {
		t.Fatalf("DroppedNear = %+v", got)
	}
	if far := e.DroppedNear(mgl32.Vec3{40, 64, 10}, 6); len(far) != 0 {
		t.Errorf("an item 30 blocks off counted as near: %+v", far)
	}
}

func TestDroppedItemsAreListedNearestFirst(t *testing.T) {
	e, _ := clocked()
	e.Apply(dropPacket(1, 1, 1, mgl32.Vec3{5, 64, 0}))
	e.Apply(dropPacket(2, 1, 1, mgl32.Vec3{1, 64, 0}))
	got := e.DroppedNear(mgl32.Vec3{}, 6)
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 1 {
		t.Fatalf("order = %+v", got)
	}
}

func TestADroppedItemFollowsItsMovesAndLeavesWhenTaken(t *testing.T) {
	e, _ := clocked()
	e.Apply(dropPacket(3, 1, 4, mgl32.Vec3{0, 70, 0}))
	e.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 3, Position: mgl32.Vec3{0, 64, 0}})
	if got := e.DroppedNear(mgl32.Vec3{}, 2); len(got) != 1 || got[0].Pos.Y() != 64 {
		t.Fatalf("after a move: %+v", got)
	}
	e.Apply(&packet.TakeItemActor{ItemEntityRuntimeID: 3, TakerEntityRuntimeID: 1})
	if got := e.DroppedNear(mgl32.Vec3{}, 2); len(got) != 0 {
		t.Errorf("a taken item is still listed: %+v", got)
	}
	if len(e.unique) != 0 {
		t.Errorf("unique ids leaked: %v", e.unique)
	}
}

func TestARemovedDropLeaves(t *testing.T) {
	e, _ := clocked()
	e.Apply(dropPacket(4, 1, 1, mgl32.Vec3{}))
	e.Apply(&packet.RemoveActor{EntityUniqueID: 4})
	if got := e.DroppedNear(mgl32.Vec3{}, 2); len(got) != 0 {
		t.Errorf("a removed item is still listed: %+v", got)
	}
}

func TestADropExpires(t *testing.T) {
	e, now := clocked()
	e.Apply(dropPacket(5, 1, 1, mgl32.Vec3{}))
	*now = now.Add(droppedTTL + time.Second)
	if got := e.DroppedNear(mgl32.Vec3{}, 2); len(got) != 0 {
		t.Errorf("an item past its lifetime is still listed: %+v", got)
	}
}

func TestDropsAreCappedKeepingTheNewest(t *testing.T) {
	e, now := clocked()
	for i := 0; i < maxDropped+10; i++ {
		*now = now.Add(time.Millisecond)
		e.Apply(dropPacket(uint64(i+1), 1, 1, mgl32.Vec3{}))
	}
	if len(e.drops) > maxDropped {
		t.Fatalf("%d drops tracked, cap is %d", len(e.drops), maxDropped)
	}
	if got := e.DroppedNear(mgl32.Vec3{}, 1); len(got) == 0 || got[len(got)-1].ID < uint64(maxDropped) {
		t.Errorf("the newest items were not kept")
	}
	if _, old := e.drops[1]; old {
		t.Error("the oldest item survived the cap")
	}
}

func TestADimensionChangeForgetsDrops(t *testing.T) {
	e, _ := clocked()
	e.Apply(dropPacket(6, 1, 1, mgl32.Vec3{}))
	e.Apply(&packet.ChangeDimension{})
	if got := e.DroppedNear(mgl32.Vec3{}, 2); len(got) != 0 {
		t.Errorf("drops from the old dimension: %+v", got)
	}
}
