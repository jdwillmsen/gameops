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
		}
	case *packet.SetActorLink:
		if m.EntityLink.RiderEntityUniqueID == uniqueID {
			if m.EntityLink.Type == protocol.EntityLinkRemove {
				p.setVehicle(0)
			} else {
				p.setVehicle(m.EntityLink.RiddenEntityUniqueID)
			}
		}
	case *packet.Respawn:
		if m.State == packet.RespawnStateReadyToSpawn && m.EntityRuntimeID == runtimeID {
			p.set(m.Position, 0)
		}
	}
}

// holdFacing keeps the bot looking along yaw by reporting the position the
// server gave it with that rotation, so the report never asks to move it.
func holdFacing(ctx context.Context, w packetWriter, p *pose, yaw float32, tick func() uint64) {
	send := func() {
		pos, pitch, paused, vehicle := p.get()
		if paused {
			return
		}
		flags := protocol.NewInputFlags(packet.InputFlagCount)
		if vehicle != 0 {
			flags.Set(packet.InputFlagClientPredictedVehicle)
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
}

const maxPoseKinds = 4096

const poseLogEvery = 2 * time.Second

func newPoseLog(log *logging.Logger) *poseLog {
	return &poseLog{log: log, last: map[string]time.Time{}, held: map[string]int{}, kinds: map[int64]string{}}
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
	case *packet.RemoveActor:
		delete(l.kinds, m.EntityUniqueID)
	case *packet.SetActorLink:
		if m.EntityLink.RiderEntityUniqueID == uniqueID {
			l.emit("ride_link", logging.Fields{
				"ridden": m.EntityLink.RiddenEntityUniqueID, "type": m.EntityLink.Type,
				"ridden_kind": l.kinds[m.EntityLink.RiddenEntityUniqueID],
			})
		}
	}
}
