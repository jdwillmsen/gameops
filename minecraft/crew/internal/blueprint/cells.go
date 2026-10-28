package blueprint

// Cell is one block to stamp, positioned relative to the footprint's minimum
// corner after rotation.
type Cell struct {
	Pos   Position
	Block Block
}

// Footprint is the width (x) and depth (z) the blueprint covers once rotated.
func (b *Blueprint) Footprint(rotation int) (w, d int) {
	w, d = b.Size.X, b.Size.Z
	if rotation == 90 || rotation == 270 {
		w, d = d, w
	}
	return w, d
}

// Cells lists the blocks to stamp in placement order, rotated clockwise by
// rotation degrees. Air is left out, and so is the upper half of a door: the
// lower half's placement makes it.
func (b *Blueprint) Cells(rotation int) []Cell {
	var out []Cell
	for _, p := range b.PlacementOrder() {
		block := b.blockAt(p.Y, p.X, p.Z)
		if block.Name == "air" || block.States["upper_block_bit"] == true {
			continue
		}
		x, z := Rotate(p.X, p.Z, b.Size.X, b.Size.Z, rotation)
		out = append(out, Cell{
			Pos:   Position{X: x, Y: p.Y, Z: z},
			Block: Block{Name: block.Name, States: RotateStates(block.States, rotation)},
		})
	}
	return out
}
