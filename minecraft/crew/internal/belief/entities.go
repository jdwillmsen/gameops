package belief

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// playerEyeHeight is how far above the feet a MovePlayer position sits.
const playerEyeHeight = 1.62

// Entity is another thing in the world, as last announced.
type Entity struct {
	ID uint64
	// Type is the server's name for it, such as minecraft:zombie. Players have
	// the type "player".
	Type string
	// Pos is the feet.
	Pos    mgl32.Vec3
	Player bool
	// Name is a player's username, empty for anything else.
	Name string
	// HeadYaw is where a mob's head faces, in degrees as a unit's own yaw
	// is: what it looks at is what it is about to go for.
	HeadYaw float32
	// FlagsKnown is false until the server has sent the actor's flag word,
	// and the three below say nothing until it is true: a false Ignited
	// then means "not lit", where before it meant "never told".
	FlagsKnown bool
	// Ignited is a creeper's fuse burning.
	Ignited bool
	// Charged is a creeper struck by lightning. Bedrock marks that with the
	// powered flag; the charged flag is read as well since servers differ.
	Charged bool
	// Angry is a hostile mob's aggro, such as an enderman that has been
	// provoked.
	Angry bool
}

// withFlags returns e with the flag-derived fields read from md, or e
// unchanged when md carries no flag word: a SetActorData that only touches
// other keys must not read as "no longer lit".
func (e Entity) withFlags(md protocol.EntityMetadata) Entity {
	v, ok := md[protocol.EntityDataKeyFlags].(int64)
	if !ok {
		return e
	}
	set := func(i uint8) bool { return v&(1<<int64(i)) != 0 }
	e.FlagsKnown = true
	e.Ignited = set(protocol.EntityDataFlagIgnited)
	e.Charged = set(protocol.EntityDataFlagPowered) || set(protocol.EntityDataFlagCharged)
	e.Angry = set(protocol.EntityDataFlagAngry)
	return e
}

// Entities is who and what is around the unit, built only from packets. Items
// and other drops are left out: nothing here acts on them.
type Entities struct {
	mu     sync.RWMutex
	self   uint64
	ents   map[uint64]Entity
	unique map[int64]uint64
}

func NewEntities() *Entities {
	return &Entities{ents: map[uint64]Entity{}, unique: map[int64]uint64{}}
}

// Spawn starts a session; the unit itself is never listed.
func (e *Entities) Spawn(self uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.self = self
	e.clearLocked()
}

// Reset forgets everything, as when a session ends.
func (e *Entities) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.self = 0
	e.clearLocked()
}

func (e *Entities) clearLocked() {
	clear(e.ents)
	clear(e.unique)
}

// Each calls f for every entity under a read lock, so f must be quick and must
// not call back into e.
func (e *Entities) Each(f func(Entity)) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, en := range e.ents {
		f(en)
	}
}

// Get returns one entity by runtime ID.
func (e *Entities) Get(id uint64) (Entity, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	en, ok := e.ents[id]
	return en, ok
}

func (e *Entities) Apply(pk packet.Packet) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch p := pk.(type) {
	case *packet.AddActor:
		if p.EntityRuntimeID == e.self {
			return
		}
		e.unique[p.EntityUniqueID] = p.EntityRuntimeID
		e.ents[p.EntityRuntimeID] = Entity{ID: p.EntityRuntimeID, Type: p.EntityType, Pos: p.Position, HeadYaw: p.HeadYaw}.withFlags(p.EntityMetadata)
	case *packet.AddPlayer:
		if p.EntityRuntimeID == e.self {
			return
		}
		e.unique[p.AbilityData.EntityUniqueID] = p.EntityRuntimeID
		e.ents[p.EntityRuntimeID] = Entity{ID: p.EntityRuntimeID, Type: "player", Pos: p.Position, Player: true, Name: p.Username}
	case *packet.SetActorData:
		if en, ok := e.ents[p.EntityRuntimeID]; ok {
			e.ents[p.EntityRuntimeID] = en.withFlags(p.EntityMetadata)
		}
	case *packet.MovePlayer:
		if en, ok := e.ents[p.EntityRuntimeID]; ok {
			en.Pos = p.Position.Sub(mgl32.Vec3{0, playerEyeHeight, 0})
			e.ents[p.EntityRuntimeID] = en
		}
	case *packet.MoveActorAbsolute:
		if en, ok := e.ents[p.EntityRuntimeID]; ok {
			en.Pos, en.HeadYaw = p.Position, p.Rotation[2]
			e.ents[p.EntityRuntimeID] = en
		}
	case *packet.MoveActorDelta:
		en, ok := e.ents[p.EntityRuntimeID]
		if !ok {
			return
		}
		if v, ok := p.PositionX.Value(); ok {
			en.Pos[0] = v
		}
		if v, ok := p.PositionY.Value(); ok {
			en.Pos[1] = v
		}
		if v, ok := p.PositionZ.Value(); ok {
			en.Pos[2] = v
		}
		if v, ok := p.RotationYHead.Value(); ok {
			en.HeadYaw = v
		}
		e.ents[p.EntityRuntimeID] = en
	case *packet.RemoveActor:
		if rid, ok := e.unique[p.EntityUniqueID]; ok {
			delete(e.ents, rid)
			delete(e.unique, p.EntityUniqueID)
		}
	case *packet.ChangeDimension:
		e.clearLocked()
	}
}
