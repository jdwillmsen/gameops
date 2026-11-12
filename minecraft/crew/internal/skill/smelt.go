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
)

const (
	// cookTicks is what a furnace spends on one item: ten seconds.
	cookTicks = 200
	// cookSlack is the wait added to the cooking time for a server that runs
	// its ticks slower than the unit sends inputs.
	cookSlack = 100
	// collectRounds is how many times the unit comes back for the result before
	// it calls the furnace broken.
	collectRounds = 3
	// maxSmelt is the most one run smelts: a furnace takes one stack.
	maxSmelt = 64
	// halfSmelts per smelt: fuel is valued in half-smelts so planks (1.5) stay
	// whole numbers.
	halfPerSmelt = 2
)

// smeltOutputs is every item a furnace turns into another that the unit is
// allowed to ask for, and what it becomes. It is closed: an item missing here
// is not a request the unit will take on.
var smeltOutputs = map[string]string{
	"raw_iron": "iron_ingot", "raw_copper": "copper_ingot", "raw_gold": "gold_ingot",
	"cobblestone": "stone", "sand": "glass", "clay_ball": "brick", "oak_log": "charcoal",
	"beef": "cooked_beef", "porkchop": "cooked_porkchop", "chicken": "cooked_chicken",
	"mutton": "cooked_mutton", "rabbit": "cooked_rabbit", "cod": "cooked_cod",
	"salmon": "cooked_salmon", "potato": "baked_potato", "kelp": "dried_kelp",
}

// SmeltOutput is what a furnace makes of an item.
func SmeltOutput(input string) (string, bool) {
	out, ok := smeltOutputs[input]
	return out, ok
}

// SmeltInputs lists the items SmeltOutput knows, sorted.
func SmeltInputs() []string {
	names := make([]string, 0, len(smeltOutputs))
	for n := range smeltOutputs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// fuelValue is how many half-smelts one item burns for: a coal smelts eight
// items, a plank one and a half. Zero is not a fuel.
func fuelValue(name string) int {
	switch name {
	case "coal", "charcoal":
		return 16
	case "coal_block":
		return 160
	case "blaze_rod":
		return 24
	case "dried_kelp_block":
		return 40
	case "stick":
		return 1
	}
	if strings.HasSuffix(name, "_planks") || strings.HasSuffix(name, "_log") {
		return 3
	}
	return 0
}

// FuelFor picks the fuel a run of qty smelts burns: the first stack of the
// unit's, in order of preference (charcoal, coal, then wood), that holds
// enough on its own, and how many of it. The input being smelted is never
// burnt, and a stack too small is passed over for the next rather than mixed:
// a furnace's fuel slot holds one kind.
func FuelFor(s belief.Snapshot, input string, qty int) (string, int, bool) {
	need := qty * halfPerSmelt
	rank := func(n string) int {
		switch n {
		case "charcoal":
			return 0
		case "coal":
			return 1
		}
		return 2
	}
	have := map[string]int{}
	var names []string
	for _, it := range s.Inventory {
		if it.Name == input || fuelValue(it.Name) == 0 {
			continue
		}
		if have[it.Name] == 0 {
			names = append(names, it.Name)
		}
		have[it.Name] += it.Count
	}
	sort.SliceStable(names, func(i, j int) bool {
		if ri, rj := rank(names[i]), rank(names[j]); ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		v := fuelValue(n)
		if k := (need + v - 1) / v; have[n] >= k {
			return n, k, true
		}
	}
	return "", 0, false
}

// Furnacer is implemented by an actuator that can work a furnace window. It is
// separate from Actuator so a skill that never smelts needs no stand-in for it.
type Furnacer interface {
	FurnaceLoad(from belief.Item, count int, slot act.FurnaceSlot) (int32, error)
	FurnaceTake(slot act.FurnaceSlot, count int, to belief.Item, toSlot int) (int32, error)
	FurnaceWindow() bool
}

type smeltPhase int

const (
	smStart smeltPhase = iota
	smPlace
	smWalk
	smClick
	smLoad
	smCook
	smCollect
	smDrain
)

// load is one thing to put in the furnace.
type load struct {
	item  string
	n     int
	slot  act.FurnaceSlot
	moved bool
}

// Smelt cooks qty of an item in the furnace at a cell. If the cell is empty it
// first sets down a furnace the unit carries. It loads the item and fuel, waits
// out the cooking with the window closed, and comes back for the result.
type Smelt struct {
	world   ChestWorld
	self    func() belief.Snapshot
	furnace protocol.BlockPos
	input   string
	output  string
	qty     int
	log     Logger

	phase   smeltPhase
	started bool
	base    int
	sub     Skill
	nav     *navigator

	loads   []load
	fuel    string
	loaded  bool
	waited  int
	until   int
	clicks  int
	rounds  int
	opened  bool
	collect bool

	pending  bool
	reqID    int32
	before   int
	settling bool
	drainErr error
}

// NewSmelt smelts qty of input in the furnace at the cell.
func NewSmelt(world ChestWorld, self func() belief.Snapshot, furnace protocol.BlockPos, input string, qty int, opts ...Option) (Skill, error) {
	output, ok := SmeltOutput(input)
	if !ok {
		return nil, fmt.Errorf("%s is not something a furnace smelts", input)
	}
	if qty < 1 || qty > maxSmelt {
		return nil, fmt.Errorf("qty must be 1 to %d", maxSmelt)
	}
	var o options
	for _, f := range opts {
		f(&o)
	}
	return &Smelt{world: world, self: self, furnace: furnace, input: input, output: output, qty: qty, log: o.log}, nil
}

func (s *Smelt) Name() string { return "smelt" }

// Progress counts the items smelted and collected.
func (s *Smelt) Progress() int {
	if !s.started {
		return 0
	}
	return max(count(s.self(), s.output)-s.base, 0)
}

func (s *Smelt) Ready(sn belief.Snapshot) error {
	if err := overworld(sn); err != nil {
		return err
	}
	if s.loaded {
		return nil
	}
	if count(sn, s.input) < s.qty {
		return fmt.Errorf("no %s in inventory", s.input)
	}
	if _, _, ok := FuelFor(sn, s.input, s.qty); !ok {
		return errors.New("no fuel in inventory")
	}
	if !s.hasFurnace() && count(sn, "furnace") == 0 {
		return errors.New("no furnace in inventory")
	}
	return nil
}

func (s *Smelt) hasFurnace() bool {
	name, _ := s.world.Block(s.furnace)
	return name == "furnace" || name == "lit_furnace"
}

func (s *Smelt) emit(event string, fields map[string]any) {
	if s.log != nil {
		s.log.Info(event, fields)
	}
}

// Resume takes the smelt back up where a fight left it. A furnace already
// loaded goes on cooking while the unit is away, so it only has to wait and
// collect; one half loaded is loaded again from what the inventory still holds.
func (s *Smelt) Resume() {
	s.nav, s.sub, s.opened, s.clicks, s.pending, s.settling, s.drainErr = nil, nil, false, 0, false, false, nil
	switch {
	case s.phase == smCook:
		// The furnace kept cooking while the unit was away; the wait goes on.
	case s.loaded || s.collect:
		s.phase, s.waited = smWalk, 0
	case s.phase == smPlace:
		s.phase, s.waited = smStart, 0
	default:
		s.phase, s.waited = smWalk, 0
	}
}

func (s *Smelt) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return s.failed(a, err)
	}
	return outcome{st: Running}
}

func (s *Smelt) fail(a Actuator, err error) (Status, error) {
	if open, _, pos := a.Container(); open && pos == s.furnace {
		_ = a.CloseContainer()
	}
	return Failed, err
}

func (s *Smelt) failed(a Actuator, err error) outcome {
	st, err := s.fail(a, err)
	return outcome{st: st, err: err}
}

// Stop shuts the furnace window when the skill is abandoned.
func (s *Smelt) Stop(a Actuator) {
	_, _ = s.fail(a, nil)
	if st, ok := s.sub.(Stopper); ok {
		st.Stop(a)
	}
}

func (s *Smelt) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return s.fail(a, err)
	}
	snap := s.self()
	if err := overworld(snap); err != nil {
		return s.fail(a, err)
	}
	fa, ok := a.(Furnacer)
	if !ok {
		return s.fail(a, errors.New("this unit cannot work a furnace"))
	}
	if !s.started {
		s.started, s.base = true, count(snap, s.output)
	}
	for {
		var o outcome
		switch s.phase {
		case smStart:
			o = s.start(snap)
		case smPlace:
			o = s.place(ctx, a)
		case smWalk:
			o = s.walk(a)
		case smClick:
			o = s.click(a, fa, snap)
		case smLoad:
			o = s.loadStep(a, fa, snap)
		case smCook:
			o = s.cook(a)
		case smCollect:
			o = s.collectStep(a, fa, snap)
		case smDrain:
			o = s.drain(a)
		}
		if !o.again {
			return o.st, o.err
		}
	}
}

// start decides whether a furnace has to be set down first.
func (s *Smelt) start(snap belief.Snapshot) outcome {
	switch {
	case s.hasFurnace():
		s.phase = smWalk
	case count(snap, "furnace") > 0:
		s.sub = NewPlaceBlock(s.world, s.self, s.furnace, "furnace", WithLogger(s.log))
		s.phase = smPlace
	default:
		return outcome{st: Failed, err: errors.New("no furnace in inventory")}
	}
	return next
}

func (s *Smelt) place(ctx context.Context, a Actuator) outcome {
	st, err := s.sub.Step(ctx, a)
	switch st {
	case Failed:
		return s.failed(a, fmt.Errorf("placing the furnace: %w", err))
	case Done:
		s.sub, s.phase = nil, smWalk
		s.emit("smelt_furnace_placed", nil)
		return next
	}
	return outcome{st: Running}
}

// walk brings the unit to a cell from which the furnace can be clicked.
func (s *Smelt) walk(a Actuator) outcome {
	if !s.hasFurnace() {
		return s.failed(a, errors.New("no furnace at the target"))
	}
	p := a.Pose()
	if s.nav == nil {
		if centre(s.furnace).Sub(eyeOf(p.Feet)).Len() <= chestRange {
			s.phase = smClick
			return next
		}
		stand, ok := standCell(s.world, s.furnace, p)
		if !ok {
			return s.failed(a, errors.New("no standing spot within reach of the furnace"))
		}
		s.nav = newNavigator(s.world, stand, arriveSlack)
	}
	st, err := s.nav.step(a)
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		s.nav, s.phase = nil, smClick
		return next
	}
	s.nav = nil
	return s.failed(a, fmt.Errorf("getting to the furnace: %w", err))
}

func (s *Smelt) clickOn(a Actuator, snap belief.Snapshot) error {
	held := holdable(snap, "")
	hash, known := s.world.BlockHash(s.furnace)
	if !known {
		return errors.New("the furnace's block is not known")
	}
	p := a.Pose()
	if err := a.SelectHotbar(held.Slot, held); err != nil {
		return err
	}
	return a.OpenChest(s.furnace, faceToward(s.furnace, eyeOf(p.Feet)), held.Slot, held, hash)
}

// click opens the furnace and waits for its window, clicking again if the
// first click is swallowed, as a chest's is.
func (s *Smelt) click(a Actuator, fa Furnacer, snap belief.Snapshot) outcome {
	if !s.opened {
		if err := s.clickOn(a, snap); err != nil {
			return s.failed(a, err)
		}
		s.opened, s.waited, s.clicks = true, 0, 1
		return s.idle(a)
	}
	if open, _, pos := a.Container(); open && pos == s.furnace && fa.FurnaceWindow() {
		if s.collect {
			s.phase = smCollect
		} else if err := s.prepare(a, snap); err != nil {
			return s.failed(a, err)
		} else {
			s.phase = smLoad
		}
		s.waited = 0
		return next
	}
	if s.waited++; s.waited >= answerTicks {
		s.drainErr = fmt.Errorf("furnace did not open within %d ticks", answerTicks)
		s.phase, s.waited = smDrain, 0
		return next
	}
	if s.clicks < openClicks && s.waited%redoOpenTicks == 0 {
		if err := s.clickOn(a, snap); err != nil {
			return s.failed(a, err)
		}
		s.clicks++
	}
	return s.idle(a)
}

// drain waits a few more ticks for a late window, so one that opens after the
// wait gave up is shut rather than left open.
func (s *Smelt) drain(a Actuator) outcome {
	if open, _, _ := a.Container(); open || s.waited >= drainTicks {
		return s.failed(a, s.drainErr)
	}
	s.waited++
	return s.idle(a)
}

// prepare works out what still has to go in, from what the furnace already
// holds: a run that was interrupted after loading, or part way through it,
// finds the ingredient and fuel it left and adds only what is missing.
func (s *Smelt) prepare(a Actuator, snap belief.Snapshot) error {
	held := func(slot act.FurnaceSlot) int {
		if c := a.ContainerContent(); int(slot) < len(c) {
			return int(c[slot].Stack.Count)
		}
		return 0
	}
	names := itemNames(a.ItemTable())
	holds := func(slot act.FurnaceSlot) string {
		if c := a.ContainerContent(); int(slot) < len(c) && c[slot].Stack.Count > 0 {
			return names[c[slot].Stack.NetworkID]
		}
		return ""
	}
	if in := holds(act.FurnaceIngredient); in != "" && in != s.input {
		return errors.New("the furnace slot holds something else")
	}
	if fuel := holds(act.FurnaceFuel); fuel != "" && fuelValue(fuel) == 0 {
		return errors.New("the furnace slot holds something else")
	}
	total := s.qty - s.smelted(snap)
	s.loads = nil
	if need := total - held(act.FurnaceIngredient); need > 0 {
		if count(snap, s.input) < need {
			return fmt.Errorf("no %s in inventory", s.input)
		}
		s.loads = append(s.loads, load{item: s.input, n: need, slot: act.FurnaceIngredient})
	}
	if held(act.FurnaceFuel) == 0 {
		fuel, n, ok := FuelFor(snap, s.input, total)
		if !ok {
			return errors.New("no fuel in inventory")
		}
		s.fuel = fuel
		s.loads = append(s.loads, load{item: fuel, n: n, slot: act.FurnaceFuel})
	}
	return nil
}

// smelted is how much of the output the unit has gained since it began.
func (s *Smelt) smelted(snap belief.Snapshot) int { return max(count(snap, s.output)-s.base, 0) }

// stackOf finds the biggest inventory stack of an item.
func stackOf(snap belief.Snapshot, item string) (belief.Item, bool) {
	var best belief.Item
	for _, it := range snap.Inventory {
		if it.Name == item && it.Count > best.Count {
			best = it
		}
	}
	return best, best.Count > 0
}

// loadStep puts the ingredient and the fuel in, one request at a time.
func (s *Smelt) loadStep(a Actuator, fa Furnacer, snap belief.Snapshot) outcome {
	if open, _, pos := a.Container(); !open || pos != s.furnace {
		return s.failed(a, errors.New("furnace closed before it was loaded"))
	}
	if s.pending {
		status, answered := a.Response(s.reqID)
		if !answered {
			if s.waited++; s.waited >= answerTicks {
				return s.failed(a, fmt.Errorf("no answer to the furnace request within %d ticks", answerTicks))
			}
			return s.idle(a)
		}
		s.pending = false
		switch status {
		case act.StatusOK:
			s.settling, s.waited = true, 0
		case act.StatusOccupied:
			return s.failed(a, errors.New("the furnace slot holds something else"))
		case act.StatusBadSource:
			return s.failed(a, errors.New("server rejected the source stack"))
		default:
			return s.failed(a, fmt.Errorf("server rejected the furnace request with status %d", status))
		}
	}
	if s.settling {
		cur := s.loads[s.current()]
		if count(snap, cur.item) > s.before-cur.n {
			if s.waited++; s.waited >= answerTicks {
				return s.failed(a, fmt.Errorf("inventory did not show the %s loaded within %d ticks", cur.item, answerTicks))
			}
			return s.idle(a)
		}
		s.loads[s.current()].moved, s.settling = true, false
	}
	i := s.current()
	if i >= len(s.loads) {
		return s.finishLoading(a)
	}
	l := s.loads[i]
	from, ok := stackOf(snap, l.item)
	if !ok || from.Count < l.n {
		return s.failed(a, fmt.Errorf("no %s in inventory", l.item))
	}
	id, err := fa.FurnaceLoad(from, l.n, l.slot)
	if err != nil {
		return s.failed(a, err)
	}
	s.pending, s.reqID, s.before, s.waited = true, id, count(snap, l.item), 0
	return s.idle(a)
}

// current is the index of the first load not yet moved.
func (s *Smelt) current() int {
	for i, l := range s.loads {
		if !l.moved {
			return i
		}
	}
	return len(s.loads)
}

// finishLoading shuts the window, so the furnace cooks without the unit in it,
// and starts the wait.
func (s *Smelt) finishLoading(a Actuator) outcome {
	if err := a.CloseContainer(); err != nil {
		return s.failed(a, err)
	}
	s.loaded, s.opened, s.clicks = true, false, 0
	s.until, s.waited, s.phase = s.cookWait(s.qty-s.smelted(s.self())), 0, smCook
	s.emit("smelt_loaded", map[string]any{"item": s.input, "qty": s.qty, "fuel": s.fuel})
	return s.idle(a)
}

// cookWait is how long n items take, with slack.
func (s *Smelt) cookWait(n int) int { return n*cookTicks + cookSlack }

func (s *Smelt) cook(a Actuator) outcome {
	if s.waited++; s.waited < s.until {
		return s.idle(a)
	}
	s.collect, s.phase = true, smWalk
	return next
}

// collectStep takes what the furnace made. A result slot that is still empty,
// or short of the amount asked for, sends the unit back to wait a while longer.
func (s *Smelt) collectStep(a Actuator, fa Furnacer, snap belief.Snapshot) outcome {
	if open, _, pos := a.Container(); !open || pos != s.furnace {
		return s.failed(a, errors.New("furnace closed before the result was taken"))
	}
	if s.pending {
		status, answered := a.Response(s.reqID)
		if !answered {
			if s.waited++; s.waited >= answerTicks {
				return s.failed(a, fmt.Errorf("no answer to the furnace request within %d ticks", answerTicks))
			}
			return s.idle(a)
		}
		s.pending = false
		if status != act.StatusOK {
			return s.failed(a, fmt.Errorf("server rejected taking the result with status %d", status))
		}
		s.settling, s.waited = true, 0
	}
	if s.settling {
		if count(snap, s.output) <= s.before {
			if s.waited++; s.waited >= answerTicks {
				return s.failed(a, fmt.Errorf("inventory did not show the %s within %d ticks", s.output, answerTicks))
			}
			return s.idle(a)
		}
		s.settling = false
	}
	if s.smelted(snap) >= s.qty {
		return s.finish(a)
	}
	content := a.ContainerContent()
	var res protocol.ItemInstance
	if int(act.FurnaceResult) < len(content) {
		res = content[act.FurnaceResult]
	}
	if res.Stack.Count > 0 {
		to, room, ok := slotFor(snap, s.output)
		if !ok {
			return s.failed(a, errors.New("no room in the inventory for the result"))
		}
		n := min(int(res.Stack.Count), room, s.qty-s.smelted(snap))
		id, err := fa.FurnaceTake(act.FurnaceResult, n, to, to.Slot)
		if err != nil {
			return s.failed(a, err)
		}
		s.pending, s.reqID, s.before, s.waited = true, id, count(snap, s.output), 0
		return s.idle(a)
	}
	// Nothing to take yet: come back after another wait, up to a few times.
	if s.rounds++; s.rounds >= collectRounds {
		return s.failed(a, fmt.Errorf("the furnace made no %s", s.output))
	}
	if err := a.CloseContainer(); err != nil {
		return s.failed(a, err)
	}
	s.collect, s.opened, s.clicks = false, false, 0
	s.until, s.waited, s.phase = s.cookWait(s.qty-s.smelted(snap)), 0, smCook
	s.emit("smelt_wait", map[string]any{"round": s.rounds})
	return s.idle(a)
}

func (s *Smelt) finish(a Actuator) outcome {
	if err := a.CloseContainer(); err != nil {
		return s.failed(a, err)
	}
	s.emit("smelt_done", map[string]any{"item": s.output, "qty": s.smelted(s.self())})
	return outcome{st: Done}
}

// slotFor finds where the unit can take an item in: onto a stack of it with
// room, else an empty slot. The Item names the stack to merge onto, or only the
// slot when empty.
func slotFor(snap belief.Snapshot, item string) (to belief.Item, room int, ok bool) {
	used := map[int]bool{}
	for _, it := range snap.Inventory {
		used[it.Slot] = true
		if it.Name == item && it.Count < fullStack {
			return it, fullStack - it.Count, true
		}
	}
	for slot := 0; slot < inventorySlots; slot++ {
		if !used[slot] {
			return belief.Item{Slot: slot}, fullStack, true
		}
	}
	return belief.Item{}, 0, false
}
