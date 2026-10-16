// Package subchunk decodes the sub-chunk payloads Bedrock sends over the
// network (formats 8 and 9) into per-layer block arrays.
//
// Palette entries are network block hashes, not runtime IDs; mapping them to
// block names is the caller's job and depends on the server's version.
package subchunk

import (
	"errors"
	"fmt"
)

const (
	// Size is the number of blocks in one layer.
	Size = 16 * 16 * 16

	maxPalette = Size
)

// ErrMalformed wraps every error caused by input that is not a valid payload.
var ErrMalformed = errors.New("malformed sub-chunk")

// Layer is one block storage of a sub-chunk. Layer 0 holds the blocks;
// layer 1, when present, holds waterlogged liquid.
type Layer struct {
	// Palette lists the distinct network block hashes in the layer.
	Palette []uint32
	// Indices maps a block position (see Block) to an entry in Palette.
	Indices [Size]uint16
}

// Block returns the network block hash at x, y, z, each in 0..15.
// Bedrock orders blocks x, then z, then y.
func (l *Layer) Block(x, y, z int) uint32 {
	return l.Palette[l.Indices[x<<8|z<<4|y]]
}

// SubChunk is one decoded 16x16x16 section.
type SubChunk struct {
	Version byte
	// Y is the sub-chunk's vertical index (block Y divided by 16). Only
	// version 9 carries it; it is 0 for version 8.
	Y      int8
	Layers []Layer
	// Trailer is whatever followed the last storage. Sub-chunk packets append
	// block entity data here; the decoder does not interpret it.
	Trailer []byte
}

type reader struct {
	b []byte
}

func (r *reader) byte() (byte, error) {
	if len(r.b) == 0 {
		return 0, fmt.Errorf("%w: unexpected end of payload", ErrMalformed)
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c, nil
}

func (r *reader) next(n int) ([]byte, error) {
	if len(r.b) < n {
		return nil, fmt.Errorf("%w: need %d bytes, have %d", ErrMalformed, n, len(r.b))
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out, nil
}

// varint32 reads a zigzag-encoded 32-bit varint, the form palette entries use.
func (r *reader) varint32() (int32, error) {
	var u uint32
	for shift := uint(0); shift < 35; shift += 7 {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		u |= uint32(c&0x7f) << shift
		if c&0x80 == 0 {
			return int32(u>>1) ^ -int32(u&1), nil
		}
	}
	return 0, fmt.Errorf("%w: varint too long", ErrMalformed)
}

// Decode parses a network sub-chunk payload. Malformed input yields an error
// wrapping ErrMalformed and never panics.
func Decode(payload []byte) (SubChunk, error) {
	r := &reader{b: payload}
	var sc SubChunk

	ver, err := r.byte()
	if err != nil {
		return SubChunk{}, err
	}
	if ver != 8 && ver != 9 {
		return SubChunk{}, fmt.Errorf("%w: unsupported version %d", ErrMalformed, ver)
	}
	sc.Version = ver

	count, err := r.byte()
	if err != nil {
		return SubChunk{}, err
	}
	if ver == 9 {
		y, err := r.byte()
		if err != nil {
			return SubChunk{}, err
		}
		sc.Y = int8(y)
	}

	sc.Layers = make([]Layer, count)
	for i := range sc.Layers {
		if err := readStorage(r, &sc.Layers[i]); err != nil {
			return SubChunk{}, fmt.Errorf("layer %d: %w", i, err)
		}
	}
	if len(r.b) > 0 {
		sc.Trailer = r.b
	}
	return sc, nil
}

func readStorage(r *reader, l *Layer) error {
	header, err := r.byte()
	if err != nil {
		return err
	}
	if header&1 == 0 {
		return fmt.Errorf("%w: persistent (NBT) palette is not a network format", ErrMalformed)
	}
	bits := int(header >> 1)

	switch bits {
	case 0, 1, 2, 3, 4, 5, 6, 8, 16:
	default:
		return fmt.Errorf("%w: unsupported %d bits per block", ErrMalformed, bits)
	}

	if bits > 0 {
		// Indices are packed into whole 32-bit words without straddling, so
		// widths that do not divide 32 leave unused bits and need one extra word.
		perWord := 32 / bits
		words := (Size + perWord - 1) / perWord
		raw, err := r.next(words * 4)
		if err != nil {
			return err
		}
		mask := uint32(1)<<bits - 1
		for i := range l.Indices {
			w := i / perWord
			word := uint32(raw[w*4]) | uint32(raw[w*4+1])<<8 | uint32(raw[w*4+2])<<16 | uint32(raw[w*4+3])<<24
			l.Indices[i] = uint16(word >> (uint(i%perWord) * uint(bits)) & mask)
		}
	}

	n := int32(1)
	if bits > 0 {
		if n, err = r.varint32(); err != nil {
			return err
		}
	}
	if n < 1 || n > maxPalette {
		return fmt.Errorf("%w: palette size %d", ErrMalformed, n)
	}
	l.Palette = make([]uint32, n)
	for i := range l.Palette {
		v, err := r.varint32()
		if err != nil {
			return err
		}
		l.Palette[i] = uint32(v)
	}
	for _, ix := range l.Indices {
		if int(ix) >= len(l.Palette) {
			return fmt.Errorf("%w: index %d outside palette of %d", ErrMalformed, ix, len(l.Palette))
		}
	}
	return nil
}
