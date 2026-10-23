package skill

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// Least wait after breaking under gravel or sand: the block is already
	// falling, in neither cell, for a few ticks before it lands.
	settleTicks = 8
	// A column that has not come down by then is not going to; the next look
	// at the cell decides what to do.
	settleCap = 100
	// Breaks of one cell before a column that keeps refilling is given up.
	maxCellBreaks = 12
	// Taller than any column of falling blocks the unit will reason about.
	maxFallingChain = 16
	// Cobblestone a unit keeps back from its deposit, and tops up from the
	// chest, to bridge a missing floor with.
	bridgeReserve = 16
	// How long a unit waits, at the start, for its kit to be issued and the
	// ground at the entrance to load: both lag a join or a respawn by seconds.
	armWait = 600
)

type shaftPhase int

const (
	shEntrance shaftPhase = iota
	shSeg
	shDig
	shBreak
	shSettle
	shReach
	shWalk
	shReturn
	shDeposit
	shBridge
	shMend
	shStock
	shArm
	shClimb
)

// ShaftSpec is a stair shaft with branch tunnels off its bottom.
type ShaftSpec struct {
	// Entrance is the feet cell the shaft starts from, on open ground.
	Entrance protocol.BlockPos
	// Dir is the horizontal heading, one of the four axis unit vectors.
	Dir          [2]int32
	Depth        int
	Branches     int
	BranchLength int
	// Area bounds every cell the unit opens.
	Area  Area
	Chest protocol.BlockPos
}

// shaftStep opens some cells from the previous stand and walks to the next.
type shaftStep struct {
	from, stand protocol.BlockPos
	// cells are opened top first, so what falls from above lands in a cell
	// that is still to be dug rather than behind the unit.
	cells []protocol.BlockPos
}

func up(c protocol.BlockPos, n int32) protocol.BlockPos {
	return protocol.BlockPos{c.X(), c.Y() + n, c.Z()}
}

// stairSteps descends one block per block forward. Three cells are opened
// per step because the unit steps down through the column it is entering.
func stairSteps(s ShaftSpec) []shaftStep {
	var out []shaftStep
	prev := s.Entrance
	for i := 1; i <= s.Depth; i++ {
		f := protocol.BlockPos{s.Entrance.X() + int32(i)*s.Dir[0], s.Entrance.Y() - int32(i), s.Entrance.Z() + int32(i)*s.Dir[1]}
		out = append(out, shaftStep{from: prev, stand: f, cells: []protocol.BlockPos{up(f, 2), up(f, 1), f}})
		prev = f
	}
	return out
}

// tunnelSteps digs a two-high tunnel level with bottom, in a direction.
func tunnelSteps(bottom protocol.BlockPos, dir [2]int32, length int) []shaftStep {
	var out []shaftStep
	prev := bottom
	for t := 1; t <= length; t++ {
		f := protocol.BlockPos{bottom.X() + int32(t)*dir[0], bottom.Y(), bottom.Z() + int32(t)*dir[1]}
		out = append(out, shaftStep{from: prev, stand: f, cells: []protocol.BlockPos{up(f, 1), f}})
		prev = f
	}
	return out
}

// branchDirs lists the headings branches take off the bottom: ahead, left,
// then right.
func branchDirs(d [2]int32) [3][2]int32 {
	return [3][2]int32{d, {d[1], -d[0]}, {-d[1], d[0]}}
}

// validate checks the request and that the full plan lies inside the area.
func (s ShaftSpec) validate() error {
	switch s.Dir {
	case [2]int32{1, 0}, [2]int32{-1, 0}, [2]int32{0, 1}, [2]int32{0, -1}:
	default:
		return errors.New("the heading must be one axis direction")
	}
	if s.Depth < 1 {
		return errors.New("the shaft needs a depth of at least 1")
	}
	if s.Branches < 0 || s.Branches > 3 || (s.Branches > 0 && s.BranchLength < 1) {
		return errors.New("branches need 0 to 3 tunnels of at least 1 cell")
	}
	if !s.Area.contains(s.Entrance) {
		return errors.New("the entrance is outside the area")
	}
	steps := stairSteps(s)
	bottom := steps[len(steps)-1].stand
	for j := 0; j < s.Branches; j++ {
		steps = append(steps, tunnelSteps(bottom, branchDirs(s.Dir)[j], s.BranchLength)...)
	}
	for _, st := range steps {
		for _, c := range st.cells {
			if !s.Area.contains(c) {
				return errors.New("the plan reaches outside the area")
			}
		}
	}
	return nil
}

// MineShaft digs a stair shaft, tunnels branches off its bottom, climbs out
// and puts everything it gained into a chest.
type MineShaft struct {
	world ChestWorld
	self  func() belief.Snapshot
	spec  ShaftSpec
	log   Logger

	started bool
	base    map[string]int
	phase   shaftPhase

	seg    int
	steps  []shaftStep
	si, ci int
	// at is the last stand reached; the shaft's bottom once it is done.
	at         protocol.BlockPos
	bottom     protocol.BlockPos
	stepsDone  int
	dug        int
	tries      int
	reachTried bool
	settleNext bool
	waited     int
	calm       int
	selected   bool
	brk        cellBreak
	nav        *navigator

	deposits []Skill
	sub      Skill
	// bridgeAt is the floor cell being filled, and bridgeThen the phase to go
	// on with once it holds; mendThen is where the stair repair returns to.
	bridgeAt   protocol.BlockPos
	bridgeThen shaftPhase
	mendThen   shaftPhase
	mendTried  bool
	climbs     int
	climbThen  shaftPhase
	cl         *climber
}

// NewMineShaft refuses a plan that would open a cell outside the area.
func NewMineShaft(world ChestWorld, self func() belief.Snapshot, spec ShaftSpec, opts ...Option) (Skill, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &MineShaft{world: world, self: self, spec: spec, log: o.log, at: spec.Entrance, phase: shArm}, nil
}

func (m *MineShaft) Name() string { return "mine_shaft" }

// Progress counts the cells opened.
func (m *MineShaft) Progress() int { return m.dug }

func (m *MineShaft) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	return nil
}

func (m *MineShaft) emit(event string, fields map[string]any) {
	if m.log != nil {
		m.log.Info(event, fields)
	}
}

// Stop releases the server's break session when the skill is abandoned.
func (m *MineShaft) Stop(a Actuator) {
	if m.brk.open {
		m.brk.open = false
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{Pos: m.brk.pos, Phase: act.BreakAbort}})
		return
	}
	if st, ok := m.sub.(Stopper); ok {
		st.Stop(a)
	}
}

// Resume forgets where in a step the skill was and keeps what it did: the
// steps taken, the cells opened and the tries on the cell in hand.
func (m *MineShaft) Resume() {
	m.nav, m.brk, m.selected, m.waited, m.reachTried = nil, cellBreak{}, false, 0, false
	switch m.phase {
	case shBreak, shSettle, shReach:
		m.phase = shDig
	case shDeposit:
		if r, ok := m.sub.(Resumer); ok {
			r.Resume()
		}
	case shArm:
		m.waited = 0
	case shClimb:
		m.cl, m.phase = nil, m.climbThen
	case shStock:
		if r, ok := m.sub.(Resumer); ok {
			r.Resume()
		}
	case shBridge:
		// The placement is asked again from the top: it reads the world, so
		// a block that did land is not placed twice.
		m.sub, m.phase = nil, m.bridgeThen
	case shMend:
		m.sub = nil
	}
}

func (m *MineShaft) fail(a Actuator, err error) (Status, error) {
	m.Stop(a)
	return Failed, err
}

func (m *MineShaft) failed(a Actuator, err error) outcome {
	st, err := m.fail(a, err)
	return outcome{st: st, err: err}
}

func (m *MineShaft) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return m.fail(a, err)
	}
	snap := m.self()
	if err := overworld(snap); err != nil {
		return m.fail(a, err)
	}
	if !m.started {
		m.started, m.base = true, map[string]int{}
		for _, it := range snap.Inventory {
			m.base[it.Name] += it.Count
		}
	}
	for {
		var o outcome
		switch m.phase {
		case shArm:
			o = m.arm(a)
		case shClimb:
			o = m.climb(a)
		case shStock:
			o = m.stock(ctx, a)
		case shEntrance:
			o = m.walk(a, m.spec.Entrance, arriveSlack, func() { m.startSegment(0) }, "walking to the entrance")
		case shSeg:
			o = m.segment()
		case shDig:
			o = m.dig(a, snap)
		case shBreak:
			o = m.breakCell(a)
		case shSettle:
			o = m.settle(a)
		case shReach:
			o = m.walk(a, m.steps[m.si].from, pickupStand, func() { m.phase, m.reachTried = shDig, true }, "")
		case shWalk:
			o = m.walk(a, m.steps[m.si].stand, pickupStand, m.stepDone, "")
		case shReturn:
			o = m.walk(a, m.spec.Entrance, arriveSlack, m.startDeposits, "climbing out")
		case shDeposit:
			o = m.deposit(ctx, a)
		case shBridge:
			o = m.bridge(ctx, a)
		case shMend:
			o = m.mend()
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// walk runs the navigator to a cell. A walk that fails ends the heading it
// was for, except the two that have no heading to give up.
func (m *MineShaft) walk(a Actuator, to protocol.BlockPos, within float64, then func(), what string) outcome {
	if m.nav == nil {
		m.nav = newNavigator(m.world, to, within)
	}
	st, err := m.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return m.failed(a, te.err)
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		m.nav = nil
		then()
		return next
	}
	m.nav = nil
	if what != "" && !m.mendTried && m.firstVoid() >= 0 {
		// A stair with a missing floor is what cut the unit off from its goal;
		// mend it from the side the unit is on, then try again.
		m.mendTried, m.mendThen, m.phase = true, m.phase, shMend
		return next
	}
	if what != "" && m.climbs < 2 {
		// No walking path and no stair floor to mend: the unit is cut off from
		// the stair, as after a knockback into a cave beneath it. Build its way up.
		m.climbs++
		m.cl = &climber{world: m.world, self: m.self, goal: to}
		m.climbThen, m.phase = m.phase, shClimb
		return next
	}
	if what != "" {
		return m.failed(a, fmt.Errorf("%s: %w", what, err))
	}
	return m.abort("blocked")
}

func (m *MineShaft) startSegment(n int) {
	m.seg, m.si, m.ci, m.tries, m.reachTried = n, 0, 0, 0, false
	m.phase = shSeg
	switch {
	case n == 0:
		m.steps = stairSteps(m.spec)
	case n <= m.spec.Branches:
		// Back to the junction first, then out along the branch.
		m.steps = append([]shaftStep{{from: m.at, stand: m.bottom}}, tunnelSteps(m.bottom, branchDirs(m.spec.Dir)[n-1], m.spec.BranchLength)...)
	default:
		m.phase, m.mendThen = shMend, shReturn
	}
}

func (m *MineShaft) endSegment() {
	if m.seg == 0 {
		m.bottom = m.at
	}
	m.startSegment(m.seg + 1)
}

func (m *MineShaft) stepDone() {
	m.at = m.steps[m.si].stand
	if m.seg == 0 {
		m.stepsDone++
	}
	m.si, m.ci, m.tries, m.reachTried = m.si+1, 0, 0, false
	m.phase = shSeg
}

// abort rules out the rest of the heading the skill is on. The shaft failing
// at its first step is the task's failure; anything later leaves the unit
// with what it has dug and goes on to the next heading.
func (m *MineShaft) abort(kind string) outcome {
	heading := "branch"
	if m.seg == 0 {
		heading = "shaft"
	}
	m.emit("mine_hazard", map[string]any{"kind": kind, "heading": heading, "step": m.si, "opened": m.dug})
	m.nav, m.brk = nil, cellBreak{}
	if m.seg == 0 && m.stepsDone == 0 {
		return outcome{st: Failed, err: fmt.Errorf("the shaft is blocked at its first step: %s", kind)}
	}
	m.endSegment()
	return next
}

// segment checks a whole step before any of it is dug, so a hazard anywhere
// in it leaves every cell closed.
func (m *MineShaft) segment() outcome {
	if m.si >= len(m.steps) {
		m.endSegment()
		return next
	}
	st := m.steps[m.si]
	for _, c := range st.cells {
		if kind := m.hazard(c); kind != "" {
			return m.abort(kind)
		}
	}
	if len(st.cells) > 0 {
		if kind := m.floor(st.stand); kind == "void" {
			return m.startBridge(up(st.stand, -1), shSeg)
		} else if kind != "" {
			return m.abort(kind)
		}
		m.phase = shDig
		return next
	}
	m.phase = shWalk
	return next
}

// dig opens the cells of the step in turn.
func (m *MineShaft) dig(a Actuator, snap belief.Snapshot) outcome {
	st := m.steps[m.si]
	for m.ci < len(st.cells) {
		c := st.cells[m.ci]
		name, known := m.world.Block(c)
		if !known {
			return m.abort("unknown")
		}
		if path.Passable(name) {
			m.ci, m.tries = m.ci+1, 0
			continue
		}
		if kind := m.hazard(c); kind != "" {
			return m.abort(kind)
		}
		if m.tries >= maxCellBreaks {
			return m.abort("unstable")
		}
		p := a.Pose()
		feet := cellOf(p.Feet)
		below := c.X() == feet.X() && c.Z() == feet.Z() && c.Y() < feet.Y()
		if below || centre(c).Sub(eyeOf(p.Feet)).Len() > mineReach {
			if m.reachTried {
				return m.abort("unreachable")
			}
			m.phase = shReach
			return next
		}
		if !m.selected {
			tool, ok := hotbarTool(snap, "pickaxe")
			if !ok {
				return m.failed(a, errors.New("no pickaxe in inventory"))
			}
			if err := a.SelectHotbar(tool.Slot, tool); err != nil {
				return m.failed(a, err)
			}
			m.selected = true
		}
		m.tries++
		m.brk = cellBreak{pos: c, name: name}
		m.settleNext = isFalling(m.nameAt(up(c, 1)))
		m.phase = shBreak
		return next
	}
	for i, c := range st.cells {
		if name, known := m.world.Block(c); !known || !path.Passable(name) {
			m.ci = i
			return next
		}
	}
	m.phase = shWalk
	return next
}

func (m *MineShaft) breakCell(a Actuator) outcome {
	p := a.Pose()
	done, err := driveBreak(m.world, &m.brk, a, p)
	if err != nil {
		return m.failed(a, err)
	}
	if !done {
		return outcome{st: Running}
	}
	m.dug++
	m.waited, m.calm = 0, 0
	m.phase = shDig
	if m.settleNext {
		m.phase = shSettle
	}
	return next
}

// settle idles while what the break left unsupported falls, so the next look
// at the cell sees it. The server may sit on the fall for a while, and once it
// lets go the block is in the air with nothing above the cell, so the minimum
// wait counts from the last tick the block above was still falling kind.
func (m *MineShaft) settle(a Actuator) outcome {
	m.waited++
	m.calm++
	if isFalling(m.nameAt(up(m.brk.pos, 1))) {
		m.calm = 0
	}
	if m.waited > settleCap || m.calm > settleTicks {
		m.phase = shDig
		return next
	}
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return m.failed(a, err)
	}
	return outcome{st: Running}
}

func (m *MineShaft) nameAt(c protocol.BlockPos) string {
	n, _ := m.world.Block(c)
	return n
}

// startDeposits queues a deposit for every item the unit has more of than it
// started with, which is what the digging dropped.
func (m *MineShaft) startDeposits() {
	snap := m.self()
	have := map[string]int{}
	for _, it := range snap.Inventory {
		have[it.Name] += it.Count
	}
	var names []string
	for name, n := range have {
		if n > m.keep(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		m.deposits = append(m.deposits, NewDepositChest(m.world, m.self, m.spec.Chest, name, have[name]-m.keep(name), WithLogger(m.log)))
	}
	m.phase = shDeposit
}

func (m *MineShaft) deposit(ctx context.Context, a Actuator) outcome {
	if m.sub == nil {
		if len(m.deposits) == 0 {
			m.emit("mine_done", map[string]any{"opened": m.dug, "depth": m.stepsDone})
			return outcome{st: Done}
		}
		m.sub, m.deposits = m.deposits[0], m.deposits[1:]
	}
	st, err := m.sub.Step(ctx, a)
	switch st {
	case Failed:
		return m.failed(a, err)
	case Done:
		m.sub = nil
		return next
	}
	return outcome{st: Running}
}

// liquidKind names a block the unit must never let near an opened cell.
func liquidKind(name string) string {
	switch name {
	case "lava", "flowing_lava":
		return "lava"
	case "water", "flowing_water":
		return "water"
	case "magma":
		return "magma"
	}
	return ""
}

func isFalling(name string) bool {
	return name == "gravel" || name == "sand" || name == "red_sand" || strings.HasSuffix(name, "_concrete_powder")
}

// beside reads a neighbour of a cell about to open: its hazard, if any.
func (m *MineShaft) beside(c protocol.BlockPos) string {
	name, known := m.world.Block(c)
	if !known {
		return "unknown"
	}
	return liquidKind(name)
}

// hazard says why a cell must stay closed, or "" if it is safe to open. It
// reads the cell's six neighbours, and follows a column of falling blocks
// overhead to what caps it, since that is what lands in the cell and what the
// column's removal exposes.
func (m *MineShaft) hazard(c protocol.BlockPos) string {
	if !m.spec.Area.contains(c) {
		return "area"
	}
	for _, d := range [6][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		if kind := m.beside(protocol.BlockPos{c.X() + d[0], c.Y() + d[1], c.Z() + d[2]}); kind != "" {
			return kind
		}
	}
	for k := int32(1); k <= maxFallingChain; k++ {
		o := up(c, k)
		name, known := m.world.Block(o)
		if !known {
			return "unknown"
		}
		if !isFalling(name) {
			return liquidKind(name)
		}
		for _, d := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if kind := m.beside(protocol.BlockPos{o.X() + d[0], o.Y(), o.Z() + d[1]}); kind != "" {
				return kind
			}
		}
	}
	return "unstable"
}

// floor says why the unit could not stand on the cell it is about to step
// into: nothing solid under it.
func (m *MineShaft) floor(stand protocol.BlockPos) string {
	name, known := m.world.Block(up(stand, -1))
	switch {
	case !known:
		return "unknown"
	case liquidKind(name) != "":
		return liquidKind(name)
	case path.Passable(name):
		return "void"
	}
	return ""
}

// bridgeBlocks are the blocks a unit may spend on a missing floor, best
// first. Falling blocks are left out: sand would drop out from under the step.
var bridgeBlocks = []string{"cobblestone", "dirt", "stone", "cobbled_deepslate", "andesite", "diorite", "granite", "netherrack"}

// bridgeBlock picks the block to fill a floor with from the inventory.
func bridgeBlock(s belief.Snapshot) (string, bool) {
	for _, name := range bridgeBlocks {
		if count(s, name) > 0 {
			return name, true
		}
	}
	return "", false
}

// keep is how many of an item stay with the unit when it deposits: what it
// started with, and for cobblestone at least the bridging reserve.
func (m *MineShaft) keep(name string) int {
	if name == "cobblestone" && m.base[name] < bridgeReserve {
		return bridgeReserve
	}
	return m.base[name]
}

// arm waits for the pickaxe and the entrance's ground, which arrive a little
// after a unit joins or respawns, instead of failing the task for it.
func (m *MineShaft) arm(a Actuator) outcome {
	_, armed := hotbarTool(m.self(), "pickaxe")
	_, here := m.world.Block(m.spec.Entrance)
	_, below := m.world.Block(up(m.spec.Entrance, -1))
	if armed && here && below {
		m.waited, m.phase = 0, shStock
		return next
	}
	if m.waited++; m.waited >= armWait {
		if !armed {
			return m.failed(a, errors.New("no pickaxe in inventory"))
		}
		m.waited, m.phase = 0, shStock
		return next
	}
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return m.failed(a, err)
	}
	return outcome{st: Running}
}

// climb pillars the unit up until a walking path to its goal exists, then
// goes back to what it was doing.
func (m *MineShaft) climb(a Actuator) outcome {
	done, err := m.cl.step(a)
	switch {
	case err != nil:
		return m.failed(a, fmt.Errorf("climbing out of a trap: %w", err))
	case done:
		m.cl, m.selected, m.phase = nil, false, m.climbThen
		return next
	}
	return outcome{st: Running}
}

// stock tops the bridging reserve up from the deposit chest. A chest that is
// empty or cannot be opened does not stop the shaft: it only matters if a
// floor turns out to be missing.
func (m *MineShaft) stock(ctx context.Context, a Actuator) outcome {
	if m.sub == nil {
		short := bridgeReserve - count(m.self(), "cobblestone")
		if short <= 0 {
			m.phase = shEntrance
			return next
		}
		m.sub = NewWithdrawChest(m.world, m.self, m.spec.Chest, map[string]int{"cobblestone": short}, WithLogger(m.log))
	}
	st, err := m.sub.Step(ctx, a)
	switch st {
	case Failed:
		m.emit("mine_restock_failed", map[string]any{"error": fmt.Sprint(err)})
		fallthrough
	case Done:
		m.sub, m.phase = nil, shEntrance
		return next
	}
	return outcome{st: Running}
}

// startBridge fills the floor cell, or, with nothing to fill it with, ends the
// heading saying so.
func (m *MineShaft) startBridge(floor protocol.BlockPos, then shaftPhase) outcome {
	item, ok := bridgeBlock(m.self())
	if !ok {
		if then == shSeg {
			return m.abort("void floor, out of blocks to bridge it")
		}
		return outcome{st: Failed, err: errors.New("a stair floor is missing and the unit is out of blocks to bridge it")}
	}
	m.bridgeAt, m.bridgeThen, m.phase = floor, then, shBridge
	m.sub = NewPlaceBlock(m.world, m.self, floor, item, WithLogger(m.log))
	return next
}

func (m *MineShaft) bridge(ctx context.Context, a Actuator) outcome {
	st, err := m.sub.Step(ctx, a)
	switch st {
	case Failed:
		m.sub = nil
		if m.bridgeThen == shSeg {
			return m.abort("void floor, bridge failed")
		}
		return m.failed(a, fmt.Errorf("bridging a missing stair floor: %w", err))
	case Done:
		m.sub, m.phase, m.selected = nil, m.bridgeThen, false
		m.emit("mine_bridge", map[string]any{"step": m.si})
		return next
	}
	return outcome{st: Running}
}

// firstVoid is the index of the deepest dug stair step whose floor is missing,
// or -1. The deepest is mended first: it is the one nearest a unit at the
// bottom, and each mended step makes the next one reachable.
func (m *MineShaft) firstVoid() int {
	steps := stairSteps(m.spec)
	for i := len(steps) - 1; i >= 0; i-- {
		st := steps[i]
		if !m.passableAt(st.stand) || !m.passableAt(up(st.stand, 1)) {
			continue
		}
		if m.floor(st.stand) == "void" {
			return i
		}
	}
	return -1
}

func (m *MineShaft) passableAt(c protocol.BlockPos) bool {
	name, known := m.world.Block(c)
	return known && path.Passable(name)
}

// mend fills the stair's missing floors one at a time, then goes on with
// whatever the unit was about to do.
func (m *MineShaft) mend() outcome {
	i := m.firstVoid()
	if i < 0 {
		m.phase = m.mendThen
		if m.phase == shMend {
			m.phase = shReturn
		}
		return next
	}
	return m.startBridge(up(stairSteps(m.spec)[i].stand, -1), shMend)
}

// hotbarTool finds a tool stack on the hotbar, the only place the server
// lets the unit select from.
func hotbarTool(s belief.Snapshot, tool string) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && hasSuffix(it.Name, tool) {
			return it, true
		}
	}
	return belief.Item{}, false
}
