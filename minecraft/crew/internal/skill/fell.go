package skill

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// Two blocks keep the unit within a fall that does no damage, so a unit
	// left on its pillar can always step off.
	maxPillar  = 2
	maxPillars = 4
	// Margin on the reach the pillar's top is planned for.
	pillarSlack = 0.2
	// The unit rises about 1.25 blocks in a jump and clears a cell it places
	// into only above its top face; this much extra keeps the server's body
	// box off the new block.
	placeClearance = 0.05
	placeTries     = 3
	// Ticks to wait for the server to confirm a placed block once landed.
	placeGrace = 6
	fallWait   = 30
	// A log breaks high up and falls to the ground while the unit climbs down,
	// so a pickup can land a second or two after the unit stands there.
	collectWait = 40
	riseWait    = 40
	// Horizontal speed, in blocks per tick, below which the unit counts as
	// standing still.
	settledSpeed = 0.005
)

type fellPhase int

const (
	fMine fellPhase = iota
	fPlan
	fWalk
	fClear
	fJump
	fCut
	fDescend
	fReturn
	fWalkOff
	fCollect
)

type jumpState int

const (
	jIdle jumpState = iota
	jRising
)

// cellBreak is one block being broken by Fell itself, outside the mine skill.
type cellBreak struct {
	pos     protocol.BlockPos
	name    string
	started bool
	open    bool
	waited  int
}

// Fell cuts down a whole tree. It runs the mine skill from the ground, and
// when logs remain that no standable cell reaches it pillars up beside the
// trunk, cuts what is then in reach, climbs down and collects the drops.
type Fell struct {
	world ChestWorld
	self  func() belief.Snapshot
	mine  *Mine
	log   Logger

	phase   fellPhase
	pillars int
	// Logs a pillar could not reach. The mine skill's own skip list is not
	// enough: it also holds logs it only failed to walk to, which a pillar
	// may still reach.
	ruled  map[protocol.BlockPos]bool
	noSrc  error
	cutErr error

	// The plan: the log aimed for, the ground cell its pillar stands on, how
	// many blocks high, what it is made of and the leaves to clear first.
	target   protocol.BlockPos
	ground   protocol.BlockPos
	height   int
	material belief.Item
	digTool  belief.Item
	leaves   []protocol.BlockPos

	nav    *navigator
	pillar []protocol.BlockPos

	// The unit was off its pillar when it came to take it down, as a fight
	// leaves it, so the log it aimed for never had its cut and stays a target.
	offPillar bool
	returned  bool

	jump     jumpState
	tries    int
	waited   int
	placed   bool
	grace    int
	selected bool
	dug      bool
	brk      cellBreak
	falling  bool

	// Logs the pillars have cut, and the item count the pickups should bring
	// the unit to. Counted from breaks, not the inventory, because a ground
	// drop can still be on the way when a pillar is planned.
	felled    int
	expect    int
	matBase   int
	cols      []protocol.BlockPos
	colsInit  bool
	collected int
	colWait   int
}

// NewFell breaks logs of a kind until qty more of item are in the inventory.
// A pillar needs dirt, or cobblestone with a pickaxe, on the hotbar; without
// it the unit takes only what it can reach from the ground.
func NewFell(world ChestWorld, self func() belief.Snapshot, block, item, tool string, qty, radius int, mineOpts []MineOption, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Fell{
		world: world, self: self, log: o.log, ruled: map[protocol.BlockPos]bool{},
		mine: NewMineBlockWith(world, self, block, item, tool, qty, radius, mineOpts...).(*Mine),
	}
}

func (f *Fell) Name() string { return "fell_tree" }

func (f *Fell) Ready(s belief.Snapshot) error { return f.mine.Ready(s) }

func (f *Fell) emit(event string, fields map[string]any) {
	if f.log != nil {
		f.log.Info(event, fields)
	}
}

// Stop releases whichever break session is open.
func (f *Fell) Stop(a Actuator) {
	if f.brk.open {
		f.brk.open = false
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{Pos: f.brk.pos, Phase: act.BreakAbort}})
		return
	}
	f.mine.Stop(a)
}

// Resume forgets where in a step the skill was and keeps what it did: the
// pillar built, the logs cut and the drops still to collect. A unit on its
// pillar carries on from there, so the fight does not strand it.
func (f *Fell) Resume() {
	f.mine.Resume()
	f.nav, f.brk, f.jump = nil, cellBreak{}, jIdle
	f.selected, f.dug, f.falling, f.placed, f.waited, f.grace, f.colWait = false, false, false, false, 0, 0, 0
	switch f.phase {
	case fPlan, fWalk:
		f.phase = fMine
	case fClear, fJump:
		// A block placed just before the fight is part of the pillar even if
		// the unit had not yet seen it land.
		if f.phase == fJump {
			cell := protocol.BlockPos{f.ground.X(), f.ground.Y() + int32(len(f.pillar)), f.ground.Z()}
			if name, _ := f.world.Block(cell); name == f.material.Name && len(f.pillar) < f.height {
				f.pillar = append(f.pillar, cell)
			}
		}
		if len(f.pillar) > 0 {
			f.enterCut()
		} else {
			f.phase = fMine
		}
	case fCut:
		f.enterCut()
	case fReturn, fWalkOff:
		f.phase = fDescend
	}
	f.returned = false
}

func (f *Fell) enterCut() {
	f.phase, f.dug = fCut, false
	f.mine.hold = true
	f.mine.Resume()
}

func (f *Fell) fail(a Actuator, err error) (Status, error) {
	f.Stop(a)
	return Failed, err
}

func (f *Fell) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return f.fail(a, err)
	}
	if err := overworld(f.self()); err != nil {
		return f.fail(a, err)
	}
	for {
		p := a.Pose()
		var o outcome
		switch f.phase {
		case fMine:
			o = f.groundMine(ctx, a)
		case fPlan:
			o = f.plan(a, p)
		case fWalk:
			o = f.walk(a, fClear)
		case fClear:
			o = f.clear(a, p)
		case fJump:
			o = f.rise(a, p)
		case fCut:
			o = f.cut(ctx, a)
		case fDescend:
			o = f.descend(a, p)
		case fReturn:
			o = f.walk(a, fDescend)
		case fWalkOff:
			o = f.walk(a, fCollect)
		case fCollect:
			o = f.collect(a, p)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

func (f *Fell) groundMine(ctx context.Context, a Actuator) outcome {
	st, err := f.mine.Step(ctx, a)
	var ns noSourceError
	if st == Failed && errors.As(err, &ns) {
		f.noSrc, f.phase = err, fPlan
		return next
	}
	return outcome{st: st, err: err}
}

func isLeaves(name string) bool { return strings.HasSuffix(name, "_leaves") }

// pillarMaterial finds a hotbar stack to build with, and the tool to take it
// down again where that needs one.
func (f *Fell) pillarMaterial(s belief.Snapshot, height int) (mat, tool belief.Item, ok bool) {
	for _, name := range []string{"dirt", "cobblestone"} {
		for _, it := range s.Inventory {
			if it.Slot >= hotbarSlots || it.Name != name || it.Count < height {
				continue
			}
			if name == "dirt" {
				return it, belief.Item{}, true
			}
			if t, found := f.pickaxe(s); found {
				return it, t, true
			}
		}
	}
	return belief.Item{}, belief.Item{}, false
}

func (f *Fell) pickaxe(s belief.Snapshot) (belief.Item, bool) {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && hasSuffix(it.Name, "pickaxe") {
			return it, true
		}
	}
	return belief.Item{}, false
}

// plan picks the nearest log the ground cannot reach that a pillar can, and
// sends the unit to the foot of that pillar.
func (f *Fell) plan(a Actuator, p act.Pose) outcome {
	snap := f.self()
	if f.pillars >= maxPillars {
		return f.finish(a, f.noSrc)
	}
	for i, l := range f.mine.candidates(p, true) {
		if i >= maxCandidate {
			break
		}
		if f.ruled[l] {
			continue
		}
		g, h, leaves, found := f.pillarSpot(l, p.Feet)
		if !found {
			continue
		}
		mat, tool, ok := f.pillarMaterial(snap, h)
		if !ok {
			continue
		}
		f.target, f.ground, f.height, f.leaves = l, g, h, leaves
		f.material, f.digTool = mat, tool
		f.pillars++
		f.matBase = count(snap, mat.Name)
		f.waited, f.tries, f.jump, f.selected = 0, 0, jIdle, false
		f.emit("fell_pillar_planned", map[string]any{"height": h, "leaves": len(leaves), "attempt": f.pillars})
		f.nav = newNavigator(f.world, g, 0.4)
		f.phase = fWalk
		return next
	}
	return f.finish(a, f.noSrc)
}

// finish ends the skill on the ground, which is where plan runs from.
func (f *Fell) finish(a Actuator, err error) outcome {
	st, err := f.fail(a, err)
	return outcome{st: st, err: err}
}

// pillarSpot finds the ground cell beside a log from which a pillar of the
// fewest blocks brings it within reach, with the leaves that stand in the
// column the unit's body and jump occupy.
func (f *Fell) pillarSpot(l protocol.BlockPos, from mgl32.Vec3) (ground protocol.BlockPos, height int, leaves []protocol.BlockPos, ok bool) {
	feet := cellOf(from)
	bestD := float32(0)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			g, found := f.groundIn(l.X()+dx, l.Z()+dz, feet.Y())
			if !found {
				continue
			}
			for h := 1; h <= maxPillar; h++ {
				// A viable pillar cannot extend above an in-area target: that target is already within ground reach from g.
				if f.mine.area != nil && !f.mine.area.contains(protocol.BlockPos{g.X(), g.Y() + int32(h) - 1, g.Z()}) {
					continue
				}
				if centre(l).Sub(standEye(protocol.BlockPos{g.X(), g.Y() + int32(h), g.Z()})).Len() > mineReach-pillarSlack {
					continue
				}
				lv, clear := f.clearColumn(g, h)
				if !clear {
					break
				}
				d := centre(g).Sub(from).LenSqr()
				if !ok || h < height || (h == height && d < bestD) {
					ground, height, leaves, ok, bestD = g, h, lv, true, d
				}
				break
			}
		}
	}
	return ground, height, leaves, ok
}

// groundIn finds the cell nearest a height in a column where the unit can
// stand, which is the ground beside the tree and not the canopy over it.
func (f *Fell) groundIn(x, z, y int32) (protocol.BlockPos, bool) {
	for _, dy := range []int32{0, -1, 1, -2, 2, -3, 3} {
		c := protocol.BlockPos{x, y + dy, z}
		if path.Standable(f.world, c) && (f.mine.area == nil || f.mine.area.contains(c)) {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}

// clearColumn lists the leaves in the cells a pillar of h blocks needs open:
// the pillar itself, the body on top and the headroom of the last jump. It
// reports false if anything else is in the way, or if clearing a leaf would
// leave the area.
func (f *Fell) clearColumn(g protocol.BlockPos, h int) (leaves []protocol.BlockPos, ok bool) {
	for k := 0; k <= h+1; k++ {
		c := protocol.BlockPos{g.X(), g.Y() + int32(k), g.Z()}
		name, known := f.world.Block(c)
		switch {
		case !known:
			return nil, false
		case isLeaves(name):
			if f.mine.area != nil && !f.mine.area.contains(c) {
				return nil, false
			}
			leaves = append(leaves, c)
		case !path.Passable(name):
			return nil, false
		}
	}
	return leaves, true
}

func (f *Fell) walk(a Actuator, then fellPhase) outcome {
	st, err := f.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		_, err := f.fail(a, te.err)
		return outcome{st: Failed, err: err}
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		f.nav, f.phase = nil, then
		return next
	}
	switch f.phase {
	case fWalkOff:
		return f.finish(a, err)
	case fReturn:
		f.nav, f.phase = nil, fDescend
		return next
	}
	// A pillar that cannot be walked to rules out its log, not the task.
	f.rule(f.target)
	f.pillars--
	f.nav, f.phase = nil, fMine
	f.mine.Resume()
	return next
}

// breakCell sends one input of breaking a block, looking at it. It reports
// true, sending nothing, once the block is gone.
func (f *Fell) breakCell(a Actuator, p act.Pose) (bool, error) {
	return driveBreak(f.world, &f.brk, a, p)
}

// driveBreak is the per-tick body of breaking one cell outside the mine
// skill, shared by the skills that dig on their own.
func driveBreak(world path.Blocks, cb *cellBreak, a Actuator, p act.Pose) (bool, error) {
	if n, known := world.Block(cb.pos); known && n != cb.name {
		cb.open = false
		return true, nil
	}
	eye := eyeOf(p.Feet)
	yaw, pitch := lookAt(eye, centre(cb.pos))
	in := act.Input{Yaw: yaw, Pitch: pitch}
	if !cb.started {
		cb.started, cb.open = true, true
		in.Break = &act.BreakAction{Pos: cb.pos, Face: faceToward(cb.pos, eye), Phase: act.BreakStart}
	} else if cb.waited++; cb.waited > breakTicks {
		return false, errors.New("block did not break")
	}
	return false, a.Tick(in)
}

// stopBreak aborts a break in progress with the input it rides.
func (f *Fell) stopBreak(a Actuator, err error) outcome {
	st, err := f.fail(a, err)
	return outcome{st: st, err: err}
}

func (f *Fell) clear(a Actuator, p act.Pose) outcome {
	if len(f.leaves) == 0 {
		f.phase = fJump
		return next
	}
	if f.brk.pos != f.leaves[0] || f.brk.name == "" {
		name, known := f.world.Block(f.leaves[0])
		if known && !isLeaves(name) {
			f.leaves, f.brk = f.leaves[1:], cellBreak{}
			return next
		}
		f.brk = cellBreak{pos: f.leaves[0], name: name}
	}
	done, err := f.breakCell(a, p)
	if err != nil {
		return f.stopBreak(a, err)
	}
	if done {
		f.leaves, f.brk = f.leaves[1:], cellBreak{}
		return next
	}
	return outcome{st: Running}
}

// rise builds the pillar one block at a time: jump, place under the feet at
// the top of the jump, land on the new block.
func (f *Fell) rise(a Actuator, p act.Pose) outcome {
	if len(f.pillar) >= f.height {
		f.enterCut()
		return next
	}
	cell := protocol.BlockPos{f.ground.X(), f.ground.Y() + int32(len(f.pillar)), f.ground.Z()}
	if cellOf(p.Feet).X() != cell.X() || cellOf(p.Feet).Z() != cell.Z() {
		return f.abandonPillar(a, errors.New("drifted off the pillar's column"))
	}
	idle := act.Input{Yaw: p.Yaw, Pitch: 90}

	if f.jump == jIdle {
		// Leftover momentum would carry the jump out of the pillar's column.
		if !p.OnGround || p.Carry.Len() > settledSpeed {
			return f.wait(a, idle)
		}
		if !f.selected {
			if err := a.SelectHotbar(f.material.Slot, f.material); err != nil {
				return f.stopBreak(a, err)
			}
			f.selected = true
		}
		f.jump, f.placed, f.waited, f.grace = jRising, false, 0, 0
		idle.Jump = true
		return f.tick(a, idle)
	}

	f.waited++
	if p.OnGround && f.waited > 1 {
		name, _ := f.world.Block(cell)
		switch {
		case name == f.material.Name:
			f.pillar = append(f.pillar, cell)
			f.tries, f.jump = 0, jIdle
			f.emit("fell_pillar_placed", map[string]any{"height": len(f.pillar)})
			return next
		case f.placed && f.grace < placeGrace:
			f.grace++
		default:
			if f.tries++; f.tries >= placeTries {
				return f.abandonPillar(a, errors.New("the server did not place the block"))
			}
			f.jump = jIdle
			return next
		}
	} else if f.waited > riseWait {
		return f.abandonPillar(a, errors.New("jump did not land"))
	}
	if !f.placed && !p.OnGround && p.Feet.Y() >= float32(cell.Y())+1+placeClearance {
		hash, known := f.world.BlockHash(protocol.BlockPos{cell.X(), cell.Y() - 1, cell.Z()})
		if !known {
			return f.abandonPillar(a, errors.New("the block under the pillar is not known"))
		}
		if err := a.PlaceOnTop(protocol.BlockPos{cell.X(), cell.Y() - 1, cell.Z()}, f.material.Slot, f.material, hash); err != nil {
			return f.stopBreak(a, err)
		}
		f.placed = true
	}
	return f.tick(a, idle)
}

// abandonPillar gives up on the log it aimed for, ruling it out, and climbs
// down whatever was built.
func (f *Fell) abandonPillar(a Actuator, why error) outcome {
	f.emit("fell_pillar_abandoned", map[string]any{"reason": why.Error(), "height": len(f.pillar)})
	f.rule(f.target)
	f.jump, f.tries = jIdle, 0
	f.phase = fDescend
	return next
}

func (f *Fell) rule(l protocol.BlockPos) {
	f.ruled[l] = true
	f.mine.skip[l] = true
}

func (f *Fell) tick(a Actuator, in act.Input) outcome {
	if err := a.Tick(in); err != nil {
		return f.stopBreak(a, err)
	}
	return outcome{st: Running}
}

func (f *Fell) wait(a Actuator, in act.Input) outcome {
	f.waited++
	if f.waited > waitTicks {
		return f.stopBreak(a, errors.New("unit never settled on the ground"))
	}
	return f.tick(a, in)
}

// cut breaks the logs in reach from the pillar, where the mine skill is held
// in place.
func (f *Fell) cut(ctx context.Context, a Actuator) outcome {
	st, err := f.mine.Step(ctx, a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		f.phase = fDescend
		return next
	}
	var ns noSourceError
	if !errors.As(err, &ns) {
		f.cutErr = err
	}
	f.phase = fDescend
	return next
}

// descend takes the pillar down from the top, breaking the block under the
// feet so the unit never falls further than one block.
func (f *Fell) descend(a Actuator, p act.Pose) outcome {
	if len(f.pillar) == 0 {
		f.phase = fCollect
		return next
	}
	top := f.pillar[len(f.pillar)-1]
	feet := cellOf(p.Feet)
	if f.falling {
		if p.OnGround && feet.Y() <= top.Y() {
			f.pillar, f.falling, f.brk, f.waited = f.pillar[:len(f.pillar)-1], false, cellBreak{}, 0
			return next
		}
		if f.waited++; f.waited > fallWait {
			return f.stopBreak(a, errors.New("unit did not come down"))
		}
		return f.tick(a, act.Input{Yaw: p.Yaw, Pitch: 90})
	}
	onTop := feet.X() == top.X() && feet.Z() == top.Z() && feet.Y() == top.Y()+1
	if !onTop {
		f.offPillar = true
	}
	if !onTop && centre(top).Sub(eyeOf(p.Feet)).Len() > mineReach {
		// Knocked off or put down elsewhere: go back and take the pillar down
		// from beside it, once. Only if that fails do the blocks stay.
		if !f.returned {
			f.returned = true
			if c, ok := f.besidePillar(top); ok {
				f.nav, f.phase = newNavigator(f.world, c, 0.4), fReturn
				return next
			}
		}
		f.emit("fell_pillar_left", map[string]any{"height": len(f.pillar)})
		f.pillar = nil
		return next
	}
	if f.brk.name == "" {
		if f.digTool.Name != "" && !f.dug {
			if err := a.SelectHotbar(f.digTool.Slot, f.digTool); err != nil {
				return f.stopBreak(a, err)
			}
			f.dug = true
		}
		f.brk = cellBreak{pos: top, name: f.material.Name}
	}
	done, err := f.breakCell(a, p)
	if err != nil {
		f.Stop(a)
		return f.walkOff(a)
	}
	if done {
		if onTop {
			f.falling, f.waited = true, 0
		} else {
			f.pillar, f.brk = f.pillar[:len(f.pillar)-1], cellBreak{}
		}
		return next
	}
	return outcome{st: Running}
}

// walkOff leaves a pillar whose blocks will not break by stepping down, which
// two blocks of height make safe.
func (f *Fell) walkOff(a Actuator) outcome {
	f.emit("fell_pillar_left", map[string]any{"height": len(f.pillar)})
	top := f.pillar[len(f.pillar)-1]
	f.pillar, f.brk = nil, cellBreak{}
	if c, ok := f.besidePillar(top); ok {
		f.nav, f.phase = newNavigator(f.world, c, 0.4), fWalkOff
		return next
	}
	return f.finish(a, errors.New("no way down from the pillar"))
}

// besidePillar finds a ground cell next to the pillar.
func (f *Fell) besidePillar(top protocol.BlockPos) (protocol.BlockPos, bool) {
	for _, d := range [][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		c := protocol.BlockPos{top.X() + d[0], f.ground.Y(), top.Z() + d[1]}
		if path.Standable(f.world, c) {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}

// collect walks to where the cut logs fell and waits for the pickup, then
// hands the unit back to the ground skill. A log that survived its pillar is
// ruled out first.
func (f *Fell) collect(a Actuator, p act.Pose) outcome {
	if !f.colsInit {
		f.colsInit = true
		f.initCollect()
	}
	if len(f.cols) == 0 || f.pickedUp() {
		return f.back(a)
	}
	if f.nav == nil && f.colWait == 0 {
		f.nav = newNavigator(f.world, f.cols[0], 0.4)
	}
	if f.nav != nil {
		st, err := f.nav.step(a)
		var te tickError
		if errors.As(err, &te) {
			return f.finish(a, te.err)
		}
		if st == Running {
			return outcome{st: Running}
		}
		// A column that cannot be reached is skipped: its drop stays.
		f.nav = nil
		if st == Failed {
			f.cols = f.cols[1:]
			return next
		}
	}
	if f.colWait++; f.colWait > collectWait {
		f.cols, f.colWait = f.cols[1:], 0
		return next
	}
	return f.tick(a, act.Input{Yaw: p.Yaw, Pitch: p.Pitch})
}

func (f *Fell) initCollect() {
	if n, known := f.world.Block(f.target); known && n == f.mine.block && !f.offPillar {
		f.rule(f.target)
	}
	type col struct{ x, z int32 }
	seen := map[col]bool{}
	var cols []protocol.BlockPos
	add := func(x, z, y int32) {
		if seen[col{x, z}] {
			return
		}
		seen[col{x, z}] = true
		for dy := int32(0); dy <= 10; dy++ {
			if c := (protocol.BlockPos{x, y - dy, z}); path.Standable(f.world, c) {
				cols = append(cols, c)
				return
			}
		}
	}
	if f.ground != (protocol.BlockPos{}) {
		add(f.ground.X(), f.ground.Z(), f.ground.Y())
	}
	for _, b := range f.mine.felled {
		add(b.X(), b.Z(), b.Y())
	}
	f.felled += len(f.mine.felled)
	f.expect = f.mine.baseline + f.mine.broken + f.felled
	f.mine.felled = nil
	if len(cols) > 1 {
		rest := cols[1:]
		sort.SliceStable(rest, func(i, j int) bool {
			return centre(rest[i]).Sub(centre(cols[0])).LenSqr() < centre(rest[j]).Sub(centre(cols[0])).LenSqr()
		})
	}
	f.cols = cols
}

// back returns to mining from the ground, or ends if the pillar's cut failed.
func (f *Fell) back(a Actuator) outcome {
	f.emit("fell_collected", map[string]any{"pillars": f.pillars})
	f.colsInit, f.colWait, f.nav = false, 0, nil
	f.offPillar, f.returned = false, false
	f.ground, f.target = protocol.BlockPos{}, protocol.BlockPos{}
	f.mine.hold = false
	f.mine.Resume()
	if err := f.cutErr; err != nil {
		f.cutErr = nil
		return f.finish(a, err)
	}
	f.phase = fMine
	return next
}

// pickedUp says whether the cut logs and the pillar's blocks are all back in
// the inventory.
func (f *Fell) pickedUp() bool {
	s := f.self()
	return count(s, f.mine.item) >= f.expect && count(s, f.material.Name) >= f.matBase
}
