package life

import (
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
)

// HealthSeen is the first health reading of a mob type in a session.
type HealthSeen struct {
	Seq    uint64  `json:"seq"`
	Entity string  `json:"entity"`
	Health float64 `json:"health"`
}

// FlagSeen is the first sight of a mob type in one flag state.
type FlagSeen struct {
	Seq     uint64 `json:"seq"`
	Type    string `json:"type"`
	Ignited bool   `json:"ignited"`
	Charged bool   `json:"charged"`
	Angry   bool   `json:"angry"`
}

// Sightings is what the session has seen of other entities, in the order it
// saw it. Seq rises across both lists, so a reader that remembers the last one
// it saw can ask what is new.
type Sightings struct {
	Health []HealthSeen `json:"health"`
	Flags  []FlagSeen   `json:"flags"`
}

// Sightings returns a copy of the session's sightings. The second result is
// false for a unit that is not a warrior, which keeps none.
func (l *Life) Sightings() (Sightings, bool) {
	if l.warrior == nil {
		return Sightings{}, false
	}
	t := &l.tel
	t.mu.Lock()
	defer t.mu.Unlock()
	return Sightings{
		Health: append([]HealthSeen{}, t.seen.Health...),
		Flags:  append([]FlagSeen{}, t.seen.Flags...),
	}, true
}

// MoveCounters is where the unit's actuator adds its sprint ticks and the
// server's corrections; the fight stats read them back.
func (l *Life) MoveCounters() *act.MoveCounters { return l.moves }

type moveBase struct{ sprint, corrections uint64 }

// since is the growth of a counter, or zero for one that fell.
func since(now, was uint64) uint64 {
	if now < was {
		return 0
	}
	return now - was
}

// noteActorFlags queues the first sight of a mob type in each flag state, so a
// lab operator can confirm that the ignited, charged and angry bits reach the
// unit. The state is part of the key: a plain creeper seen first must not
// hide the charged one that follows.
func (l *Life) noteActorFlags(pk packet.Packet) {
	var id uint64
	switch p := pk.(type) {
	case *packet.AddActor:
		id = p.EntityRuntimeID
	case *packet.SetActorData:
		id = p.EntityRuntimeID
	default:
		return
	}
	if id == l.selfID {
		return
	}
	if e, ok := l.ents.Get(id); ok && e.FlagsKnown && kindOf(e.Type) != other {
		l.tel.noteActorFlags(name(e.Type), e.Ignited, e.Charged, e.Angry)
	}
}

func (t *telemetry) noteActorFlags(typ string, ignited, charged, angry bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := fmt.Sprintf("%s/%t/%t/%t", typ, ignited, charged, angry)
	if _, ok := t.flagKeys[key]; ok || len(t.flagKeys) >= maxFlagSightings {
		return
	}
	if t.flagKeys == nil {
		t.flagKeys = map[string]struct{}{}
	}
	t.flagKeys[key] = struct{}{}
	t.seq++
	f := FlagSeen{Seq: t.seq, Type: typ, Ignited: ignited, Charged: charged, Angry: angry}
	t.flagPending = append(t.flagPending, f)
	t.seen.Flags = append(t.seen.Flags, f)
}

func (t *telemetry) takeFlagSightings() []FlagSeen {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.flagPending) == 0 {
		return nil
	}
	out := t.flagPending
	t.flagPending = nil
	return out
}
