package markers

import (
	"encoding/binary"
	"errors"
	"math"
)

// The tag types of the little-endian NBT that Bedrock writes to disk.
const (
	tagEnd byte = iota
	tagByte
	tagShort
	tagInt
	tagLong
	tagFloat
	tagDouble
	tagByteArray
	tagString
	tagList
	tagCompound
	tagIntArray
	tagLongArray
)

// maxDepth is how deep a record may nest. A shulker box inside a chest is
// about eight levels; past this a record is refused rather than followed.
const maxDepth = 64

var errRecord = errors.New("malformed record")

// A general decoder builds every tag of a record in memory, and a chest's
// record is mostly its contents. This reader instead steps over a record in
// place and hands out only the tags directly inside it, so it allocates
// nothing and a length written in the data can never be more than a number
// to check against the bytes that are actually there.

// fields calls visit for each tag directly inside the compound b starts
// with, and returns what follows that compound. A chunk's block entities are
// several compounds one after another, so the caller loops.
func fields(b []byte, visit func(name []byte, tag byte, payload []byte)) ([]byte, error) {
	if len(b) < 3 || b[0] != tagCompound {
		return nil, errRecord
	}
	off := 3 + int(binary.LittleEndian.Uint16(b[1:3]))
	for {
		if off >= len(b) {
			return nil, errRecord
		}
		tag := b[off]
		off++
		if tag == tagEnd {
			return b[off:], nil
		}
		if len(b)-off < 2 {
			return nil, errRecord
		}
		nameLen := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2
		if len(b)-off < nameLen {
			return nil, errRecord
		}
		name := b[off : off+nameLen]
		off += nameLen
		size, err := payloadSize(tag, b[off:], 1)
		if err != nil {
			return nil, err
		}
		visit(name, tag, b[off:off+size])
		off += size
	}
}

// payloadSize is how many bytes the payload of a tag of this type takes at
// the start of b.
func payloadSize(tag byte, b []byte, depth int) (int, error) {
	if depth > maxDepth {
		return 0, errRecord
	}
	size := 0
	switch tag {
	case tagByte:
		size = 1
	case tagShort:
		size = 2
	case tagInt, tagFloat:
		size = 4
	case tagLong, tagDouble:
		size = 8
	case tagString:
		if len(b) < 2 {
			return 0, errRecord
		}
		size = 2 + int(binary.LittleEndian.Uint16(b))
	case tagByteArray, tagIntArray, tagLongArray:
		if len(b) < 4 {
			return 0, errRecord
		}
		n := int64(int32(binary.LittleEndian.Uint32(b)))
		width := int64(1)
		if tag == tagIntArray {
			width = 4
		} else if tag == tagLongArray {
			width = 8
		}
		if n < 0 || 4+n*width > int64(len(b)) {
			return 0, errRecord
		}
		size = 4 + int(n*width)
	case tagList:
		return listSize(b, depth)
	case tagCompound:
		return compoundSize(b, depth)
	default:
		return 0, errRecord
	}
	if size > len(b) {
		return 0, errRecord
	}
	return size, nil
}

func listSize(b []byte, depth int) (int, error) {
	if len(b) < 5 {
		return 0, errRecord
	}
	elem, n := b[0], int64(int32(binary.LittleEndian.Uint32(b[1:5])))
	// An empty list is written with the end tag as its element type. With
	// any other count that type has no size, and counting through such a
	// list would be a loop the data chose the length of.
	if n < 0 || (elem == tagEnd && n != 0) {
		return 0, errRecord
	}
	if width := scalarWidth(elem); width > 0 {
		if 5+n*width > int64(len(b)) {
			return 0, errRecord
		}
		return 5 + int(n*width), nil
	}
	off := 5
	for range n {
		// Every element type left takes at least a byte, so this ends
		// within the record whatever count it claims.
		size, err := payloadSize(elem, b[off:], depth+1)
		if err != nil {
			return 0, err
		}
		off += size
	}
	return off, nil
}

func scalarWidth(tag byte) int64 {
	switch tag {
	case tagByte:
		return 1
	case tagShort:
		return 2
	case tagInt, tagFloat:
		return 4
	case tagLong, tagDouble:
		return 8
	}
	return 0
}

func compoundSize(b []byte, depth int) (int, error) {
	off := 0
	for {
		if off >= len(b) {
			return 0, errRecord
		}
		tag := b[off]
		off++
		if tag == tagEnd {
			return off, nil
		}
		if len(b)-off < 2 {
			return 0, errRecord
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
		if off > len(b) {
			return 0, errRecord
		}
		size, err := payloadSize(tag, b[off:], depth+1)
		if err != nil {
			return 0, err
		}
		off += size
	}
}

// intOf reads a whole-number tag of any width.
func intOf(tag byte, payload []byte) (int32, bool) {
	switch tag {
	case tagByte:
		return int32(payload[0]), true
	case tagShort:
		return int32(int16(binary.LittleEndian.Uint16(payload))), true
	case tagInt:
		return int32(binary.LittleEndian.Uint32(payload)), true
	}
	return 0, false
}

func stringOf(tag byte, payload []byte) (string, bool) {
	if tag != tagString {
		return "", false
	}
	return string(payload[2:]), true
}

// listLen is how many elements a list tag holds.
func listLen(tag byte, payload []byte) int {
	if tag != tagList {
		return 0
	}
	return int(int32(binary.LittleEndian.Uint32(payload[1:5])))
}

// floatsOf reads a list of exactly three floats, which is how an actor's
// position is stored.
func floatsOf(tag byte, payload []byte) (x, y, z float64, ok bool) {
	if tag != tagList || payload[0] != tagFloat || listLen(tag, payload) != 3 {
		return 0, 0, 0, false
	}
	at := func(i int) float64 {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(payload[5+4*i:])))
	}
	return at(0), at(1), at(2), true
}
