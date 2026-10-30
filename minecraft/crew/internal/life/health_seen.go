package life

import "github.com/sandertv/gophertunnel/minecraft/protocol/packet"

// noteHealthSeen queues the first sight of a mob's health per type, so a lab
// operator can confirm from the log that other entities' health packets reach
// the unit at all. Plain animals are left out: the check is about the mobs a
// warrior fights.
func (l *Life) noteHealthSeen(pk packet.Packet) {
	var id uint64
	switch p := pk.(type) {
	case *packet.AddActor:
		id = p.EntityRuntimeID
	case *packet.UpdateAttributes:
		id = p.EntityRuntimeID
	default:
		return
	}
	if id == l.selfID {
		return
	}
	if e, ok := l.ents.Get(id); ok && e.HealthKnown && kindOf(e.Type) != other {
		l.tel.noteHealthSeen(name(e.Type), e.Health)
	}
}
