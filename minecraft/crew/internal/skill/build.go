package skill

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/blueprint"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// maxClears is how often one cell is cleared before it is given up: a
	// block that comes back each time is being put there by something else.
	maxClears = 3
	// bodyReach is how close, centre to centre, a cell must be to the unit's
	// feet column for the server to refuse a block placed in it.
	bodyReach  = 0.8
	bodyHeight = 1.8
	// stepOffApart keeps a unit that leaves a cell far enough from it that
	// arriving a little short of its spot still clears the cell.
	stepOffApart = 1.8
)

// BuildCell is one block of a stamp: its absolute cell, the block's name as
// the item table spells it, and the states the blueprint asks for.
type BuildCell struct {
	Pos    protocol.BlockPos
	Block  string
	States map[string]any
}

// BuildSpec is a stamp in the order it must be placed: solid blocks before
// the ones that attach to them.
type BuildSpec struct {
	Cells  []BuildCell
	DryRun bool
}

// BuildReport is what a stamp did, or with a dry run would do. It names
// blocks and counts only, never cells.
type BuildReport struct {
	DryRun         bool `json:"dry_run,omitempty"`
	Total          int  `json:"total"`
	Placed         int  `json:"placed"`
	AlreadyCorrect int  `json:"already_correct"`
	Cleared        int  `json:"cleared"`
	// ToPlace is the cells a dry run found still to place.
	ToPlace int `json:"to_place,omitempty"`
	// SkippedState counts, by block name, the cells whose states a click
	// cannot choose: the server derives them from where the unit stands and
	// looks, so placing one would give it the wrong facing.
	SkippedState map[string]int `json:"skipped_state,omitempty"`
	// SkippedItem counts, by block name, the cells whose block has no item
	// of the same name to place.
	SkippedItem map[string]int `json:"skipped_item,omitempty"`
}

// BuildReporter is implemented by skills that report a stamp, so the runner
// can pass the report on with the task.
type BuildReporter interface {
	Report() BuildReport
}

// StateWorld is a block store that can read a block's states back.
type StateWorld interface {
	State(protocol.BlockPos) (name string, props map[string]any, known bool)
}

// clickStates are the states a placement gets from the click alone: a block
// set on the top face of the one below it. Anything else (stair facing, door
// hinge, repeater direction) comes from the unit's look and cannot be asked
// for by item name.
var clickStates = map[string]any{"torch_facing_direction": "top", "pillar_axis": "y"}

type buildStage int

const (
	bsPlan buildStage = iota
	bsPick
	bsRead
	bsApproach
	bsBreak
	bsStepOff
	bsPlace
)

// Build stamps a list of blocks into the world. A cell already holding its
// block is left alone, a wrong block is broken first, and a block is counted
// only once the world shows it, so running a stamp twice builds it once.
type Build struct {
	world path.Blocks
	self  func() belief.Snapshot
	spec  BuildSpec
	log   Logger

	rep   BuildReport
	stage buildStage
	// queue holds the cells still to handle; a cell that cannot be reached
	// yet is deferred to the end once, when later blocks may support it.
	queue    []int
	deferred []int
	tried    map[int]bool

	cur         int
	nav         *navigator
	cb          cellBreak
	clears      int
	approached  bool
	skipAfter   bool
	place       Skill
	verifyState bool
}

// NewBuild stamps spec's cells.
func NewBuild(world path.Blocks, self func() belief.Snapshot, spec BuildSpec, opts ...Option) *Build {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Build{world: world, self: self, spec: spec, log: o.log, tried: map[int]bool{}, rep: BuildReport{DryRun: spec.DryRun}}
}

func (b *Build) Name() string { return "build_blueprint" }

func (b *Build) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	// The stamp's blocks come from the creative inventory; there is no kit.
	if !b.spec.DryRun && !s.Creative {
		return errors.New("the unit is not in creative mode")
	}
	return nil
}

// Progress is the cells the world shows right, placed now or found so.
func (b *Build) Progress() int { return b.rep.Placed + b.rep.AlreadyCorrect }

func (b *Build) Report() BuildReport { return b.rep }

func (b *Build) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return b.fail(a, err)
	}
	if err := overworld(b.self()); err != nil {
		return b.fail(a, err)
	}
	for {
		var o outcome
		switch b.stage {
		case bsPlan:
			o = b.planCells(a)
		case bsPick:
			o = b.pick()
		case bsRead:
			o = b.read()
		case bsApproach:
			o = b.approach(a)
		case bsBreak:
			o = b.breakCell(a)
		case bsStepOff:
			o = b.stepOff(a)
		case bsPlace:
			o = b.placeCell(a)
		}
		if !o.again {
			if o.st == Failed {
				b.Stop(a)
			}
			return o.st, o.err
		}
	}
}

func (b *Build) fail(a Actuator, err error) (Status, error) {
	b.Stop(a)
	return Failed, err
}

// Stop aborts a break in progress.
func (b *Build) Stop(a Actuator) {
	if b.cb.open {
		b.cb.open = false
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{Pos: b.cb.pos, Phase: act.BreakAbort}})
	}
}

// Resume goes back to reading the cell the unit was on, keeping the counts.
func (b *Build) Resume() {
	if b.stage != bsPlan && b.stage != bsPick {
		b.queue = append([]int{b.cur}, b.queue...)
	}
	if b.stage != bsPlan {
		b.stage = bsPick
	}
	b.nav, b.cb, b.place = nil, cellBreak{}, nil
}

// planCells sorts every cell into already right, to place, or one the unit
// will not place, before anything is touched.
func (b *Build) planCells(a Actuator) outcome {
	items := a.ItemTable()
	_, canRead := b.world.(StateWorld)
	for i, c := range b.spec.Cells {
		b.rep.Total++
		switch {
		case b.matches(c):
			b.rep.AlreadyCorrect++
		case !clickDerivable(c.States) || (len(c.States) > 0 && !canRead):
			bump(&b.rep.SkippedState, c.Block)
		case items[c.Block] == 0:
			bump(&b.rep.SkippedItem, c.Block)
		default:
			b.queue = append(b.queue, i)
		}
	}
	b.rep.ToPlace = len(b.queue)
	if b.spec.DryRun {
		return b.finish()
	}
	b.rep.ToPlace = 0
	b.stage = bsPick
	return next
}

func bump(m *map[string]int, k string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[k]++
}

// clickDerivable says every state the cell asks for is one the click gives.
func clickDerivable(states map[string]any) bool {
	for k, v := range states {
		if want, ok := clickStates[k]; !ok || want != v {
			return false
		}
	}
	return true
}

// matches says the world already shows the cell's block, with its states
// when it asks for any and the world can read them back.
func (b *Build) matches(c BuildCell) bool {
	if len(c.States) == 0 {
		n, known := b.world.Block(c.Pos)
		return known && n == c.Block
	}
	sw, ok := b.world.(StateWorld)
	if !ok {
		return false
	}
	n, props, known := sw.State(c.Pos)
	if !known || n != c.Block || props == nil {
		return false
	}
	for k, want := range c.States {
		if !sameState(props[k], want) {
			return false
		}
	}
	return true
}

// sameState compares a state read off the wire with one from a blueprint,
// whose integers and flags may not share a Go type.
func sameState(got, want any) bool {
	g, gi := stateInt(got)
	w, wi := stateInt(want)
	if gi && wi {
		return g == w
	}
	return got == want
}

func stateInt(v any) (int64, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case float64:
		return int64(n), n == math.Trunc(n)
	}
	return 0, false
}

func (b *Build) finish() outcome {
	if b.log != nil {
		b.log.Info("blueprint_stamped", map[string]any{
			"total": b.rep.Total, "placed": b.rep.Placed, "already_correct": b.rep.AlreadyCorrect,
			"cleared": b.rep.Cleared, "skipped_state": sum(b.rep.SkippedState), "skipped_item": sum(b.rep.SkippedItem),
			"dry_run": b.rep.DryRun,
		})
	}
	return outcome{st: Done}
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func (b *Build) pick() outcome {
	if len(b.queue) == 0 {
		b.queue, b.deferred = b.deferred, nil
	}
	if len(b.queue) == 0 {
		return b.finish()
	}
	b.cur, b.queue = b.queue[0], b.queue[1:]
	b.nav, b.cb, b.place = nil, cellBreak{}, nil
	b.clears, b.approached, b.skipAfter, b.verifyState = 0, false, false, false
	b.stage = bsRead
	return next
}

func (b *Build) cell() BuildCell { return b.spec.Cells[b.cur] }

// giveUp puts a cell that cannot be done now at the end of the queue, once,
// then fails the stamp on its second refusal.
func (b *Build) giveUp(err error) outcome {
	if b.tried[b.cur] {
		return outcome{st: Failed, err: fmt.Errorf("placing %s: %w", b.cell().Block, err)}
	}
	b.tried[b.cur] = true
	b.deferred = append(b.deferred, b.cur)
	b.stage = bsPick
	return next
}

func (b *Build) read() outcome {
	c := b.cell()
	name, known := b.world.Block(c.Pos)
	switch {
	case known && b.matches(c):
		b.rep.AlreadyCorrect++
		b.stage = bsPick
	case known && (name == "air" || name == "water" || name == "flowing_water"):
		b.stage = bsStepOff
	case !b.approached:
		b.stage = bsApproach
	case !known:
		return outcome{st: Failed, err: fmt.Errorf("a cell for %s is in an unloaded chunk", c.Block)}
	default:
		b.cb = cellBreak{pos: c.Pos, name: name}
		b.stage = bsBreak
	}
	return next
}

// approach brings the eye within reach of the cell, so what is in it can be
// read for certain and cleared.
func (b *Build) approach(a Actuator) outcome {
	c := b.cell()
	pose := a.Pose()
	if centre(c.Pos).Sub(eyeOf(pose.Feet)).Len() <= placeReach-arriveSlack {
		b.approached, b.nav, b.stage = true, nil, bsRead
		return next
	}
	if b.nav == nil {
		stand, ok := standNear(b.world, c.Pos, pose, 0)
		if !ok {
			return b.giveUp(errors.New("no standing spot within reach"))
		}
		b.nav = newNavigator(b.world, stand, arriveSlack)
	}
	st, err := b.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		b.approached, b.nav, b.stage = true, nil, bsRead
		return next
	}
	return b.giveUp(fmt.Errorf("getting to the cell: %w", err))
}

func (b *Build) breakCell(a Actuator) outcome {
	done, err := driveBreak(b.world, &b.cb, a, a.Pose())
	if err != nil {
		return outcome{st: Failed, err: err}
	}
	if !done {
		return outcome{st: Running}
	}
	b.rep.Cleared++
	if b.skipAfter {
		bump(&b.rep.SkippedState, b.cell().Block)
		b.stage = bsPick
		return next
	}
	if b.clears++; b.clears > maxClears {
		return outcome{st: Failed, err: fmt.Errorf("a cell for %s keeps refilling", b.cell().Block)}
	}
	b.stage = bsRead
	return next
}

// stepOff moves the unit out of the cell it is about to fill: the server
// refuses a block placed into the placer's own body.
func (b *Build) stepOff(a Actuator) outcome {
	c := b.cell()
	pose := a.Pose()
	if !bodyIn(pose.Feet, c.Pos) {
		b.nav, b.stage = nil, bsPlace
		return next
	}
	if b.nav == nil {
		stand, ok := standNear(b.world, c.Pos, pose, stepOffApart)
		if !ok {
			return b.giveUp(errors.New("no standing spot beside the cell"))
		}
		b.nav = newNavigator(b.world, stand, arriveSlack)
	}
	st, err := b.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		b.nav, b.stage = nil, bsPlace
		return next
	}
	return b.giveUp(fmt.Errorf("stepping off the cell: %w", err))
}

func bodyIn(feet mgl32.Vec3, at protocol.BlockPos) bool {
	return math.Abs(float64(feet.X())-float64(at.X())-0.5) < bodyReach &&
		math.Abs(float64(feet.Z())-float64(at.Z())-0.5) < bodyReach &&
		float64(feet.Y()) < float64(at.Y())+1 && float64(feet.Y())+bodyHeight > float64(at.Y())
}

func (b *Build) placeCell(a Actuator) outcome {
	c := b.cell()
	if b.place == nil {
		b.place = NewPlaceBlock(b.world, b.self, c.Pos, c.Block, WithLogger(b.log))
	}
	st, err := b.place.Step(context.Background(), a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Failed:
		if errors.Is(err, act.ErrNoSupport) {
			return b.giveUp(err)
		}
		return outcome{st: Failed, err: fmt.Errorf("placing %s: %w", c.Block, err)}
	}
	// The name matched; the states the click was meant to give are read
	// back too, and a block that came out otherwise is taken away again
	// rather than left standing wrong.
	if len(c.States) > 0 && !b.matches(c) {
		b.skipAfter, b.approached = true, true
		b.cb = cellBreak{pos: c.Pos, name: c.Block}
		b.stage = bsBreak
		return next
	}
	b.rep.Placed++
	b.stage = bsPick
	return next
}

// BlueprintCells places a blueprint's blocks in the world: rotated, with min
// the corner of the footprint, in the order they must be placed.
func BlueprintCells(bp *blueprint.Blueprint, min protocol.BlockPos, rotation int) []BuildCell {
	cells := bp.Cells(rotation)
	out := make([]BuildCell, len(cells))
	for i, c := range cells {
		out[i] = BuildCell{
			Pos:    protocol.BlockPos{min.X() + int32(c.Pos.X), min.Y() + int32(c.Pos.Y), min.Z() + int32(c.Pos.Z)},
			Block:  c.Block.Name,
			States: c.Block.States,
		}
	}
	return out
}
