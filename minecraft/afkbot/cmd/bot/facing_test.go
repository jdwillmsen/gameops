package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
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
	p.track(&packet.MovePlayer{EntityRuntimeID: 2, Position: mgl32.Vec3{1, 2, 3}}, 1, 0)
	if pos, _, _, _ := p.get(); pos != (mgl32.Vec3{}) {
		t.Fatalf("another player moved the pose: %v", pos)
	}
	p.track(&packet.MovePlayer{EntityRuntimeID: 1, Position: mgl32.Vec3{4, 5, 6}, Pitch: 3}, 1, 0)
	p.track(&packet.Respawn{EntityRuntimeID: 1, State: packet.RespawnStateReadyToSpawn, Position: mgl32.Vec3{7, 8, 9}}, 1, 0)
	if pos, _, _, _ := p.get(); pos != (mgl32.Vec3{7, 8, 9}) {
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

func TestSeatedBotNamesItsVehicleAndTurnsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := recorder{got: make(chan *packet.PlayerAuthInput, 4)}
	p := &pose{}
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiderEntityUniqueID: 5, RiddenEntityUniqueID: 9, Type: protocol.EntityLinkRider}}, 1, 5)
	go holdFacing(ctx, r, p, 96, func() uint64 { return 0 })
	pk := <-r.got
	veh, ok := pk.ClientPredictedVehicle.Value()
	rot, rok := pk.VehicleRotation.Value()
	if !ok || veh != 9 || !rok || rot[1] != 96 || !pk.InputData.Load(packet.InputFlagClientPredictedVehicle) {
		t.Fatalf("packet = %+v", pk)
	}
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiderEntityUniqueID: 5, RiddenEntityUniqueID: 9, Type: protocol.EntityLinkRemove}}, 1, 5)
	if _, _, _, v := p.get(); v != 0 {
		t.Fatalf("vehicle not cleared: %d", v)
	}
}

func TestStaleRemovalDoesNotUnseatTheBot(t *testing.T) {
	p := &pose{}
	link := func(ridden int64, typ byte) packet.Packet {
		return &packet.SetActorLink{EntityLink: protocol.EntityLink{RiderEntityUniqueID: 5, RiddenEntityUniqueID: ridden, Type: typ}}
	}
	p.track(link(9, protocol.EntityLinkRider), 1, 5)
	p.track(link(10, protocol.EntityLinkRider), 1, 5)
	p.track(link(9, protocol.EntityLinkRemove), 1, 5)
	if _, _, _, v := p.get(); v != 10 {
		t.Fatalf("vehicle = %d, want 10", v)
	}
	p.track(link(10, protocol.EntityLinkPassenger), 1, 5)
	if _, _, _, v := p.get(); v != 0 {
		t.Fatalf("a passenger seat set vehicle %d", v)
	}
}

func TestTeleportIsAcknowledgedOnceAndReportedAtOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := recorder{got: make(chan *packet.PlayerAuthInput, 8)}
	p := newPose()
	p.set(mgl32.Vec3{1, 2, 3}, 0)
	go holdFacing(ctx, r, p, 90, func() uint64 { return 1 })
	first := <-r.got
	if first.InputData.Load(packet.InputFlagHandledTeleport) {
		t.Fatal("acknowledged a teleport that never happened")
	}

	p.track(&packet.MovePlayer{EntityRuntimeID: 5, Position: mgl32.Vec3{9, 9, 9}, Mode: packet.MoveModeTeleport}, 5, 1)
	select {
	case pk := <-r.got:
		if !pk.InputData.Load(packet.InputFlagHandledTeleport) || pk.Position != (mgl32.Vec3{9, 9, 9}) {
			t.Fatalf("teleport not acknowledged at the new position: %+v", pk)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("teleport waited for the next interval")
	}
	if p.takeTeleportAck() {
		t.Fatal("acknowledgement not cleared after it was sent")
	}
}
