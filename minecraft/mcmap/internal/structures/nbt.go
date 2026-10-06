package structures

import (
	"encoding/binary"
	"errors"
)

// The tag types of the little-endian NBT a village's records are written in.
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

// maxDepth is how deep a village record may nest. A real one is five deep:
// the record, its list, a villager, that villager's list, one entry. A
// record is walked once per level, so this also bounds the work one of a
// given size can ask for.
const maxDepth = 12

var errNBT = errors.New("record is not sound NBT")

// This reader steps over a record in place and hands out the tags directly
// inside a compound or a list. It builds nothing, so a length written in the
// data is only ever a number to check against the bytes that are there.

// rootCompound is the payload of the single unnamed compound a record is.
func rootCompound(b []byte) ([]byte, error) {
	if len(b) < 3 || b[0] != tagCompound {
		return nil, errNBT
	}
	name := 3 + int(binary.LittleEndian.Uint16(b[1:3]))
	if name > len(b) {
		return nil, errNBT
	}
	b = b[name:]
	size, err := payloadSize(tagCompound, b, 1)
	if err != nil || size != len(b) {
		// Bytes after the record's end mean it is not the record it was
		// taken for, whatever its start looks like.
		return nil, errNBT
	}
	return b, nil
}

// eachField calls visit for each tag directly inside the compound whose
// payload b starts with. b has been measured already, so it cannot run out.
func eachField(b []byte, visit func(name []byte, tag byte, payload []byte) error) error {
	for {
		tag := b[0]
		if tag == tagEnd {
			return nil
		}
		n := int(binary.LittleEndian.Uint16(b[1:3]))
		name := b[3 : 3+n]
		b = b[3+n:]
		size, err := payloadSize(tag, b, 1)
		if err != nil {
			return err
		}
		if err := visit(name, tag, b[:size]); err != nil {
			return err
		}
		b = b[size:]
	}
}

// eachCompound calls visit with the payload of each compound in a list of
// them, and returns how many there were. A list of anything else that is not
// empty is refused.
func eachCompound(tag byte, payload []byte, visit func(compound []byte) error) (int, error) {
	if tag != tagList {
		return 0, errNBT
	}
	elem, n := payload[0], int(int32(binary.LittleEndian.Uint32(payload[1:5])))
	if n == 0 {
		return 0, nil
	}
	if elem != tagCompound {
		return 0, errNBT
	}
	b := payload[5:]
	for range n {
		size, err := payloadSize(tagCompound, b, 1)
		if err != nil {
			return 0, err
		}
		if visit != nil {
			if err := visit(b[:size]); err != nil {
				return 0, err
			}
		}
		b = b[size:]
	}
	return n, nil
}

// payloadSize is how many bytes the payload of a tag of this type takes at
// the start of b.
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
	if n < 0 {
		return 0, errNBT
	}
	width := int64(0)
	switch elem {
	case tagByte:
		width = 1
	case tagShort:
		width = 2
	case tagInt, tagFloat:
		width = 4
	case tagLong, tagDouble:
		width = 8
	}
	if width > 0 {
		if 5+n*width > int64(len(b)) {
			return 0, errNBT
		}
		return 5 + int(n*width), nil
	}
	off := 5
	for range n {
		// Every element type left takes at least a byte or is refused, so
		// this ends within the record whatever count it claims.
		size, err := payloadSize(elem, b[off:], depth+1)
		if err != nil {
			return 0, err
		}
		off += size
	}
	return off, nil
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

// intOf reads a 32-bit whole number, which is how every coordinate in a
// village record is written.
func intOf(tag byte, payload []byte) (int32, bool) {
	if tag != tagInt {
		return 0, false
	}
	return int32(binary.LittleEndian.Uint32(payload)), true
}
