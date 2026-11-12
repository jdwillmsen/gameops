package skill

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
)

const (
	// shelterRadius is how far the unit will walk, horizontally, for a spot
	// better than where it stands.
	shelterRadius = 16
	// shelterRise is how far above or below its feet a spot may be.
	shelterRise = 3
	// DefaultShelterTicks is one Bedrock night at 20 inputs a second: from
	// dusk to the end of dawn is 12000 game ticks.
	DefaultShelterTicks = 12000
	// MaxShelterTicks leaves room for a night that starts late and a slow
	// server, without letting a task sit for hours.
	MaxShelterTicks = 24000
	// shelterWalkFails is how many spots may be given up on before the skill
	// does.
	shelterWalkFails = 3
	// shelterDrift is how far from the middle of its cell the unit may be
	// pushed before it walks back.
	shelterDrift = 0.7
)

type shelterPhase int

const (
	shlPlan shelterPhase = iota
	shlWalk
	shlBuild
	shlConfirm
	shlWait
	shlDig
)

// shelterBlocks is what the unit builds with, best first: whole cubes that do
// not fall and are cheap to carry. Planks are matched by suffix.
var shelterBlocks = []string{"dirt", "cobblestone", "cobbled_deepslate", "stone", "netherrack",
	"andesite", "diorite", "granite", "tuff", "deepslate"}

func isShelterBlock(name string) bool {
	if strings.HasSuffix(name, "_planks") {
		return true
	}
	for _, b := range shelterBlocks {
		if name == b {
			return true
		}
	}
	return false
}

// shelterCell is a candidate stand cell with what it takes to wall it in.
type shelterCell struct {
	cell protocol.BlockPos
	// place lists the cells to fill in the order they go: the feet ring, the
	// head ring, the bridge, the roof.
	place []protocol.BlockPos
	// walls is every cell of the enclosure, placed or already solid, that a
	// breach is read from.
	walls []protocol.BlockPos
	// bridge is the column the roof was reached from, kept shut when the unit
	// leaves, or -1 when the roof needs none.
	bridge int
}

var sideOffsets = [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// Shelter makes the unit safe until dawn without a bed: it walls itself into
// a one-cell room with the blocks it carries, waits there, and digs out when
// the day comes. Walls the terrain already has cost nothing, so a hillside
// takes fewer blocks than open ground.
type Shelter struct {
	world    ChestWorld
	self     func() belief.Snapshot
	maxTicks int
	log      Logger

	phase   shelterPhase
	room    shelterCell
	nav     *navigator
	given   map[protocol.BlockPos]bool
	fails   int
	drifted int

	idx      int
	waitMove int
	retried  bool
	// mine is the cells the shelter itself filled.
	mine map[protocol.BlockPos]bool
	// opened says Stop started breaking the exit.
	opened bool
	item   string
	settle int

	waited int
	exits  []protocol.BlockPos
	brk    cellBreak
	tool   bool
	placed int
}

// NewShelter builds the skill. maxTicks bounds the wait inside the shelter;
// zero means one night.
func NewShelter(world ChestWorld, self func() belief.Snapshot, maxTicks int, opts ...Option) Skill {
	var o options
	for _, f := range opts {
		f(&o)
	}
	if maxTicks <= 0 {
		maxTicks = DefaultShelterTicks
	}
	return &Shelter{world: world, self: self, maxTicks: maxTicks, log: o.log, given: map[protocol.BlockPos]bool{}, mine: map[protocol.BlockPos]bool{}}
}

func (s *Shelter) Name() string { return "shelter" }

func (s *Shelter) Ready(snap belief.Snapshot) error {
	if err := overworld(snap); err != nil {
		return err
	}
	if daytime(snap.Env.DayPhase) {
		return errors.New("it is already day")
	}
	return nil
}

// daytime says the dark is over. An unannounced time counts as night so the
// unit errs towards hiding.
func daytime(phase string) bool { return phase == belief.PhaseDawn || phase == belief.PhaseDay }

// Progress is the number of blocks placed.
func (s *Shelter) Progress() int { return s.placed }

// Resume replans after a fight: where the unit stands and what it holds have
// changed, but the blocks already up still count. A room Stop opened is
// closed again, one block, before the wait goes on.
func (s *Shelter) Resume() {
	s.nav, s.brk, s.tool = nil, cellBreak{}, false
	reseal := s.opened
	s.opened = false
	switch {
	case reseal && s.phase == shlWait:
		if sc, ok := s.assess(s.room.cell); ok {
			s.room.place, s.room.walls, s.idx, s.phase = sc.place, sc.walls, 0, shlBuild
			return
		}
		s.phase = shlPlan
	case s.phase != shlDig && s.phase != shlWait:
		s.phase = shlPlan
	}
}

// Stop is called when the task is cancelled or the unit is pulled off it. The
// room it is waiting in must not outlive the task, or the next one inherits an
// unit walled in, so one input starts breaking the head cell of the exit
// column; the server finishes the break on its own. Resume closes that cell
// again for a task that carries on. Mid-dig the exit is already being opened
// and the break is left running for the same reason.
func (s *Shelter) Stop(a Actuator) {
	switch {
	case s.phase == shlWait && !s.opened:
		exits, err := s.exitCells()
		if err != nil {
			return
		}
		s.opened = true
		eye := eyeOf(a.Pose().Feet)
		yaw, pitch := lookAt(eye, centre(exits[0]))
		_ = a.Tick(act.Input{Yaw: yaw, Pitch: pitch, Break: &act.BreakAction{Pos: exits[0], Face: faceToward(exits[0], eye), Phase: act.BreakStart}})
	case s.phase != shlDig:
		s.abortBreak(a)
	}
}

// abortBreak releases an open break session.
func (s *Shelter) abortBreak(a Actuator) {
	if s.brk.open {
		s.brk.open = false
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch, Break: &act.BreakAction{Pos: s.brk.pos, Phase: act.BreakAbort}})
	}
}

func (s *Shelter) emit(event string, fields map[string]any) {
	if s.log != nil {
		s.log.Info(event, fields)
	}
}

func (s *Shelter) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		s.Stop(a)
		return Failed, err
	}
	snap := s.self()
	if err := overworld(snap); err != nil {
		return Failed, err
	}
	if snap.Dead {
		return Failed, errors.New("unit died")
	}
	for {
		var o outcome
		switch s.phase {
		case shlPlan:
			o = s.plan(a, snap)
		case shlWalk:
			o = s.walk(a)
		case shlBuild:
			o = s.build(a, snap)
		case shlConfirm:
			o = s.confirm(a)
		case shlWait:
			o = s.wait(a, snap)
		case shlDig:
			o = s.dig(a, snap)
		}
		if !o.again {
			if o.st == Failed {
				s.abortBreak(a)
			}
			return o.st, o.err
		}
	}
}

func (s *Shelter) idle(a Actuator) outcome {
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return outcome{st: Failed, err: tickError{err}}
	}
	return outcome{st: Running}
}

// notCubeSuffixes and notCubeNames are blocks that fill less than a cell or
// can be walked or shot through, which the block table does not model and so
// reports as cubes. Anything it does model (slabs, stairs, fences, walls,
// doors, trapdoors, carpets, snow, ladders) is caught by the shape check.
var notCubeSuffixes = []string{"_slab", "_stairs", "_fence", "_fence_gate", "_wall", "_pane", "_door", "_trapdoor",
	"_carpet", "_leaves", "_bed", "_sign", "_hanging_sign", "_banner", "_button", "_pressure_plate", "_sapling",
	"_torch", "_rail", "_head", "_skull", "_candle", "_candle_cake", "_cake", "_anvil", "_pot", "_bars", "_chain",
	"_vines", "_bush", "_coral", "_coral_fan", "_wall_fan", "_dripleaf", "_cluster", "_campfire", "_repeater",
	"_comparator", "_shulker_box", "_chest", "_rod"}

var notCubeNames = map[string]bool{
	"snow_layer": true, "ladder": true, "web": true, "cobweb": true, "torch": true, "vine": true, "glass_pane": true,
	"iron_bars": true, "chain": true, "lantern": true, "soul_lantern": true, "candle": true, "cake": true,
	"cactus": true, "campfire": true, "fire": true, "sweet_berry_bush": true, "bamboo": true, "scaffolding": true,
	"hopper": true, "cauldron": true, "composter": true, "chest": true, "trapped_chest": true, "ender_chest": true,
	"flower_pot": true, "anvil": true, "bell": true, "lectern": true, "enchanting_table": true, "end_portal_frame": true,
	"end_rod": true, "lightning_rod": true, "brewing_stand": true, "stonecutter_block": true, "grindstone": true,
	"conduit": true, "dragon_egg": true, "farmland": true, "dirt_path": true, "grass_path": true, "turtle_egg": true,
	"sea_pickle": true, "decorated_pot": true, "daylight_detector": true, "daylight_detector_inverted": true,
	"lever": true, "tripwire": true, "tripwire_hook": true, "redstone_wire": true, "repeater": true, "comparator": true,
	"sculk_sensor": true, "pointed_dripstone": true, "amethyst_cluster": true, "glow_lichen": true, "sculk_vein": true,
	"frog_spawn": true, "waterlily": true, "kelp": true, "seagrass": true, "mud_brick_slab": true, "soul_sand": true,
	"mud": true, "moss_carpet": true, "heavy_core": true, "end_gateway": true, "end_portal": true, "portal": true,
	"nether_portal": true, "structure_void": true, "barrier": true, "light_block": true,
}

// fullCube says a block is a solid cube that stops a mob and an arrow. A name
// the shape table cannot place as exactly one full box is not one, and the
// deny lists above catch the shapes the table calls full by default.
func fullCube(name string) bool {
	if name == "" || airLike(name) || liquid(name) || notCubeNames[name] {
		return false
	}
	for _, suffix := range notCubeSuffixes {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	boxes := shape.Boxes(name, nil)
	return len(boxes) == 1 && boxes[0] == shape.Full()
}

func airLike(name string) bool {
	switch name {
	case "air", "cave_air", "void_air", "short_grass", "tall_grass", "fern", "large_fern":
		return true
	}
	return false
}

func liquid(name string) bool {
	switch name {
	case "water", "flowing_water", "lava", "flowing_lava":
		return true
	}
	return false
}

// floorOK says a block is safe to stand on for a night.
func floorOK(name string) bool {
	return fullCube(name) && name != "magma_block" && name != "magma" && name != "cactus"
}

func (s *Shelter) at(c protocol.BlockPos) (string, bool) { return s.world.Block(c) }

func off(c protocol.BlockPos, dx, dy, dz int32) protocol.BlockPos {
	return protocol.BlockPos{c.X() + dx, c.Y() + dy, c.Z() + dz}
}

// assess reads the 3x3x3 room around a stand cell and says what walling it in
// takes. It refuses a cell whose floor is not solid, whose body space is not
// open, or whose ring touches a liquid or a gap it cannot close from inside.
func (s *Shelter) assess(c protocol.BlockPos) (shelterCell, bool) {
	sc := shelterCell{cell: c, bridge: -1}
	if n, known := s.at(off(c, 0, -1, 0)); !known || !floorOK(n) {
		return sc, false
	}
	for dy := int32(0); dy <= 1; dy++ {
		if n, known := s.at(off(c, 0, dy, 0)); !known || !(airLike(n) || isEmptyNonHazard(n)) {
			return sc, false
		}
	}
	for dy := int32(0); dy <= 1; dy++ {
		for _, d := range sideOffsets {
			w := off(c, d[0], dy, d[1])
			n, known := s.at(w)
			if !known {
				return sc, false
			}
			switch {
			case fullCube(n):
			case airLike(n):
				if dy == 0 {
					// The block goes on the floor beside the unit, so that floor
					// has to be one to click.
					if f, known := s.at(off(w, 0, -1, 0)); !known || !fullCube(f) {
						return sc, false
					}
				}
				sc.place = append(sc.place, w)
			default:
				return sc, false
			}
			sc.walls = append(sc.walls, w)
		}
	}
	roof := off(c, 0, 2, 0)
	n, known := s.at(roof)
	if !known {
		return sc, false
	}
	switch {
	case fullCube(n):
	case airLike(n):
		// The roof cell sits above the unit's head with no neighbour to click
		// from the inside, so a block goes beside it first and the roof is
		// placed against that.
		hasSide, bridge := false, -1
		for i, d := range sideOffsets {
			bn, bk := s.at(off(roof, d[0], 0, d[1]))
			switch {
			case bk && fullCube(bn):
				hasSide = true
			case bk && airLike(bn) && bridge < 0:
				bridge = i
			}
		}
		if !hasSide {
			if bridge < 0 {
				return sc, false
			}
			sc.bridge = bridge
			d := sideOffsets[bridge]
			sc.place = append(sc.place, off(roof, d[0], 0, d[1]))
		}
		sc.place = append(sc.place, roof)
	default:
		return sc, false
	}
	sc.walls = append(sc.walls, roof)
	return sc, true
}

// isEmptyNonHazard accepts the small plants a unit can stand among.
func isEmptyNonHazard(name string) bool {
	return !liquid(name) && !fullCube(name) && (strings.HasSuffix(name, "_sapling") || name == "dandelion" || name == "poppy" || name == "deadbush")
}

// stock totals the building blocks carried, and the stack names by size.
func stock(snap belief.Snapshot) (total int, byName map[string]int) {
	byName = map[string]int{}
	for _, it := range snap.Inventory {
		if isShelterBlock(it.Name) {
			byName[it.Name] += it.Count
			total += it.Count
		}
	}
	return total, byName
}

// material picks the building block there is most of, so a stack runs out
// cleanly before the next is started.
func material(snap belief.Snapshot) (string, bool) {
	_, by := stock(snap)
	best, bestN := "", 0
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if by[n] > bestN {
			best, bestN = n, by[n]
		}
	}
	return best, best != ""
}

// plan picks the stand cell: where the unit is when that cell will do,
// otherwise the nearest cheaper one within shelterRadius.
func (s *Shelter) plan(a Actuator, snap belief.Snapshot) outcome {
	have, _ := stock(snap)
	if snap.Creative {
		have = math.MaxInt32
	}
	pose := a.Pose()
	here := cellOf(pose.Feet)

	minNeed := -1
	note := func(n int) {
		if minNeed < 0 || n < minNeed {
			minNeed = n
		}
	}
	if sc, ok := s.assess(here); ok && path.Standable(s.world, here) {
		if len(sc.place) <= have {
			return s.settleOn(sc)
		}
		note(len(sc.place))
	}

	type cand struct {
		sc    shelterCell
		score float64
	}
	var best *cand
	for dx := int32(-shelterRadius); dx <= shelterRadius; dx++ {
		for dz := int32(-shelterRadius); dz <= shelterRadius; dz++ {
			dist := math.Hypot(float64(dx), float64(dz))
			if dist > shelterRadius {
				continue
			}
			for dy := int32(-shelterRise); dy <= shelterRise; dy++ {
				c := off(here, dx, dy, dz)
				if s.given[c] || c == here {
					continue
				}
				sc, ok := s.assess(c)
				if !ok {
					continue
				}
				note(len(sc.place))
				if len(sc.place) > have {
					continue
				}
				// A block costs a trip to place; two blocks weigh about a
				// step of walking each, so a hillside beats open ground only
				// when it is not far.
				score := dist + float64(abs32(dy)) + 2*float64(len(sc.place))
				if best != nil && score >= best.score {
					continue
				}
				if !path.Standable(s.world, c) {
					continue
				}
				best = &cand{sc, score}
			}
		}
	}
	if best == nil {
		if minNeed >= 0 {
			return outcome{st: Failed, err: fmt.Errorf("not enough blocks to shelter (need %d)", minNeed)}
		}
		return outcome{st: Failed, err: fmt.Errorf("no standing spot to shelter within %d blocks", shelterRadius)}
	}
	s.emit("shelter_spot", map[string]any{"blocks": len(best.sc.place)})
	s.room = best.sc
	s.nav, s.phase = newNavigator(s.world, best.sc.cell, 0.3), shlWalk
	return next
}

func (s *Shelter) settleOn(sc shelterCell) outcome {
	s.room, s.idx = sc, 0
	s.nav, s.phase = nil, shlBuild
	return next
}

func (s *Shelter) walk(a Actuator) outcome {
	st, err := s.nav.step(a)
	var te tickError
	if errors.As(err, &te) {
		return outcome{st: Failed, err: err}
	}
	switch st {
	case Running:
		return outcome{st: Running}
	case Done:
		return s.settleOn(s.room)
	}
	// A spot that cannot be reached is given up, not the whole night.
	s.given[s.room.cell] = true
	if s.fails++; s.fails >= shelterWalkFails {
		return outcome{st: Failed, err: fmt.Errorf("getting to a shelter spot: %w", err)}
	}
	s.nav, s.phase = nil, shlPlan
	return next
}

// pushed says the unit was knocked off the middle of its cell.
func (s *Shelter) pushed(p act.Pose) bool {
	c := centre(s.room.cell)
	return math.Hypot(float64(p.Feet.X()-c.X()), float64(p.Feet.Z()-c.Z())) > shelterDrift ||
		cellOf(p.Feet) != s.room.cell
}

// back sends the unit back to the middle of its cell, a few times at most.
func (s *Shelter) back(a Actuator) outcome {
	if s.drifted++; s.drifted > shelterWalkFails*4 {
		return outcome{st: Failed, err: errors.New("pushed out of the shelter spot too often")}
	}
	s.nav, s.phase = newNavigator(s.world, s.room.cell, 0.3), shlWalk
	return next
}

func (s *Shelter) build(a Actuator, snap belief.Snapshot) outcome {
	p := a.Pose()
	if s.pushed(p) {
		return s.back(a)
	}
	for s.idx < len(s.room.place) {
		t := s.room.place[s.idx]
		if n, known := s.at(t); known && fullCube(n) {
			s.idx++
			continue
		}
		break
	}
	if s.idx >= len(s.room.place) {
		s.phase, s.waited = shlWait, 0
		s.emit("shelter_sealed", map[string]any{"placed": s.placed})
		return next
	}
	item, ok := material(snap)
	if snap.Creative && !ok {
		item, ok = "cobblestone", true
	}
	if !ok {
		return outcome{st: Failed, err: fmt.Errorf("not enough blocks to shelter (need %d)", len(s.room.place)-s.idx)}
	}
	s.item = item
	target := s.room.place[s.idx]
	err := a.PlaceBlock(target, item)
	switch {
	case err == nil:
		s.phase, s.settle = shlConfirm, 0
		return next
	case errors.Is(err, act.ErrItemMoving):
		if s.waitMove++; s.waitMove >= answerTicks {
			return outcome{st: Failed, err: fmt.Errorf("no answer to the move within %d ticks", answerTicks)}
		}
		return s.idle(a)
	}
	return outcome{st: Failed, err: err}
}

// confirm waits for the world to show the placed block, retrying once on
// silence like a plain placement does.
func (s *Shelter) confirm(a Actuator) outcome {
	target := s.room.place[s.idx]
	if name, known := s.at(target); known && placedAs(s.item, name) {
		s.placed++
		s.mine[target] = true
		s.idx++
		s.waitMove, s.retried = 0, false
		s.phase = shlBuild
		return next
	}
	if s.settle++; s.settle >= confirmTicks {
		if s.retried {
			return outcome{st: Failed, err: errors.New("the server did not confirm the placed block")}
		}
		s.retried, s.settle, s.phase = true, 0, shlBuild
		return next
	}
	return s.idle(a)
}

// breached finds a wall cell that was solid and is not any more.
func (s *Shelter) breached() bool {
	for _, w := range s.room.walls {
		if n, known := s.at(w); known && !fullCube(n) {
			return true
		}
	}
	return false
}

func (s *Shelter) wait(a Actuator, snap belief.Snapshot) outcome {
	if s.breached() {
		return outcome{st: Failed, err: errors.New("the shelter was breached")}
	}
	if daytime(snap.Env.DayPhase) || s.waited >= s.maxTicks {
		s.emit("shelter_leaving", map[string]any{"waited": s.waited})
		exits, err := s.exitCells()
		if err != nil {
			return outcome{st: Failed, err: err}
		}
		s.exits, s.phase = exits, shlDig
		return next
	}
	s.waited++
	return s.idle(a)
}

// exitCells is the wall column to open, head cell first so the unit can step
// up into the gap once the feet cell goes too. Its far side must be open air:
// digging into natural rock leaves the unit sealed in, and into a liquid
// floods the room. A column the shelter placed itself is preferred, as it is
// known to be a plain block, and the bridge column is never used, as its roof
// block would hang over the gap.
func (s *Shelter) exitCells() ([]protocol.BlockPos, error) {
	c := s.room.cell
	best, bestRank := -1, 3
	for i, d := range sideOffsets {
		if i == s.room.bridge {
			continue
		}
		open := true
		for dy := int32(0); dy <= 1; dy++ {
			if n, known := s.at(off(c, 2*d[0], dy, 2*d[1])); !known || !airLike(n) {
				open = false
			}
		}
		if !open {
			continue
		}
		rank := 1
		if s.mine[off(c, d[0], 0, d[1])] && s.mine[off(c, d[0], 1, d[1])] {
			rank = 0
		}
		if rank < bestRank {
			best, bestRank = i, rank
		}
	}
	if best < 0 {
		return nil, errors.New("no safe exit from the shelter: every wall has rock or liquid behind it")
	}
	d := sideOffsets[best]
	return []protocol.BlockPos{off(c, d[0], 1, d[1]), off(c, d[0], 0, d[1])}, nil
}

// dig opens the exit with the tool that suits the block.
func (s *Shelter) dig(a Actuator, snap belief.Snapshot) outcome {
	for len(s.exits) > 0 {
		if n, known := s.at(s.exits[0]); known && !fullCube(n) {
			s.exits, s.brk, s.tool = s.exits[1:], cellBreak{}, false
			continue
		}
		break
	}
	if len(s.exits) == 0 {
		return outcome{st: Done}
	}
	p := a.Pose()
	if s.brk.name == "" {
		name, _ := s.at(s.exits[0])
		s.brk = cellBreak{pos: s.exits[0], name: name}
	}
	if !s.tool {
		s.tool = true
		if t, ok := hotbarTool(snap, toolFor(s.brk.name)); ok {
			if err := a.SelectHotbar(t.Slot, t); err != nil {
				return outcome{st: Failed, err: err}
			}
		}
	}
	done, err := driveBreak(s.world, &s.brk, a, p)
	if err != nil {
		return outcome{st: Failed, err: err}
	}
	if done {
		s.exits, s.brk, s.tool = s.exits[1:], cellBreak{}, false
		return next
	}
	return outcome{st: Running}
}

// toolFor names the tool that breaks a block fastest.
func toolFor(block string) string {
	switch {
	case block == "dirt" || block == "grass_block" || block == "sand" || block == "gravel":
		return "shovel"
	case strings.HasSuffix(block, "_planks") || strings.HasSuffix(block, "_log"):
		return "axe"
	}
	return "pickaxe"
}
