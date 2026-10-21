package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	defaultRadius = 24
	mineReach     = 4.5
	// The unit counts as arrived within this distance of a stand cell, so a
	// cell is only a candidate if the block is in reach with that much slack.
	arriveSlack = 0.75
	// A healthy server finishes a wooden-pickaxe stone break in 23 ticks. The
	// cap is far above that because a loaded server runs its ticks slower than
	// the unit sends inputs, and a break that is only late must not fail.
	breakTicks = 200
	// Least wait for a drop after the block went; the time the break itself
	// took is added, as it measures how slowly this server is running.
	pickupTicks  = 40
	maxMisses    = 3
	maxWalkFails = 12
	maxCandidate = 64
	// A drop is collected within 1.5 blocks; stay well inside it.
	pickupStand = 0.4
	hotbarSlots = 9
)

type minePhase int

const (
	mFind minePhase = iota
	mWalk
	mBreak
	mPickup
)

// Mine collects an item by breaking the nearest reachable block of a kind.
type Mine struct {
	world  path.Blocks
	self   func() belief.Snapshot
	block  string
	item   string
	tool   string
	qty    int
	radius int
	area   *Area

	started  bool
	baseline int
	have     int
	phase    minePhase

	target    protocol.BlockPos
	skip      map[protocol.BlockPos]bool
	walkFails int
	nav       *navigator

	// hold keeps the unit where it stands: only blocks already in reach are
	// broken, drops are left, and progress is not judged. Fell uses it from a
	// pillar, where walking to a drop would mean leaving.
	hold   bool
	felled []protocol.BlockPos
	// Blocks broken from the ground, whose drops may still be on their way.
	broken int

	selected     bool
	breakStarted bool
	// A Start is outstanding: the server is still timing a break that has not
	// finished. Once the block is gone there is nothing left to abort.
	sessionOpen bool
	waited      int
	// Inputs the last finished break took.
	lastBreak int
	misses    int
	pickInit  bool
}

// Area is an inclusive box of blocks.
type Area struct{ Min, Max protocol.BlockPos }

func (a Area) contains(b protocol.BlockPos) bool {
	return b.X() >= a.Min.X() && b.X() <= a.Max.X() &&
		b.Y() >= a.Min.Y() && b.Y() <= a.Max.Y() &&
		b.Z() >= a.Min.Z() && b.Z() <= a.Max.Z()
}

// MineOption configures a Mine.
type MineOption func(*Mine)

// InArea limits the blocks the skill will break to those inside the box, so a
// misplaced unit cannot dig up anything outside the area it was given.
func InArea(a Area) MineOption { return func(m *Mine) { m.area = &a } }

// NewMineBlock breaks blocks until qty more of item are in the inventory.
// tool is a name suffix: "pickaxe" matches any "*_pickaxe". A radius of zero
// or less means 24.
func NewMineBlock(world path.Blocks, self func() belief.Snapshot, block, item, tool string, qty, radius int) Skill {
	return NewMineBlockWith(world, self, block, item, tool, qty, radius)
}

// NewMineBlockWith is NewMineBlock with options.
func NewMineBlockWith(world path.Blocks, self func() belief.Snapshot, block, item, tool string, qty, radius int, opts ...MineOption) Skill {
	if radius <= 0 {
		radius = defaultRadius
	}
	m := &Mine{world: world, self: self, block: block, item: item, tool: tool, qty: qty, radius: radius, skip: map[protocol.BlockPos]bool{}}
	for _, o := range opts {
		o(m)
	}
	return m
}

func (m *Mine) Name() string { return "mine_block" }

// toolStack finds a matching stack on the hotbar, the only place the server
// lets the unit select from.
func (m *Mine) toolStack(s belief.Snapshot) (belief.Item, bool) { return hotbarTool(s, m.tool) }

// Resume goes back to finding a block. Where the unit stands and what it holds
// may have changed, but the baseline, the blocks ruled out and the misses
// stay: they are what the skill has learned.
func (m *Mine) Resume() {
	m.phase, m.nav, m.target = mFind, nil, protocol.BlockPos{}
	m.breakStarted, m.sessionOpen, m.selected = false, false, false
	m.waited, m.pickInit = 0, false
}

func (m *Mine) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if m.tool == "" {
		return nil
	}
	if _, ok := m.toolStack(s); !ok {
		return fmt.Errorf("no %s in inventory", m.tool)
	}
	return nil
}

// outcome is what a phase decided: another phase to run in this same step
// because it sent no input, or the step's result.
type outcome struct {
	st    Status
	err   error
	again bool
}

var next = outcome{again: true}

func (m *Mine) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return m.fail(a, err)
	}
	snap := m.self()
	if err := overworld(snap); err != nil {
		return m.fail(a, err)
	}
	have := count(snap, m.item)
	if !m.started {
		m.started, m.baseline, m.have = true, have, have
	}
	if !m.hold && have-m.baseline >= m.qty {
		return Done, nil
	}

	for {
		p := a.Pose()
		var o outcome
		switch m.phase {
		case mFind:
			o = m.find(a, p)
		case mWalk:
			o = m.walk(a)
		case mBreak:
			o = m.breakStep(a, snap, p)
		case mPickup:
			o = m.pickupStep(a, have, p)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// noSourceError says no block of the kind can be reached from anywhere the
// unit can stand.
type noSourceError struct{ msg string }

func (e noSourceError) Error() string { return e.msg }

func (m *Mine) noSource() error {
	if m.area != nil {
		return noSourceError{fmt.Sprintf("no reachable %s in the area", m.block)}
	}
	return noSourceError{fmt.Sprintf("no reachable %s within %d blocks", m.block, m.radius)}
}

// fail ends the skill, aborting a break that is still in progress first. The
// abort rides one last input.
func (m *Mine) fail(a Actuator, err error) (Status, error) {
	if m.sessionOpen {
		m.sessionOpen = false
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{Pos: m.target, Phase: act.BreakAbort}})
	}
	return Failed, err
}

// candidates lists the blocks of the kind within the radius that the skill
// may break, nearest first, leaving out those it ruled out unless asked.
func (m *Mine) candidates(p act.Pose, withSkipped bool) []protocol.BlockPos {
	feet := cellOf(p.Feet)
	r := m.radius
	var cands []protocol.BlockPos
	for dx := -r; dx <= r; dx++ {
		for dy := -r; dy <= r; dy++ {
			for dz := -r; dz <= r; dz++ {
				if dx*dx+dy*dy+dz*dz > r*r {
					continue
				}
				b := protocol.BlockPos{feet.X() + int32(dx), feet.Y() + int32(dy), feet.Z() + int32(dz)}
				if name, known := m.world.Block(b); known && name == m.block && (withSkipped || !m.skip[b]) && (m.area == nil || m.area.contains(b)) {
					cands = append(cands, b)
				}
			}
		}
	}
	d2 := func(b protocol.BlockPos) float32 { return centre(b).Sub(p.Feet).LenSqr() }
	sort.SliceStable(cands, func(i, j int) bool { return d2(cands[i]) < d2(cands[j]) })
	return cands
}

// find picks the nearest block that can be broken from somewhere standable,
// and sets up the walk there if the unit is not already in reach.
func (m *Mine) find(a Actuator, p act.Pose) outcome {
	feet := cellOf(p.Feet)
	cands := m.candidates(p, m.hold)

	under := protocol.BlockPos{feet.X(), feet.Y() - 1, feet.Z()}
	for i, b := range cands {
		if i >= maxCandidate {
			break
		}
		// Breaking the floor the unit stands on drops it.
		if b == under {
			continue
		}
		if centre(b).Sub(eyeOf(p.Feet)).Len() <= mineReach {
			m.aim(b, mBreak)
			return next
		}
		if m.hold {
			continue
		}
		if stand, ok := m.standCell(b, p.Feet); ok {
			m.aim(b, mWalk)
			m.nav = newNavigator(m.world, stand, arriveSlack)
			return next
		}
	}
	return m.failed(a, m.noSource())
}

func (m *Mine) aim(b protocol.BlockPos, phase minePhase) {
	m.target, m.phase = b, phase
	m.breakStarted, m.waited, m.pickInit = false, 0, false
}

// Stop releases the server's break session when the skill is abandoned.
func (m *Mine) Stop(a Actuator) { _, _ = m.fail(a, nil) }

func (m *Mine) failed(a Actuator, err error) outcome {
	st, err := m.fail(a, err)
	return outcome{st: st, err: err}
}

func (m *Mine) walk(a Actuator) outcome {
	st, err := m.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return m.failed(a, te.err)
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		m.phase = mBreak
	default:
		// An unreachable block is not a failed skill; another may be reachable.
		m.skip[m.target] = true
		if m.walkFails++; m.walkFails >= maxWalkFails {
			return m.failed(a, m.noSource())
		}
		m.phase = mFind
	}
	return next
}

// standCell finds the standable cell nearest the unit from which a block is in
// reach, never the cell above the block itself.
func (m *Mine) standCell(b protocol.BlockPos, from mgl32.Vec3) (protocol.BlockPos, bool) {
	best, bestD, found := protocol.BlockPos{}, float32(math.MaxFloat32), false
	const span = 4
	for dx := int32(-span); dx <= span; dx++ {
		for dy := int32(-span); dy <= span; dy++ {
			for dz := int32(-span); dz <= span; dz++ {
				c := protocol.BlockPos{b.X() + dx, b.Y() + dy, b.Z() + dz}
				if c.X() == b.X() && c.Z() == b.Z() && c.Y()-1 == b.Y() {
					continue
				}
				if centre(b).Sub(standEye(c)).Len() > mineReach-arriveSlack || !path.Standable(m.world, c) {
					continue
				}
				if d := centre(c).Sub(from).LenSqr(); d < bestD {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}

func (m *Mine) breakStep(a Actuator, snap belief.Snapshot, p act.Pose) outcome {
	if name, known := m.world.Block(m.target); known && name != m.block {
		m.sessionOpen = false
		m.lastBreak = m.waited
		if m.hold {
			m.phase = mFind
			return next
		}
		m.broken++
		m.phase, m.waited, m.pickInit = mPickup, 0, false
		return next
	}
	yaw, pitch := lookAt(eyeOf(p.Feet), centre(m.target))
	in := act.Input{Yaw: yaw, Pitch: pitch}
	if !m.breakStarted {
		if m.tool != "" && !m.selected {
			tool, ok := m.toolStack(snap)
			if !ok {
				return m.failed(a, fmt.Errorf("no %s in inventory", m.tool))
			}
			if err := a.SelectHotbar(tool.Slot, tool); err != nil {
				return m.failed(a, err)
			}
			m.selected = true
		}
		m.breakStarted, m.sessionOpen = true, true
		if m.hold {
			// Noted at the start: a fight can end the skill's step after the
			// block has gone and before it sees so.
			m.felled = append(m.felled, m.target)
		}
		in.Break = &act.BreakAction{Pos: m.target, Face: faceToward(m.target, eyeOf(p.Feet)), Phase: act.BreakStart}
	} else if m.waited++; m.waited > breakTicks {
		return m.failed(a, errors.New("block did not break"))
	}
	if err := a.Tick(in); err != nil {
		return m.failed(a, err)
	}
	return outcome{st: Running}
}

// pickupStep waits for the inventory to show the drop. Progress is counted
// from the inventory because a break can succeed with nothing collected: a
// full inventory, a drop out of reach or the wrong tool.
func (m *Mine) pickupStep(a Actuator, have int, p act.Pose) outcome {
	if have > m.have {
		m.have, m.misses = have, 0
		if m.have-m.baseline >= m.qty {
			return outcome{st: Done}
		}
		m.phase = mFind
		return next
	}
	if m.waited >= pickupTicks+m.lastBreak {
		if m.misses++; m.misses >= maxMisses {
			return m.failed(a, errors.New("no pickup after 3 breaks"))
		}
		m.phase = mFind
		return next
	}
	if !m.pickInit {
		m.pickInit = true
		m.nav = nil
		// The drop is collected only within 1.5 blocks, and a block with
		// another above it cannot be stood in, so stand on the nearest
		// cell beside the drop instead.
		if cell, ok := m.pickupCell(p); ok {
			m.nav = newNavigator(m.world, cell, pickupStand)
		}
	}
	m.waited++
	if m.nav != nil {
		st, err := m.nav.step(a)
		var te tickError
		if errors.As(err, &te) {
			return m.failed(a, te.err)
		}
		if st == Running {
			return outcome{st: Running}
		}
		m.nav = nil
	}
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return m.failed(a, err)
	}
	return outcome{st: Running}
}

// maxDropFall bounds the search for where a drop lands; a mined area is a few
// blocks deep.
const maxDropFall = 8

// dropRest is the cell a drop from the target comes to rest in. It falls
// straight down through open cells, so a block mined over a hole drops its item
// to the bottom of the hole, not into the cell it left.
func (m *Mine) dropRest() protocol.BlockPos {
	c := m.target
	for i := 0; i < maxDropFall; i++ {
		below := protocol.BlockPos{c.X(), c.Y() - 1, c.Z()}
		if name, known := m.world.Block(below); !known || !path.Passable(name) {
			break
		}
		c = below
	}
	return c
}

// pickupCell finds the standable cell nearest the drop, within one block of
// it, or none if the unit is already close enough to collect it.
func (m *Mine) pickupCell(p act.Pose) (protocol.BlockPos, bool) {
	rest := m.dropRest()
	drop := centre(rest)
	near := func(c mgl32.Vec3) float64 {
		return math.Hypot(float64(drop.X()-c.X()), float64(drop.Z()-c.Z()))
	}
	if near(p.Feet) <= pickupStand+0.5 {
		return protocol.BlockPos{}, false
	}
	best, bestD, found := protocol.BlockPos{}, math.MaxFloat64, false
	for dx := int32(-1); dx <= 1; dx++ {
		for dy := int32(-1); dy <= 1; dy++ {
			for dz := int32(-1); dz <= 1; dz++ {
				c := protocol.BlockPos{rest.X() + dx, rest.Y() + dy, rest.Z() + dz}
				if d := near(centre(c)); d <= 1.0+1e-6 && d < bestD && path.Standable(m.world, c) {
					best, bestD, found = c, d, true
				}
			}
		}
	}
	return best, found
}
