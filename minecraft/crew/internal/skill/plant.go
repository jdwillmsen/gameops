package skill

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// plantClearance is the air a sapling needs above its ground block: a tree
// that finds a block in its way stays a sapling.
const plantClearance = 4

// Plant sets saplings on a grid inside a box, one every spacing blocks in x and
// z, on grass or dirt with open sky above and nothing already planted. It
// places each through the Place skill, so the hotbar move and the confirmation
// are Place's.
type Plant struct {
	world   path.Blocks
	self    func() belief.Snapshot
	item    string
	area    Area
	spacing int
	qty     int
	opts    []Option

	cells   [][2]int32
	idx     int
	cur     Skill
	planted int
	lastErr error
}

// NewPlant plants up to qty of item. spacing is the distance between grid
// cells and must be at least 1.
func NewPlant(world path.Blocks, self func() belief.Snapshot, item string, area Area, spacing, qty int, opts ...Option) Skill {
	return &Plant{world: world, self: self, item: item, area: area, spacing: max(spacing, 1), qty: qty, opts: opts}
}

func (p *Plant) Name() string { return "plant" }

// Progress counts saplings planted, which the inventory cannot show: planting
// spends the item.
func (p *Plant) Progress() int { return p.planted }

func (p *Plant) Ready(s belief.Snapshot) error {
	if err := overworld(s); err != nil {
		return err
	}
	if !s.Creative && count(s, p.item) < 1 {
		return fmt.Errorf("no %s in inventory", p.item)
	}
	return nil
}

// Resume drops the placement in flight, which the interruption left half done;
// its cell is judged afresh.
func (p *Plant) Resume() {
	if p.cur != nil {
		p.cur = nil
		p.idx--
	}
}

func (p *Plant) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	snap := p.self()
	if err := overworld(snap); err != nil {
		return Failed, err
	}
	if p.cells == nil {
		p.cells = p.grid()
	}
	for {
		if p.planted >= p.qty {
			return Done, nil
		}
		if p.cur == nil {
			if !snap.Creative && count(snap, p.item) < 1 {
				return p.finish()
			}
			target, ok := p.nextTarget()
			if !ok {
				return p.finish()
			}
			p.cur = NewPlaceBlock(p.world, p.self, target, p.item, p.opts...)
		}
		st, err := p.cur.Step(ctx, a)
		switch st {
		case Running:
			return Running, nil
		case Done:
			p.planted++
		default:
			// One unreachable cell must not end the grove; a dead connection
			// or a cancelled run must.
			var ce cellError
			if ctx.Err() != nil || !errors.As(err, &ce) {
				return Failed, err
			}
			p.lastErr = err
		}
		p.cur = nil
		snap = p.self()
	}
}

// finish ends a run that has nothing more to plant. A grove that took no
// sapling because every placement failed is a failure; one already full, or
// out of saplings, is done.
func (p *Plant) finish() (Status, error) {
	if p.planted == 0 && p.lastErr != nil {
		return Failed, fmt.Errorf("planting %s: %w", p.item, p.lastErr)
	}
	return Done, nil
}

// grid lists the cells of the box a spacing apart, row by row with every other
// row reversed so the walk between them stays short.
func (p *Plant) grid() [][2]int32 {
	cells := [][2]int32{}
	step := int32(p.spacing)
	row := 0
	for z := p.area.Min.Z(); z <= p.area.Max.Z(); z += step {
		var line [][2]int32
		for x := p.area.Min.X(); x <= p.area.Max.X(); x += step {
			line = append(line, [2]int32{x, z})
		}
		if row%2 == 1 {
			for i, j := 0, len(line)-1; i < j; i, j = i+1, j-1 {
				line[i], line[j] = line[j], line[i]
			}
		}
		cells = append(cells, line...)
		row++
	}
	return cells
}

// nextTarget advances to the next grid cell that can take a sapling and gives
// the cell the sapling goes in.
func (p *Plant) nextTarget() (protocol.BlockPos, bool) {
	for p.idx < len(p.cells) {
		c := p.cells[p.idx]
		p.idx++
		if t, ok := p.plantable(c[0], c[1]); ok {
			return t, true
		}
	}
	return protocol.BlockPos{}, false
}

// plantable looks down a column of the box for its top solid block and says
// where a sapling would go if that block is grass or dirt with clear space
// above and no sapling already in it. A column topped by leaves or a log has
// nothing to plant in.
func (p *Plant) plantable(x, z int32) (protocol.BlockPos, bool) {
	for y := p.area.Max.Y(); y >= p.area.Min.Y(); y-- {
		name, known := p.world.Block(protocol.BlockPos{x, y, z})
		if !known {
			return protocol.BlockPos{}, false
		}
		if path.Passable(name) {
			continue
		}
		if name != "grass_block" && name != "dirt" {
			return protocol.BlockPos{}, false
		}
		for h := int32(1); h <= plantClearance; h++ {
			above, known := p.world.Block(protocol.BlockPos{x, y + h, z})
			if !known || !path.Passable(above) || strings.HasSuffix(above, "_sapling") {
				return protocol.BlockPos{}, false
			}
		}
		return protocol.BlockPos{x, y + 1, z}, true
	}
	return protocol.BlockPos{}, false
}
