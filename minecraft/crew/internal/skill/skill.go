// Package skill holds the things a crew unit can be asked to do: walk, mine
// and fill a chest. A skill is a state machine that a runner steps once per
// tick; every step that reports Running has sent exactly one input, because
// the server expects one per tick while the unit is active.
package skill

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type Status int

const (
	Running Status = iota
	Done
	Failed
)

// Actuator is what skills use of *act.Actuator.
type Actuator interface {
	Pose() act.Pose
	Tick(act.Input) error
	// Ticks counts the inputs sent so far.
	Ticks() uint64
	SelectHotbar(slot int, item belief.Item) error
	Attack(target uint64, slot int, held belief.Item) error
	UseItem(slot int, held belief.Item) error
	ReleaseItem(slot int, held belief.Item) error
	OpenChest(pos protocol.BlockPos, face int32, hotbar int, held belief.Item, blockRuntimeID uint32) error
	Container() (open bool, windowID byte, pos protocol.BlockPos)
	ContainerContent() []protocol.ItemInstance
	ItemTable() map[string]int32
	Deposit(from belief.Item, count int, toSlot byte) (int32, error)
	Withdraw(fromSlot byte, count int, to belief.Item, toSlot int) (int32, error)
	Response(requestID int32) (status uint8, done bool)
	CloseContainer() error
}

type Skill interface {
	Name() string
	// Ready says whether the skill can start from this belief, with the reason
	// it cannot.
	Ready(belief.Snapshot) error
	// Step advances by one tick. A non-nil error comes with Failed.
	Step(ctx context.Context, a Actuator) (Status, error)
}

// Stopper is implemented by skills that leave server-side state behind when
// they are abandoned mid-run: an open chest window or a live break session.
// It must be called from the goroutine that steps the skill.
type Stopper interface {
	Stop(Actuator)
}

// Resumer is implemented by skills that can pick up again after the unit left
// them to fight. The runner stops the skill first when it is a Stopper, then
// calls Resume instead of Ready: Ready judges a fresh start, and a skill part
// way through no longer meets it. Resume forgets where in its steps the skill
// was and keeps what it has accomplished.
type Resumer interface {
	Resume()
}

// Progresser is implemented by skills that count their own work, for tasks
// whose progress is not one item's inventory count.
type Progresser interface {
	Progress() int
}

// Logger is the part of crew's logger skills write to.
type Logger interface {
	Info(event string, fields map[string]any)
}

// Option configures a skill.
type Option func(*options)

type options struct{ log Logger }

// WithLogger sets where a skill reports what it observed.
func WithLogger(l Logger) Option { return func(o *options) { o.log = l } }

var errOverworld = errors.New("only the overworld is supported")

const (
	eyeHeight = 1.62
	// A wait that long is a fault, not slowness: 5 s at 20 ticks per second.
	waitTicks = 100
)

func overworld(s belief.Snapshot) error {
	if s.Dimension != 0 {
		return errOverworld
	}
	return nil
}

// count totals the stacks of an item.
func count(s belief.Snapshot, item string) int {
	n := 0
	for _, it := range s.Inventory {
		if it.Name == item {
			n += it.Count
		}
	}
	return n
}

func cellOf(p mgl32.Vec3) protocol.BlockPos {
	// A unit standing on a floor sits at an exact integer height, which float
	// error can leave a hair below it.
	return protocol.BlockPos{int32(math.Floor(float64(p.X()))), int32(math.Floor(float64(p.Y()) + 0.01)), int32(math.Floor(float64(p.Z())))}
}

func centre(b protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(b.X()) + 0.5, float32(b.Y()) + 0.5, float32(b.Z()) + 0.5}
}

func eyeOf(feet mgl32.Vec3) mgl32.Vec3 { return feet.Add(mgl32.Vec3{0, eyeHeight, 0}) }

// standEye is the eye position of a unit standing in a feet cell.
func standEye(c protocol.BlockPos) mgl32.Vec3 {
	return eyeOf(mgl32.Vec3{float32(c.X()) + 0.5, float32(c.Y()), float32(c.Z()) + 0.5})
}

// lookAt gives the yaw and pitch, in degrees, that face a point from the eye.
func lookAt(eye, to mgl32.Vec3) (yaw, pitch float32) {
	d := to.Sub(eye)
	yaw = float32(math.Atan2(float64(-d.X()), float64(d.Z())) * 180 / math.Pi)
	pitch = float32(math.Atan2(float64(-d.Y()), math.Hypot(float64(d.X()), float64(d.Z()))) * 180 / math.Pi)
	return yaw, pitch
}

// faceToward picks the side of a block that faces a point.
func faceToward(b protocol.BlockPos, from mgl32.Vec3) int32 {
	d := from.Sub(centre(b))
	ax, ay, az := math.Abs(float64(d.X())), math.Abs(float64(d.Y())), math.Abs(float64(d.Z()))
	switch {
	case ay >= ax && ay >= az:
		if d.Y() > 0 {
			return 1
		}
		return 0
	case ax >= az:
		if d.X() > 0 {
			return 5
		}
		return 4
	}
	if d.Z() > 0 {
		return 3
	}
	return 2
}

func hasSuffix(name, tool string) bool { return name == tool || strings.HasSuffix(name, "_"+tool) }
