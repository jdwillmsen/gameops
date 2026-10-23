package main

import (
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/craft"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
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
	life      *life.Life
	conn      packetWriter
	items     []protocol.ItemEntry
	// book indexes the recipes the server sends this session.
	book *craft.Book

	act           *act.Actuator
	correctionLog act.CorrectionLogger
}

func newUnit(runtimeID uint64, tracker *belief.Tracker, world *blocks.Store, hub *camera.Hub, runner *task.Runner, lf *life.Life, conn packetWriter) *unit {
	if lf != nil {
		lf.Spawn(runtimeID)
	}
	return &unit{runtimeID: runtimeID, tracker: tracker, world: world, hub: hub, runner: runner, life: lf, conn: conn, book: craft.NewBook()}
}

func (u *unit) actuator() *act.Actuator { return u.act }

// apply is the read loop's per-packet fan-out.
func (u *unit) apply(pk packet.Packet) error {
	// The recipe book is stored whatever else the packet reaches, because
	// CraftingData can arrive before the actuator exists to read it.
	if cd, ok := pk.(*packet.CraftingData); ok {
		u.book.Store(cd, u.items)
	}
	u.tracker.Apply(pk)
	if u.life != nil {
		u.life.Apply(pk)
	}
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

// heldRecorder tells the tracker (and the camera) what the unit itself did:
// the server echoes back neither its hotbar selection nor its walking.
type heldRecorder struct {
	packetWriter
	runtimeID uint64
	tracker   *belief.Tracker
	hub       *camera.Hub
}

func (h heldRecorder) WritePacket(pk packet.Packet) error {
	err := h.packetWriter.WritePacket(pk)
	if err != nil {
		return err
	}
	switch m := pk.(type) {
	case *packet.MobEquipment:
		if m.EntityRuntimeID == h.runtimeID {
			h.tracker.SetHeld(int(m.HotBarSlot))
		}
	case *packet.PlayerAuthInput:
		h.tracker.SetPose(m.Position, m.Yaw, m.Pitch)
		if h.hub != nil {
			h.hub.PublishSelf()
		}
	}
	return err
}

// startActing waits for the position the server reports: the spawn game data
// carries only a placeholder, and an actuator started from it would be
// corrected on its first tick.
func (u *unit) startActing() error {
	snap := u.tracker.Snapshot()
	if !snap.PositionKnown {
		return nil
	}
	a := act.New(heldRecorder{packetWriter: u.conn, runtimeID: u.runtimeID, tracker: u.tracker, hub: u.hub}, u.runtimeID, startPose(snap), path.NewSolid(u.world))
	a.SetCorrectionDiagnostics(u.correctionLog, u.world)
	a.ItemNames(u.items)
	a.Crafting(u.book, func() []belief.Item { return u.tracker.Snapshot().Inventory })
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
func (u *unit) died() {
	u.runner.Died()
	if u.act != nil {
		u.act.Died()
	}
}

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

func (u *unit) end() {
	u.runner.SessionEnded()
	if u.life != nil {
		u.life.End()
	}
}

// lifeConfig maps the settings onto what the unit does on its own. The kit is
// on only when a chest position is configured.
func lifeConfig(cfg config.Config, world skill.ChestWorld) life.Config {
	lc := life.Config{Wander: cfg.IdleWander, Radius: cfg.IdleRadius, Defend: cfg.Defend, Eat: cfg.Eat, Role: cfg.Role}
	if c := cfg.KitChest; c != nil {
		lc.Kit = &life.Kit{Chest: protocol.BlockPos{c[0], c[1], c[2]}, Food: cfg.KitFood, World: world}
	}
	return lc
}
