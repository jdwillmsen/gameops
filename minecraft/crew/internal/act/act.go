// Package act turns skill intent into the packets the server accepts as
// actions, and keeps the unit's predicted pose in step with what it sent. It
// is the only writer of action packets.
package act

import (
	"sync"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// firstTick is arbitrary: the server only needs ticks to rise.
const firstTick = 100

// PacketWriter is the connection's write half.
type PacketWriter interface{ WritePacket(packet.Packet) error }

// Input is one tick of intent.
type Input struct {
	// Forward is the walk input in [0, 1] along Yaw.
	Forward    float32
	Yaw, Pitch float32
	Jump       bool
	// Break rides this tick's input as a block action.
	Break *BreakAction
	// StartUsing marks this tick as the one where the unit begins using the
	// held item, as eating does. Send it once, after UseItem.
	StartUsing bool
}

// Actuator is safe to call from a skill goroutine while Apply runs on the
// read loop.
type Actuator struct {
	mu        sync.Mutex
	w         PacketWriter
	runtimeID uint64
	solid     Solid
	pose      Pose
	tick      uint64

	itemIDs     map[string]int32
	cont        container
	nextRequest int32
	responses   map[int32]uint8
}

// New starts the unit at pose. Collision queries go to solid.
func New(w PacketWriter, runtimeID uint64, start Pose, solid Solid) *Actuator {
	return &Actuator{
		w: w, runtimeID: runtimeID, solid: solid, pose: start,
		tick: firstTick, nextRequest: -3, responses: map[int32]uint8{},
	}
}

// EndLoadingScreen must be sent once per session, after spawn and the chunk
// radius request and before the first Tick. Until then the server holds the
// unit under a loading-screen ability layer and drops every action without
// an error.
func (a *Actuator) EndLoadingScreen() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.w.WritePacket(&packet.ServerBoundLoadingScreen{Type: packet.LoadingScreenTypeEnd})
}

// Pose is the predicted pose.
func (a *Actuator) Pose() Pose {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pose
}

// Ticks counts the inputs sent so far, so the runner can tell whether a step
// already sent its input.
func (a *Actuator) Ticks() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tick - firstTick
}

// Tick sends exactly one PlayerAuthInput and advances the pose by it. Call it
// every 50 ms while the unit is spawned, with or without a skill; an idle tick
// still has to be sent so the server sees the unit's leftover momentum.
func (a *Actuator) Tick(in Input) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	p := a.pose
	p.Yaw, p.Pitch = in.Yaw, in.Pitch
	next, step := p.advance(in.Forward, in.Jump, a.solid)

	flags := protocol.NewInputFlags(packet.InputFlagCount)
	flags.Set(packet.InputFlagBlockBreakingDelayEnabled)
	switch {
	case in.Forward > 0:
		flags.Set(packet.InputFlagUp)
	case in.Forward < 0:
		flags.Set(packet.InputFlagDown)
	}
	if step.takeoff {
		flags.Set(packet.InputFlagJumping)
		flags.Set(packet.InputFlagStartJumping)
		flags.Set(packet.InputFlagJumpDown)
	}
	if step.airborne {
		flags.Set(packet.InputFlagJumping)
	}
	if next.OnGround {
		flags.Set(packet.InputFlagVerticalCollision)
	}

	pk := &packet.PlayerAuthInput{
		Pitch:              in.Pitch,
		Yaw:                in.Yaw,
		Position:           next.Feet.Add(mgl32.Vec3{0, eyeHeight, 0}),
		MoveVector:         mgl32.Vec2{0, in.Forward},
		HeadYaw:            in.Yaw,
		InputData:          flags,
		InputMode:          packet.InputModeMouse,
		PlayMode:           packet.PlayModeNormal,
		InteractionModel:   packet.InteractionModelClassic,
		InteractPitch:      in.Pitch,
		InteractYaw:        in.Yaw,
		Tick:               a.tick,
		Delta:              step.delta,
		AnalogueMoveVector: mgl32.Vec2{0, in.Forward},
		RawMoveVector:      mgl32.Vec2{0, in.Forward},
	}
	if in.StartUsing {
		pk.InputData.Set(packet.InputFlagStartUsingItem)
	}
	if in.Break != nil {
		pk.InputData.Set(packet.InputFlagPerformBlockActions)
		pk.BlockActions = protocol.Option([]protocol.PlayerBlockAction{in.Break.action()})
	}
	if err := a.w.WritePacket(pk); err != nil {
		return err
	}
	a.pose = next
	a.tick++
	return nil
}

// Apply feeds server packets in. It adopts the server's position when it
// corrects or teleports the unit, takes the server's knockback as velocity,
// and tracks the container and item-request replies Deposit depends on.
func (a *Actuator) Apply(pk packet.Packet) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch p := pk.(type) {
	case *packet.CorrectPlayerMovePrediction:
		a.pose = Pose{
			Feet:     p.Position.Sub(mgl32.Vec3{0, eyeHeight, 0}),
			Pitch:    p.Rotation.X(),
			Yaw:      p.Rotation.Y(),
			OnGround: p.OnGround,
			// Taking the horizontal delta as the carry resumes walking
			// without a second correction.
			Carry: mgl32.Vec2{p.Delta.X(), p.Delta.Z()},
		}
		if !p.OnGround {
			a.pose.vy = (p.Delta.Y() - gravity) * verticalDrag
		}
	case *packet.SetActorMotion:
		if p.EntityRuntimeID == a.runtimeID {
			a.pose.knock(p.Velocity)
		}
	case *packet.MovePlayer:
		if p.EntityRuntimeID != a.runtimeID {
			return
		}
		a.pose = Pose{
			Feet:     p.Position.Sub(mgl32.Vec3{0, eyeHeight, 0}),
			Pitch:    p.Pitch,
			Yaw:      p.Yaw,
			OnGround: p.OnGround,
		}
	default:
		a.applyContainerLocked(pk)
	}
}
