package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type recorder struct{ got chan *packet.PlayerAuthInput }

func (r recorder) WritePacket(p packet.Packet) error {
	r.got <- p.(*packet.PlayerAuthInput)
	return nil
}

func TestHoldFacingReportsSpawnPositionWithYaw(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := recorder{got: make(chan *packet.PlayerAuthInput, 4)}
	pos := mgl32.Vec3{127.5, 71.6, 229.5}
	go holdFacing(ctx, r, pos, 90, func() uint64 { return 7 })

	select {
	case pk := <-r.got:
		if pk.Yaw != 90 || pk.HeadYaw != 90 || pk.Position != pos || pk.Tick != 7 {
			t.Fatalf("packet = %+v", pk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no packet sent at start")
	}
}
