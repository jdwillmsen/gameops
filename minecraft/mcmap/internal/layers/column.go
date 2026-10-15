// Package layers draws the inside of the world: a cut through a dimension
// at a chosen height, in the colours of its blocks, and the floors a cut
// finds, which the Nether's topography is drawn from.
//
// It reads the sub-chunks of the save itself. A chunk column is a stack of
// 16-block-tall slices, each a version, one or more layers of blocks and,
// per layer, a palette of the blocks the slice uses. Reading a column costs
// a lookup per slice that the dimension can hold, which dominates the cost
// of drawing, so a tile is drawn a chunk at a time from one reused buffer
// and nothing of a chunk is kept once it has been painted.
package layers

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/util"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	// tagSubChunk is a slice's record: the chunk's key, this tag, and the
	// slice's height index as one signed byte.
	tagSubChunk = 0x2f
	// blocksPerSub is every block of a slice, indexed x, then z, then y.
	blocksPerSub = 4096
	// maxSlots is the most slices a column has, the overworld's 24.
	maxSlots = 24
	// maxRecord bounds a slice record. A full slice at sixteen bits a block
	// and a palette of every block in it is far below this.
	maxRecord = 1 << 20
)

// span is the height indexes of a dimension's slices: the lowest and how
// many there are.
func span(d chunks.Dimension) (lowest, count int, ok bool) {
	switch d {
	case chunks.Overworld:
		return -4, 24, true
	case chunks.Nether:
		return 0, 8, true
	case chunks.End:
		return 0, 16, true
	}
	return 0, 0, false
}

// MinY and MaxY are the lowest and the highest block of a dimension.
func MinY(d chunks.Dimension) int {
	lowest, _, _ := span(d)
	return lowest * 16
}

func MaxY(d chunks.Dimension) int {
	lowest, count, _ := span(d)
	return (lowest+count)*16 - 1
}

type slice struct {
	present bool
	// uniform says every block is one, id, and ids is not filled; most
	// slices of air above the ground are this.
	uniform bool
	id      ID
	ids     [blocksPerSub]ID
	// wet marks, a bit a block, the blocks the slice's second layer fills
	// with a liquid: the water a waterlogged block stands in.
	hasWet bool
	wet    [blocksPerSub / 8]byte
}

// Column is the slices of one chunk. It is large, 200 KB, and is meant to
// be read into again and again.
type Column struct {
	Pos    chunks.Pos
	lowest int
	count  int
	slots  [maxSlots]slice
	// Slices is how many were found.
	Slices int

	palette [blocksPerSub]ID
	index   [blocksPerSub]uint16
}

var columns = sync.Pool{New: func() any { return new(Column) }}

// GetColumn and PutColumn lend a Column from a pool.
func GetColumn() *Column  { return columns.Get().(*Column) }
func PutColumn(c *Column) { columns.Put(c) }

// ID is the block at x, z within the chunk and world y, or air where there
// is no slice.
func (c *Column) ID(x, y, z int) ID {
	s := y>>4 - c.lowest
	if s < 0 || s >= c.count {
		return AirID
	}
	sl := &c.slots[s]
	if !sl.present {
		return AirID
	}
	if sl.uniform {
		return sl.id
	}
	return sl.ids[x<<8|z<<4|y&15]
}

// Wet says the block holds a liquid in its second layer.
func (c *Column) Wet(x, y, z int) bool {
	s := y>>4 - c.lowest
	if s < 0 || s >= c.count {
		return false
	}
	sl := &c.slots[s]
	if !sl.present || !sl.hasWet {
		return false
	}
	at := x<<8 | z<<4 | y&15
	return sl.wet[at>>3]>>(at&7)&1 != 0
}

// Reader reads columns out of a world's database.
type Reader struct {
	DB       *leveldb.DB
	Registry *Registry
}

// begin starts c on the chunk at pos with no slices.
func (c *Column) begin(pos chunks.Pos) bool {
	lowest, count, ok := span(pos.Dim)
	if !ok {
		return false
	}
	c.Pos, c.lowest, c.count, c.Slices = pos, lowest, count, 0
	for i := range c.slots[:count] {
		c.slots[i].present = false
	}
	return true
}

// load reads the record of the slice at height index h into the column. A
// record that cannot be read is counted as missing: the chunk is then drawn
// with a hole where it was, not refused whole.
func (c *Column) load(reg *Registry, h int, data []byte) error {
	i := h - c.lowest
	if i < 0 || i >= c.count || len(data) > maxRecord {
		return nil
	}
	if c.slots[i].present {
		c.Slices--
		c.slots[i].present = false
	}
	if err := c.decode(reg, &c.slots[i], data); err != nil {
		return err
	}
	c.slots[i].present = true
	c.Slices++
	return nil
}

// Read fills c with the chunk at pos by looking up each slice the
// dimension can hold, and says whether it holds any. It is for a chunk or
// a tile's worth of them; Walk is for all of them.
func (r *Reader) Read(pos chunks.Pos, c *Column) (bool, error) {
	if !c.begin(pos) {
		return false, errors.New("not a dimension of the world")
	}
	prefix := chunks.RecordKey(pos, tagSubChunk)
	// One seek and a few steps find the chunk's slices, where a lookup for
	// each of the dimension's would mostly be for ones that are not there.
	it := r.DB.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()
	var firstErr error
	for it.Next() {
		k := it.Key()
		if len(k) != len(prefix)+1 {
			continue
		}
		if err := c.load(r.Registry, int(int8(k[len(prefix)])), it.Value()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := it.Error(); err != nil && firstErr == nil {
		firstErr = err
	}
	return c.Slices > 0, firstErr
}

// Walk reads every chunk of a dimension in the order the database holds
// them, handing each to fn in turn; the column is reused, so fn must not
// keep it. A walk is one pass over the whole database, which is cheaper
// per chunk than looking each up once a good part of the dimension is
// wanted, and it stops at the first error fn returns.
func (r *Reader) Walk(ctx context.Context, dim chunks.Dimension, fn func(*Column) error) error {
	c := new(Column)
	it := r.DB.NewIterator(nil, nil)
	defer it.Release()
	have := false
	flush := func() error {
		if have && c.Slices > 0 {
			return fn(c)
		}
		return nil
	}
	for n := 0; it.Next(); n++ {
		if n%65536 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		k := it.Key()
		pos, tag, ok := chunks.RecordOf(k)
		if !ok || tag != tagSubChunk || pos.Dim != dim || len(k) != len(chunks.RecordKey(pos, tagSubChunk))+1 {
			continue
		}
		if !have || pos != c.Pos {
			if err := flush(); err != nil {
				return err
			}
			c.begin(pos)
			have = true
		}
		// A slice that will not read is a hole in the chunk, as in Read.
		_ = c.load(r.Registry, int(int8(k[len(k)-1])), it.Value())
	}
	if err := it.Error(); err != nil {
		return err
	}
	return flush()
}

var errSlice = errors.New("slice is not laid out as this reads it")

// decode reads a slice record: a version, then layers of blocks. Versions 8
// and 9 are the ones a current world writes, 9 adding the slice's own
// height; 1 is the older single layer. Each layer is a width in bits, every
// block's index into a palette packed into 32-bit words at that width, and
// the palette: a count, then that many entries each naming a block. The
// first layer is the blocks and the second the liquid they stand in.
func (c *Column) decode(reg *Registry, sl *slice, data []byte) error {
	if len(data) < 2 {
		return errSlice
	}
	var layerCount int
	switch data[0] {
	case 1:
		layerCount, data = 1, data[1:]
	case 8:
		layerCount, data = int(data[1]), data[2:]
	case 9:
		if len(data) < 3 {
			return errSlice
		}
		layerCount, data = int(data[1]), data[3:]
	default:
		return errSlice
	}
	if layerCount == 0 {
		return errSlice
	}
	sl.hasWet = false
	for layer := range min(layerCount, 2) {
		n, rest, uniform, err := c.storage(reg, data)
		if err != nil {
			return err
		}
		data = rest
		if layer == 0 {
			if uniform {
				sl.uniform, sl.id = true, c.palette[0]
				continue
			}
			sl.uniform = false
			for i, p := range c.index {
				if int(p) >= n {
					return errSlice
				}
				sl.ids[i] = c.palette[p]
			}
			continue
		}
		blocks := reg.Blocks()
		var liquid [blocksPerSub]bool
		anyLiquid := false
		for i := range n {
			if blocks[c.palette[i]].Flags&Liquid != 0 {
				liquid[i], anyLiquid = true, true
			}
		}
		if !anyLiquid {
			continue
		}
		clear(sl.wet[:])
		for i, p := range c.index {
			if uniform && liquid[0] || !uniform && int(p) < n && liquid[p] {
				sl.wet[i>>3] |= 1 << (i & 7)
				sl.hasWet = true
			}
		}
	}
	return nil
}

// storage reads one layer into c.index and c.palette, and returns the
// palette's size and what follows the layer. A layer at no width at all is
// one entry for every block and has no count; its index is not filled.
func (c *Column) storage(reg *Registry, b []byte) (n int, rest []byte, uniform bool, err error) {
	if len(b) < 1 {
		return 0, nil, false, errSlice
	}
	// The low bit marks indexes meant for the network, which have no
	// palette to look anything up in. Nothing on disk is written so.
	if b[0]&1 != 0 {
		return 0, nil, false, errSlice
	}
	bits := int(b[0] >> 1)
	b = b[1:]
	if bits == 0 {
		n, uniform = 1, true
	} else {
		if bits > 16 {
			return 0, nil, false, errSlice
		}
		perWord := 32 / bits
		words := (blocksPerSub + perWord - 1) / perWord
		if len(b) < words*4+4 {
			return 0, nil, false, errSlice
		}
		mask := uint32(1)<<bits - 1
		at := 0
		for w := range words {
			word := binary.LittleEndian.Uint32(b[w*4:])
			for k := 0; k < perWord && at < blocksPerSub; k++ {
				c.index[at] = uint16(word >> (k * bits) & mask)
				at++
			}
		}
		b = b[words*4:]
		count := int32(binary.LittleEndian.Uint32(b))
		if count < 1 || count > blocksPerSub {
			return 0, nil, false, errSlice
		}
		n, b = int(count), b[4:]
	}
	for i := range n {
		name, after, err := blockName(b)
		if err != nil || len(name) == 0 {
			return 0, nil, false, errSlice
		}
		c.palette[i] = reg.intern(name)
		b = after
	}
	return n, b, uniform, nil
}
