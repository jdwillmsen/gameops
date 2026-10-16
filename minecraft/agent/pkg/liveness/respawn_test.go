package liveness

import (
	"errors"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type recorder struct {
	sent []packet.Packet
	err  error
}

func (r *recorder) WritePacket(pk packet.Packet) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, pk)
	return nil
}

const runtimeID = 42

// step is one recorded server packet and what the respawner must do with it.
type step struct {
	pk packet.Packet
	// reply is the packet the client must answer with, nil for none.
	reply       packet.Packet
	wantHandled bool
	wantDead    bool
}

var (
	clientReady = &packet.Respawn{State: packet.RespawnStateClientReadyToSpawn, EntityRuntimeID: runtimeID}
	respawnAsk  = &packet.PlayerAction{EntityRuntimeID: runtimeID, ActionType: protocol.PlayerActionRespawn}
)

// deathSequence is what a 1.26.52 dedicated server sends a client that dies,
// as recorded from a lab server, with the replies that respawned it. The
// first searching state is sent when the death is processed and the server
// then waits for ClientReadyToSpawn before it searches; the ready state
// carries the chosen spawn point, and only a PlayerAction sent after it
// respawns the player.
func deathSequence() []step {
	return []step{
		{pk: &packet.DeathInfo{Cause: "death.attack.generic"}, wantHandled: true, wantDead: true},
		{pk: &packet.Respawn{Position: mgl32.Vec3{10.5, 70.62, 20.5}, State: packet.RespawnStateSearchingForSpawn}, reply: clientReady, wantDead: true},
		{pk: &packet.Respawn{Position: mgl32.Vec3{10, 32767, 20}, State: packet.RespawnStateSearchingForSpawn}, reply: clientReady, wantDead: true},
		{pk: &packet.Respawn{Position: mgl32.Vec3{12.5, 71.62, 18.5}, State: packet.RespawnStateReadyToSpawn}, reply: respawnAsk, wantHandled: true},
	}
}

func play(t *testing.T, r *Respawner, steps []step) {
	t.Helper()
	for i, s := range steps {
		w := &recorder{}
		handled, err := r.Handle(s.pk, w)
		if err != nil {
			t.Fatalf("step %d Handle(%T): %v", i, s.pk, err)
		}
		if handled != s.wantHandled {
			t.Errorf("step %d Handle(%T) handled = %v, want %v", i, s.pk, handled, s.wantHandled)
		}
		if r.Dead() != s.wantDead {
			t.Errorf("step %d after %T Dead() = %v, want %v", i, s.pk, r.Dead(), s.wantDead)
		}
		switch {
		case s.reply == nil && len(w.sent) != 0:
			t.Errorf("step %d answered %T with %+v, want no reply", i, s.pk, w.sent)
		case s.reply != nil && (len(w.sent) != 1 || !reflect.DeepEqual(w.sent[0], s.reply)):
			t.Errorf("step %d answered %T with %+v, want [%+v]", i, s.pk, w.sent, s.reply)
		}
	}
}

// Asking with a PlayerAction on death, as older servers accepted, is dropped
// and the search never starts. Saying ClientReadyToSpawn on DeathInfo is
// dropped too whenever the server's searching state lands a tick later,
// which in the lab was about one death in four. Both leave the player dead
// for good.
func TestDeathCompletesTheHandshakeInServerOrder(t *testing.T) {
	play(t, New(runtimeID), deathSequence())
}

// A second death on the same session must go through the same exchange: the
// unit that broke in the lab could be killed once and never again.
func TestConsecutiveDeathsEachRespawn(t *testing.T) {
	r := New(runtimeID)
	play(t, r, deathSequence())
	play(t, r, deathSequence())
}

// Joining runs the same Respawn states before the player has ever died. The
// connection's own spawn handles them, and answering would send a respawn
// request for a player who is alive.
func TestJoinSpawnSequenceIsIgnored(t *testing.T) {
	join := deathSequence()[1:]
	for i := range join {
		join[i].reply, join[i].wantHandled, join[i].wantDead = nil, false, false
	}
	play(t, New(runtimeID), join)
}

// A Respawn arriving outside a death -- on a dimension change, say -- must not
// make the agent confirm a respawn nobody asked for.
func TestRespawnOutsideADeathIsIgnored(t *testing.T) {
	r, w := New(runtimeID), &recorder{}
	handled, err := r.Handle(&packet.Respawn{State: packet.RespawnStateReadyToSpawn}, w)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if handled || len(w.sent) != 0 {
		t.Errorf("confirmed a respawn with no death; sent %d packets", len(w.sent))
	}
}

// Ordinary traffic must be untouched: this sits in the packet path of a live
// session, and a handler that reacts to the wrong packet is worse than none.
func TestUnrelatedPacketsAreLeftAlone(t *testing.T) {
	r, w := New(runtimeID), &recorder{}
	for _, pk := range []packet.Packet{&packet.Text{}, &packet.PlayerList{}, &packet.SetHealth{}} {
		if handled, err := r.Handle(pk, w); handled || err != nil {
			t.Errorf("Handle(%T) = (%v, %v), want (false, nil)", pk, handled, err)
		}
	}
	if len(w.sent) != 0 {
		t.Errorf("sent %d packets for unrelated traffic", len(w.sent))
	}
}

// A failed write must surface. Swallowing it would leave the agent dead with
// nothing recording why -- the exact silence this package exists to end.
func TestWriteFailureIsReported(t *testing.T) {
	r := New(runtimeID)
	_, _ = r.Handle(&packet.DeathInfo{}, &recorder{})
	w := &recorder{err: errors.New("connection closed")}
	for _, state := range []byte{packet.RespawnStateSearchingForSpawn, packet.RespawnStateReadyToSpawn} {
		if _, err := r.Handle(&packet.Respawn{State: state}, w); err == nil {
			t.Errorf("Respawn{State: %d}: want an error when the reply cannot be written", state)
		}
	}
}
