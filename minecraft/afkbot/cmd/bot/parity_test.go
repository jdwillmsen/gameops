package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// crewParityGolden is this bot's verified input, which crew's actuator is held
// to by its own test until this bot is deleted. It lives with crew because
// crew is what stays.
const crewParityGolden = "../../../crew/internal/act/testdata/afkbot_input.golden.json"

// parityView is what the server reads of an input for a bot that stands, sits
// or was just teleported. Tick, the move vectors and Delta are left out: they
// differ from crew's by design (see crew's afkbot_parity_test.go).
type parityView struct {
	Position               mgl32.Vec3
	Pitch, Yaw, HeadYaw    float32
	InteractPitch          float32
	InteractYaw            float32
	InteractionModel       int32
	InputMode              uint32
	Flags                  []string
	ClientPredictedVehicle *int64      `json:",omitempty"`
	VehicleRotation        *mgl32.Vec2 `json:",omitempty"`
}

// parityFlagNames must match crew's; any other set flag shows up by number
// rather than going unseen.
var parityFlagNames = map[int]string{
	packet.InputFlagBlockBreakingDelayEnabled: "BlockBreakingDelayEnabled",
	packet.InputFlagVerticalCollision:         "VerticalCollision",
	packet.InputFlagClientPredictedVehicle:    "ClientPredictedVehicle",
	packet.InputFlagHandledTeleport:           "HandledTeleport",
	packet.InputFlagHorizontalCollision:       "HorizontalCollision",
	packet.InputFlagUp:                        "Up",
	packet.InputFlagJumping:                   "Jumping",
	packet.InputFlagSprinting:                 "Sprinting",
	packet.InputFlagSneaking:                  "Sneaking",
}

func viewOf(in *packet.PlayerAuthInput) parityView {
	v := parityView{
		Position: in.Position, Pitch: in.Pitch, Yaw: in.Yaw, HeadYaw: in.HeadYaw,
		InteractPitch: in.InteractPitch, InteractYaw: in.InteractYaw,
		InteractionModel: in.InteractionModel, InputMode: in.InputMode,
		Flags: []string{},
	}
	for i := 0; i < packet.InputFlagCount; i++ {
		if !in.InputData.Load(i) {
			continue
		}
		name, ok := parityFlagNames[i]
		if !ok {
			name = fmt.Sprintf("flag_%d", i)
		}
		v.Flags = append(v.Flags, name)
	}
	if id, ok := in.ClientPredictedVehicle.Value(); ok {
		v.ClientPredictedVehicle = &id
	}
	if r, ok := in.VehicleRotation.Value(); ok {
		v.VehicleRotation = &r
	}
	return v
}

// firstReport is the input holdFacing sends at once for p.
func firstReport(t *testing.T, p *pose, yaw float32) *packet.PlayerAuthInput {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := recorder{got: make(chan *packet.PlayerAuthInput, 4)}
	go holdFacing(ctx, r, p, yaw, time.Hour, newTickCounter())
	select {
	case pk := <-r.got:
		return pk
	case <-time.After(2 * time.Second):
		t.Fatal("no report sent")
		return nil
	}
}

func TestHoldFacingMatchesTheCrewGolden(t *testing.T) {
	const (
		runtimeID, uniqueID = 7, -7
		yaw, pitch          = 90, 10
		vehicle             = 42
	)
	// Crew's pose is the feet and the packet carries the eye, so the shared
	// numbers are feet and this bot is told the eye, as the server tells it.
	eye := mgl32.Vec3{0, 1.62, 0}
	feet, teleport := mgl32.Vec3{10.5, 64, -3.5}, mgl32.Vec3{20.5, 64, 5.5}
	got := map[string]parityView{}

	p := &pose{}
	p.track(&packet.MovePlayer{EntityRuntimeID: runtimeID, Position: feet.Add(eye), Pitch: pitch, OnGround: true}, runtimeID, uniqueID)
	got["standing"] = viewOf(firstReport(t, p, yaw))

	p = &pose{}
	p.track(&packet.MovePlayer{EntityRuntimeID: runtimeID, Position: feet.Add(eye), Pitch: pitch, OnGround: true}, runtimeID, uniqueID)
	p.track(&packet.SetActorLink{EntityLink: protocol.EntityLink{RiddenEntityUniqueID: vehicle, RiderEntityUniqueID: uniqueID, Type: protocol.EntityLinkRider}}, runtimeID, uniqueID)
	got["seated"] = viewOf(firstReport(t, p, yaw))

	p = &pose{}
	p.track(&packet.MovePlayer{EntityRuntimeID: runtimeID, Position: feet.Add(eye), Pitch: pitch, OnGround: true}, runtimeID, uniqueID)
	p.track(&packet.MovePlayer{EntityRuntimeID: runtimeID, Position: teleport.Add(eye), Pitch: pitch, Mode: packet.MoveModeTeleport, OnGround: true}, runtimeID, uniqueID)
	got["teleported"] = viewOf(firstReport(t, p, yaw))

	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(crewParityGolden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(want)) {
		t.Errorf("holdFacing differs from the golden.\ngot:\n%s\nwant:\n%s", b, want)
	}
}
