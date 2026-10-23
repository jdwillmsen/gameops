package camera

// Wire format, version 4. Every websocket message from the camera is binary
// and starts with a type byte; integers are little-endian.
//
//	hello        [1][version u8]                         first message of a connection
//	materials    [2][n u16]{id u16, alpha u8, r g b u8, flags u8, len u8, name}...
//	sub-chunk    [3][cx i32][cy i32][cz i32][n u16][palette n x u16][indices]
//	block        [4][x i32][y i32][z i32][material u16]
//	tick         [5][flags u8][self 5 x f32 if flags&1][n uvarint][entity]...
//	remove       [6][n uvarint][id uvarint]...
//	reset        [7]
//	entity types [8][n u16]{len u8, name}...             index = type id
//	atlas        [9][atlas version u8]                   a texture atlas can be fetched
//	names        [10][n uvarint]{id uvarint, len u8, name}...   player name labels
//	entity data  [11][n uvarint]{id uvarint, flags u8, colour u8, len u8, item}...
//	models       [12][models version u8]                 entity models can be fetched
//	skin         [13][id uvarint][flags u8][len uvarint][png]   a player's skin
//	armour       [14][n uvarint]{id uvarint, 4 x {len u8, item}}...   worn armour
//
// Entity data carries what changes how an entity looks and is not part of
// its pose: flags bit 0 is baby and bit 1 sheared, colour is the dye index
// (0 to 15) of a sheep's wool, and item is the name of a dropped item without
// its namespace. It is sent when an entity appears and whenever one of these
// changes; an entity never mentioned has all of them zero. Pages that predate
// a frame type skip it. The models frame carries no data either: the page
// fetches /v1/camera/entities.json and /v1/camera/entities.png, and a page
// that never gets the frame keeps drawing stand-in boxes.
//
// The skin frame carries a PNG in the standard 64-wide player layout (any whole
// multiple of it; a 64x32 skin has had its left limbs filled in), flags bit 0
// marks slim arms, and id 0 is the unit's own body, drawn in the third-person
// modes. The armour frame lists the item names (without namespace, "" for
// none) in helmet, chestplate, leggings, boots order; it replaces what was
// known for each id it names. A block's name may end in @ and its boxes in
// sixteenths of a block, "x0,y0,z0,x1,y1,z1;..." (stairs, top slabs, doors),
// which the page draws instead of its fixed shape for that kind of block.
//
// Material flag bit 0 marks a block that is not a full cube; a page without an
// atlas has no shape to draw it with and skips it. Version 2 added the flags
// byte; version 3 added the head yaw to entity records; version 4 widened
// material ids from one byte to two, and made a materials frame additive: it
// defines the ids it lists and leaves the rest as they were, so the server
// sends only what is new instead of the whole table each time. A page asks for
// version 4 with ?wire=4 on the websocket; without it the server speaks
// version 3 (ids above 255 all drawn as id 255, the full table in every
// materials frame), so a page loaded before an upgrade keeps working until it
// reloads. A page that reads a hello of 3 is talking to a server that
// predates version 4 and decodes ids as single bytes.
//
// The atlas frame carries no data: the page fetches the PNG and the table from
// /v1/camera/atlas.png and /v1/camera/atlas.json, and a page that never gets
// the frame keeps drawing flat colours.
//
// A sub-chunk's palette lists the materials it uses (1 to 4096 entries; 0 is
// "not drawn"). Indices are packed LSB-first, bits(n) each, in
// (y*16+z)*16+x order, with no padding between cells; a palette of one entry
// has zero index bits. An entity record is
// [id uvarint][type u16][x y z f32][yaw u8][pitch u8][head yaw u8], angles in
// 360/256 degree steps, x y z at the entity's feet (the hub shifts MovePlayer's
// eye height down); self is [x y z yaw pitch f32]. A tick frame holds the newest
// state of everything that moved since the last one.
//
// The client sends [1][radius u8] to widen the sub-chunk radius (in chunks)
// it wants replayed.

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"slices"
	"sync"
	"unicode/utf8"
)

const (
	wireVersion byte = 4

	// legacyWireVersion is what a page that does not ask for a version gets.
	legacyWireVersion byte = 3

	frameHello       byte = 1
	frameMaterials   byte = 2
	frameSubChunk    byte = 3
	frameBlock       byte = 4
	frameTick        byte = 5
	frameRemove      byte = 6
	frameReset       byte = 7
	frameEntityTypes byte = 8
	frameAtlas       byte = 9
	frameNames       byte = 10
	frameEntityData  byte = 11
	frameModels      byte = 12
	frameSkin        byte = 13
	frameArmour      byte = 14

	// Entity data flag bits.
	entBaby    byte = 1
	entSheared byte = 2

	matNonCube byte = 1

	// atlasFormat is the version of the table document the page fetches.
	atlasFormat byte = 1

	cmdRadius byte = 1

	subChunkCells = 4096
	subHeaderSize = 1 + 12
)

type selfPose struct{ X, Y, Z, Yaw, Pitch float32 }

func helloFrame() []byte { return []byte{frameHello, wireVersion} }

func atlasFrame() []byte { return []byte{frameAtlas, atlasFormat} }

// modelsFormat is the version of the entity table document the page fetches.
const modelsFormat byte = 1

func modelsFrame() []byte { return []byte{frameModels, modelsFormat} }

// entityLook is the appearance data of one entity.
type entityLook struct {
	Flags byte
	Color byte
	Item  string
}

func (l entityLook) zero() bool { return l == entityLook{} }

func encodeEntityData(looks map[uint64]entityLook) []byte {
	ids := make([]uint64, 0, len(looks))
	for id := range looks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	f := binary.AppendUvarint([]byte{frameEntityData}, uint64(len(ids)))
	for _, id := range ids {
		l := looks[id]
		item := l.Item
		if len(item) > maxNameLen {
			item = ""
		}
		f = binary.AppendUvarint(f, id)
		f = append(f, l.Flags, l.Color, uint8(len(item)))
		f = append(f, item...)
	}
	return f
}

func resetFrame() []byte { return []byte{frameReset} }

// indexBits is the width of one palette index.
func indexBits(palette int) int {
	if palette <= 1 {
		return 0
	}
	return bits.Len(uint(palette - 1))
}

// slotPool holds the id-to-palette-slot scratch table, 128 KiB, so encoding a
// sub-chunk neither allocates nor clears it: each use puts back only the
// entries it set.
var slotPool = sync.Pool{New: func() any {
	s := new([maxMaterials + 1]int16)
	for i := range s {
		s[i] = -1
	}
	return s
}}

func encodeSubChunk(cx, cy, cz int32, c *[subChunkCells]uint16) []byte {
	slot := slotPool.Get().(*[maxMaterials + 1]int16)
	palette := make([]uint16, 0, 8)
	for _, m := range c {
		if slot[m] < 0 {
			slot[m] = int16(len(palette))
			palette = append(palette, m)
		}
	}
	w := indexBits(len(palette))
	f := make([]byte, subHeaderSize+2, subHeaderSize+2+2*len(palette)+subChunkCells*w/8)
	f[0] = frameSubChunk
	binary.LittleEndian.PutUint32(f[1:], uint32(cx))
	binary.LittleEndian.PutUint32(f[5:], uint32(cy))
	binary.LittleEndian.PutUint32(f[9:], uint32(cz))
	binary.LittleEndian.PutUint16(f[13:], uint16(len(palette)))
	for _, m := range palette {
		f = binary.LittleEndian.AppendUint16(f, m)
	}
	if w > 0 {
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
	}
	for _, m := range palette {
		slot[m] = -1
	}
	slotPool.Put(slot)
	return f
}

func decodeSubChunk(f []byte) (cx, cy, cz int32, c [subChunkCells]uint16, err error) {
	if len(f) < subHeaderSize+2 || f[0] != frameSubChunk {
		return 0, 0, 0, c, errors.New("camera: not a sub-chunk frame")
	}
	cx = int32(binary.LittleEndian.Uint32(f[1:]))
	cy = int32(binary.LittleEndian.Uint32(f[5:]))
	cz = int32(binary.LittleEndian.Uint32(f[9:]))
	n := int(binary.LittleEndian.Uint16(f[13:]))
	if n < 1 || n > subChunkCells {
		return 0, 0, 0, c, errors.New("camera: bad palette size")
	}
	w := indexBits(n)
	if len(f) != subHeaderSize+2+2*n+subChunkCells*w/8 {
		return 0, 0, 0, c, errors.New("camera: bad sub-chunk length")
	}
	pal, body := f[subHeaderSize+2:subHeaderSize+2+2*n], f[subHeaderSize+2+2*n:]
	for i := range c {
		var v int
		for b := 0; b < w; b++ {
			at := i*w + b
			v |= int(body[at>>3]>>(at&7)&1) << b
		}
		if v >= n {
			return 0, 0, 0, c, errors.New("camera: palette index out of range")
		}
		c[i] = binary.LittleEndian.Uint16(pal[2*v:])
	}
	return cx, cy, cz, c, nil
}

func encodeBlock(x, y, z int32, material uint16) []byte {
	b := make([]byte, 15)
	b[0] = frameBlock
	binary.LittleEndian.PutUint32(b[1:], uint32(x))
	binary.LittleEndian.PutUint32(b[5:], uint32(y))
	binary.LittleEndian.PutUint32(b[9:], uint32(z))
	binary.LittleEndian.PutUint16(b[13:], material)
	return b
}

func angleByte(deg float32) byte {
	return byte(int(math.Round(float64(deg)/360*256)) & 255)
}

func entityRecord(id uint64, typ uint16, x, y, z, yaw, pitch, headYaw float32) []byte {
	r := binary.AppendUvarint(make([]byte, 0, 24), id)
	r = binary.LittleEndian.AppendUint16(r, typ)
	r = appendF32(r, x, y, z)
	return append(r, angleByte(yaw), angleByte(pitch), angleByte(headYaw))
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
		var flags byte
		if m.NonCube {
			flags |= matNonCube
		}
		f = binary.LittleEndian.AppendUint16(f, m.ID)
		f = append(f, uint8(math.Round(float64(m.Alpha)*255)), r, g, b, flags, uint8(len(m.Name)))
		f = append(f, m.Name...)
	}
	return f
}

// maxNameLen is what the length byte can carry.
const maxNameLen = 255

func encodeNames(names map[uint64]string) []byte {
	ids := make([]uint64, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	f := binary.AppendUvarint([]byte{frameNames}, uint64(len(ids)))
	for _, id := range ids {
		n := names[id]
		if len(n) > maxNameLen {
			n = n[:maxNameLen]
			// A cut inside a multi-byte character would send invalid UTF-8.
			for len(n) > 0 && !utf8.ValidString(n) {
				n = n[:len(n)-1]
			}
		}
		f = binary.AppendUvarint(f, id)
		f = append(f, uint8(len(n)))
		f = append(f, n...)
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
