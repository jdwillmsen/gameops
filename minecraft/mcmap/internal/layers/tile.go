package layers

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	// TileSize is a tile's width and height in pixels, as every other
	// tile's is.
	TileSize = 256
	// MinZoom is the widest tile drawn. A tile there is 1,024 blocks across
	// and reads 4,096 chunks; wider ones are left to the surface map, as
	// reading a world's depths for them is not a cost worth paying on
	// request.
	MinZoom = -2
	// MaxZoom is the finest: a block to a pixel, which the page enlarges.
	MaxZoom = 0
)

// Timings is where the time of a tile went.
type Timings struct {
	// Chunks is how many chunk columns were read, Slices how many
	// sub-chunks of them were found.
	Chunks, Slices int
	// Unreadable is how many chunks had a sub-chunk that would not read.
	// The chunk is drawn from what did, and a tile is not refused for it.
	Unreadable int
	// Read is the lookups and the decoding of the sub-chunks; Paint is
	// working out what each column shows, lighting it and scaling it to
	// the tile; Encode is the picture.
	Read, Paint, Encode time.Duration
	Bytes               int
}

// Renderer draws tiles of cuts through a world.
type Renderer struct {
	Reader
}

type pixel struct {
	r, g, b, a uint8
	// h is the y of what is seen, for lighting from the neighbours.
	h int16
}

// work is the memory a tile is drawn in, kept for the next.
type work struct {
	col    Column
	px     []pixel
	out    *image.NRGBA
	pngBuf bytes.Buffer
}

var works = sync.Pool{New: func() any { return new(work) }}

// Tile draws the tile tx, ty at zoom of a cut as a PNG, addressed as every
// other tile is: at zoom 0 tile x, y starts at block x*256, z y*256, and
// each zoom below doubles the blocks to a pixel. ok is false for a tile
// that has no block in the band, which most of a sparse world's grid has
// none of.
func (r *Renderer) Tile(spec Spec, zoom, tx, ty int) (data []byte, t Timings, ok bool, err error) {
	if err := spec.Valid(); err != nil {
		return nil, t, false, err
	}
	if zoom < MinZoom || zoom > MaxZoom {
		return nil, t, false, errors.New("no tiles at that zoom")
	}
	w := works.Get().(*work)
	defer works.Put(w)

	shift := -zoom
	blocks := TileSize << shift
	x0, z0 := tx*blocks, ty*blocks
	// A row and a column beyond the north and west edges, which light the
	// edge pixels from the tile's neighbours so that tiles meet without a
	// seam.
	side := blocks + 1
	if cap(w.px) < side*side {
		w.px = make([]pixel, side*side)
	}
	px := w.px[:side*side]
	clear(px)

	cx0, cz0 := (x0-1)>>4, (z0-1)>>4
	cx1, cz1 := (x0+blocks-1)>>4, (z0+blocks-1)>>4
	known := r.Registry.Blocks()
	for cz := cz0; cz <= cz1; cz++ {
		for cx := cx0; cx <= cx1; cx++ {
			began := time.Now()
			found, err := r.Read(chunks.Pos{Dim: spec.Dim, X: int32(cx), Z: int32(cz)}, &w.col)
			t.Read += time.Since(began)
			t.Chunks++
			t.Slices += w.col.Slices
			if err != nil {
				t.Unreadable++
			}
			if !found {
				continue
			}
			began = time.Now()
			known = r.Registry.Blocks()
			paintChunk(&w.col, known, spec, px, side, x0, z0, cx, cz)
			t.Paint += time.Since(began)
		}
	}

	began := time.Now()
	if w.out == nil {
		w.out = image.NewNRGBA(image.Rect(0, 0, TileSize, TileSize))
	}
	drawn := compose(px, side, shift, w.out)
	t.Paint += time.Since(began)
	if !drawn {
		return nil, t, false, nil
	}

	began = time.Now()
	w.pngBuf.Reset()
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&w.pngBuf, w.out); err != nil {
		return nil, t, false, err
	}
	t.Encode = time.Since(began)
	data = bytes.Clone(w.pngBuf.Bytes())
	t.Bytes = len(data)
	return data, t, true, nil
}

// paintChunk works out the 256 columns of one chunk into the tile's pixels
// at a block to a pixel, with the light not yet put on.
func paintChunk(col *Column, blocks []Block, spec Spec, px []pixel, side, x0, z0, cx, cz int) {
	for z := range 16 {
		pz := cz*16 + z - z0 + 1
		if pz < 0 || pz >= side {
			continue
		}
		for x := range 16 {
			pxx := cx*16 + x - x0 + 1
			if pxx < 0 || pxx >= side {
				continue
			}
			s := col.see(blocks, x, z, spec)
			if s.Kind == Nothing {
				continue
			}
			px[pz*side+pxx] = colourOfSight(blocks, s, spec)
		}
	}
}

// colourOfSight is the colour a column is drawn in before the light
// from its neighbours: its block's own, darkened by how far below the cut it
// is so that a cave reads as a pit, with water laid over what it covers.
func colourOfSight(blocks []Block, s Sight, spec Spec) pixel {
	b := &blocks[s.ID]
	rgb := [3]int{int(b.RGB[0]), int(b.RGB[1]), int(b.RGB[2])}
	if b.Flags&Water != 0 {
		// The bed shows through shallow water and the water's own colour
		// takes over with depth: a block of water lets through about
		// four fifths of the light under it.
		bed := [3]int{rgb[0] / 3, rgb[1] / 3, rgb[2] / 3}
		if s.Bed != AirID {
			bb := &blocks[s.Bed]
			bed = [3]int{int(bb.RGB[0]), int(bb.RGB[1]), int(bb.RGB[2])}
		}
		share := min(96+s.Depth*20, 224)
		for i := range rgb {
			rgb[i] = (bed[i]*(256-share) + rgb[i]*share) / 256
		}
	} else if s.Wet {
		for i, w := range [3]int{0x3f, 0x76, 0xe4} {
			rgb[i] = (rgb[i]*3 + w) / 4
		}
	}
	level := 256
	if b.Flags&Lava == 0 {
		// Down to 60% at the bottom of the band.
		depth := spec.Cut - s.Y
		level = 256 - 102*min(max(depth, 0), spec.Thickness)/max(spec.Thickness, 1)
		if s.Kind == Mass {
			level = level * 11 / 20
		}
	}
	return pixel{
		r: uint8(rgb[0] * level / 256), g: uint8(rgb[1] * level / 256), b: uint8(rgb[2] * level / 256),
		a: 255, h: int16(s.Y),
	}
}

// compose lights the pixels from the two neighbours to their north and
// west, as maps always have been, scales them to the tile's zoom and
// writes the tile. It says whether anything was drawn.
func compose(px []pixel, side, shift int, out *image.NRGBA) bool {
	at := func(x, z int) pixel { return px[(z+1)*side+x+1] }
	lit := func(x, z int) (r, g, b, a int) {
		p := at(x, z)
		if p.a == 0 {
			return 0, 0, 0, 0
		}
		d := 0
		if n := at(x, z-1); n.a != 0 {
			d += int(p.h) - int(n.h)
		}
		if w := at(x-1, z); w.a != 0 {
			d += int(p.h) - int(w.h)
		}
		d = min(max(d, -6), 6)
		k := 256 + d*9
		return min(int(p.r)*k/256, 255), min(int(p.g)*k/256, 255), min(int(p.b)*k/256, 255), 255
	}
	n := 1 << shift
	found := false
	for oz := range TileSize {
		row := out.Pix[oz*out.Stride:][:TileSize*4]
		for ox := range TileSize {
			var sr, sg, sb, sa int
			for dz := range n {
				for dx := range n {
					r, g, b, a := lit(ox*n+dx, oz*n+dz)
					sr, sg, sb, sa = sr+r, sg+g, sb+b, sa+a
				}
			}
			o := row[ox*4:][:4]
			if sa == 0 {
				o[0], o[1], o[2], o[3] = 0, 0, 0, 0
				continue
			}
			found = true
			cnt := sa / 255
			o[0], o[1], o[2] = uint8(sr/cnt), uint8(sg/cnt), uint8(sb/cnt)
			o[3] = uint8(sa / (n * n))
		}
	}
	return found
}
