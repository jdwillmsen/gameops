package main

import (
	"context"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
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

func (p *pose) get() (pos mgl32.Vec3, pitch float32, paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos, p.pitch, p.paused
}

// track follows the server's placement of the bot from the packets that move
// it.
func (p *pose) track(pk packet.Packet, runtimeID uint64) {
	switch m := pk.(type) {
	case *packet.MovePlayer:
		if m.EntityRuntimeID == runtimeID {
			p.set(m.Position, m.Pitch)
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
		pos, pitch, paused := p.get()
		if paused {
			return
		}
		_ = w.WritePacket(&packet.PlayerAuthInput{
			Position:         pos,
			Pitch:            pitch,
			Yaw:              yaw,
			HeadYaw:          yaw,
			InteractPitch:    pitch,
			InteractYaw:      yaw,
			InputMode:        packet.InputModeMouse,
			InteractionModel: packet.InteractionModelCrosshair,
			Tick:             tick(),
			InputData:        protocol.NewInputFlags(packet.InputFlagCount),
		})
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
