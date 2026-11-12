package skill

import (
	"context"
	"errors"
	"sort"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// oreScan is the half-width of the cube around the unit searched for ore.
	// It is a little under the reach, since the ore must also be in reach.
	oreScan = 4
	// orePickupTicks is the least wait for an ore's drop to come to the unit.
	orePickupTicks = 24
	// torchNear is how far from the stand the cell it left may be for a torch
	// to go there: a junction revisit leaves the unit a branch away from it.
	torchNear = 3
	// torchDepth is how far below the entrance the unit is before it counts as
	// underground. The first steps of a stair are open to the sky, whose light
	// the model does not carry.
	torchDepth = 3
	// maxOreBreaks bounds the ore one shaft takes, so a vein as big as the
	// plan cannot keep the unit digging after the plan's steps are done.
	maxOreBreaks = 96
)

// plannedFloors are the cells the stands of a spec rest on. An ore there is
// left in place: breaking it would open a hole in the unit's own way.
func plannedFloors(spec ShaftSpec) map[protocol.BlockPos]bool {
	floors := map[protocol.BlockPos]bool{}
	steps := stairSteps(spec)
	bottom := steps[len(steps)-1].stand
	for j := 0; j < spec.Branches; j++ {
		steps = append(steps, tunnelSteps(bottom, branchDirs(spec.Dir)[j], spec.BranchLength)...)
	}
	for _, st := range steps {
		floors[up(st.stand, -1)] = true
	}
	return floors
}

// goalsMet reports that every item of the ore goal has been gained since the
// shaft started.
func (m *MineShaft) goalsMet() bool {
	if len(m.spec.OreGoal) == 0 {
		return false
	}
	snap := m.self()
	for item, qty := range m.spec.OreGoal {
		if count(snap, item)-m.base[item] < qty {
			return false
		}
	}
	return true
}

// goalShortfall names the first goal item (in name order, for a stable answer)
// the shaft gained less of than asked, with what it gained and what was asked.
func (m *MineShaft) goalShortfall() (item string, gained, wanted int) {
	names := make([]string, 0, len(m.spec.OreGoal))
	for n := range m.spec.OreGoal {
		names = append(names, n)
	}
	sort.Strings(names)
	snap := m.self()
	for _, n := range names {
		if got := max(count(snap, n)-m.base[n], 0); got < m.spec.OreGoal[n] {
			return n, got, m.spec.OreGoal[n]
		}
	}
	return "", 0, 0
}

// afterArrival is where the shaft goes from a stand once any torch is done.
func (m *MineShaft) afterArrival() {
	m.phase = shSeg
	if len(m.oreBlocks) > 0 {
		m.phase = shOre
	}
}

// needsTorch says whether the stand just reached is dark enough, and deep
// enough, for a torch.
func (m *MineShaft) needsTorch() bool {
	return m.spec.Torches && m.at.Y() <= m.spec.Entrance.Y()-torchDepth && blockLight(m.world, m.at) < torchBelow
}

// torchCell is the cell a torch goes on: the stand the unit just left, if it
// is still open, has a floor and is close.
func (m *MineShaft) torchCell() (protocol.BlockPos, bool) {
	c := m.torchFrom
	if c == m.at || centre(c).Sub(centre(m.at)).Len() > torchNear {
		return protocol.BlockPos{}, false
	}
	if !m.passableAt(c) || m.floor(c) != "" {
		return protocol.BlockPos{}, false
	}
	return c, true
}

// torch lights the stand if it is dark. A torch is a comfort, never a reason
// to stop: a unit out of torches, or one whose placement fails, digs on.
func (m *MineShaft) torch(ctx context.Context, a Actuator) outcome {
	if m.sub == nil {
		if !m.needsTorch() {
			m.afterArrival()
			return next
		}
		cell, ok := m.torchCell()
		switch {
		case count(m.self(), "torch") == 0:
			if !m.noTorches {
				m.noTorches = true
				m.emit("mine_dark", map[string]any{"step": m.si, "reason": "no torches"})
			}
		case !ok:
			m.emit("mine_dark", map[string]any{"step": m.si, "reason": "no cell for a torch"})
		}
		if !ok || m.noTorches {
			m.afterArrival()
			return next
		}
		m.sub = NewPlaceBlock(m.world, m.self, cell, "torch", WithLogger(m.log))
	}
	st, err := m.sub.Step(ctx, a)
	switch st {
	case Failed:
		var te tickError
		if errors.As(err, &te) {
			return m.failed(a, te.err)
		}
		m.emit("mine_torch_failed", map[string]any{"step": m.si, "error": err.Error()})
	case Done:
		m.emit("mine_torch", map[string]any{"step": m.si})
	default:
		return outcome{st: Running}
	}
	// Placing selected the torch, not the pickaxe.
	m.sub, m.selected = nil, false
	m.afterArrival()
	return next
}

// exposed says an ore shows: a neighbour of it is open air the unit could have
// been standing in. Ore sealed in stone is not taken, since reaching it means
// digging toward a cell the plan did not clear.
func (m *MineShaft) exposed(c protocol.BlockPos) bool {
	for _, d := range sixWays {
		name, known := m.world.Block(protocol.BlockPos{c.X() + d[0], c.Y() + d[1], c.Z() + d[2]})
		if known && m.passable(name) {
			return true
		}
	}
	return false
}

func (m *MineShaft) passable(name string) bool { return liquidKind(name) == "" && path.Passable(name) }

// nextOre picks the nearest exposed ore in reach that the pickaxe can harvest
// and that is safe to open. Ore it rules out stays out for good.
func (m *MineShaft) nextOre(p act.Pose, tier int) (protocol.BlockPos, OreKind, bool) {
	feet := cellOf(p.Feet)
	var cands []protocol.BlockPos
	for dx := int32(-oreScan); dx <= oreScan; dx++ {
		for dy := int32(-oreScan); dy <= oreScan; dy++ {
			for dz := int32(-oreScan); dz <= oreScan; dz++ {
				c := protocol.BlockPos{feet.X() + dx, feet.Y() + dy, feet.Z() + dz}
				name, _ := m.world.Block(c)
				if _, ore := m.oreBlocks[name]; !ore || m.oreSkip[c] {
					continue
				}
				cands = append(cands, c)
			}
		}
	}
	d2 := func(c protocol.BlockPos) float32 { return centre(c).Sub(p.Feet).LenSqr() }
	sort.SliceStable(cands, func(i, j int) bool { return d2(cands[i]) < d2(cands[j]) })
	for _, c := range cands {
		name, _ := m.world.Block(c)
		kind := m.oreBlocks[name]
		switch {
		case kind.Tier > tier:
			m.oreSkip[c] = true
			m.emit("mine_ore_skipped", map[string]any{"kind": kind.Name, "why": "pickaxe too weak"})
		case !m.exposed(c) || centre(c).Sub(eyeOf(p.Feet)).Len() > mineReach:
			// Not ruled out: the next stand may see it, or reach it.
		case c.X() == feet.X() && c.Z() == feet.Z() && c.Y() < feet.Y(), m.floors[c]:
			m.oreSkip[c] = true
		case !collectable(m.world, dropRestAt(m.world, c), p):
			// Breaking it would lose the drop: not ruled out, since another
			// stand may be beside it.
		default:
			why := m.hazard(c)
			if why == "" {
				why = m.dropHazard(c)
			}
			if why != "" {
				m.oreSkip[c] = true
				m.emit("mine_ore_skipped", map[string]any{"kind": kind.Name, "why": why})
				continue
			}
			return c, kind, true
		}
	}
	return protocol.BlockPos{}, OreKind{}, false
}

// dropHazard says why an ore's drop must not be gone after, or "". The drop
// falls to rest in whatever open cells lie below the ore and the unit then
// walks to a cell beside it; the path planner does not avoid lava (it counts a
// cell above a lava block as standable), so the ore is left unless liquid is
// clear of the fall, the rest cell and every cell the unit could stand in to
// pick it up. A falling block over the ore is left too: opening the ore lets it
// drop through the head cell onto the unit.
func (m *MineShaft) dropHazard(c protocol.BlockPos) string {
	rest := dropRestAt(m.world, c)
	for y := c.Y(); y >= rest.Y(); y-- {
		if kind := m.beside(protocol.BlockPos{c.X(), y, c.Z()}); kind != "" {
			return kind
		}
	}
	// Two below the rest cell: a stand one lower rests on the cell under it.
	for dy := int32(-2); dy <= 1; dy++ {
		for dx := int32(-1); dx <= 1; dx++ {
			for dz := int32(-1); dz <= 1; dz++ {
				if kind := m.beside(protocol.BlockPos{rest.X() + dx, rest.Y() + dy, rest.Z() + dz}); kind != "" {
					return kind
				}
			}
		}
	}
	if name, _ := m.world.Block(up(c, 1)); isFalling(name) {
		return "falling"
	}
	return ""
}

// ore looks for ore to take from where the unit stands.
func (m *MineShaft) ore(a Actuator, snap belief.Snapshot) outcome {
	if m.oreCount >= maxOreBreaks || m.goalsMet() {
		m.phase = shSeg
		return next
	}
	tier, err := m.selectPickaxe(a, snap)
	if err != nil {
		return m.failed(a, err)
	}
	c, kind, ok := m.nextOre(a.Pose(), tier)
	if !ok {
		m.phase = shSeg
		return next
	}
	name, _ := m.world.Block(c)
	m.brk, m.oreAt, m.oreKind = cellBreak{pos: c, name: name}, c, kind
	m.phase = shOreBreak
	return next
}

func (m *MineShaft) oreBreak(a Actuator) outcome {
	done, err := driveBreak(m.world, &m.brk, a, a.Pose())
	if err != nil {
		return m.failed(a, err)
	}
	if !done {
		return outcome{st: Running}
	}
	m.dug++
	m.oreCount++
	m.oreSkip[m.oreAt] = true
	m.emit("mine_ore", map[string]any{"kind": m.oreKind.Name, "taken": m.oreCount})
	m.oreRest = dropRestAt(m.world, m.oreAt)
	m.brk, m.waited, m.pickInit = cellBreak{}, 0, false
	m.phase = shOrePick
	return next
}

// orePick walks to the ore's drop if it is out of range and waits for the
// server to hand it over.
func (m *MineShaft) orePick(a Actuator) outcome {
	p := a.Pose()
	if !m.pickInit {
		m.pickInit = true
		if cell, ok := pickupCellAt(m.world, m.oreRest, p); ok {
			m.nav = newNavigator(m.world, cell, pickupStand)
		}
	}
	if m.nav != nil {
		st, err := m.nav.step(a)
		var te tickError
		if errors.As(err, &te) {
			return m.failed(a, te.err)
		}
		if st == Running {
			return outcome{st: Running}
		}
		// A drop that cannot be walked to is left, not a reason to stop.
		m.nav = nil
	}
	if m.waited++; m.waited > orePickupTicks {
		m.pickInit, m.phase = false, shOre
		return next
	}
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return m.failed(a, err)
	}
	return outcome{st: Running}
}
