package belief

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func listed(e *Entities) map[uint64]Entity {
	out := map[uint64]Entity{}
	e.Each(func(en Entity) { out[en.ID] = en })
	return out
}

func TestEntitiesTrackAddMoveAndRemove(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:zombie", Position: mgl32.Vec3{1, 2, 3}})
	e.Apply(&packet.AddPlayer{EntityRuntimeID: 60, AbilityData: protocol.AbilityData{EntityUniqueID: -60}, Position: mgl32.Vec3{9, 0, 9}})
	got := listed(e)
	if len(got) != 2 || got[50].Type != "minecraft:zombie" || got[50].Player || !got[60].Player || got[60].Type != "player" {
		t.Fatalf("entities = %+v", got)
	}

	e.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: mgl32.Vec3{4, 5, 6}})
	if p := listed(e)[50].Pos; p != (mgl32.Vec3{4, 5, 6}) {
		t.Errorf("absolute move gave %v", p)
	}
	e.Apply(&packet.MoveActorDelta{EntityRuntimeID: 50, PositionX: protocol.Option(float32(7))})
	if p := listed(e)[50].Pos; p != (mgl32.Vec3{7, 5, 6}) {
		t.Errorf("delta move gave %v, want only x changed", p)
	}
	e.Apply(&packet.MovePlayer{EntityRuntimeID: 60, Position: mgl32.Vec3{2, 1.62, 2}})
	if p := listed(e)[60].Pos; p != (mgl32.Vec3{2, 0, 2}) {
		t.Errorf("player move gave %v, want the feet", p)
	}

	e.Apply(&packet.RemoveActor{EntityUniqueID: -50})
	if _, ok := e.Get(50); ok {
		t.Error("the zombie is still listed after RemoveActor")
	}
}

func TestEntitiesNeverListTheUnitItself(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddPlayer{EntityRuntimeID: 1})
	e.Apply(&packet.AddActor{EntityRuntimeID: 1, EntityType: "minecraft:zombie"})
	if n := len(listed(e)); n != 0 {
		t.Errorf("%d entities listed", n)
	}
}

func TestEntitiesForgetEverythingOnDimensionChangeAndReset(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: 5, EntityRuntimeID: 5, EntityType: "minecraft:zombie"})
	e.Apply(&packet.ChangeDimension{})
	if n := len(listed(e)); n != 0 {
		t.Errorf("%d entities after a dimension change", n)
	}
	e.Apply(&packet.AddActor{EntityUniqueID: 6, EntityRuntimeID: 6, EntityType: "minecraft:zombie"})
	e.Reset()
	if n := len(listed(e)); n != 0 {
		t.Errorf("%d entities after Reset", n)
	}
}

func TestEntitiesTrackWhereAMobsHeadFaces(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:warden", HeadYaw: 45})
	if y := listed(e)[50].HeadYaw; y != 45 {
		t.Fatalf("spawn head yaw = %v, want 45", y)
	}
	e.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Rotation: mgl32.Vec3{0, 10, 90}})
	if y := listed(e)[50].HeadYaw; y != 90 {
		t.Errorf("absolute move head yaw = %v, want the head's 90 rather than the body's 10", y)
	}
	e.Apply(&packet.MoveActorDelta{EntityRuntimeID: 50, PositionX: protocol.Option(float32(3))})
	if y := listed(e)[50].HeadYaw; y != 90 {
		t.Errorf("a delta without rotation changed the head yaw to %v", y)
	}
	e.Apply(&packet.MoveActorDelta{EntityRuntimeID: 50, RotationYHead: protocol.Option(float32(-30))})
	if y := listed(e)[50].HeadYaw; y != -30 {
		t.Errorf("delta head yaw = %v, want -30", y)
	}
}

func TestEntitiesKeepAPlayersName(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddPlayer{EntityRuntimeID: 60, Username: "Rusty"})
	e.Apply(&packet.AddActor{EntityRuntimeID: 50, EntityType: "minecraft:zombie"})
	got := listed(e)
	if got[60].Name != "Rusty" || got[50].Name != "" {
		t.Errorf("names = %q, %q", got[60].Name, got[50].Name)
	}
}
