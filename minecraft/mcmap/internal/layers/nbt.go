package layers

import (
	"encoding/binary"
	"errors"
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

// maxDepth bounds nesting so that a record whose lengths loop back on
// themselves is refused and not followed.
const maxDepth = 32

var errNBT = errors.New("malformed palette entry")

// blockName steps over one palette entry, a compound naming a block and
// holding its states, and returns the name and what follows the entry. The
// states are skipped in place: they are most of an entry, and the colour of
// a block does not depend on them.
func blockName(b []byte) (name []byte, rest []byte, err error) {
	if len(b) < 3 || b[0] != tagCompound {
		return nil, nil, errNBT
	}
	off := 3 + int(binary.LittleEndian.Uint16(b[1:3]))
	for {
		if off >= len(b) {
			return nil, nil, errNBT
		}
		tag := b[off]
		off++
		if tag == tagEnd {
			return name, b[off:], nil
		}
		if len(b)-off < 2 {
			return nil, nil, errNBT
		}
		n := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2
		if len(b)-off < n {
			return nil, nil, errNBT
		}
		field := b[off : off+n]
		off += n
		size, err := payloadSize(tag, b[off:], 1)
		if err != nil {
			return nil, nil, err
		}
		if tag == tagString && string(field) == "name" {
			name = b[off+2 : off+size]
		}
		off += size
	}
}

func payloadSize(tag byte, b []byte, depth int) (int, error) {
	if depth > maxDepth {
		return 0, errNBT
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
			return 0, errNBT
		}
		size = 2 + int(binary.LittleEndian.Uint16(b))
	case tagByteArray, tagIntArray, tagLongArray:
		if len(b) < 4 {
			return 0, errNBT
		}
		n := int64(int32(binary.LittleEndian.Uint32(b)))
		width := int64(1)
		switch tag {
		case tagIntArray:
			width = 4
		case tagLongArray:
			width = 8
		}
		if n < 0 || 4+n*width > int64(len(b)) {
			return 0, errNBT
		}
		size = 4 + int(n*width)
	case tagList:
		return listSize(b, depth)
	case tagCompound:
		return compoundSize(b, depth)
	default:
		return 0, errNBT
	}
	if size > len(b) {
		return 0, errNBT
	}
	return size, nil
}

func listSize(b []byte, depth int) (int, error) {
	if len(b) < 5 {
		return 0, errNBT
	}
	elem, n := b[0], int64(int32(binary.LittleEndian.Uint32(b[1:5])))
	// An empty list is written with the end tag as its element type; with a
	// count that type has no size, and the loop would be the data's to size.
	if n < 0 || (elem == tagEnd && n != 0) {
		return 0, errNBT
	}
	if width := scalarWidth(elem); width > 0 {
		if 5+n*width > int64(len(b)) {
			return 0, errNBT
		}
		return 5 + int(n*width), nil
	}
	off := 5
	for range n {
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
			return 0, errNBT
		}
		tag := b[off]
		off++
		if tag == tagEnd {
			return off, nil
		}
		if len(b)-off < 2 {
			return 0, errNBT
		}
		off += 2 + int(binary.LittleEndian.Uint16(b[off:]))
		if off > len(b) {
			return 0, errNBT
		}
		size, err := payloadSize(tag, b[off:], depth+1)
		if err != nil {
			return 0, err
		}
		off += size
	}
}
