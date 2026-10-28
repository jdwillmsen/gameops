package skill

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/repair"
)

const (
	// maxRepairCells is the most cells one task changes. A cell costs a
	// round trip or two each way plus walking, so this keeps a run well
	// inside the task timeout; bigger damage belongs to the console tool.
	maxRepairCells = 1000
	// maxCellFailures stops a run that keeps failing on one cause, such as a
	// unit that is not in creative mode, instead of trying every cell.
	maxCellFailures = 10
	// reportBytes bounds the result text, which travels as a task reason.
	reportBytes = 400
	// guardRadius is how far from the unit a mob or visitor stops the work.
	guardRadius = 32
)

// RepairWorld is the unit's block store as repair reads it.
type RepairWorld interface {
	path.Blocks
	repair.BlockSource
}

// Guard says who is around the unit. A threatening mob or a player who is not
// crew stops a repair, as they stop the console tool.
type Guard struct {
	// Each visits every entity the unit knows of.
	Each func(func(belief.Entity))
	// Threat says whether a mob type is one to stop for.
	Threat func(entityType string) bool
	// Crew are the player names that may share the arena.
	Crew []string
}

// WithGuard sets who a repair stops for. Without one it never stops.
func WithGuard(g Guard) Option { return func(o *options) { o.guard = &g } }

// near returns why work must stop, or nil.
func (g *Guard) near(feet [3]float32) error {
	if g == nil || g.Each == nil {
		return nil
	}
	var err error
	g.Each(func(e belief.Entity) {
		if err != nil {
			return
		}
		dx, dz := e.Pos.X()-feet[0], e.Pos.Z()-feet[2]
		if dx*dx+dz*dz > guardRadius*guardRadius {
			return
		}
		switch {
		case e.Player && !g.isCrew(e.Name):
			err = errors.New("a player who is not crew is nearby")
		case !e.Player && g.Threat != nil && g.Threat(e.Type):
			err = errors.New("a hostile mob is nearby")
		}
	})
	return err
}

func (g *Guard) isCrew(name string) bool {
	for _, c := range g.Crew {
		if strings.EqualFold(c, name) {
			return true
		}
	}
	return false
}

// RepairSpec is what a repair restores: the part of the plan inside Area.
type RepairSpec struct {
	Plan   []repair.Op
	Layout arena.Layout
	Area   Area
	// DryRun triages and reports without touching a block.
	DryRun bool
}

// Reporter is implemented by skills whose result is a short text, which the
// runner passes on with the finished task.
type Reporter interface {
	// Report is false until the skill has something to say.
	Report() (string, bool)
}

type repairPhase int

const (
	rpTriage repairPhase = iota
	rpCell
	rpBreak
	rpPlace
	rpVerify
)

// Repair triages an area against the plan from the unit's own loaded chunks,
// then walks the damage in the planner's order, breaking what should not be
// there and placing what should.
type Repair struct {
	world RepairWorld
	self  func() belief.Snapshot
	spec  RepairSpec
	log   Logger
	guard *Guard

	box        repair.Box
	plan       []repair.Op
	match      *repair.Matcher
	phase      repairPhase
	work       []repair.Rect
	ri         int
	cx, cy, cz int
	inRect     bool

	target protocol.BlockPos
	want   string
	// redo makes the next cell step re-read the cell the unit was on rather
	// than move to the one after it.
	redo    bool
	curRect repair.Rect
	ap      approach
	broke   bool
	waited  int
	retried bool
	place   Skill

	report                    string
	reported                  bool
	changed, failed           int
	lastErr                   error
	stateOnly, liquid, noItem int
}

// NewRepair builds the skill. The world must be the unit's block store.
func NewRepair(world RepairWorld, self func() belief.Snapshot, spec RepairSpec, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	a := spec.Area
	box := repair.Box{
		X0: int(a.Min.X()), Y0: int(a.Min.Y()), Z0: int(a.Min.Z()),
		X1: int(a.Max.X()), Y1: int(a.Max.Y()), Z1: int(a.Max.Z()),
	}
	plan := repair.ClipOps(spec.Plan, box)
	return &Repair{world: world, self: self, spec: spec, log: o.log, guard: o.guard,
		box: box, plan: plan, match: repair.NewMatcher(plan)}
}

func (r *Repair) Name() string { return "repair_build" }

func (r *Repair) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if !r.spec.DryRun && !s.Creative {
		return errors.New("the unit is not in creative mode")
	}
	return nil
}

// Report is the triage on a dry run, and what the repair did otherwise.
func (r *Repair) Report() (string, bool) { return r.report, r.reported }

// Progress counts the cells changed.
func (r *Repair) Progress() int { return r.changed }

// Resume re-reads the cell the unit was on and walks to it afresh: a fight
// moved the unit and left the block as it was.
func (r *Repair) Resume() {
	if r.phase == rpBreak || r.phase == rpPlace {
		r.phase, r.redo = rpCell, true
	}
	r.ap, r.place, r.waited, r.retried = approach{}, nil, 0, false
}

func (r *Repair) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(r.self()); err != nil {
		return Failed, err
	}
	if !r.spec.DryRun {
		if err := r.guard.near(a.Pose().Feet); err != nil {
			return Failed, err
		}
	}
	for {
		var o outcome
		switch r.phase {
		case rpTriage:
			o = r.triage(a)
		case rpCell:
			o = r.cell()
		case rpBreak:
			o = r.breakCell(a)
		case rpPlace:
			o = r.placeCell(a)
		case rpVerify:
			o = r.verify()
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

func (r *Repair) diff() (*repair.Damage, error) {
	return repair.Diff(r.plan, repair.NewBlocksReader(r.world, r.plan), r.box, r.spec.Layout)
}

func (r *Repair) triage(a Actuator) outcome {
	d, err := r.diff()
	if err != nil {
		return outcome{st: Failed, err: err}
	}
	rep := repair.NewReport(d, repair.Plan{})
	rep.Dry = r.spec.DryRun
	summary := rep.Summary(reportBytes - len("triage: "))
	r.say("triage: " + summary)
	if r.log != nil {
		r.log.Info("repair_triage", map[string]any{
			"damaged": d.Cells, "scanned": d.Scanned, "unscanned": d.Unscanned,
			"rectangles": len(d.Rects), "dry_run": r.spec.DryRun,
			"by_kind": d.ByKind, "by_zone": d.ByZone,
		})
	}
	if r.spec.DryRun || d.Cells == 0 {
		return outcome{st: Done}
	}

	items := a.ItemTable()
	var types []string
	seen := map[string]bool{}
	cells := 0
	for _, rc := range repair.OrderRects(d.Rects) {
		n := boxCells(rc.Box)
		if !repair.IsAir(rc.Block) {
			item := baseName(rc.Block)
			if _, ok := items[item]; !ok {
				r.noItem += n
				continue
			}
			if !seen[item] {
				seen[item] = true
				types = append(types, item)
			}
		}
		cells += n
		r.work = append(r.work, rc)
	}
	if cells > maxRepairCells {
		return outcome{st: Failed, err: fmt.Errorf("repair needs %d cells changed, more than a unit walks in one task (%d); use arenarepair for this damage", cells, maxRepairCells)}
	}
	if free, held := r.hotbar(); len(types) > free {
		need := 0
		for _, t := range types {
			if !held[t] {
				need++
			}
		}
		if need > free {
			return outcome{st: Failed, err: fmt.Errorf("repair needs %d block types and the hotbar has room for %d; narrow the area or use arenarepair", need, free)}
		}
	}
	r.phase = rpCell
	return next
}

// hotbar is how many hotbar slots are empty and which items the hotbar holds.
func (r *Repair) hotbar() (free int, held map[string]bool) { return hotbarRoom(r.self()) }

// hotbarRoom counts the empty hotbar slots and names the items held there. A
// creative take fills a slot that nothing empties again, so a task needing
// more block types than there are free slots must be refused before it starts.
func hotbarRoom(s belief.Snapshot) (free int, held map[string]bool) {
	held = map[string]bool{}
	used := 0
	for _, it := range s.Inventory {
		if it.Slot >= 0 && it.Slot < hotbarSlots {
			used++
			held[it.Name] = true
		}
	}
	return hotbarSlots - used, held
}

// cell moves to the next cell that still differs from the plan and decides
// what it needs. The world is read again here, so a cell another unit or a
// player already fixed is left alone.
func (r *Repair) cell() outcome {
	for {
		pos, rc, ok := r.target, r.curRect, true
		if r.redo {
			r.redo = false
		} else {
			pos, rc, ok = r.advance()
		}
		if !ok {
			r.phase = rpVerify
			return next
		}
		name, props, known := r.world.State(pos)
		if !known {
			continue
		}
		cur := r.match.Canonical(name, repair.RenderStates(props))
		if cur == rc.Block {
			continue
		}
		curName, wantName := baseName(cur), baseName(rc.Block)
		if curName == wantName {
			// The block is there with other states, which a click cannot set.
			r.stateOnly++
			continue
		}
		if isLiquid(curName) && repair.IsAir(rc.Block) {
			r.liquid++
			continue
		}
		r.target, r.want, r.curRect = pos, rc.Block, rc
		r.ap, r.broke, r.waited, r.retried, r.place = approach{world: r.world, target: pos}, false, 0, false, nil
		if !repair.IsAir(cur) && !isLiquid(curName) {
			r.phase = rpBreak
			return next
		}
		if repair.IsAir(rc.Block) {
			continue
		}
		r.phase = rpPlace
		return next
	}
}

// advance steps the cursor to the next cell of the work list, y then z then x
// so a rect is walked a row at a time.
func (r *Repair) advance() (protocol.BlockPos, repair.Rect, bool) {
	for r.ri < len(r.work) {
		rc := r.work[r.ri]
		b := rc.Box
		if !r.inRect {
			r.cx, r.cy, r.cz, r.inRect = b.X0, b.Y0, b.Z0, true
		} else if r.cx++; r.cx > b.X1 {
			r.cx = b.X0
			if r.cz++; r.cz > b.Z1 {
				r.cz = b.Z0
				if r.cy++; r.cy > b.Y1 {
					r.ri++
					r.inRect = false
					continue
				}
			}
		}
		return protocol.BlockPos{int32(r.cx), int32(r.cy), int32(r.cz)}, rc, true
	}
	return protocol.BlockPos{}, repair.Rect{}, false
}

func (r *Repair) breakCell(a Actuator) outcome {
	if !r.broke {
		arrived, st, err := r.ap.step(a)
		switch {
		case st == Failed:
			return r.cellFailed(err)
		case !arrived:
			return outcome{st: Running}
		}
		p := a.Pose()
		in := act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{
			Pos: r.target, Face: faceToward(r.target, eyeOf(p.Feet)), Phase: act.BreakStart}}
		if err := a.Tick(in); err != nil {
			return outcome{st: Failed, err: err}
		}
		r.broke, r.waited = true, 0
		return outcome{st: Running}
	}
	if name, known := r.world.Block(r.target); known && name == "air" {
		if repair.IsAir(r.want) {
			r.changed++
			r.phase = rpCell
			return next
		}
		r.phase = rpPlace
		return next
	}
	if r.waited++; r.waited >= confirmTicks {
		if r.retried {
			return r.cellFailed(errors.New("the server did not break the block"))
		}
		r.retried, r.broke = true, false
	}
	return r.idle(a)
}

func (r *Repair) placeCell(a Actuator) outcome {
	if r.place == nil {
		r.place = NewPlaceBlock(r.world, r.self, r.target, baseName(r.want), WithLogger(r.log))
		if err := r.place.Ready(r.self()); err != nil {
			return r.cellFailed(err)
		}
	}
	st, err := r.place.Step(context.Background(), a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		r.changed++
		r.phase = rpCell
		return next
	}
	return r.cellFailed(err)
}

// cellFailed gives up on the cell, not the run, unless failures pile up: one
// cell with no support must not strand the rest of the repair.
func (r *Repair) cellFailed(err error) outcome {
	var te tickError
	if errors.As(err, &te) {
		return outcome{st: Failed, err: te.err}
	}
	r.failed++
	r.lastErr = err
	if r.failed >= maxCellFailures {
		return outcome{st: Failed, err: fmt.Errorf("giving up after %d cells failed, the last: %w", r.failed, err)}
	}
	r.place, r.phase = nil, rpCell
	return next
}

func (r *Repair) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return outcome{st: Failed, err: err}
	}
	return outcome{st: Running}
}

// verify diffs the area again so the result says what is still wrong.
func (r *Repair) verify() outcome {
	d, err := r.diff()
	if err != nil {
		return outcome{st: Failed, err: err}
	}
	head := fmt.Sprintf("repaired %d cells", r.changed)
	if r.failed > 0 {
		head += fmt.Sprintf(", %d failed", r.failed)
	}
	if r.noItem+r.stateOnly+r.liquid > 0 {
		head += fmt.Sprintf(", %d left (no item %d, states %d, liquid %d)", r.noItem+r.stateOnly+r.liquid, r.noItem, r.stateOnly, r.liquid)
	}
	left := "nothing left to repair"
	if d.Cells > 0 {
		left = "still damaged: " + repair.NewReport(d, repair.Plan{}).Summary(reportBytes-len(head)-len("; still damaged: ")-1)
	}
	r.say(head + "; " + left)
	if r.log != nil {
		r.log.Info("repair_done", map[string]any{"changed": r.changed, "failed": r.failed, "remaining": d.Cells})
	}
	return outcome{st: Done}
}

func (r *Repair) say(s string) {
	if len(s) > reportBytes {
		s = s[:reportBytes]
	}
	r.report, r.reported = s, true
}

func boxCells(b repair.Box) int {
	return (b.X1 - b.X0 + 1) * (b.Y1 - b.Y0 + 1) * (b.Z1 - b.Z0 + 1)
}

// baseName is a block string without its states, which is also the name of the
// item that places it.
func baseName(block string) string {
	name, _, _ := strings.Cut(block, " ")
	return name
}

func isLiquid(name string) bool {
	switch name {
	case "water", "flowing_water", "lava", "flowing_lava":
		return true
	}
	return false
}
