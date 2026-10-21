// Package act turns skill intent into the packets the server accepts as
// actions, and keeps the unit's predicted pose in step with what it sent. It
// is the only writer of action packets.
package act

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// metricCorrections counts the times the server overruled the predicted pose.
// A model that matches the server's keeps it near zero; a rise after a
// movement change means the prediction has drifted from the server's.
var metricCorrections = promauto.NewCounter(prometheus.CounterOpts{
	Name: "crew_movement_corrections_total",
	Help: "Server corrections of the unit's predicted movement.",
})

// firstTick is arbitrary: the server only needs ticks to rise.
const firstTick = 100

// sentHistory covers a correction's round trip: the server names the corrected
// tick, and by then later inputs have already been sent.
const sentHistory = 64

// sentTick is what one PlayerAuthInput claimed, kept so a correction is
// compared with the tick it names rather than the newest one.
type sentTick struct {
	tick   uint64
	pose   Pose
	stepUp bool
	skill  string
}

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
	mu                    sync.Mutex
	w                     PacketWriter
	runtimeID             uint64
	solid                 Solid
	pose                  Pose
	tick                  uint64
	lastStepUp            bool
	skill                 string
	correctionLogger      CorrectionLogger
	blockNames            BlockNames
	lastCorrectionLog     time.Time
	suppressedCorrections uint64
	now                   func() time.Time
	sent                  [sentHistory]sentTick

	itemIDs map[string]int32
	// inv is the unit's own window-0 inventory, kept from the server's slot
	// updates so a placement can find the item to hold.
	inv         [inventorySlots]protocol.ItemInstance
	move        pendingMove
	cont        container
	nextRequest int32
	responses   map[int32]uint8
}

type CorrectionLogger interface {
	Info(event string, fields map[string]any)
}

type BlockNames interface {
	Block(protocol.BlockPos) (string, bool)
}

// New starts the unit at pose. Collision queries go to solid.
func New(w PacketWriter, runtimeID uint64, start Pose, solid Solid) *Actuator {
	return &Actuator{
		w: w, runtimeID: runtimeID, solid: solid, pose: start,
		tick: firstTick, nextRequest: -3, responses: map[int32]uint8{}, now: time.Now,
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

func (a *Actuator) SetCorrectionDiagnostics(log CorrectionLogger, blocks BlockNames) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.correctionLogger, a.blockNames = log, blocks
}

func (a *Actuator) SetSkill(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.skill = name
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
	if step.swimUp {
		flags.Set(packet.InputFlagJumping)
		flags.Set(packet.InputFlagJumpDown)
		flags.Set(packet.InputFlagWantUp)
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
	a.lastStepUp = step.stepUp
	a.sent[a.tick%sentHistory] = sentTick{tick: a.tick, pose: next, stepUp: step.stepUp, skill: a.skill}
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
		metricCorrections.Inc()
		a.logCorrection(p)
		a.lastStepUp = false
		a.pose = Pose{
			Feet:     p.Position.Sub(mgl32.Vec3{0, eyeHeight, 0}),
			Pitch:    p.Rotation.X(),
			Yaw:      p.Rotation.Y(),
			OnGround: p.OnGround,
			// Taking the horizontal delta as the carry resumes walking
			// without a second correction.
			Carry: mgl32.Vec2{p.Delta.X(), p.Delta.Z()},
		}
		switch {
		case p.OnGround:
		case WaterDepth(a.solid, a.pose.Feet) > 0:
			// The swimming model already folds drag and sinking into a step.
			a.pose.vy = p.Delta.Y()
		default:
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

func (a *Actuator) logCorrection(p *packet.CorrectPlayerMovePrediction) {
	if a.correctionLogger == nil {
		return
	}
	now := a.now()
	if !a.lastCorrectionLog.IsZero() && now.Sub(a.lastCorrectionLog) < 10*time.Second {
		a.suppressedCorrections++
		return
	}
	sent, found := a.sent[p.Tick%sentHistory], true
	if sent.tick != p.Tick || p.Tick < firstTick {
		sent, found = sentTick{pose: a.pose, stepUp: a.lastStepUp, skill: a.skill}, false
	}
	predicted := sent.pose.Feet
	corrected := p.Position.Sub(mgl32.Vec3{0, eyeHeight, 0})
	delta := corrected.Sub(predicted)
	axis, component := "x", delta[0]
	for i, name := range []string{"y", "z"} {
		if math.Abs(float64(delta[i+1])) > math.Abs(float64(component)) {
			axis, component = name, delta[i+1]
		}
	}
	sign := "+"
	if component < 0 {
		sign = "-"
	}
	feet := blockPos(predicted)
	below := protocol.BlockPos{feet[0], int32(math.Floor(float64(predicted[1]) - 0.01)), feet[2]}
	dir := forwardDir(sent.pose.Yaw)
	ahead := blockPos(mgl32.Vec3{predicted[0] + dir[0], predicted[1], predicted[2] + dir[1]})
	fields := map[string]any{
		"skill": skillOrIdle(sent.skill), "on_ground": sent.pose.OnGround,
		"step": sent.stepUp, "vy": round2(sent.pose.vy), "tick_found": found,
		"dist": round2(float32(math.Sqrt(float64(delta.Dot(delta))))),
		"axis": axis, "axis_sign": sign,
		"below": a.blockName(below), "feet": a.blockName(feet), "ahead": a.blockName(ahead),
		"suppressed": a.suppressedCorrections,
	}
	a.correctionLogger.Info("movement_corrected", fields)
	a.lastCorrectionLog = now
	a.suppressedCorrections = 0
}

func (a *Actuator) blockName(pos protocol.BlockPos) string {
	if a.blockNames == nil {
		return "unknown"
	}
	name, ok := a.blockNames.Block(pos)
	if !ok || name == "" {
		return "unknown"
	}
	return strings.TrimPrefix(name, "minecraft:")
}

func skillOrIdle(name string) string {
	if name == "" {
		return "idle"
	}
	return name
}

func round2(v float32) float64 { return math.Round(float64(v)*100) / 100 }

func blockPos(v mgl32.Vec3) protocol.BlockPos {
	return protocol.BlockPos{int32(math.Floor(float64(v[0]))), int32(math.Floor(float64(v[1]))), int32(math.Floor(float64(v[2])))}
}
