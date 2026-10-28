package repair

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Op states one box as one block, in the order the generator wrote it: a later
// op overrides an earlier one where they overlap.
type Op struct {
	Box   Box
	Block string
}

// ParseOps reads fill and setblock lines, the vocabulary of the generator's
// clear.cmds and build.cmds. Other commands (kill, summon, replaceitem) are
// entity work and are skipped; a fill or setblock it cannot read is an error,
// because skipping it would silently shrink the plan.
func ParseOps(r io.Reader) ([]Op, error) {
	var ops []Op
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		var coords, rest []string
		switch f[0] {
		case "fill":
			if len(f) < 8 {
				return nil, fmt.Errorf("line %d: fill needs 6 coordinates and a block", n)
			}
			coords, rest = f[1:7], f[7:]
		case "setblock":
			if len(f) < 5 {
				return nil, fmt.Errorf("line %d: setblock needs 3 coordinates and a block", n)
			}
			coords, rest = f[1:4], f[4:]
		default:
			continue
		}
		nums := make([]int, len(coords))
		for i, c := range coords {
			v, err := strconv.Atoi(c)
			if err != nil {
				return nil, fmt.Errorf("line %d: coordinate %q is not a whole number", n, c)
			}
			nums[i] = v
		}
		if len(nums) == 3 {
			nums = append(nums, nums...)
		}
		block, err := Normalize(strings.Join(rest, " "))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		b := Box{
			X0: min(nums[0], nums[3]), Y0: min(nums[1], nums[4]), Z0: min(nums[2], nums[5]),
			X1: max(nums[0], nums[3]), Y1: max(nums[1], nums[4]), Z1: max(nums[2], nums[5]),
		}
		ops = append(ops, Op{Box: b, Block: block})
	}
	return ops, sc.Err()
}

// Paint applies ops to g in order.
func Paint(g *Grid, pal *Palette, ops []Op) {
	for _, op := range ops {
		g.Paint(op.Box, pal.ID(op.Block))
	}
}

// ClipOps keeps the part of each op inside box, in order. A later op still
// overrides an earlier one, so painting the clipped ops over box gives the
// same cells as painting the whole plan.
func ClipOps(ops []Op, box Box) []Op {
	var out []Op
	for _, op := range ops {
		if c, ok := intersect(box, op.Box); ok {
			out = append(out, Op{Box: c, Block: op.Block})
		}
	}
	return out
}

// Reader returns what is in the world for a box. Cells it cannot vouch for
// stay Unknown and are counted as unscanned, never repaired.
type Reader interface {
	Read(b Box, pal *Palette) (*Grid, error)
}

// OpsReader serves a scan stored as fill/setblock lines, the same vocabulary
// the plan uses, so a snapshot, a hand-made damage fixture and a scan from a
// unit all share one format.
type OpsReader struct{ Ops []Op }

func (r OpsReader) Read(b Box, pal *Palette) (*Grid, error) {
	g := NewGrid(b)
	Paint(g, pal, r.Ops)
	return g, nil
}
