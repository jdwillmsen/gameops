package main

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/blocks"
)

// eyeHeight is how far the server places a player's position above the feet.
const eyeHeight = 1.62

type packetWriter interface{ WritePacket(packet.Packet) error }

// unit is one session's read-loop wiring: it fans every packet out to what
// listens to it and owns the actuator, which lives exactly as long as the
// session. Only the read loop calls it.
type unit struct {
	runtimeID uint64
	tracker   *belief.Tracker
	world     *blocks.Store
	hub       *camera.Hub
	runner    *task.Runner
	conn      packetWriter
	items     []protocol.ItemEntry

	act *act.Actuator
}

func newUnit(runtimeID uint64, tracker *belief.Tracker, world *blocks.Store, hub *camera.Hub, runner *task.Runner, conn packetWriter) *unit {
	return &unit{runtimeID: runtimeID, tracker: tracker, world: world, hub: hub, runner: runner, conn: conn}
}

func (u *unit) actuator() *act.Actuator { return u.act }

// apply is the read loop's per-packet fan-out.
func (u *unit) apply(pk packet.Packet) error {
	u.tracker.Apply(pk)
	// The store is the only requester of sub-chunks, so it sees every
	// packet whether or not the camera is on.
	u.world.Apply(pk, u.conn)
	// After belief: the hub reads the unit's pose back from it.
	if u.hub != nil {
		u.hub.Apply(pk, u.conn)
	}
	if u.act != nil {
		u.act.Apply(pk)
		return nil
	}
	return u.startActing()
}

// startActing waits for the position the server reports: the spawn game data
// carries only a placeholder, and an actuator started from it would be
// corrected on its first tick.
func (u *unit) startActing() error {
	snap := u.tracker.Snapshot()
	if !snap.PositionKnown {
		return nil
	}
	a := act.New(u.conn, u.runtimeID, startPose(snap), path.NewSolid(u.world))
	a.ItemNames(u.items)
	// Until the loading screen ends, the server drops every action without an
	// error, so this precedes any task tick.
	if err := a.EndLoadingScreen(); err != nil {
		return err
	}
	u.act = a
	u.runner.SetActuator(a)
	return nil
}

func startPose(s belief.Snapshot) act.Pose {
	return act.Pose{
		Feet:     mgl32.Vec3{s.Position[0], s.Position[1] - eyeHeight, s.Position[2]},
		Yaw:      s.Yaw,
		Pitch:    s.Pitch,
		OnGround: true,
	}
}

// died fails the running task: a dead unit has no chest open and no break
// running, and its queue waits for the respawn.
func (u *unit) died() { u.runner.Died() }

// respawned moves the actuator to where the server put the unit, keeping its
// tick counter so ticks keep rising across the death.
func (u *unit) respawned() {
	if u.act == nil {
		return
	}
	s := u.tracker.Snapshot()
	u.act.Apply(&packet.MovePlayer{
		EntityRuntimeID: u.runtimeID,
		Position:        mgl32.Vec3(s.Position),
		Yaw:             s.Yaw,
		Pitch:           s.Pitch,
		OnGround:        true,
	})
}

func (u *unit) end() { u.runner.SessionEnded() }
