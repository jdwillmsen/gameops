// Package belief is what crew knows about itself, built only from packets the
// server sent. It is a belief rather than the truth: a packet the server never
// sends leaves it stale, which is why every snapshot carries UpdatedAt.
package belief

import (
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const healthAttribute = "minecraft:health"

// Snapshot is a copy, safe to hand to another goroutine.
type Snapshot struct {
	Spawned   bool  `json:"spawned"`
	Dead      bool  `json:"dead"`
	Dimension int32 `json:"dimension"`
	// As the server reports it. Bedrock places players at eye height, so this
	// is not the block the unit stands on.
	Position    [3]float32 `json:"position"`
	Health      float32    `json:"health"`
	HealthKnown bool       `json:"health_known"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Tracker is written by the session's read loop and read by the status API.
type Tracker struct {
	mu   sync.RWMutex
	now  func() time.Time
	self uint64
	s    Snapshot
}

func New(now func() time.Time) *Tracker { return &Tracker{now: now} }

func (t *Tracker) Spawn(runtimeID uint64, pos mgl32.Vec3, dimension int32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.self = runtimeID
	t.s = Snapshot{Spawned: true, Dimension: dimension, Position: pos, UpdatedAt: t.now()}
}

func (t *Tracker) Apply(pk packet.Packet) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.s.Spawned {
		return
	}
	switch p := pk.(type) {
	case *packet.MovePlayer:
		if p.EntityRuntimeID != t.self {
			return
		}
		t.s.Position = p.Position
	case *packet.UpdateAttributes:
		if p.EntityRuntimeID != t.self {
			return
		}
		for _, a := range p.Attributes {
			if a.Name == healthAttribute {
				t.s.Health, t.s.HealthKnown = a.Value, true
			}
		}
	case *packet.Respawn:
		// Only ever about the receiving player, so no runtime ID check; the
		// position is settled only once the server is ready to spawn.
		if p.State != packet.RespawnStateReadyToSpawn {
			return
		}
		t.s.Position = p.Position
	case *packet.ChangeDimension:
		t.s.Dimension, t.s.Position = p.Dimension, p.Position
	default:
		return
	}
	t.s.UpdatedAt = t.now()
}

func (t *Tracker) SetDead(dead bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.s.Dead = dead
	t.s.UpdatedAt = t.now()
}

// Reset is called when a session ends, so a reader never mistakes the last
// session's state for a live one.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.self = 0
	t.s = Snapshot{UpdatedAt: t.now()}
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.s
}
