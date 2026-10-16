// Copied from github.com/jdwillmsen/gameops/minecraft/agent/pkg/liveness.
//
// Copied rather than imported: importing would make the agent's dependency
// floors this bot's, and put every agent release up for review here, to share
// code upstream has stopped changing. docs/decisions.md has the full reasoning.
//
// Fix bugs upstream first, then port here.

// Package liveness keeps the agent alive in the world, not merely connected
// to it.
//
// Death and disconnection are different things, and only the second was ever
// handled. A killed player keeps its session: the connection stays open, the
// server keeps listing it, /readyz keeps reporting ready, and the agent sits
// on a death screen indefinitely. Measured on the live server on 2026-09-08 by
// killing the agent from the console -- pod 1/1 Running, zero restarts, still
// in the player list, and not one log line. Nothing anywhere would have said
// so.
//
// The same hazard applies more expensively to the AFK bots, whose entire
// purpose is holding chunks loaded: a dead one stops doing that while still
// counting as an available replica.
package liveness

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Writer is the part of a Bedrock connection this package needs.
//
// Narrowed to one method so the respawn handshake can be tested against a
// recording fake rather than a live server -- there is no way to die on
// demand in a unit test.
type Writer interface {
	WritePacket(pk packet.Packet) error
}

// Respawner completes the respawn handshake on the agent's behalf.
//
// Bedrock respawn is an exchange, not a single packet, and the order matters:
// the server announces it is searching for a spawn point, the client says it
// is ready with ClientReadyToSpawn, the server answers with the spawn point
// it chose, and only then does the client ask with a PlayerAction. A server
// since 1.26.5x does not search without the second step, drops it if it
// arrives before the first, and drops a request sent before the third; each
// leaves the player dead for good, which is why this is a small state
// machine rather than one write.
type Respawner struct {
	runtimeID uint64
	// dead tracks whether a death has been seen but not yet completed, so a
	// stray Respawn packet outside a death cannot make the agent request a
	// respawn it does not need.
	dead bool
}

// New builds a Respawner for the player with the given entity runtime ID.
func New(runtimeID uint64) *Respawner {
	return &Respawner{runtimeID: runtimeID}
}

// Dead reports whether the agent is currently between dying and respawning.
// Readiness reads this: an agent on a death screen is connected but not doing
// its job, and reporting ready would be a lie the whole point of this package
// is to stop telling.
func (r *Respawner) Dead() bool { return r.dead }

// Handle inspects one incoming packet and drives the respawn exchange.
//
// Returns true when the packet changed whether the agent is dead, so the
// caller can log and count the death or the respawn exactly once; every other
// packet, including the exchange's intermediate steps, reports false.
func (r *Respawner) Handle(pk packet.Packet, w Writer) (handled bool, err error) {
	switch p := pk.(type) {
	case *packet.DeathInfo:
		// Nothing to send yet: a ClientReadyToSpawn that reaches the server
		// before it has started waiting for one is silently dropped.
		r.dead = true
		return true, nil

	case *packet.Respawn:
		// Joining sends the same states with no death, and those are the
		// connection's own to answer.
		if !r.dead {
			return false, nil
		}
		switch p.State {
		case packet.RespawnStateSearchingForSpawn:
			// The position is the server's to choose, not ours.
			return false, w.WritePacket(&packet.Respawn{
				State:           packet.RespawnStateClientReadyToSpawn,
				EntityRuntimeID: r.runtimeID,
			})
		case packet.RespawnStateReadyToSpawn:
			// This is the respawn button, and nobody is at a keyboard to
			// press it.
			r.dead = false
			return true, w.WritePacket(&packet.PlayerAction{
				EntityRuntimeID: r.runtimeID,
				ActionType:      protocol.PlayerActionRespawn,
			})
		}
	}
	return false, nil
}
