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

func flagWord(bits ...uint8) protocol.EntityMetadata {
	var v int64
	for _, b := range bits {
		v |= 1 << int64(b)
	}
	return protocol.EntityMetadata{protocol.EntityDataKeyFlags: v}
}

func TestEntitiesReadActorFlagsFromAddAndSetActorData(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:creeper",
		EntityMetadata: flagWord(protocol.EntityDataFlagPowered)})
	got := listed(e)[50]
	if !got.FlagsKnown || !got.Charged || got.Ignited || got.Angry {
		t.Fatalf("after add = %+v, want known and charged only", got)
	}

	e.Apply(&packet.SetActorData{EntityRuntimeID: 50,
		EntityMetadata: flagWord(protocol.EntityDataFlagPowered, protocol.EntityDataFlagIgnited)})
	if got := listed(e)[50]; !got.Ignited || !got.Charged {
		t.Errorf("after ignite = %+v", got)
	}

	e.Apply(&packet.SetActorData{EntityRuntimeID: 50, EntityMetadata: flagWord(protocol.EntityDataFlagPowered)})
	if got := listed(e)[50]; got.Ignited || !got.Charged || !got.FlagsKnown {
		t.Errorf("a flag word without the bit must clear it, got %+v", got)
	}

	e.Apply(&packet.AddActor{EntityUniqueID: -51, EntityRuntimeID: 51, EntityType: "minecraft:enderman",
		EntityMetadata: flagWord(protocol.EntityDataFlagAngry)})
	e.Apply(&packet.AddActor{EntityUniqueID: -52, EntityRuntimeID: 52, EntityType: "minecraft:creeper",
		EntityMetadata: flagWord(protocol.EntityDataFlagCharged)})
	if got := listed(e); !got[51].Angry || !got[52].Charged {
		t.Errorf("enderman %+v, creeper %+v", got[51], got[52])
	}
}

func TestEntitiesLeaveFlagsUnknownWithoutAFlagWord(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:creeper"})
	e.Apply(&packet.SetActorData{EntityRuntimeID: 50,
		EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFuseTime: int32(30)}})
	if got := listed(e)[50]; got.FlagsKnown || got.Ignited || got.Charged || got.Angry {
		t.Fatalf("entity without a flag word = %+v, want zero flags", got)
	}

	e.Apply(&packet.SetActorData{EntityRuntimeID: 50, EntityMetadata: flagWord(protocol.EntityDataFlagIgnited)})
	e.Apply(&packet.SetActorData{EntityRuntimeID: 50,
		EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFuseTime: int32(10)}})
	if got := listed(e)[50]; !got.Ignited || !got.FlagsKnown {
		t.Errorf("a packet without the flag word cleared the flags: %+v", got)
	}

	e.Apply(&packet.SetActorData{EntityRuntimeID: 999, EntityMetadata: flagWord(protocol.EntityDataFlagIgnited)})
	if _, ok := e.Get(999); ok {
		t.Error("SetActorData for an unknown actor must not create one")
	}
}

func healthAttr(hp float32) protocol.Attribute {
	return protocol.Attribute{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: hp}}
}

func TestEntitiesReadAMobsHealthFromAddActorAndUpdateAttributes(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:zombie",
		Attributes: []protocol.AttributeValue{{Name: "minecraft:movement", Value: 0.23}, {Name: "minecraft:health", Value: 20, Max: 20}}})
	if en := listed(e)[50]; !en.HealthKnown || en.Health != 20 {
		t.Fatalf("after AddActor health = %v (known %v), want 20", en.Health, en.HealthKnown)
	}

	e.Apply(&packet.UpdateAttributes{EntityRuntimeID: 50, Attributes: []protocol.Attribute{healthAttr(14)}})
	if en := listed(e)[50]; !en.HealthKnown || en.Health != 14 {
		t.Fatalf("after UpdateAttributes health = %v (known %v), want 14", en.Health, en.HealthKnown)
	}

	e.Apply(&packet.UpdateAttributes{EntityRuntimeID: 50, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:absorption", Value: 4}}}})
	if en := listed(e)[50]; en.Health != 14 {
		t.Errorf("an update with no health attribute changed health to %v", en.Health)
	}
}

func TestEntitiesLeaveHealthUnknownWithoutAHealthAttribute(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityRuntimeID: 50, EntityType: "minecraft:zombie"})
	e.Apply(&packet.AddActor{EntityRuntimeID: 51, EntityType: "minecraft:zombie",
		Attributes: []protocol.AttributeValue{{Name: "minecraft:movement", Value: 0.23}}})
	for _, id := range []uint64{50, 51} {
		if en := listed(e)[id]; en.HealthKnown || en.Health != 0 {
			t.Errorf("entity %d health = %v (known %v), want unknown", id, en.Health, en.HealthKnown)
		}
	}
}

func TestEntitiesIgnoreAttributesOfTheUnitAndOfUnknownActors(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.UpdateAttributes{EntityRuntimeID: 1, Attributes: []protocol.Attribute{healthAttr(9)}})
	e.Apply(&packet.UpdateAttributes{EntityRuntimeID: 99, Attributes: []protocol.Attribute{healthAttr(9)}})
	if got := listed(e); len(got) != 0 {
		t.Fatalf("entities = %+v, want none invented from attribute updates", got)
	}
}

func TestEntitiesForgetHealthWhenAMobIsRemovedAndReAdded(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:zombie",
		Attributes: []protocol.AttributeValue{{Name: "minecraft:health", Value: 20}}})
	e.Apply(&packet.RemoveActor{EntityUniqueID: -50})
	e.Apply(&packet.AddActor{EntityUniqueID: -50, EntityRuntimeID: 50, EntityType: "minecraft:zombie"})
	if en := listed(e)[50]; en.HealthKnown {
		t.Errorf("a re-added mob kept the old health %v", en.Health)
	}
}

func TestEntitiesReadPetFlagsOwnerAndTarget(t *testing.T) {
	e := NewEntities()
	e.Spawn(1)
	flags := int64(1<<protocol.EntityDataFlagTamed | 1<<protocol.EntityDataFlagSitting)
	e.Apply(&packet.AddActor{EntityUniqueID: 9, EntityRuntimeID: 9, EntityType: "gameops:lion", EntityMetadata: protocol.EntityMetadata{
		protocol.EntityDataKeyFlags:  flags,
		protocol.EntityDataKeyOwner:  int64(77),
		protocol.EntityDataKeyTarget: int64(88),
		protocol.EntityDataKeyName:   "Leo",
	}})
	got, _ := e.Get(9)
	if !got.Tamed || !got.Sitting || got.Owner != 77 || got.Target != 88 || got.CustomName != "Leo" || got.UniqueID != 9 {
		t.Fatalf("got %+v", got)
	}
	e.Apply(&packet.SetActorData{EntityRuntimeID: 9, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFlags: int64(1 << protocol.EntityDataFlagTamed)}})
	got, _ = e.Get(9)
	if !got.Tamed || got.Sitting {
		t.Errorf("after stand: %+v", got)
	}
	e.Apply(&packet.AddPlayer{EntityRuntimeID: 3, Username: "A", AbilityData: protocol.AbilityData{EntityUniqueID: 33}})
	if p, _ := e.Get(3); p.UniqueID != 33 {
		t.Errorf("player unique id = %d", p.UniqueID)
	}
}
