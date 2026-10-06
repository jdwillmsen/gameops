// Package markers finds the things players leave in the world that are
// worth a mark on the map: beds, containers with something in them, and
// mobs someone has named.
package markers

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// The most of each kind kept per dimension. The world this was measured on
// holds about 1,500 beds, 600 containers with contents and five named mobs,
// so these are several times what is real and exist for a world that is
// not: every marker is sent to the browser and drawn.
const (
	MaxBeds       = 5000
	MaxContainers = 5000
	MaxMobs       = 1000
)

// collectFactor is how many times a limit the scan holds on to before it
// stops collecting and only counts. Beds are stored as two halves and a
// large chest as two chests, so the scan has to hold more than it keeps;
// past this it is a hostile world, and which markers survive no longer
// matters.
const collectFactor = 4

const (
	// MaxName is the longest name sent to a browser, in characters. An
	// anvil stops at 50; a name can still be written into a save at any
	// length.
	MaxName = 64
	// maxRecord is the largest record read. The largest in the measured
	// world is 212 KB, a chunk of full chests.
	maxRecord = 8 << 20
	// maxCoordinate is past the edge of any Bedrock world.
	maxCoordinate = 32_000_000
	// tagBlockEntities is the chunk record listing its block entities:
	// NBT compounds one after another.
	tagBlockEntities = 0x31
)

var (
	// Each actor is one record under this prefix and its eight-byte
	// storage key; a chunk's digp record is the storage keys of the actors
	// in it, and the only place an actor's dimension is written.
	actorPrefix = []byte("actorprefix")
	digpPrefix  = []byte("digp")

	nameTag = []byte("CustomName")
)

// Marker is one mark on the map, at a block.
type Marker struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
	// Kind is what a container is (chest, barrel, shulker) or a mob's type.
	Kind string `json:"k,omitempty"`
	// Name is text a player chose. It is cleaned and cut here, and is
	// still theirs: nothing may treat it as markup.
	Name string `json:"n,omitempty"`
	// Colour is a bed's or a shulker box's: one of Colours, or Undyed for
	// a shulker box nobody dyed. It is left out when the world does not
	// say.
	Colour string `json:"c,omitempty"`
	// Trapped is set on a chest that is a trapped chest.
	Trapped bool `json:"t,omitempty"`
	// Baby is set on a named mob the world records as not grown.
	Baby bool `json:"b,omitempty"`
}

// More is how many markers of each kind a dimension has beyond its limit.
type More struct {
	Beds       int `json:"beds"`
	Containers int `json:"containers"`
	Mobs       int `json:"mobs"`
}

// Layer is one dimension's markers, nearest the origin first.
type Layer struct {
	Beds       []Marker
	Containers []Marker
	Mobs       []Marker
	More       More
}

// World is every dimension's markers.
type World map[chunks.Dimension]*Layer

// Stats is what a scan read, for the log.
type Stats struct {
	Beds, Containers, Mobs int
	// Skipped counts records that could not be read or made no sense.
	Skipped int
}

type bed struct {
	x, y, z int32
	color   int32
}

type container struct {
	Marker
	paired       bool
	pairX, pairZ int32
}

type scan struct {
	beds       map[chunks.Dimension][]bed
	containers map[chunks.Dimension][]container
	// named is the named actors not yet placed, by storage key.
	named   map[[8]byte]Marker
	mobs    map[chunks.Dimension][]Marker
	more    map[chunks.Dimension]*More
	skipped int
}

// Scan reads the markers out of a world. It reads every key once, in the
// database's own order, which puts every actor before every chunk's actor
// list: 'a' sorts before 'd'.
func Scan(ctx context.Context, db *leveldb.DB) (World, Stats, error) {
	s := &scan{
		beds:       map[chunks.Dimension][]bed{},
		containers: map[chunks.Dimension][]container{},
		named:      map[[8]byte]Marker{},
		mobs:       map[chunks.Dimension][]Marker{},
		more:       map[chunks.Dimension]*More{},
	}
	for _, d := range chunks.Dimensions {
		s.more[d] = &More{}
	}
	it := db.NewIterator(nil, nil)
	defer it.Release()
	for n := 0; it.Next(); n++ {
		if n%16384 == 0 && ctx.Err() != nil {
			return nil, Stats{}, ctx.Err()
		}
		k := it.Key()
		switch {
		case bytes.HasPrefix(k, actorPrefix):
			s.actor(k, it.Value())
		case bytes.HasPrefix(k, digpPrefix):
			s.place(k, it.Value())
		default:
			// Only block entity records have a value worth fetching.
			if pos, tag, ok := chunks.RecordOf(k); ok && tag == tagBlockEntities && (len(k) == 9 || len(k) == 13) {
				s.blockEntities(pos, it.Value())
			}
		}
	}
	if err := it.Error(); err != nil {
		return nil, Stats{}, fmt.Errorf("read world: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, Stats{}, err
	}

	world, stats := World{}, Stats{Skipped: s.skipped}
	for _, d := range chunks.Dimensions {
		l := &Layer{More: *s.more[d]}
		l.Beds, l.More.Beds = keep(wholeBeds(s.beds[d]), MaxBeds, l.More.Beds)
		l.Containers, l.More.Containers = keep(wholeContainers(s.containers[d]), MaxContainers, l.More.Containers)
		// Only for the containers kept: each is a lookup of its own.
		for i := range l.Containers {
			if i%1024 == 0 && ctx.Err() != nil {
				return nil, Stats{}, ctx.Err()
			}
			describe(db, d, &l.Containers[i])
		}
		l.Mobs, l.More.Mobs = keep(s.mobs[d], MaxMobs, l.More.Mobs)
		world[d] = l
		stats.Beds += len(l.Beds) + l.More.Beds
		stats.Containers += len(l.Containers) + l.More.Containers
		stats.Mobs += len(l.Mobs) + l.More.Mobs
	}
	return world, stats, nil
}

// describe adds what only a container's block says of it: whether a chest
// is trapped, and which colour a shulker box is.
func describe(db *leveldb.DB, dim chunks.Dimension, m *Marker) {
	if m.Kind != "chest" && m.Kind != "shulker" {
		return
	}
	block, ok := blockAt(db, dim, m.X, m.Y, m.Z)
	if !ok {
		return
	}
	if m.Kind == "chest" {
		m.Trapped = block == "trapped_chest"
		return
	}
	m.Colour = shulkerColour(block)
}

// keep sorts markers nearest the origin first, which is where a world's
// players mostly are, and cuts them to limit.
func keep(m []Marker, limit, more int) ([]Marker, int) {
	distance := func(a Marker) int64 { return int64(a.X)*int64(a.X) + int64(a.Z)*int64(a.Z) }
	slices.SortFunc(m, func(a, b Marker) int {
		return cmp.Or(cmp.Compare(distance(a), distance(b)), cmp.Compare(a.X, b.X), cmp.Compare(a.Z, b.Z), cmp.Compare(a.Y, b.Y))
	})
	if len(m) > limit {
		more += len(m) - limit
		m = m[:limit]
	}
	return m, more
}

func (s *scan) blockEntities(pos chunks.Pos, v []byte) {
	if len(v) > maxRecord {
		s.skipped++
		return
	}
	// Most chunks with block entities hold only spawners, sensors and
	// pots; those are not walked at all.
	if !bytes.Contains(v, []byte("Bed")) && !bytes.Contains(v, []byte("Chest")) &&
		!bytes.Contains(v, []byte("Barrel")) && !bytes.Contains(v, []byte("ShulkerBox")) {
		return
	}
	for len(v) > 0 {
		var e struct {
			id, name            string
			x, y, z             int32
			hasX, hasY, hasZ    bool
			color, pairX, pairZ int32
			hasPairX, hasPairZ  bool
			items               int
			unopened            bool
		}
		// A bed whose record holds no colour is not a white one.
		e.color = -1
		rest, err := fields(v, func(name []byte, tag byte, payload []byte) {
			switch string(name) {
			case "id":
				e.id, _ = stringOf(tag, payload)
			case "x":
				e.x, e.hasX = intOf(tag, payload)
			case "y":
				e.y, e.hasY = intOf(tag, payload)
			case "z":
				e.z, e.hasZ = intOf(tag, payload)
			case "color":
				// Only a whole number is a colour. Anything else read as
				// one would come out as nought, which is white.
				if color, ok := intOf(tag, payload); ok {
					e.color = color
				}
			case "pairx":
				e.pairX, e.hasPairX = intOf(tag, payload)
			case "pairz":
				e.pairZ, e.hasPairZ = intOf(tag, payload)
			case "Items":
				e.items = listLen(tag, payload)
			case "LootTable":
				e.unopened = true
			case "CustomName":
				e.name, _ = stringOf(tag, payload)
			}
		})
		if err != nil {
			s.skipped++
			return
		}
		v = rest
		kind := ""
		switch e.id {
		case "Bed":
		case "Chest":
			kind = "chest"
		case "Barrel":
			kind = "barrel"
		case "ShulkerBox":
			kind = "shulker"
		default:
			continue
		}
		// A block entity lies in the chunk whose record holds it. One that
		// says otherwise is not somewhere the map should point.
		if !e.hasX || !e.hasY || !e.hasZ || e.x>>4 != pos.X || e.z>>4 != pos.Z {
			s.skipped++
			continue
		}
		if e.id == "Bed" {
			if len(s.beds[pos.Dim]) >= collectFactor*MaxBeds {
				s.more[pos.Dim].Beds++
				continue
			}
			s.beds[pos.Dim] = append(s.beds[pos.Dim], bed{e.x, e.y, e.z, e.color})
			continue
		}
		// A container still carrying its loot table has never been opened,
		// and an empty one tells a player nothing. Together they are 96% of
		// the containers in the measured world, nearly all of them chests
		// the world generator placed.
		if e.unopened || e.items <= 0 {
			continue
		}
		if len(s.containers[pos.Dim]) >= collectFactor*MaxContainers {
			s.more[pos.Dim].Containers++
			continue
		}
		s.containers[pos.Dim] = append(s.containers[pos.Dim], container{
			Marker: Marker{X: e.x, Y: e.y, Z: e.z, Kind: kind, Name: CleanName(e.name)},
			paired: e.hasPairX && e.hasPairZ, pairX: e.pairX, pairZ: e.pairZ,
		})
	}
}

// wholeBeds turns bed halves into beds. Both blocks of a bed are block
// entities and neither says which it is, so two halves of one colour side
// by side are taken as one bed, marked at the half nearer the origin of
// the axes.
func wholeBeds(halves []bed) []Marker {
	slices.SortFunc(halves, func(a, b bed) int {
		return cmp.Or(cmp.Compare(a.y, b.y), cmp.Compare(a.x, b.x), cmp.Compare(a.z, b.z))
	})
	type at struct{ x, y, z int32 }
	free := make(map[at]int32, len(halves))
	for _, h := range halves {
		free[at{h.x, h.y, h.z}] = h.color
	}
	var out []Marker
	for _, h := range halves {
		here := at{h.x, h.y, h.z}
		if _, ok := free[here]; !ok {
			continue
		}
		delete(free, here)
		for _, next := range []at{{h.x + 1, h.y, h.z}, {h.x, h.y, h.z + 1}} {
			if color, ok := free[next]; ok && color == h.color {
				delete(free, next)
				break
			}
		}
		out = append(out, Marker{X: h.x, Y: h.y, Z: h.z, Colour: colourOf(h.color)})
	}
	return out
}

// wholeContainers marks a large chest once. Each half names the other, and
// either may be the only one with anything in it.
func wholeContainers(found []container) []Marker {
	type at struct{ x, y, z int32 }
	held := make(map[at]bool, len(found))
	for _, c := range found {
		held[at{c.X, c.Y, c.Z}] = true
	}
	out := make([]Marker, 0, len(found))
	for _, c := range found {
		other := at{c.pairX, c.Y, c.pairZ}
		first := c.X < other.x || (c.X == other.x && c.Z < other.z)
		if c.paired && held[other] && !first {
			continue
		}
		out = append(out, c.Marker)
	}
	return out
}

func (s *scan) actor(k, v []byte) {
	// Nearly every actor is unnamed, and those are not walked at all.
	if len(k) != len(actorPrefix)+8 || len(v) > maxRecord || !bytes.Contains(v, nameTag) {
		return
	}
	var (
		name, kind string
		x, y, z    float64
		placed     bool
		baby       bool
	)
	if _, err := fields(v, func(tagName []byte, tag byte, payload []byte) {
		switch string(tagName) {
		case "CustomName":
			name, _ = stringOf(tag, payload)
		case "identifier":
			kind, _ = stringOf(tag, payload)
		case "Pos":
			x, y, z, placed = floatsOf(tag, payload)
		case "IsBaby":
			flag, _ := intOf(tag, payload)
			baby = flag != 0
		}
	}); err != nil {
		s.skipped++
		return
	}
	if name = CleanName(name); name == "" {
		return
	}
	if !placed || !inWorld(x) || !inWorld(y) || !inWorld(z) {
		s.skipped++
		return
	}
	// Which dimension an actor is in is not known until its chunk's list is
	// read, so past this there is no dimension to count it as left out of.
	if len(s.named) >= collectFactor*MaxMobs*len(chunks.Dimensions) {
		s.skipped++
		return
	}
	s.named[[8]byte(k[len(actorPrefix):])] = Marker{
		X: int32(math.Floor(x)), Y: int32(math.Floor(y)), Z: int32(math.Floor(z)),
		Kind: cleanKind(kind), Name: name, Baby: baby,
	}
}

func inWorld(v float64) bool {
	return !math.IsNaN(v) && math.Abs(v) <= maxCoordinate
}

// place gives the named actors a chunk lists their dimension. An actor no
// chunk lists is a leftover the game itself never loads, and is not drawn.
func (s *scan) place(k, v []byte) {
	if len(s.named) == 0 {
		return
	}
	var dim chunks.Dimension
	switch len(k) - len(digpPrefix) {
	case 8:
	case 12:
		dim = chunks.Dimension(int32(binary.LittleEndian.Uint32(k[len(digpPrefix)+8:])))
	default:
		return
	}
	if !slices.Contains(chunks.Dimensions, dim) || len(v)%8 != 0 {
		return
	}
	for ; len(v) >= 8; v = v[8:] {
		id := [8]byte(v[:8])
		m, ok := s.named[id]
		if !ok {
			continue
		}
		delete(s.named, id)
		s.mobs[dim] = append(s.mobs[dim], m)
	}
}

// CleanName makes player-chosen text fit to send: valid, on one line,
// without the game's formatting codes, and no longer than MaxName. It does
// not make it safe to use as markup; nothing does.
func CleanName(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	skip, space, n := false, false, 0
	for _, r := range strings.ToValidUTF8(s, "") {
		switch {
		case skip:
			skip = false
		case r == '§':
			// The section sign and the character after it set a colour
			// or style in game and are not part of the name.
			skip = true
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = b.Len() > 0
		default:
			need := 1
			if space {
				need = 2
			}
			if n+need > MaxName {
				return b.String()
			}
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
			n += need
		}
	}
	return b.String()
}

// cleanKind reduces an actor's identifier to its type, as the live layer
// names it. An identifier that is not one is not passed on.
func cleanKind(id string) string {
	id = strings.TrimPrefix(id, "minecraft:")
	if id == "" || utf8.RuneCountInString(id) > MaxName {
		return "unknown"
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != ':' && r != '.' && r != '-' {
			return "unknown"
		}
	}
	return id
}
