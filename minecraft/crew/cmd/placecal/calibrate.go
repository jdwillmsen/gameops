package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

const (
	// settle lets the second half of a bed or door and neighbour updates
	// arrive before the cells are read.
	settle = 60 * time.Millisecond
	// verifyN is how many clicks of a sibling's sweep are repeated before a
	// family is trusted.
	verifyN = 8
	// hotbarLimit is how many hotbar slots may be taken before the hotbar is
	// cleared, leaving room for the next take and the stone of the neighbour
	// pass (a hotbar has nine).
	hotbarLimit = 6
)

var errBudget = errors.New("placement budget spent")

// item is one creative item to calibrate.
type item struct {
	name     string
	hasBlock bool
}

// itemResult is what one item gave: every block its clicks produced.
type itemResult struct {
	item      string
	outcomes  map[string]map[string]*seen // block -> state key -> cheapest click
	props     map[string]map[string]map[string]any
	derived   map[string]map[string]bool // block -> derived keys
	inferred  map[string]bool
	truncated bool
	trials    int
	// unplaced says no click placed a block.
	unplaced bool
	// unmapped says a click placed a block the vocabulary does not know.
	unmapped bool
	// soil is the block the lane floor was changed to for the item to
	// place; empty for the default dirt.
	soil string
}

type seen struct {
	c    click
	cost float64
}

func newItemResult(name string) *itemResult {
	return &itemResult{
		item:     name,
		outcomes: map[string]map[string]*seen{},
		props:    map[string]map[string]map[string]any{},
		derived:  map[string]map[string]bool{},
		inferred: map[string]bool{},
	}
}

func (r *itemResult) add(c click, o outcome) {
	if o.Unmapped {
		r.unmapped = true
	}
	if !o.Placed || o.Unmapped {
		return
	}
	k := place.Key(o.Props, nil)
	m := r.outcomes[o.Name]
	if m == nil {
		m = map[string]*seen{}
		r.outcomes[o.Name] = m
		r.props[o.Name] = map[string]map[string]any{}
	}
	cost := c.cost()
	if cur := m[k]; cur == nil || cost < cur.cost || (cost == cur.cost && c.key() < cur.c.key()) {
		m[k] = &seen{c: c, cost: cost}
	}
	r.props[o.Name][k] = o.Props
}

// family is a finished sweep other blocks with the same first-pass responses
// may be inferred from.
type family struct {
	grid    []click
	results map[string]outcome // click key -> outcome
}

// run holds what the workers share.
type calRun struct {
	cfg    config
	vocab  vocabulary
	con    *safeConsole
	budget int64
	placed atomic.Int64

	mu       sync.Mutex
	families map[string]*family
	// derivedBy is the derived keys found for a family, by first-pass
	// signature: the neighbour pass runs once per family, not per block.
	derivedBy map[string]map[string]bool
	done      []*itemResult
	timings   map[string]time.Duration

	stoneRetry atomic.Int64
}

func (r *calRun) spend() error {
	if r.placed.Add(1) > r.budget {
		return errBudget
	}
	return nil
}

// worker is one unit working one bay.
type worker struct {
	r   *calRun
	u   *unit
	bay bay
	log func(string, ...any)
	// slots counts the hotbar slots taken since the last clear.
	slots int
	// retryMisses, once an item is known to place somewhere, has a click
	// that shows nothing tried again: on a busy server a placement is
	// sometimes dropped, and one dropped click would hide a whole face.
	retryMisses bool
}

// placeWait is how long a click may take to show a block before it is called
// a no-op. A placement is answered in 80 to 125 ms; a loaded server needs
// more, which --place-wait sets.
var placeWait = 350 * time.Millisecond

// missRetries is how many more times a click that placed nothing is sent.
var missRetries = 2

// trial places item with the click, reads what appeared at the target, and
// puts the bay back.
func (w *worker) trial(ctx context.Context, name string, c click) (outcome, error) {
	if err := ctx.Err(); err != nil {
		return outcome{}, err
	}
	if !w.u.alive() {
		return outcome{}, w.u.failure()
	}
	if err := w.r.spend(); err != nil {
		return outcome{}, err
	}
	against, target := w.bay.site(c)
	tries := 1
	if w.retryMisses {
		tries += missRetries
	}
	var o outcome
	for try := 0; try < tries && !o.Placed; try++ {
		if try > 0 {
			if err := w.r.spend(); err != nil {
				return outcome{}, err
			}
		}
		w.u.setLook(ctx, c)
		if err := w.click(ctx, against, c.Face, hitVec(c.Face, c.Hit), name); err != nil {
			w.restore(context.WithoutCancel(ctx))
			return outcome{}, fmt.Errorf("click: %w", err)
		}
		if w.u.waitFor(ctx, placeWait, func() bool { return !w.u.isAir(target) }) {
			time.Sleep(settle)
			o = w.withSecondHalf(w.u.read(target), target)
			if o.Unmapped {
				w.log("%s placed a block the vocabulary does not know (%s)", name, o.Name)
			}
		}
		w.restore(context.WithoutCancel(ctx))
	}
	return o, nil
}

// restore breaks everything in the scan area, top first so nothing sits on a
// block that is gone, and sets a fixture back if it is gone.
func (w *worker) restore(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for pass := 0; pass < 3; pass++ {
		stray := w.stray()
		if len(stray) == 0 {
			break
		}
		for _, p := range stray {
			if !w.u.breakAt(ctx, p) {
				// The console is the last resort for a block that will not
				// break; the cell is cleaned before the next trial either way.
				_, _ = w.r.con.Exec(setAir(p))
				w.u.waitFor(ctx, time.Second, func() bool { return w.u.isAir(p) })
			}
		}
	}
	for _, f := range w.bay.fixtures() {
		if h, ok := w.u.hash(f); ok && h == airHash {
			w.log("fixture %v was gone; setting it again", f)
			_, _ = w.r.con.Exec(setStone(f))
			w.u.waitFor(ctx, 2*time.Second, func() bool { return !w.u.isAir(f) })
		}
	}
}

// stray lists the cells of the scan area that hold a block, highest first.
func (w *worker) stray() []protocol.BlockPos {
	var out []protocol.BlockPos
	for _, p := range w.bay.scan() {
		if h, ok := w.u.hash(p); ok && h != airHash {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Y() > out[j].Y() })
	return out
}

// firstPass runs the probe clicks, then the look clicks around the baseline.
func (w *worker) firstPass(ctx context.Context, name string, hasBlock bool, ir *itemResult) (res map[string]outcome, c0 click, ok bool, err error) {
	res = map[string]outcome{}
	run := func(c click) error {
		o, err := w.trial(ctx, name, c)
		if err != nil {
			return err
		}
		ir.trials++
		res[c.key()] = o
		ir.add(c, o)
		return nil
	}
	// An item that is not a block item is tried on two faces only: most are
	// tools and food, and each miss costs the wait for an answer.
	limit := len(faceOrder)
	if !hasBlock {
		limit = 2
	}
	for _, f := range faceOrder[:limit] {
		c := click{Face: f, Hit: hMid}
		if err := run(c); err != nil {
			return nil, c0, false, err
		}
		if res[c.key()].Placed {
			c0, ok = c, true
			break
		}
	}
	if !ok {
		// Beds, plants and the like need flat ground around the block.
		c := click{Face: fTop, Hit: hMid, Lane: true}
		if err := run(c); err != nil {
			return nil, c0, false, err
		}
		if !res[c.key()].Placed {
			// Cactus, carrots and the like need sand, farmland or the like
			// under them; offer each soil in turn before calling the item
			// unplaceable.
			if !hasBlock && !soilItem(name) {
				return res, c0, false, nil
			}
			if err := w.findSoil(ctx, ir, func() (bool, error) {
				if err := run(c); err != nil {
					return false, err
				}
				return res[c.key()].Placed, nil
			}); err != nil {
				return nil, c0, false, err
			}
			if ir.soil == "" {
				return res, c0, false, nil
			}
		}
		c0, ok = c, true
	}
	// A block with a single state cannot depend on the click, whatever its
	// item does elsewhere, so the rest of the probes would only repeat it.
	o := res[c0.key()]
	w.retryMisses = true
	if o.Unmapped || !w.r.vocab.hasStates(o.Name) {
		return res, c0, true, nil
	}
	for _, c := range probeClicks(c0.Lane) {
		if _, done := res[c.key()]; done {
			continue
		}
		if err := run(c); err != nil {
			return nil, c0, false, err
		}
	}
	// The baseline may be a face whose probes ran first; settle it on the
	// full set.
	if !c0.Lane {
		c0, _ = baseline(res)
	}
	for _, c := range lookClicks(c0) {
		if err := run(c); err != nil {
			return nil, c0, false, err
		}
	}
	return res, c0, true, nil
}

// sixteenWay names the state key of blocks that turn in 16 steps of 22.5
// degrees; the 90 degree yaws of the matrix reach only four of them.
const sixteenWay = "ground_sign_direction"

// rotations tries the yaws between the matrix's, for a block that turns in 16
// steps, around the baseline click.
func (w *worker) rotations(ctx context.Context, name, block string, c0 click, ir *itemResult) error {
	if !w.r.vocab.has(block, sixteenWay) {
		return nil
	}
	for _, y := range sixteenYaws() {
		c := c0
		c.Yaw = y
		o, err := w.trial(ctx, name, c)
		if err != nil {
			return err
		}
		ir.trials++
		ir.add(c, o)
	}
	return nil
}

// soilItem says an item is a plant the creative inventory lists without a
// block form, which only grows on sand, farmland and the like.
func soilItem(name string) bool {
	for _, part := range []string{"cactus", "carrot", "potato", "beetroot", "wheat", "melon_seeds", "pumpkin_seeds", "torchflower_seeds", "pitcher_pod", "sugar_cane", "kelp", "nether_wart", "chorus", "cocoa", "sweet_berries"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// altSoils are the floors tried under an item the dirt lane did not take.
var altSoils = []string{"sand", "farmland", "end_stone", "soul_sand", "grass_block"}

// setSoil changes the block under the lane cell.
func (w *worker) setSoil(ctx context.Context, soil string) error {
	p := floorUnder(w.bay.lane())
	out, err := w.r.con.Exec(fmt.Sprintf("setblock %d %d %d %s", p.X(), p.Y(), p.Z(), soil))
	if err != nil {
		return fmt.Errorf("soil %s: %w (%s)", soil, err, strings.TrimSpace(out))
	}
	time.Sleep(150 * time.Millisecond)
	return ctx.Err()
}

func (w *worker) resetSoil(ctx context.Context) error { return w.setSoil(ctx, "dirt") }

// findSoil offers each alternate soil to try in turn and records the first
// that works in ir.soil, leaving it in place for the item's later trials
// (calibrate resets it afterwards). When none works, or a step fails, the
// lane floor goes back to dirt here, since calibrate resets only after a
// success.
func (w *worker) findSoil(ctx context.Context, ir *itemResult, try func() (bool, error)) (err error) {
	defer func() {
		if ir.soil != "" && err == nil {
			return
		}
		ir.soil = ""
		if rerr := w.resetSoil(context.WithoutCancel(ctx)); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}()
	for _, soil := range altSoils {
		if err := w.setSoil(ctx, soil); err != nil {
			return err
		}
		placed, err := try()
		if err != nil {
			return err
		}
		if placed {
			ir.soil = soil
			return nil
		}
	}
	return nil
}

// withSecondHalf folds in what the other cell of a two-cell block says. A
// door's hinge is stored on the upper half; the clicked cell is the lower.
func (w *worker) withSecondHalf(o outcome, cell protocol.BlockPos) outcome {
	if !o.Placed || o.Unmapped {
		return o
	}
	if _, door := o.Props["door_hinge_bit"]; !door {
		return o
	}
	if _, upper := o.Props["upper_block_bit"]; !upper {
		return o
	}
	up := w.u.read(protocol.BlockPos{cell.X(), cell.Y() + 1, cell.Z()})
	if !up.Placed || up.Unmapped {
		return o
	}
	merged := make(map[string]any, len(o.Props))
	for k, v := range o.Props {
		merged[k] = v
	}
	if v, ok := up.Props["door_hinge_bit"]; ok {
		merged["door_hinge_bit"] = v
	}
	o.Props = merged
	return o
}

// byConstruction names the state keys present in props that the placement of
// the other half decides, not the click: a door's upper half, a bed's head
// and whether anyone sleeps in it.
func byConstruction(block string, props map[string]any) []string {
	var out []string
	// A plate's signal follows whatever stands on it (the unit's own feet
	// read 1, nothing reads 0), never the click.
	if _, ok := props["redstone_signal"]; ok && strings.HasSuffix(block, "pressure_plate") {
		out = append(out, "redstone_signal")
	}
	for _, k := range []string{"head_piece_bit", "occupied_bit", "upper_block_bit"} {
		if _, ok := props[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// calibrate runs one item to completion.
func (w *worker) calibrate(ctx context.Context, it item) (_ *itemResult, err error) {
	ir := newItemResult(it.name)
	defer func() {
		if ir.soil != "" {
			if rerr := w.resetSoil(context.WithoutCancel(ctx)); rerr != nil {
				err = errors.Join(err, rerr)
			}
		}
	}()
	w.retryMisses = false
	if err := w.stockHotbar(ctx); err != nil {
		return nil, err
	}
	w.slots++
	res, c0, ok, err := w.firstPass(ctx, it.name, it.hasBlock, ir)
	if err != nil {
		return nil, err
	}
	if !ok {
		ir.unplaced = true
		return ir, nil
	}
	o0 := res[c0.key()]
	if o0.Unmapped || !w.r.vocab.hasStates(o0.Name) {
		return ir, nil
	}
	d := analyse(res, c0)
	clicks, truncated := grid(d, res, c0)
	ir.truncated = truncated
	sig := signature(res, c0)
	if err := w.sweep(ctx, it.name, o0.Name, sig, clicks, res, ir); err != nil {
		return nil, err
	}
	if err := w.rotations(ctx, it.name, o0.Name, c0, ir); err != nil {
		return nil, err
	}
	for _, k := range byConstruction(o0.Name, o0.Props) {
		if ir.derived[o0.Name] == nil {
			ir.derived[o0.Name] = map[string]bool{}
		}
		ir.derived[o0.Name][k] = true
	}
	if c0.Face == fTop && !w.r.cfg.skipDerived {
		w.r.mu.Lock()
		keys, known := w.r.derivedBy[sig]
		w.r.mu.Unlock()
		if !known {
			var err error
			w.slots++ // the stone the neighbours are made of
			if keys, err = w.neighbours(ctx, it.name, c0, o0, d.Yaw); err != nil {
				return nil, err
			}
			w.r.mu.Lock()
			if old := w.r.derivedBy[sig]; old != nil {
				for k := range old {
					keys[k] = true
				}
			}
			w.r.derivedBy[sig] = keys
			w.r.mu.Unlock()
		}
		if len(keys) > 0 {
			if ir.derived[o0.Name] == nil {
				ir.derived[o0.Name] = map[string]bool{}
			}
			for k := range keys {
				ir.derived[o0.Name][k] = true
			}
		}
	}
	return ir, nil
}

// sweep runs the full grid, or confirms a sibling's on a sample and copies it.
func (w *worker) sweep(ctx context.Context, name, block, sig string, clicks []click, first map[string]outcome, ir *itemResult) error {
	w.r.mu.Lock()
	fam := w.r.families[sig]
	w.r.mu.Unlock()
	results := map[string]outcome{}
	for k, o := range first {
		results[k] = o
	}
	if fam != nil && !w.r.cfg.noInfer && sameGrid(fam.grid, clicks) {
		agree := true
		for _, c := range sample(clicks, verifyN) {
			if _, have := results[c.key()]; !have {
				o, err := w.trial(ctx, name, c)
				if err != nil {
					return err
				}
				ir.trials++
				results[c.key()] = o
			}
			want, have := fam.results[c.key()]
			if !have || results[c.key()].states() != want.states() {
				agree = false
				break
			}
		}
		if agree {
			for _, c := range clicks {
				o := fam.results[c.key()]
				o.Name = block
				ir.add(c, o)
				results[c.key()] = o
			}
			ir.inferred[block] = true
			return nil
		}
	}
	for _, c := range clicks {
		if _, have := results[c.key()]; have {
			ir.add(c, results[c.key()])
			continue
		}
		o, err := w.trial(ctx, name, c)
		if err != nil {
			return err
		}
		ir.trials++
		results[c.key()] = o
		ir.add(c, o)
	}
	w.r.mu.Lock()
	if w.r.families[sig] == nil {
		w.r.families[sig] = &family{grid: clicks, results: results}
	}
	w.r.mu.Unlock()
	return nil
}

func sameGrid(a, b []click) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// neighbours finds the state keys that follow the blocks around, not the
// click. The block is placed on bare floor with the same click each time and
// the cells around it filled differently; a key that changes is derived.
// A block whose state follows the unit's yaw is also surrounded by copies of
// itself turned to the sides, which is what makes stairs bend.
func (w *worker) neighbours(ctx context.Context, name string, c0 click, base outcome, turns bool) (map[string]bool, error) {
	lane := w.bay.lane()
	ring := w.bay.laneNeighbours()
	type cell struct {
		f     face
		block string
		yaw   int
	}
	configs := [][]cell{
		{{fEast, "stone", 0}},
		{{fEast, "stone", 0}, {fWest, "stone", 0}, {fNorth, "stone", 0}, {fSouth, "stone", 0}},
		{{fEast, name, 0}},
		{{fNorth, name, 0}, {fSouth, name, 0}},
	}
	if turns {
		for _, y := range []int{90, 270} {
			configs = append(configs, []cell{{fNorth, name, y}}, []cell{{fSouth, name, y}}, []cell{{fNorth, name, y}, {fSouth, name, 360 - y}})
		}
	}
	// The baseline is the same click on the same floor and read the same way,
	// with nothing around: comparing it with the first-pass read (beside a
	// stone fixture, after 60 ms) would call every slow-settling key derived.
	if b, err := w.placeAt(ctx, name, c0, lane); err != nil {
		return nil, err
	} else if b.Placed && !b.Unmapped {
		base = b
	}
	w.restore(context.WithoutCancel(ctx))
	keys := map[string]bool{}
	for _, cfg := range configs {
		for _, n := range cfg {
			if err := w.placeOnFloor(ctx, n.block, ring[n.f], n.yaw); err != nil {
				return nil, err
			}
		}
		o, err := w.placeAt(ctx, name, c0, lane)
		if err != nil {
			return nil, err
		}
		// A config that left the block unplaced, as a bed with no room for its
		// head, says nothing about its states.
		if !o.Placed || o.Unmapped {
			w.restore(context.WithoutCancel(ctx))
			continue
		}
		diff := diffKeys(base.Props, o.Props)
		if len(diff) > 0 && os.Getenv("PLACECAL_TRACE") != "" {
			w.log("%s neighbours %+v: %v -> %v", name, cfg, base.Props, o.Props)
		}
		for k := range diff {
			keys[k] = true
		}
		w.restore(context.WithoutCancel(ctx))
	}
	return keys, nil
}

// placeOnFloor puts block into the cell by clicking the floor below it.
func (w *worker) placeOnFloor(ctx context.Context, block string, cell protocol.BlockPos, yaw int) error {
	if err := w.r.spend(); err != nil {
		return err
	}
	w.u.setLook(ctx, click{Yaw: yaw})
	if err := w.click(ctx, floorUnder(cell), fTop, hitVec(fTop, hMid), block); err != nil {
		return fmt.Errorf("neighbour %s: %w", block, err)
	}
	w.u.waitFor(ctx, placeWait, func() bool { return !w.u.isAir(cell) })
	return nil
}

// placeAt places item with the click c, whose face is the top one, into the
// lane cell, clicking the floor below.
func (w *worker) placeAt(ctx context.Context, name string, c click, cell protocol.BlockPos) (outcome, error) {
	if err := w.r.spend(); err != nil {
		return outcome{}, err
	}
	// A neighbour bed's head or a door's upper half can land in the cell.
	if !w.u.isAir(cell) && !w.u.breakAt(ctx, cell) {
		return outcome{}, nil
	}
	w.u.setLook(ctx, c)
	if err := w.click(ctx, floorUnder(cell), c.Face, hitVec(c.Face, c.Hit), name); err != nil {
		return outcome{}, fmt.Errorf("click: %w", err)
	}
	if w.u.waitFor(ctx, placeWait, func() bool { return !w.u.isAir(cell) }) {
		return w.withSecondHalf(w.stableRead(ctx, cell, 250*time.Millisecond), cell), nil
	}
	return outcome{}, nil
}

// stableRead waits at least min after a block shows, then until its hash has
// not changed for a while, so a neighbour update that lands late on a busy
// server is read too.
func (w *worker) stableRead(ctx context.Context, p protocol.BlockPos, min time.Duration) outcome {
	time.Sleep(min)
	last, _ := w.u.hash(p)
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		time.Sleep(80 * time.Millisecond)
		h, _ := w.u.hash(p)
		if h == last {
			break
		}
		last = h
	}
	return w.u.read(p)
}

// diffKeys names the keys whose values differ between two states of a block.
func diffKeys(a, b map[string]any) map[string]bool {
	out := map[string]bool{}
	for k, v := range a {
		if w, ok := b[k]; !ok || !place.SameValue(v, w) {
			out[k] = true
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out[k] = true
		}
	}
	return out
}

// stockHotbar clears the hotbar when it is nearly full, so the next take has
// a free slot. The unit counts its own takes: the server never names a taken
// item, so no packet tells belief a slot is used until a click is answered.
func (w *worker) stockHotbar(ctx context.Context) error {
	if w.slots < hotbarLimit {
		return nil
	}
	return w.clearHotbar(ctx)
}

// clearHotbar empties the unit's inventory through the lab console, the only
// way to do it.
func (w *worker) clearHotbar(ctx context.Context) error {
	out, err := w.r.con.Exec(fmt.Sprintf("clear %q", w.u.name))
	if err != nil {
		return fmt.Errorf("clear: %w (%s)", err, strings.TrimSpace(out))
	}
	// Let the inventory packet reach the actuator.
	time.Sleep(400 * time.Millisecond)
	w.slots = 0
	return ctx.Err()
}

// click is unit.click that clears a full hotbar and tries once more.
func (w *worker) click(ctx context.Context, against protocol.BlockPos, f face, hit [3]float32, item string) error {
	err := w.u.click(ctx, against, f, hit, item)
	if errors.Is(err, act.ErrHotbarFull) {
		if cerr := w.clearHotbar(ctx); cerr != nil {
			return cerr
		}
		err = w.u.click(ctx, against, f, hit, item)
	}
	return err
}

// isFatal says an error ends the worker rather than the item.
func isFatal(err error) bool {
	return errors.Is(err, errBudget) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, act.ErrNotCreative)
}
