package main

import (
	"context"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/afkbot/internal/logging"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// facingInterval is how often the rotation is restated. The server restores
// its own saved rotation on rejoin and may reset it after a teleport, so a
// single packet at spawn would not stay.
const facingInterval = 5 * time.Second

type packetWriter interface {
	WritePacket(packet.Packet) error
}

// pose is where the server last put the bot. holdFacing reports this, not the
// spawn position, so a teleport or respawn is never undone by a stale report.
type pose struct {
	mu     sync.Mutex
	pos    mgl32.Vec3
	pitch  float32
	paused bool
	// vehicle is the unique ID of what the bot rides, or 0. A rider's rotation
	// belongs to its vehicle, so the report has to name it and turn it.
	vehicle int64
	// ackTeleport is set when the server teleported the bot. The server
	// ignores the bot's input, rotation included, until a report says the
	// teleport was handled.
	ackTeleport bool
	nudge       chan struct{}
}

func newPose() *pose { return &pose{nudge: make(chan struct{}, 1)} }

// takeTeleportAck returns whether a teleport is waiting to be acknowledged,
// and clears it.
func (p *pose) takeTeleportAck() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.ackTeleport
	p.ackTeleport = false
	return v
}

// report reads the pose and takes any pending teleport acknowledgement in one
// step, so an acknowledgement is never sent with a position older than the
// teleport it answers. Nothing is taken while paused.
func (p *pose) report() (pos mgl32.Vec3, pitch float32, paused bool, vehicle int64, ack bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.paused {
		ack, p.ackTeleport = p.ackTeleport, false
	}
	return p.pos, p.pitch, p.paused, p.vehicle, ack
}

func (p *pose) markTeleport() {
	p.mu.Lock()
	p.ackTeleport = true
	p.mu.Unlock()
	if p.nudge != nil {
		select {
		case p.nudge <- struct{}{}:
		default:
		}
	}
}

func (p *pose) clearVehicle(id int64) {
	p.mu.Lock()
	if p.vehicle == id {
		p.vehicle = 0
	}
	p.mu.Unlock()
}

func (p *pose) setVehicle(id int64) {
	p.mu.Lock()
	p.vehicle = id
	p.mu.Unlock()
}

func (p *pose) set(pos mgl32.Vec3, pitch float32) {
	p.mu.Lock()
	p.pos, p.pitch = pos, pitch
	p.mu.Unlock()
}

// setPaused stops reports while the bot is dead: it has no position to claim.
func (p *pose) setPaused(v bool) {
	p.mu.Lock()
	p.paused = v
	p.mu.Unlock()
}

func (p *pose) get() (pos mgl32.Vec3, pitch float32, paused bool, vehicle int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos, p.pitch, p.paused, p.vehicle
}

// track follows the server's placement of the bot from the packets that move
// it.
func (p *pose) track(pk packet.Packet, runtimeID uint64, uniqueID int64) {
	switch m := pk.(type) {
	case *packet.MovePlayer:
		if m.EntityRuntimeID == runtimeID {
			p.set(m.Position, m.Pitch)
			if m.Mode == packet.MoveModeTeleport {
				p.markTeleport()
			}
		}
	case *packet.SetActorLink:
		if m.EntityLink.RiderEntityUniqueID == uniqueID {
			switch {
			case m.EntityLink.Type == protocol.EntityLinkRider:
				p.setVehicle(m.EntityLink.RiddenEntityUniqueID)
			case m.EntityLink.Type == protocol.EntityLinkRemove:
				// Only the ride the bot is on: a stale removal of an earlier
				// vehicle must not unseat it from the new one.
				p.clearVehicle(m.EntityLink.RiddenEntityUniqueID)
			default:
				// A passenger does not control its vehicle, so it has no
				// rotation to claim.
				p.setVehicle(0)
			}
		}
	case *packet.Respawn:
		if m.State == packet.RespawnStateReadyToSpawn && m.EntityRuntimeID == runtimeID {
			p.set(m.Position, 0)
			p.setVehicle(0)
		}
	}
}

// holdFacing keeps the bot looking along yaw by reporting the position the
// server gave it with that rotation, so the report never asks to move it.
func holdFacing(ctx context.Context, w packetWriter, p *pose, yaw float32, tick func() uint64) {
	send := func() {
		pos, pitch, paused, vehicle, ack := p.report()
		if paused {
			return
		}
		flags := protocol.NewInputFlags(packet.InputFlagCount)
		if vehicle != 0 {
			flags.Set(packet.InputFlagClientPredictedVehicle)
		}
		if ack {
			flags.Set(packet.InputFlagHandledTeleport)
		}
		pk := &packet.PlayerAuthInput{
			Position:         pos,
			Pitch:            pitch,
			Yaw:              yaw,
			HeadYaw:          yaw,
			InteractPitch:    pitch,
			InteractYaw:      yaw,
			InputMode:        packet.InputModeMouse,
			InteractionModel: packet.InteractionModelCrosshair,
			Tick:             tick(),
			InputData:        flags,
		}
		if vehicle != 0 {
			// Seated: name the vehicle and give it the rotation, which is the
			// one a rider's own yaw is overridden by.
			pk.ClientPredictedVehicle = protocol.Option(vehicle)
			pk.VehicleRotation = protocol.Option(mgl32.Vec2{pitch, yaw})
		}
		_ = w.WritePacket(pk)
	}
	send()
	t := time.NewTicker(facingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			send()
		case <-p.nudge:
			send()
		}
	}
}

// poseLog records what the server does to the bot's placement. It is the only
// evidence of whether a facing is honoured: the bot cannot see itself, and a
// seated bot has its rotation set by what it rides, not by what it reports.
// A server that fights the held position can correct every tick, so each kind
// of event is logged at most once per poseLogEvery, with how many were held back.
type poseLog struct {
	log  *logging.Logger
	last map[string]time.Time
	held map[string]int
	// kinds remembers what each nearby actor is, so a ride link can say what
	// the bot sits on. Bounded so a busy world cannot grow it without limit.
	kinds map[int64]string
	// seats holds the actors of seatKind with where they stand, for the
	// seat mount. Bounded like kinds.
	seats map[int64]seatActor
}

const maxPoseKinds = 4096

const poseLogEvery = 2 * time.Second

func newPoseLog(log *logging.Logger) *poseLog {
	return &poseLog{log: log, last: map[string]time.Time{}, held: map[string]int{}, kinds: map[int64]string{}, seats: map[int64]seatActor{}}
}

func (l *poseLog) emit(kind string, f logging.Fields) {
	now := time.Now()
	if t, ok := l.last[kind]; ok && now.Sub(t) < poseLogEvery {
		l.held[kind]++
		return
	}
	l.last[kind] = now
	f["suppressed"] = l.held[kind]
	l.held[kind] = 0
	l.log.Info(kind, f)
}

func (l *poseLog) event(pk packet.Packet, runtimeID uint64, uniqueID int64) {
	switch m := pk.(type) {
	case *packet.MovePlayer:
		if m.EntityRuntimeID == runtimeID {
			l.emit("server_moved_bot", logging.Fields{
				"x": m.Position[0], "y": m.Position[1], "z": m.Position[2],
				"yaw": m.Yaw, "head_yaw": m.HeadYaw, "mode": m.Mode,
			})
		}
	case *packet.CorrectPlayerMovePrediction:
		l.emit("movement_corrected", logging.Fields{
			"x": m.Position[0], "y": m.Position[1], "z": m.Position[2], "tick": m.Tick,
			"pitch": m.Rotation[0], "yaw": m.Rotation[1], "prediction_type": m.PredictionType,
		})
	case *packet.AddActor:
		if len(l.kinds) < maxPoseKinds {
			l.kinds[m.EntityUniqueID] = m.EntityType
		}
		if m.EntityType == seatKind && len(l.seats) < maxPoseKinds {
			l.seats[m.EntityUniqueID] = seatActor{runtimeID: m.EntityRuntimeID, pos: m.Position}
		}
	case *packet.RemoveActor:
		delete(l.kinds, m.EntityUniqueID)
		delete(l.seats, m.EntityUniqueID)
	case *packet.SetActorLink:
		if m.EntityLink.RiderEntityUniqueID == uniqueID {
			l.emit("ride_link", logging.Fields{
				"ridden": m.EntityLink.RiddenEntityUniqueID, "type": m.EntityLink.Type,
				"ridden_kind": l.kinds[m.EntityLink.RiddenEntityUniqueID],
			})
		}
	}
}
