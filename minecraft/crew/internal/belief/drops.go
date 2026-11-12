package belief

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// A dropped item despawns after five minutes; one the server never
	// removed is forgotten a minute later, so a missed RemoveActor cannot
	// send a unit to an item that is long gone.
	droppedTTL = 6 * time.Minute
	// A busy fight or farm drops hundreds of items; only the newest matter.
	maxDropped = 256
)

// Dropped is an item entity lying in the world, as last announced.
type Dropped struct {
	ID       uint64
	UniqueID int64
	// Item is the server's network id of the item; names live in the item
	// table, which the entity tracker does not hold.
	Item  int32
	Count int
	// Pos is where the item rests or last moved to.
	Pos  mgl32.Vec3
	Seen time.Time
}

// DroppedNear lists the unexpired items within radius blocks of pos on the
// horizontal plane, nearest first.
func (e *Entities) DroppedNear(pos mgl32.Vec3, radius float64) []Dropped {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := e.now()
	var out []Dropped
	for _, d := range e.drops {
		if now.Sub(d.Seen) > droppedTTL {
			continue
		}
		if horizontal(d.Pos, pos) <= radius {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := horizontal(out[i].Pos, pos), horizontal(out[j].Pos, pos)
		if di != dj {
			return di < dj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func horizontal(a, b mgl32.Vec3) float64 {
	dx, dz := float64(a.X()-b.X()), float64(a.Z()-b.Z())
	return math.Hypot(dx, dz)
}

// applyDropLocked tracks item entities, which the rest of Apply leaves out.
func (e *Entities) applyDropLocked(pk packet.Packet) {
	switch p := pk.(type) {
	case *packet.AddItemActor:
		if p.Item.Stack.Count == 0 {
			return
		}
		e.pruneDropsLocked()
		e.unique[p.EntityUniqueID] = p.EntityRuntimeID
		e.drops[p.EntityRuntimeID] = Dropped{ID: p.EntityRuntimeID, UniqueID: p.EntityUniqueID, Item: p.Item.Stack.NetworkID, Count: int(p.Item.Stack.Count), Pos: p.Position, Seen: e.now()}
	case *packet.TakeItemActor:
		e.dropLocked(p.ItemEntityRuntimeID)
	case *packet.RemoveActor:
		if rid, ok := e.unique[p.EntityUniqueID]; ok {
			delete(e.drops, rid)
		}
	case *packet.MoveActorAbsolute:
		if d, ok := e.drops[p.EntityRuntimeID]; ok {
			d.Pos = p.Position
			e.drops[d.ID] = d
		}
	case *packet.MoveActorDelta:
		d, ok := e.drops[p.EntityRuntimeID]
		if !ok {
			return
		}
		if v, ok := p.PositionX.Value(); ok {
			d.Pos[0] = v
		}
		if v, ok := p.PositionY.Value(); ok {
			d.Pos[1] = v
		}
		if v, ok := p.PositionZ.Value(); ok {
			d.Pos[2] = v
		}
		e.drops[d.ID] = d
	}
}

func (e *Entities) dropLocked(id uint64) {
	if d, ok := e.drops[id]; ok {
		delete(e.unique, d.UniqueID)
		delete(e.drops, id)
	}
}

// pruneDropsLocked makes room for one more item: expired items go first, then
// the oldest.
func (e *Entities) pruneDropsLocked() {
	now := e.now()
	for id, d := range e.drops {
		if now.Sub(d.Seen) > droppedTTL {
			e.dropLocked(id)
		}
	}
	for len(e.drops) >= maxDropped {
		all := make([]Dropped, 0, len(e.drops))
		for _, d := range e.drops {
			all = append(all, d)
		}
		oldest := slices.MinFunc(all, func(a, b Dropped) int { return a.Seen.Compare(b.Seen) })
		e.dropLocked(oldest.ID)
	}
}
