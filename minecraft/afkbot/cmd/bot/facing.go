package main

import (
	"context"
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

// holdFacing keeps the bot looking along yaw by reporting its own position
// with that rotation. The position is the one the server gave at spawn, so
// the report never asks the server to move the bot.
func holdFacing(ctx context.Context, w packetWriter, pos mgl32.Vec3, yaw float32, tick func() uint64) {
	send := func() {
		_ = w.WritePacket(&packet.PlayerAuthInput{
			Position:  pos,
			Yaw:       yaw,
			HeadYaw:   yaw,
			Tick:      tick(),
			InputData: protocol.NewInputFlags(packet.InputFlagCount),
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
