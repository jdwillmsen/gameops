package players

import (
	"encoding/binary"
	"errors"
)

// The tag types of the little-endian NBT a record is written in.
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

const (
	// maxListLength is far past the recipes of a player who has unlocked
	// every one, which is the longest list a record has.
	maxListLength = 1 << 16
	maxDepth      = 32
)

var errGuard = errors.New("record has a length or depth the game would not write")

// guard walks a record without building anything and refuses one whose
// lists claim more elements than there are bytes left to hold them. The
// decoder makes a slice of whatever length a list header gives, so a few
// bytes could otherwise ask it for gigabytes; here each element is known to
// take at least a byte, and the record's own size bounds the count.
func guard(b []byte) error {
	if len(b) < 3 || b[0] != tagCompound {
		return errGuard
	}
	name := 3 + int(binary.LittleEndian.Uint16(b[1:3]))
	if name > len(b) {
		return errGuard
	}
	_, err := skip(tagCompound, b[name:], 1)
	return err
}

func skip(tag byte, b []byte, depth int) ([]byte, error) {
	if depth > maxDepth {
		return nil, errGuard
	}
	fixed := map[byte]int{tagByte: 1, tagShort: 2, tagInt: 4, tagLong: 8, tagFloat: 4, tagDouble: 8}
	if n, ok := fixed[tag]; ok {
		return advance(b, n)
	}
	switch tag {
	case tagString:
		if len(b) < 2 {
			return nil, errGuard
		}
		return advance(b[2:], int(binary.LittleEndian.Uint16(b)))
	case tagByteArray, tagIntArray, tagLongArray:
		if len(b) < 4 {
			return nil, errGuard
		}
		n := int(int32(binary.LittleEndian.Uint32(b)))
		width := map[byte]int{tagByteArray: 1, tagIntArray: 4, tagLongArray: 8}[tag]
		if n < 0 || n > maxListLength || n*width > len(b)-4 {
			return nil, errGuard
		}
		return b[4+n*width:], nil
	case tagList:
		if len(b) < 5 {
			return nil, errGuard
		}
		elem, n := b[0], int(int32(binary.LittleEndian.Uint32(b[1:5])))
		b = b[5:]
		// An element takes a byte at least: a compound its end tag.
		if n < 0 || n > maxListLength || n > len(b) || (elem == tagEnd && n > 0) || elem > tagLongArray {
			return nil, errGuard
		}
		for range n {
			var err error
			if b, err = skip(elem, b, depth+1); err != nil {
				return nil, err
			}
		}
		return b, nil
	case tagCompound:
		for {
			if len(b) < 1 {
				return nil, errGuard
			}
			inner := b[0]
			if inner == tagEnd {
				return b[1:], nil
			}
			if len(b) < 3 {
				return nil, errGuard
			}
			var err error
			if b, err = advance(b[3:], int(binary.LittleEndian.Uint16(b[1:3]))); err != nil {
				return nil, err
			}
			if b, err = skip(inner, b, depth+1); err != nil {
				return nil, err
			}
		}
	}
	return nil, errGuard
}

func advance(b []byte, n int) ([]byte, error) {
	if n < 0 || n > len(b) {
		return nil, errGuard
	}
	return b[n:], nil
}
