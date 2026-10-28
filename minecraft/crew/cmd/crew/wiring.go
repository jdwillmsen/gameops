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
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
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
	// creative is the creative inventory the server sent. It arrives at
	// login, possibly before the actuator exists to read it.
	creative *packet.CreativeContent

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
	if cc, ok := pk.(*packet.CreativeContent); ok {
		u.creative = cc
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
	a.SetClipDiagnostics(u.correctionLog)
	a.ItemNames(u.items)
	a.Crafting(u.book, func() []belief.Item { return u.tracker.Snapshot().Inventory })
	a.SetCreative(u.tracker.Creative)
	if u.creative != nil {
		a.Apply(u.creative)
	}
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
	lc := life.Config{Wander: cfg.IdleWander, Radius: cfg.IdleRadius, Defend: cfg.Defend, Eat: cfg.Eat, Role: warriorRole(cfg.Role)}
	if c := cfg.KitChest; c != nil {
		lc.Kit = &life.Kit{Chest: protocol.BlockPos{c[0], c[1], c[2]}, Food: cfg.KitFood, World: world}
	}
	return lc
}

// The warrior engine and the squad client meet in squadmsg's types, so the
// client is the engine's chain of command with no adapter between them. The
// engine finds the roster by asserting it on the Squad it was handed, so the
// one client must be both; a wrapper around it would silently switch the
// boss tactics' roster off.
var (
	_ life.Squad  = (*squadclient.Client)(nil)
	_ life.Roster = (*squadclient.Client)(nil)
)

// warriorRole is the role the life engine fights as. A builder role gives
// it nothing, so the unit keeps the reactive defence.
func warriorRole(r config.Role) string {
	if r.IsWarrior() {
		return string(r)
	}
	return ""
}

// newSquadClient builds the warrior's seat in its squad over the same
// crew-control base URL and work token the task client uses. It returns nil
// for a unit with no warrior role, or a warrior with no crew-control to
// reach — that one still fights, but leads itself and hears no orders.
func newSquadClient(cfg config.Config, snapshot func() belief.Snapshot, log squadclient.Logger) *squadclient.Client {
	if !cfg.Role.IsWarrior() || cfg.ControlURL == nil {
		return nil
	}
	c := squadclient.New(cfg.ControlURL, cfg.UnitID, cfg.Squad, "", squadmsg.Role(cfg.Role), cfg.ControlToken, log)
	c.SetSource(func() squadmsg.Status { return squadStatus(snapshot()) })
	return c
}

// squadStatus reports how the unit is, straight from belief, on the wire the
// squad routes read. The position is the feet — the leader anchor and the
// console's "where it stands" both mean the ground, while belief carries the
// eye position the server reports. Health unknown is not a dead heart: the
// store reads a reported zero as dead, so until the server says a number
// the wire carries a full one, which nothing reads as lost.
func squadStatus(s belief.Snapshot) squadmsg.Status {
	weapon := ""
	if s.HeldKnown {
		for _, it := range s.Inventory {
			if it.Slot == s.HeldSlot && it.Count > 0 {
				weapon = it.Name
				break
			}
		}
	}
	hp := 20.0
	if s.HealthKnown {
		hp = float64(s.Health)
	}
	return squadmsg.Status{
		HP:     hp,
		Food:   int(s.Hunger),
		Weapon: weapon,
		Pos:    squadmsg.Pos{X: s.Position[0], Y: s.Position[1] - eyeHeight, Z: s.Position[2]},
	}
}
