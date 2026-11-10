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
	p := &pose{}
	p.set(pos, 12)
	go holdFacing(ctx, r, p, 90, func() uint64 { return 7 })

	select {
	case pk := <-r.got:
		if pk.Yaw != 90 || pk.HeadYaw != 90 || pk.Position != pos || pk.Pitch != 12 || pk.Tick != 7 {
			t.Fatalf("packet = %+v", pk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no packet sent at start")
	}
}

func TestPoseTracksOnlyItsOwnPlayer(t *testing.T) {
	p := &pose{}
	p.track(&packet.MovePlayer{EntityRuntimeID: 2, Position: mgl32.Vec3{1, 2, 3}}, 1)
	if pos, _, _ := p.get(); pos != (mgl32.Vec3{}) {
		t.Fatalf("another player moved the pose: %v", pos)
	}
	p.track(&packet.MovePlayer{EntityRuntimeID: 1, Position: mgl32.Vec3{4, 5, 6}, Pitch: 3}, 1)
	p.track(&packet.Respawn{EntityRuntimeID: 1, State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{7, 8, 9}}, 1)
	if pos, _, _ := p.get(); pos != (mgl32.Vec3{7, 8, 9}) {
		t.Fatalf("respawn did not move the pose: %v", pos)
	}
}

func TestHoldFacingSendsNothingWhileDead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := recorder{got: make(chan *packet.PlayerAuthInput, 4)}
	p := &pose{}
	p.setPaused(true)
	go holdFacing(ctx, r, p, 90, func() uint64 { return 0 })
	select {
	case <-r.got:
		t.Fatal("sent a position for a dead bot")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestPoseLogHoldsBackRepeats(t *testing.T) {
	l := newPoseLog(nil)
	l.last["movement_corrected"] = time.Now().Add(time.Hour)
	l.emit("movement_corrected", nil)
	l.emit("movement_corrected", nil)
	if l.held["movement_corrected"] != 2 {
		t.Fatalf("held = %d, want 2", l.held["movement_corrected"])
	}
}
