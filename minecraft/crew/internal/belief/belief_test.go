package belief

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const self uint64 = 7

func fixedClock() func() time.Time {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time { return t0 }
}

func spawned(t *testing.T) *Tracker {
	t.Helper()
	tr := New(fixedClock())
	tr.Spawn(self, mgl32.Vec3{1, 2, 3}, 10, 20, 0)
	return tr
}

func TestSpawnRecordsTheStartingState(t *testing.T) {
	s := spawned(t).Snapshot()
	if !s.Spawned || s.Dead || s.Position != [3]float32{1, 2, 3} || s.Dimension != 0 {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestOwnMovementUpdatesPosition(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self, Position: mgl32.Vec3{4, 5, 6}})
	if got := tr.Snapshot().Position; got != [3]float32{4, 5, 6} {
		t.Errorf("position = %v, want [4 5 6]", got)
	}
}

func TestOtherEntitiesDoNotMoveTheUnit(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self + 1, Position: mgl32.Vec3{9, 9, 9}})
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self + 1, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 1}},
	}})
	s := tr.Snapshot()
	if s.Position != [3]float32{1, 2, 3} || s.HealthKnown {
		t.Errorf("another entity changed the unit: %+v", s)
	}
}

func TestHealthComesFromOwnAttributes(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self, Attributes: []protocol.Attribute{
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:movement", Value: 0.1}},
		{AttributeValue: protocol.AttributeValue{Name: "minecraft:health", Value: 14}},
	}})
	s := tr.Snapshot()
	if !s.HealthKnown || s.Health != 14 {
		t.Errorf("health = %v known=%v, want 14 known", s.Health, s.HealthKnown)
	}
}

func TestHungerComesFromOwnAttributes(t *testing.T) {
	tr := spawned(t)
	if s := tr.Snapshot(); s.HungerKnown {
		t.Fatal("hunger known before any attribute update")
	}
	attr := func(name string, v float32) protocol.Attribute {
		return protocol.Attribute{AttributeValue: protocol.AttributeValue{Name: name, Value: v}}
	}
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self + 1, Attributes: []protocol.Attribute{attr("minecraft:player.hunger", 1)}})
	if tr.Snapshot().HungerKnown {
		t.Fatal("another entity's hunger was taken")
	}
	tr.Apply(&packet.UpdateAttributes{EntityRuntimeID: self, Attributes: []protocol.Attribute{
		attr("minecraft:player.hunger", 13),
	}})
	s := tr.Snapshot()
	if !s.HungerKnown || s.Hunger != 13 {
		t.Errorf("hunger %v known=%v", s.Hunger, s.HungerKnown)
	}
}

func TestChangeDimensionMovesTheUnit(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.ChangeDimension{Dimension: 1, Position: mgl32.Vec3{8, 64, 8}})
	s := tr.Snapshot()
	if s.Dimension != 1 || s.Position != [3]float32{8, 64, 8} {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestResetForgetsTheSession(t *testing.T) {
	tr := spawned(t)
	tr.SetDead(true)
	tr.Reset()
	s := tr.Snapshot()
	if s.Spawned || s.Dead || s.HealthKnown {
		t.Errorf("after reset = %+v, want an unspawned zero state", s)
	}
}

// Before Spawn there is no runtime ID to match against, so nothing may be
// attributed to the unit -- runtime ID 0 is a real ID on some servers.
func TestPacketsBeforeSpawnAreIgnored(t *testing.T) {
	tr := New(fixedClock())
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: 0, Position: mgl32.Vec3{9, 9, 9}})
	if s := tr.Snapshot(); s.Spawned || s.Position != [3]float32{} {
		t.Errorf("snapshot = %+v, want untouched", s)
	}
}

// After a death the server places the unit at its spawn point and says so
// only in the Respawn packet; without it the unit would report where it died.
func TestRespawnMovesTheUnitToWhereTheServerPutIt(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.Respawn{State: packet.RespawnStateSearchingForSpawn, Position: mgl32.Vec3{0, 0, 0}})
	if got := tr.Snapshot().Position; got != [3]float32{1, 2, 3} {
		t.Fatalf("a respawn still searching moved the unit to %v", got)
	}
	tr.Apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{7, 70, 7}})
	if got := tr.Snapshot().Position; got != [3]float32{7, 70, 7} {
		t.Errorf("position = %v, want [7 70 7]", got)
	}
}

// GameData carries a placeholder Y, so the spawn position must not be
// reported as known until a packet that carries the real one.
func TestSpawnPositionIsUntrustedUntilAReliablePacket(t *testing.T) {
	tr := spawned(t)
	if tr.Snapshot().PositionKnown {
		t.Fatal("position known straight after Spawn")
	}
	tr.Apply(&packet.Respawn{State: packet.RespawnStateSearchingForSpawn})
	if tr.Snapshot().PositionKnown {
		t.Fatal("a respawn still searching made the position known")
	}
	tr.Apply(&packet.Respawn{State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{7, 70, 7}})
	if s := tr.Snapshot(); !s.PositionKnown || s.Position != [3]float32{7, 70, 7} {
		t.Errorf("after ready respawn = %+v", s)
	}
}

func TestOwnMovePlayerMakesThePositionKnown(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self + 1, Position: mgl32.Vec3{9, 9, 9}})
	if tr.Snapshot().PositionKnown {
		t.Fatal("another entity's move made the position known")
	}
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self, Position: mgl32.Vec3{4, 5, 6}})
	if !tr.Snapshot().PositionKnown {
		t.Error("own MovePlayer did not make the position known")
	}
}

func TestSpawnSeedsRotation(t *testing.T) {
	s := spawned(t).Snapshot()
	if s.Yaw != 10 || s.Pitch != 20 {
		t.Errorf("yaw=%v pitch=%v, want 10 20", s.Yaw, s.Pitch)
	}
}

func TestOwnMovePlayerUpdatesRotation(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self + 1, Yaw: 99, Pitch: 99})
	if s := tr.Snapshot(); s.Yaw != 10 || s.Pitch != 20 {
		t.Fatalf("another entity changed rotation: %+v", s)
	}
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self, Position: mgl32.Vec3{4, 5, 6}, Yaw: 90, Pitch: -15})
	if s := tr.Snapshot(); s.Yaw != 90 || s.Pitch != -15 {
		t.Errorf("yaw=%v pitch=%v, want 90 -15", s.Yaw, s.Pitch)
	}
}

func TestResetForgetsKnownPosition(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MovePlayer{EntityRuntimeID: self})
	tr.Reset()
	if tr.Snapshot().PositionKnown {
		t.Error("position still known after reset")
	}
}

func TestHeldSlotIsUnknownUntilTheServerOrUnitSetsIt(t *testing.T) {
	tr := spawned(t)
	if s := tr.Snapshot(); s.HeldKnown {
		t.Fatalf("held known at spawn: %+v", s)
	}
	tr.Apply(&packet.PlayerHotBar{SelectedHotBarSlot: 4, SelectHotBarSlot: true})
	if s := tr.Snapshot(); !s.HeldKnown || s.HeldSlot != 4 {
		t.Errorf("after PlayerHotBar: %+v", s)
	}
	// A hotbar packet that does not move the selection, or names another
	// window or a slot off the bar, must not.
	tr.Apply(&packet.PlayerHotBar{SelectedHotBarSlot: 6})
	tr.Apply(&packet.PlayerHotBar{SelectedHotBarSlot: 6, SelectHotBarSlot: true, WindowID: 3})
	tr.Apply(&packet.PlayerHotBar{SelectedHotBarSlot: 9, SelectHotBarSlot: true})
	tr.SetHeld(-1)
	tr.SetHeld(9)
	if s := tr.Snapshot(); s.HeldSlot != 4 {
		t.Errorf("held = %d, want 4", s.HeldSlot)
	}
	tr.SetHeld(8)
	if s := tr.Snapshot(); s.HeldSlot != 8 {
		t.Errorf("held = %d, want 8", s.HeldSlot)
	}
	tr.Reset()
	if s := tr.Snapshot(); s.HeldKnown {
		t.Errorf("held survived a session end: %+v", s)
	}
}

func TestCorrectionMovesTheUnit(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.CorrectPlayerMovePrediction{Position: mgl32.Vec3{5, 66, 7}, Rotation: mgl32.Vec2{20, 40}})
	s := tr.Snapshot()
	if !s.PositionKnown || s.Position != [3]float32{5, 66, 7} || s.Pitch != 20 || s.Yaw != 40 {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestSetPoseRecordsTheUnitsOwnMove(t *testing.T) {
	tr := spawned(t)
	tr.SetPose(mgl32.Vec3{1, 2, 3}, 30, 4)
	s := tr.Snapshot()
	if !s.PositionKnown || s.Position != [3]float32{1, 2, 3} || s.Yaw != 30 || s.Pitch != 4 {
		t.Errorf("snapshot = %+v", s)
	}
}

func TestEffectsFollowTheServerPackets(t *testing.T) {
	tr := spawned(t)
	if s := tr.Snapshot(); len(s.Effects) != 0 {
		t.Fatalf("effects before any packet: %+v", s.Effects)
	}
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectAdd, EffectType: 10, Amplifier: 0, Duration: 400})
	// An amplifier is the level less one; the packet's is kept as it is.
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectAdd, EffectType: 1, Amplifier: 1, Duration: -1})
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectModify, EffectType: 10, Amplifier: 2, Duration: 200})
	s := tr.Snapshot()
	if len(s.Effects) != 2 {
		t.Fatalf("effects = %+v", s.Effects)
	}
	want := []Effect{
		{ID: 1, Name: "speed", Amplifier: 1, Duration: -1},
		{ID: 10, Name: "regeneration", Amplifier: 2, Duration: 200},
	}
	for i, e := range s.Effects {
		if e != want[i] {
			t.Errorf("effect %d = %+v, want %+v", i, e, want[i])
		}
	}
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectRemove, EffectType: 10})
	if s := tr.Snapshot(); len(s.Effects) != 1 || s.Effects[0].ID != 1 {
		t.Errorf("after removal = %+v", s.Effects)
	}
}

func TestAnotherEntitysEffectsAreNotTaken(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self + 1, Operation: packet.MobEffectAdd, EffectType: 19, Amplifier: 0, Duration: 100})
	if s := tr.Snapshot(); len(s.Effects) != 0 {
		t.Errorf("another entity's effect was taken: %+v", s.Effects)
	}
}

func TestAnEffectTheTableDoesNotKnowKeepsItsId(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectAdd, EffectType: 99, Amplifier: 0, Duration: 5})
	s := tr.Snapshot()
	if len(s.Effects) != 1 || s.Effects[0].ID != 99 || s.Effects[0].Name != "" {
		t.Errorf("unknown effect = %+v", s.Effects)
	}
}

func TestResetAndRespawnForgetTheEffects(t *testing.T) {
	tr := spawned(t)
	tr.Apply(&packet.MobEffect{EntityRuntimeID: self, Operation: packet.MobEffectAdd, EffectType: 19, Amplifier: 0, Duration: 100})
	tr.Reset()
	if s := tr.Snapshot(); len(s.Effects) != 0 {
		t.Errorf("after reset = %+v", s.Effects)
	}
	tr.Spawn(self, mgl32.Vec3{1, 2, 3}, 10, 20, 0)
	if s := tr.Snapshot(); len(s.Effects) != 0 {
		t.Errorf("after respawn = %+v", s.Effects)
	}
}

func TestUniqueIDIsKeptPerSessionAsASignedDecimal(t *testing.T) {
	tr := spawned(t)
	if s := tr.Snapshot(); s.UniqueID != "" {
		t.Errorf("before the game data = %q", s.UniqueID)
	}
	tr.SetUniqueID(-4294967290)
	if s := tr.Snapshot(); s.UniqueID != "-4294967290" {
		t.Errorf("unique id = %q", s.UniqueID)
	}
	tr.Spawn(self, mgl32.Vec3{}, 0, 0, 0)
	if s := tr.Snapshot(); s.UniqueID != "" {
		t.Errorf("after respawn = %q, the last session's id was kept", s.UniqueID)
	}
	tr.SetUniqueID(7)
	tr.Reset()
	if s := tr.Snapshot(); s.UniqueID != "" {
		t.Errorf("after reset = %q", s.UniqueID)
	}
}
