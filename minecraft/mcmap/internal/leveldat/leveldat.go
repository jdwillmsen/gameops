// Package leveldat reads the two facts the map needs from a Bedrock world's
// level.dat: the seed the world generates from, and where it spawns players.
//
// The file is only ever opened for reading, and only the copy in the mirror:
// the server rewrites its own while it runs.
package leveldat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Level is what the map uses of a world's settings.
type Level struct {
	// Seed is RandomSeed, the whole 64 bits of it. It is not served to
	// browsers: with it, a seed map shows everything the world has not
	// generated yet.
	Seed int64
	// SpawnX and SpawnZ are the world spawn, in blocks.
	SpawnX, SpawnZ int32
	// SpawnY is only meaningful when SpawnYKnown: a world whose spawn height
	// was never resolved stores a sentinel instead.
	SpawnY      int32
	SpawnYKnown bool
}

const (
	// A real level.dat is a few kilobytes. The bound is what stops a
	// damaged or substituted file being read into memory whole.
	maxSize = 1 << 20
	// The file starts with a storage version and the length of what
	// follows, both 32-bit little-endian.
	headerSize = 8
	// Nothing the map reads is nested, but skipping a tag means walking
	// into it; this bounds the recursion on a file that nests without end.
	maxDepth = 64
	// What a world stores for a spawn height it has not worked out yet.
	unresolvedY = 32767
)

// Tag types of the NBT format, as stored.
const (
	tagEnd = iota
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

var errShort = errors.New("level.dat ends inside a tag")

// Read parses the level.dat at path.
func Read(path string) (Level, error) {
	f, err := os.Open(path)
	if err != nil {
		return Level{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return Level{}, err
	}
	if len(data) > maxSize {
		return Level{}, fmt.Errorf("level.dat is over %d bytes", maxSize)
	}
	return Parse(data)
}

// Parse reads a level.dat held in memory.
func Parse(data []byte) (Level, error) {
	if len(data) < headerSize {
		return Level{}, errors.New("level.dat is shorter than its header")
	}
	body := data[headerSize:]
	if declared := binary.LittleEndian.Uint32(data[4:8]); uint64(declared) != uint64(len(body)) {
		// A file caught half-written has a header that promises more than
		// is there; reading on would take whatever tags did arrive as the
		// whole of the world's settings.
		return Level{}, fmt.Errorf("level.dat header declares %d bytes and %d follow", declared, len(body))
	}
	r := &reader{b: body}
	if kind, err := r.byte(); err != nil || kind != tagCompound {
		return Level{}, errors.New("level.dat does not start with a compound tag")
	}
	if _, err := r.string(); err != nil {
		return Level{}, err
	}

	var level Level
	var seen struct{ seed, x, y, z bool }
	for {
		kind, err := r.byte()
		if err != nil {
			return Level{}, err
		}
		if kind == tagEnd {
			break
		}
		name, err := r.string()
		if err != nil {
			return Level{}, err
		}
		switch {
		case name == "RandomSeed" && kind == tagLong:
			v, err := r.uint(8)
			if err != nil {
				return Level{}, err
			}
			level.Seed, seen.seed = int64(v), true
		case name == "SpawnX" && kind == tagInt, name == "SpawnY" && kind == tagInt, name == "SpawnZ" && kind == tagInt:
			v, err := r.uint(4)
			if err != nil {
				return Level{}, err
			}
			switch name {
			case "SpawnX":
				level.SpawnX, seen.x = int32(v), true
			case "SpawnY":
				level.SpawnY, seen.y = int32(v), true
			case "SpawnZ":
				level.SpawnZ, seen.z = int32(v), true
			}
		default:
			if err := r.skip(kind, 0); err != nil {
				return Level{}, err
			}
		}
	}
	// Zero is a seed and the origin is a spawn, so absence has to be an
	// error rather than a default the map would then draw.
	if !seen.seed {
		return Level{}, errors.New("level.dat has no RandomSeed")
	}
	if !seen.x || !seen.z {
		return Level{}, errors.New("level.dat has no world spawn")
	}
	level.SpawnYKnown = seen.y && level.SpawnY != unresolvedY
	return level, nil
}

type reader struct {
	b []byte
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.b) {
		return nil, errShort
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out, nil
}

func (r *reader) byte() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// uint reads a little-endian integer of size bytes.
func (r *reader) uint(size int) (uint64, error) {
	b, err := r.take(size)
	if err != nil {
		return 0, err
	}
	var v uint64
	for i := size - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v, nil
}

func (r *reader) string() (string, error) {
	n, err := r.uint(2)
	if err != nil {
		return "", err
	}
	b, err := r.take(int(n))
	return string(b), err
}

// count reads an array or list length. A negative one is malformed, and one
// longer than the file cannot be honest, which is what keeps a hostile
// length from becoming a long loop.
func (r *reader) count() (int, error) {
	v, err := r.uint(4)
	if err != nil {
		return 0, err
	}
	n := int32(v)
	if n < 0 || int(n) > len(r.b) {
		return 0, errShort
	}
	return int(n), nil
}

func (r *reader) skip(kind byte, depth int) error {
	if depth > maxDepth {
		return errors.New("level.dat nests deeper than any real one")
	}
	fixed := func(n int) error {
		_, err := r.take(n)
		return err
	}
	array := func(width int) error {
		n, err := r.count()
		if err != nil {
			return err
		}
		if n > len(r.b)/width {
			return errShort
		}
		return fixed(n * width)
	}
	switch kind {
	case tagByte:
		return fixed(1)
	case tagShort:
		return fixed(2)
	case tagInt, tagFloat:
		return fixed(4)
	case tagLong, tagDouble:
		return fixed(8)
	case tagByteArray:
		return array(1)
	case tagIntArray:
		return array(4)
	case tagLongArray:
		return array(8)
	case tagString:
		_, err := r.string()
		return err
	case tagList:
		element, err := r.byte()
		if err != nil {
			return err
		}
		v, err := r.uint(4)
		if err != nil {
			return err
		}
		n := int32(v)
		// An empty list may declare the end tag as its element type, and
		// an end tag has no payload to bound the count by.
		if n <= 0 {
			return nil
		}
		if element == tagEnd || int(n) > len(r.b) {
			return errShort
		}
		for range n {
			if err := r.skip(element, depth+1); err != nil {
				return err
			}
		}
		return nil
	case tagCompound:
		for {
			inner, err := r.byte()
			if err != nil {
				return err
			}
			if inner == tagEnd {
				return nil
			}
			if _, err := r.string(); err != nil {
				return err
			}
			if err := r.skip(inner, depth+1); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("level.dat holds an unknown tag type %d", kind)
}
