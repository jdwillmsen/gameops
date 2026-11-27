package act

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// afkbotGolden is afkbot 1.14.5's input, verified live on FWB, for the three
// states idle mode must reproduce. afkbot's own test writes the same view of
// its holdFacing into it, so the two are held to one file until afkbot is
// deleted.
const afkbotGolden = "testdata/afkbot_input.golden.json"

// parityView is what the server reads of an input for a unit that stands,
// sits or was just teleported. Left out because they differ by design: Tick
// (each client numbers its own inputs, and the server only needs them to
// rise), the move vectors (afkbot never walks; crew sends its walk input) and
// Delta (crew sends its model's gravity step for a standing unit, afkbot
// sends none).
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

// parityFlagNames names the flags either client sets in these states; any
// other set flag shows up by number rather than going unseen.
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

// The shared state: afkbot's test uses the same numbers.
var (
	parityFeet     = mgl32.Vec3{10.5, 64, -3.5}
	parityTeleport = mgl32.Vec3{20.5, 64, 5.5}
)

const (
	parityYaw     = 90
	parityPitch   = 10
	parityVehicle = 42
)

func TestInputMatchesAfkbot(t *testing.T) {
	ground := world{floor: 63}
	got := map[string]parityView{}

	a, s := newAct(ground, Pose{Feet: parityFeet, Pitch: parityPitch})
	_ = a.Tick(Input{Yaw: parityYaw, Pitch: parityPitch})
	got["standing"] = viewOf(s.inputs(t)[0])

	a, s = newAct(ground, Pose{Feet: parityFeet, Pitch: parityPitch})
	a.SetVehicle(func() int64 { return parityVehicle })
	_ = a.Tick(Input{Yaw: parityYaw, Pitch: parityPitch})
	got["seated"] = viewOf(s.inputs(t)[0])

	a, s = newAct(ground, Pose{Feet: parityFeet, Pitch: parityPitch})
	a.Apply(&packet.MovePlayer{
		EntityRuntimeID: 7, Position: parityTeleport.Add(mgl32.Vec3{0, eyeHeight, 0}),
		Pitch: parityPitch, Mode: packet.MoveModeTeleport, OnGround: true,
	})
	_ = a.Tick(Input{Yaw: parityYaw, Pitch: parityPitch})
	got["teleported"] = viewOf(s.inputs(t)[0])

	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(afkbotGolden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(want)) {
		t.Errorf("crew's input differs from afkbot's.\ngot:\n%s\nwant:\n%s", b, want)
	}
}
