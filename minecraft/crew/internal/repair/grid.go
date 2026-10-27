package repair

import (
	"fmt"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// Box is an inclusive block box.
type Box = arena.Box

// Unknown marks a cell nobody has stated a block for. Unknown cells are never
// repaired, so a plan that does not mention a cell leaves it alone.
const Unknown int32 = 0

// Palette interns block strings ("stone_bricks", "lantern [\"hanging\"=true]")
// so a grid of millions of cells holds small ints.
type Palette struct {
	ids   map[string]int32
	names []string
}

func NewPalette() *Palette {
	return &Palette{ids: map[string]int32{}, names: []string{""}}
}

func (p *Palette) ID(block string) int32 {
	if id, ok := p.ids[block]; ok {
		return id
	}
	id := int32(len(p.names))
	p.ids[block] = id
	p.names = append(p.names, block)
	return id
}

func (p *Palette) Name(id int32) string {
	if id <= 0 || int(id) >= len(p.names) {
		return ""
	}
	return p.names[id]
}

// IsAir reports whether the block string is air.
func IsAir(block string) bool { return block == "air" }

// Normalize reduces a block as written in a command to its canonical string:
// no namespace, and block states with their spaces removed, so the generator's
// `lantern ["hanging"=true]` and a scan's `lantern["hanging"=true]` compare
// equal.
func Normalize(block string) (string, error) {
	block = strings.TrimSpace(block)
	name, states := block, ""
	if i := strings.IndexAny(block, " ["); i >= 0 {
		name, states = block[:i], strings.ReplaceAll(block[i:], " ", "")
	}
	name = strings.TrimPrefix(name, "minecraft:")
	if name == "" {
		return "", fmt.Errorf("empty block name")
	}
	if states != "" && (!strings.HasPrefix(states, "[") || !strings.HasSuffix(states, "]")) {
		return "", fmt.Errorf("block %q: unexpected text after the name", block)
	}
	if states == "" {
		return name, nil
	}
	return name + " " + states, nil
}

// Grid is a dense box of palette ids.
type Grid struct {
	Box   Box
	Cells []int32
}

func dims(b Box) (dx, dy, dz int) { return b.X1 - b.X0 + 1, b.Y1 - b.Y0 + 1, b.Z1 - b.Z0 + 1 }

func valid(b Box) bool { return b.X1 >= b.X0 && b.Y1 >= b.Y0 && b.Z1 >= b.Z0 }

func NewGrid(b Box) *Grid {
	dx, dy, dz := dims(b)
	return &Grid{Box: b, Cells: make([]int32, dx*dy*dz)}
}

func (g *Grid) index(x, y, z int) int {
	dx, _, dz := dims(g.Box)
	return ((y-g.Box.Y0)*dz+(z-g.Box.Z0))*dx + (x - g.Box.X0)
}

func (g *Grid) At(x, y, z int) int32 { return g.Cells[g.index(x, y, z)] }

// Paint sets every cell of b that lies inside the grid.
func (g *Grid) Paint(b Box, id int32) {
	c, ok := intersect(g.Box, b)
	if !ok {
		return
	}
	for y := c.Y0; y <= c.Y1; y++ {
		for z := c.Z0; z <= c.Z1; z++ {
			i := g.index(c.X0, y, z)
			for x := c.X0; x <= c.X1; x++ {
				g.Cells[i] = id
				i++
			}
		}
	}
}

func intersect(a, b Box) (Box, bool) {
	c := Box{
		X0: max(a.X0, b.X0), Y0: max(a.Y0, b.Y0), Z0: max(a.Z0, b.Z0),
		X1: min(a.X1, b.X1), Y1: min(a.Y1, b.Y1), Z1: min(a.Z1, b.Z1),
	}
	return c, valid(c)
}

func contains(outer, inner Box) bool {
	return inner.X0 >= outer.X0 && inner.X1 <= outer.X1 &&
		inner.Y0 >= outer.Y0 && inner.Y1 <= outer.Y1 &&
		inner.Z0 >= outer.Z0 && inner.Z1 <= outer.Z1
}
