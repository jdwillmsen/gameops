package structures

import (
	"encoding/binary"
	"errors"
	"math"
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

// recordDepth is how deep an actor or a block entity may nest. A shulker
// box inside a chest is about eight levels; past this a record is refused
// rather than followed. These are measured once and only their own fields
// are looked at, so depth costs them nothing but the recursion.
const recordDepth = 64

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
	size, err := measure(tagCompound, b, 1, maxDepth)
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
		size, err := measure(tag, b, 1, recordDepth)
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
		size, err := measure(tagCompound, b, 1, recordDepth)
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

// measure is how many bytes the payload of a tag of this type takes at the
// start of b, refusing one that nests deeper than limit.
func measure(tag byte, b []byte, depth, limit int) (int, error) {
	if depth > limit {
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
		return listSize(b, depth, limit)
	case tagCompound:
		return compoundSize(b, depth, limit)
	default:
		return 0, errNBT
	}
	if size > len(b) {
		return 0, errNBT
	}
	return size, nil
}

func listSize(b []byte, depth, limit int) (int, error) {
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
		size, err := measure(elem, b[off:], depth+1, limit)
		if err != nil {
			return 0, err
		}
		off += size
	}
	return off, nil
}

func compoundSize(b []byte, depth, limit int) (int, error) {
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
		size, err := measure(tag, b[off:], depth+1, limit)
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

// fieldsAt calls visit for each tag directly inside the named compound b
// starts with, and returns what follows that compound: an actor's record is
// one, and a chunk's block entities are several, one after another. The
// compound is measured as it is walked, so visit has seen its first tags
// before a later one is found to be damaged; a caller keeps nothing of a
// compound this returns an error for.
func fieldsAt(b []byte, visit func(name []byte, tag byte, payload []byte)) ([]byte, error) {
	if len(b) < 3 || b[0] != tagCompound {
		return nil, errNBT
	}
	off := 3 + int(binary.LittleEndian.Uint16(b[1:3]))
	for {
		if off >= len(b) {
			return nil, errNBT
		}
		tag := b[off]
		off++
		if tag == tagEnd {
			return b[off:], nil
		}
		if len(b)-off < 2 {
			return nil, errNBT
		}
		n := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2
		if len(b)-off < n {
			return nil, errNBT
		}
		name := b[off : off+n]
		off += n
		size, err := measure(tag, b[off:], 1, recordDepth)
		if err != nil {
			return nil, err
		}
		visit(name, tag, b[off:off+size])
		off += size
	}
}

// wholeOf reads a whole number of any width.
func wholeOf(tag byte, payload []byte) (int64, bool) {
	switch tag {
	case tagByte:
		return int64(int8(payload[0])), true
	case tagShort:
		return int64(int16(binary.LittleEndian.Uint16(payload))), true
	case tagInt:
		return int64(int32(binary.LittleEndian.Uint32(payload))), true
	case tagLong:
		return int64(binary.LittleEndian.Uint64(payload)), true
	}
	return 0, false
}

func textOf(tag byte, payload []byte) (string, bool) {
	if tag != tagString {
		return "", false
	}
	return string(payload[2:]), true
}

// bytesOf is a string's text where it lies in its record, for a reader
// that passes many strings and keeps few. It is good only as long as the
// record is.
func bytesOf(tag byte, payload []byte) ([]byte, bool) {
	if tag != tagString {
		return nil, false
	}
	return payload[2:], true
}

// placeOf reads a list of exactly three floats, which is how an actor's
// position is stored, as the block it is in.
func placeOf(tag byte, payload []byte) (x, y, z int32, ok bool) {
	if tag != tagList || payload[0] != tagFloat || binary.LittleEndian.Uint32(payload[1:5]) != 3 {
		return 0, 0, 0, false
	}
	var at [3]int32
	for i := range at {
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(payload[5+4*i:])))
		if math.IsNaN(v) || math.Abs(v) > maxCoordinate {
			return 0, 0, 0, false
		}
		at[i] = int32(math.Floor(v))
	}
	return at[0], at[1], at[2], true
}
