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
	tr.Spawn(self, mgl32.Vec3{1, 2, 3}, 0)
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
