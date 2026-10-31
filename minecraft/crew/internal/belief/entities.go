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
	// The two bits are OR-ed on purpose: either one means a stronger blast
	// to stay out of, and a false alarm only costs some extra distance.
	Charged bool
	// Angry is a hostile mob's aggro, such as an enderman that has been
	// provoked.
	Angry bool
	// Health is the entity's last announced minecraft:health, and says
	// nothing until HealthKnown: a mob the server never sent one for is not
	// at zero.
	Health      float32
	HealthKnown bool
	// UniqueID is the id other actors' Owner and Target name this one by.
	UniqueID int64
	// Tamed and Sitting are the pet flags, valid with FlagsKnown.
	Tamed   bool
	Sitting bool
	// Owner is the unique id of the entity that owns this one, and Target the
	// one it is going for; both are 0 when the metadata names none.
	Owner  int64
	Target int64
	// CustomName is the entity's name tag, empty for none.
	CustomName string
}

// withHealth returns e with the health read from attrs, or e unchanged when
// they carry none.
func (e Entity) withHealth(attrs []protocol.AttributeValue) Entity {
	for _, a := range attrs {
		if a.Name == healthAttribute {
			e.Health, e.HealthKnown = a.Value, true
		}
	}
	return e
}

// noneIsZero maps the server's -1, "no such actor", to 0.
func noneIsZero(id int64) int64 {
	if id == -1 {
		return 0
	}
	return id
}

// withFlags returns e with the flag-derived fields read from md, or e
// unchanged when md carries no flag word: a SetActorData that only touches
// other keys must not read as "no longer lit".
func (e Entity) withFlags(md protocol.EntityMetadata) Entity {
	if v, ok := md[protocol.EntityDataKeyOwner].(int64); ok {
		e.Owner = noneIsZero(v)
	}
	if v, ok := md[protocol.EntityDataKeyTarget].(int64); ok {
		e.Target = noneIsZero(v)
	}
	if v, ok := md[protocol.EntityDataKeyName].(string); ok {
		e.CustomName = v
	}
	v, ok := md[protocol.EntityDataKeyFlags].(int64)
	if !ok {
		return e
	}
	set := func(i uint8) bool { return v&(1<<int64(i)) != 0 }
	e.FlagsKnown = true
	e.Ignited = set(protocol.EntityDataFlagIgnited)
	e.Charged = set(protocol.EntityDataFlagPowered) || set(protocol.EntityDataFlagCharged)
	e.Angry = set(protocol.EntityDataFlagAngry)
	e.Tamed = set(protocol.EntityDataFlagTamed)
	e.Sitting = set(protocol.EntityDataFlagSitting)
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
		e.ents[p.EntityRuntimeID] = Entity{ID: p.EntityRuntimeID, UniqueID: p.EntityUniqueID, Type: p.EntityType, Pos: p.Position, HeadYaw: p.HeadYaw}.withFlags(p.EntityMetadata).withHealth(p.Attributes)
	case *packet.AddPlayer:
		if p.EntityRuntimeID == e.self {
			return
		}
		e.unique[p.AbilityData.EntityUniqueID] = p.EntityRuntimeID
		e.ents[p.EntityRuntimeID] = Entity{ID: p.EntityRuntimeID, UniqueID: p.AbilityData.EntityUniqueID, Type: "player", Pos: p.Position, Player: true, Name: p.Username}
	case *packet.UpdateAttributes:
		// The unit's own health is the tracker's; an entity never announced
		// is not invented from an attribute update.
		if p.EntityRuntimeID == e.self {
			return
		}
		if en, ok := e.ents[p.EntityRuntimeID]; ok {
			vals := make([]protocol.AttributeValue, len(p.Attributes))
			for i, a := range p.Attributes {
				vals[i] = a.AttributeValue
			}
			e.ents[p.EntityRuntimeID] = en.withHealth(vals)
		}
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
