package camera

// Wire format, version 1. Every websocket message from the camera is binary
// and starts with a type byte; integers are little-endian.
//
//	hello        [1][version u8]                         first message of a connection
//	materials    [2][n u16]{id u8, alpha u8, r g b u8, len u8, name}...
//	sub-chunk    [3][cx i32][cy i32][cz i32][n u16][palette n x u8][indices]
//	block        [4][x i32][y i32][z i32][material u8]
//	tick         [5][flags u8][self 5 x f32 if flags&1][n uvarint][entity]...
//	remove       [6][n uvarint][id uvarint]...
//	reset        [7]
//	entity types [8][n u16]{len u8, name}...             index = type id
//
// A sub-chunk's palette lists the materials it uses (1 to 256 entries; 0 is
// "not drawn"). Indices are packed LSB-first, bits(n) each, in
// (y*16+z)*16+x order, with no padding between cells; a palette of one entry
// has zero index bits. An entity record is
// [id uvarint][type u16][x y z f32][yaw u8][pitch u8], angles in 360/256
// degree steps; self is [x y z yaw pitch f32]. A tick frame holds the newest
// state of everything that moved since the last one.
//
// The client sends [1][radius u8] to widen the sub-chunk radius (in chunks)
// it wants replayed.

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

const (
	wireVersion byte = 1

	frameHello       byte = 1
	frameMaterials   byte = 2
	frameSubChunk    byte = 3
	frameBlock       byte = 4
	frameTick        byte = 5
	frameRemove      byte = 6
	frameReset       byte = 7
	frameEntityTypes byte = 8

	cmdRadius byte = 1

	subChunkCells = 4096
	subHeaderSize = 1 + 12
)

type selfPose struct{ X, Y, Z, Yaw, Pitch float32 }

func helloFrame() []byte { return []byte{frameHello, wireVersion} }

func resetFrame() []byte { return []byte{frameReset} }

// indexBits is the width of one palette index.
func indexBits(palette int) int {
	if palette <= 1 {
		return 0
	}
	return bits.Len(uint(palette - 1))
}

func encodeSubChunk(cx, cy, cz int32, c *[subChunkCells]uint8) []byte {
	var slot [256]int16
	for i := range slot {
		slot[i] = -1
	}
	palette := make([]uint8, 0, 8)
	for _, m := range c {
		if slot[m] < 0 {
			slot[m] = int16(len(palette))
			palette = append(palette, m)
		}
	}
	w := indexBits(len(palette))
	f := make([]byte, subHeaderSize+2, subHeaderSize+2+len(palette)+subChunkCells*w/8)
	f[0] = frameSubChunk
	binary.LittleEndian.PutUint32(f[1:], uint32(cx))
	binary.LittleEndian.PutUint32(f[5:], uint32(cy))
	binary.LittleEndian.PutUint32(f[9:], uint32(cz))
	binary.LittleEndian.PutUint16(f[13:], uint16(len(palette)))
	f = append(f, palette...)
	if w == 0 {
		return f
	}
	f = append(f, make([]byte, subChunkCells*w/8)...)
	body := f[len(f)-subChunkCells*w/8:]
	for i, m := range c {
		v, at := uint(slot[m]), i*w
		for b := 0; b < w; b++ {
			if v>>b&1 != 0 {
				body[(at+b)>>3] |= 1 << ((at + b) & 7)
			}
		}
	}
	return f
}

func decodeSubChunk(f []byte) (cx, cy, cz int32, c [subChunkCells]uint8, err error) {
	if len(f) < subHeaderSize+2 || f[0] != frameSubChunk {
		return 0, 0, 0, c, errors.New("camera: not a sub-chunk frame")
	}
	cx = int32(binary.LittleEndian.Uint32(f[1:]))
	cy = int32(binary.LittleEndian.Uint32(f[5:]))
	cz = int32(binary.LittleEndian.Uint32(f[9:]))
	n := int(binary.LittleEndian.Uint16(f[13:]))
	if n < 1 || n > 256 {
		return 0, 0, 0, c, errors.New("camera: bad palette size")
	}
	w := indexBits(n)
	if len(f) != subHeaderSize+2+n+subChunkCells*w/8 {
		return 0, 0, 0, c, errors.New("camera: bad sub-chunk length")
	}
	palette, body := f[subHeaderSize+2:subHeaderSize+2+n], f[subHeaderSize+2+n:]
	for i := range c {
		var v int
		for b := 0; b < w; b++ {
			at := i*w + b
			v |= int(body[at>>3]>>(at&7)&1) << b
		}
		if v >= n {
			return 0, 0, 0, c, errors.New("camera: palette index out of range")
		}
		c[i] = palette[v]
	}
	return cx, cy, cz, c, nil
}

func encodeBlock(x, y, z int32, material uint8) []byte {
	b := make([]byte, 14)
	b[0] = frameBlock
	binary.LittleEndian.PutUint32(b[1:], uint32(x))
	binary.LittleEndian.PutUint32(b[5:], uint32(y))
	binary.LittleEndian.PutUint32(b[9:], uint32(z))
	b[13] = material
	return b
}

func angleByte(deg float32) byte {
	return byte(int(math.Round(float64(deg)/360*256)) & 255)
}

func entityRecord(id uint64, typ uint16, x, y, z, yaw, pitch float32) []byte {
	r := binary.AppendUvarint(make([]byte, 0, 24), id)
	r = binary.LittleEndian.AppendUint16(r, typ)
	r = appendF32(r, x, y, z)
	return append(r, angleByte(yaw), angleByte(pitch))
}

func appendF32(b []byte, vs ...float32) []byte {
	for _, v := range vs {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func encodeTick(self *selfPose, ents [][]byte) []byte {
	f := []byte{frameTick, 0}
	if self != nil {
		f[1] = 1
		f = appendF32(f, self.X, self.Y, self.Z, self.Yaw, self.Pitch)
	}
	f = binary.AppendUvarint(f, uint64(len(ents)))
	for _, e := range ents {
		f = append(f, e...)
	}
	return f
}

func encodeRemove(ids []uint64) []byte {
	f := binary.AppendUvarint([]byte{frameRemove}, uint64(len(ids)))
	for _, id := range ids {
		f = binary.AppendUvarint(f, id)
	}
	return f
}

func encodeMaterials(list []material) []byte {
	f := binary.LittleEndian.AppendUint16([]byte{frameMaterials}, uint16(len(list)))
	for _, m := range list {
		r, g, b := parseColor(m.Color)
		f = append(f, m.ID, uint8(math.Round(float64(m.Alpha)*255)), r, g, b, uint8(len(m.Name)))
		f = append(f, m.Name...)
	}
	return f
}

func encodeEntityTypes(names []string) []byte {
	f := binary.LittleEndian.AppendUint16([]byte{frameEntityTypes}, uint16(len(names)))
	for _, n := range names {
		f = append(f, uint8(len(n)))
		f = append(f, n...)
	}
	return f
}

// parseColor reads the "#rrggbb" form materials.go produces.
func parseColor(s string) (r, g, b uint8) {
	h := func(c byte) uint8 {
		switch {
		case c >= 'a':
			return c - 'a' + 10
		case c >= 'A':
			return c - 'A' + 10
		}
		return c - '0'
	}
	if len(s) != 7 {
		return 0xff, 0, 0xff
	}
	return h(s[1])<<4 | h(s[2]), h(s[3])<<4 | h(s[4]), h(s[5])<<4 | h(s[6])
}
