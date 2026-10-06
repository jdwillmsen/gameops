package biomes

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Bounds on what one world may make this hold. The FWB world is 144,000
// chunks with biomes, 34,000 of them holding more than one, 62 kinds and
// 171,000 chunk-and-kind pairs; these are several times that, and are what
// a damaged or hostile world is held to.
const (
	// MaxChunks is the most chunks kept, over all dimensions. A chunk is
	// twelve bytes and a map slot.
	MaxChunks = 1_000_000
	// maxMixed is the most chunks kept column by column, at 256 bytes
	// each: 64 MB. Past it a chunk is kept as its commonest biome.
	maxMixed = 250_000
	// maxEntries is the most chunk-and-kind pairs indexed for search, at
	// sixteen bytes each. A chunk past it is drawn and not found.
	maxEntries = 2_000_000
	// maxKinds is how many different biomes one dimension may hold and
	// still tell apart; a column is one byte. The game has 89.
	maxKinds = 254
	// overflowKind is the byte every biome past maxKinds shares.
	overflowKind = maxKinds
	// maxChunkCoordinate is past the edge of any Bedrock world, and keeps
	// a chunk's block coordinates inside 32 bits.
	maxChunkCoordinate = 2_000_000

	mixedFlag = 1 << 31

	// joinReach is how many chunks apart two chunks holding a biome may
	// be and still be one stretch of it: neighbours, and chunks with one
	// between them. Joined only edge to edge, a single island comes out
	// as a dozen stretches, most of them a few blocks of shoreline that
	// the chunk grid happened to cut off.
	joinReach = 2
)

// Layer is one dimension's biomes.
type Layer struct {
	// ids is the biome each kind byte stands for.
	ids []uint32
	// cells is every chunk, by position. A chunk of one biome is that
	// kind; any other is mixedFlag and the index of its 256 columns in
	// mixed, at z*16+x.
	cells map[uint64]uint32
	mixed []byte

	// entries is every kind in every chunk, sorted by kind, then z, then
	// x, which is what lets one kind be walked, a chunk be found, and a
	// row of chunks be read off as a run.
	entries []entry
	// first is where each kind's entries begin; one more than there are
	// kinds, so that the last has an end.
	first   []int32
	regions []region
	// firstRegion is where each kind's regions begin, as first is.
	firstRegion []int32
}

type entry struct {
	cx, cz int32
	region int32
	// count is how many of the chunk's columns are this kind.
	count uint16
	kind  uint8
}

type region struct {
	area                   uint64
	chunks                 int32
	minX, minZ, maxX, maxZ int32
}

// Stats is what one reading of the world came to.
type Stats struct {
	// Chunks is how many chunks' biomes are held.
	Chunks int
	// Kinds is how many different biomes they hold.
	Kinds int
	// Unknown is how many of those this version's list does not name.
	Unknown int
	// Malformed counts records that could not be read, OutOfRange chunks
	// past the edge of any world, and OverLimit chunks past MaxChunks; none
	// of those are held. Coarsened chunks are held as one biome each,
	// KindsLeftOut biomes share one colour, and Unindexed chunk-and-kind
	// pairs are drawn but not found by a search.
	Malformed, OutOfRange, OverLimit, Coarsened, KindsLeftOut, Unindexed int
	// Unchanged means the world was not read: nothing had been generated
	// since the last reading, which still stands.
	Unchanged bool
}

// World is the biomes of every generated chunk as of one snapshot. It is
// never changed once built, so any number of requests may read it.
type World struct {
	// SnapshotAt is when the snapshot it was read from was taken.
	SnapshotAt time.Time
	// CensusChunks is how many chunks the world held then, by the chunk
	// count's reckoning, or -1 if nothing had counted them.
	CensusChunks int
	Stats        Stats

	layers [3]*Layer
}

func cellKey(cx, cz int32) uint64 { return uint64(uint32(cx))<<32 | uint64(uint32(cz)) }

func (w *World) layer(d chunks.Dimension) *Layer {
	if w == nil || d < 0 || int(d) >= len(w.layers) {
		return nil
	}
	return w.layers[d]
}

// kindAt is the kind byte of the column at block x, z.
func (l *Layer) kindAt(x, z int32) (uint8, bool) {
	ref, ok := l.cells[cellKey(x>>4, z>>4)]
	if !ok {
		return 0, false
	}
	if ref&mixedFlag == 0 {
		return uint8(ref), true
	}
	return l.mixed[int(ref&^mixedFlag)*Columns+int(z&15)*16+int(x&15)], true
}

// At is the biome of the highest block in the column at block x, z. It is
// false where the world has generated no chunk, or stored no biomes for
// the one it has.
func (w *World) At(d chunks.Dimension, x, z int32) (Biome, bool) {
	l := w.layer(d)
	if l == nil {
		return Biome{}, false
	}
	k, ok := l.kindAt(x, z)
	if !ok {
		return Biome{}, false
	}
	return Lookup(l.ids[k]), true
}

// Presence is one biome's share of a dimension.
type Presence struct {
	Biome
	// Area is how many columns have it at the surface: square blocks.
	Area uint64 `json:"area"`
	// Chunks is how many chunks hold any of it, and Regions how many
	// separate stretches of it there are.
	Chunks  int `json:"chunks"`
	Regions int `json:"regions"`
}

// Present lists the biomes a dimension holds, largest first.
func (w *World) Present(d chunks.Dimension) []Presence {
	l := w.layer(d)
	if l == nil {
		return nil
	}
	out := make([]Presence, 0, len(l.ids))
	for k, id := range l.ids {
		p := Presence{Biome: Lookup(id), Chunks: int(l.first[k+1] - l.first[k]), Regions: int(l.firstRegion[k+1] - l.firstRegion[k])}
		for _, r := range l.regions[l.firstRegion[k]:l.firstRegion[k+1]] {
			p.Area += r.area
		}
		if p.Chunks > 0 {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Presence) int { return cmp.Or(cmp.Compare(b.Area, a.Area), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Extent is one stretch of a biome: every chunk holding the biome that is
// next to another, or has one chunk between. It is put together chunk by
// chunk, so patches up to 47 blocks apart count as one stretch.
type Extent struct {
	// Area is the stretch's columns: square blocks.
	Area   uint64 `json:"area"`
	Chunks int    `json:"chunks"`
	// The box around its chunks, in blocks, both corners inclusive.
	MinX int32 `json:"minX"`
	MinZ int32 `json:"minZ"`
	MaxX int32 `json:"maxX"`
	MaxZ int32 `json:"maxZ"`
}

func (r region) extent() Extent {
	return Extent{Area: r.area, Chunks: int(r.chunks), MinX: r.minX * 16, MinZ: r.minZ * 16, MaxX: r.maxX*16 + 15, MaxZ: r.maxZ*16 + 15}
}

// Hit is the nearest column of one stretch of a biome.
type Hit struct {
	X int32 `json:"x"`
	Z int32 `json:"z"`
	// Distance is from the point asked about, in blocks.
	Distance float64 `json:"distance"`
	Region   Extent  `json:"region"`
}

func (l *Layer) kindOf(id uint32) (int, bool) {
	k := slices.Index(l.ids, id)
	return k, k >= 0
}

// Nearest finds the stretches of a biome nearest block x, z, nearest
// first, one hit each and at most limit of them, and says how many more
// stretches there are.
func (w *World) Nearest(d chunks.Dimension, id uint32, x, z int32, limit int) (hits []Hit, more int) {
	l := w.layer(d)
	if l == nil || limit <= 0 {
		return nil, 0
	}
	k, ok := l.kindOf(id)
	if !ok {
		return nil, 0
	}
	type candidate struct {
		entry    int32
		distance int64
	}
	base := l.firstRegion[k]
	best := make([]candidate, l.firstRegion[k+1]-base)
	for i := range best {
		best[i].entry = -1
	}
	for i := l.first[k]; i < l.first[k+1]; i++ {
		e := l.entries[i]
		dx := gapTo(int64(x), int64(e.cx)*16)
		dz := gapTo(int64(z), int64(e.cz)*16)
		if c := &best[e.region-base]; c.entry < 0 || dx*dx+dz*dz < c.distance {
			*c = candidate{i, dx*dx + dz*dz}
		}
	}
	slices.SortFunc(best, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(a.distance, b.distance), cmp.Compare(a.entry, b.entry))
	})
	if len(best) > limit {
		more = len(best) - limit
		best = best[:limit]
	}
	for _, c := range best {
		e := l.entries[c.entry]
		hx, hz := l.nearestColumn(e, uint8(k), x, z)
		hits = append(hits, Hit{
			X: hx, Z: hz,
			Distance: math.Round(math.Hypot(float64(hx)-float64(x), float64(hz)-float64(z))),
			Region:   l.regions[e.region].extent(),
		})
	}
	// The nearest chunk of a stretch is found by its edge and the column
	// inside it by looking, which can reorder two stretches about as far.
	slices.SortStableFunc(hits, func(a, b Hit) int { return cmp.Compare(a.Distance, b.Distance) })
	return hits, more
}

// gapTo is how far v is outside the sixteen blocks starting at low.
func gapTo(v, low int64) int64 {
	return max(low-v, v-(low+15), 0)
}

// nearestColumn is the column of this kind in the entry's chunk nearest
// block x, z.
func (l *Layer) nearestColumn(e entry, kind uint8, x, z int32) (int32, int32) {
	lowX, lowZ := e.cx*16, e.cz*16
	ref := l.cells[cellKey(e.cx, e.cz)]
	if ref&mixedFlag == 0 {
		return min(max(x, lowX), lowX+15), min(max(z, lowZ), lowZ+15)
	}
	columns := l.mixed[int(ref&^mixedFlag)*Columns:][:Columns]
	bestX, bestZ, best := lowX, lowZ, int64(math.MaxInt64)
	for i, k := range columns {
		if k != kind {
			continue
		}
		cx, cz := lowX+int32(i&15), lowZ+int32(i>>4)
		dx, dz := int64(cx)-int64(x), int64(cz)-int64(z)
		if d := dx*dx + dz*dz; d < best {
			bestX, bestZ, best = cx, cz, d
		}
	}
	return bestX, bestZ
}

// Rect is a box of blocks, both corners inclusive: minimum x and z, then
// maximum.
type Rect [4]int32

// Region is the stretch of biome that the column at block x, z belongs to:
// its biome, its extent, and the chunks it covers as at most maxRects
// rows, with how many rows were left out.
func (w *World) Region(d chunks.Dimension, x, z int32, maxRects int) (b Biome, e Extent, rects []Rect, more int, ok bool) {
	l := w.layer(d)
	if l == nil {
		return Biome{}, Extent{}, nil, 0, false
	}
	kind, found := l.kindAt(x, z)
	if !found {
		return Biome{}, Extent{}, nil, 0, false
	}
	k := int(kind)
	at, found := l.find(k, x>>4, z>>4)
	if !found {
		// Drawn, and past what the search index holds.
		return Biome{}, Extent{}, nil, 0, false
	}
	id := l.entries[at].region
	var run *Rect
	for _, en := range l.entries[l.first[k]:l.first[k+1]] {
		if en.region != id {
			continue
		}
		if run != nil && run[1] == en.cz*16 && run[2]+1 == en.cx*16 {
			run[2] += 16
			continue
		}
		if len(rects) >= maxRects {
			more++
			// A run that is not kept still has to be followed to its end.
			run = &Rect{en.cx * 16, en.cz * 16, en.cx*16 + 15, en.cz*16 + 15}
			continue
		}
		rects = append(rects, Rect{en.cx * 16, en.cz * 16, en.cx*16 + 15, en.cz*16 + 15})
		run = &rects[len(rects)-1]
	}
	return Lookup(l.ids[k]), l.regions[id].extent(), rects, more, true
}

// find is the index of a kind's entry for one chunk.
func (l *Layer) find(kind int, cx, cz int32) (int32, bool) {
	lo, hi := int(l.first[kind]), int(l.first[kind+1])
	i := lo + sort.Search(hi-lo, func(i int) bool {
		e := l.entries[lo+i]
		return e.cz > cz || (e.cz == cz && e.cx >= cx)
	})
	return int32(i), i < hi && l.entries[i].cx == cx && l.entries[i].cz == cz
}

// builder puts a layer together a chunk at a time.
type builder struct {
	layer *Layer
	kinds map[uint32]uint8
	// leftOut is the biomes past maxKinds, as far as they are counted.
	leftOut map[uint32]struct{}
	stats   *Stats
	// room is how many more chunks, over every dimension, may be held.
	room *int
	// mixedLimit is maxMixed, but for tests.
	mixedLimit int
}

func newBuilder(stats *Stats, room *int) *builder {
	return &builder{
		layer: &Layer{cells: map[uint64]uint32{}},
		kinds: map[uint32]uint8{}, leftOut: map[uint32]struct{}{},
		stats: stats, room: room, mixedLimit: maxMixed,
	}
}

func (b *builder) kind(id uint32) uint8 {
	if k, ok := b.kinds[id]; ok {
		return k
	}
	if len(b.kinds) >= maxKinds {
		if _, seen := b.leftOut[id]; !seen && len(b.leftOut) < 4096 {
			b.leftOut[id] = struct{}{}
			b.stats.KindsLeftOut++
		}
		if len(b.layer.ids) == overflowKind {
			b.layer.ids = append(b.layer.ids, overflowID)
		}
		return overflowKind
	}
	k := uint8(len(b.layer.ids))
	b.layer.ids = append(b.layer.ids, id)
	b.kinds[id] = k
	return k
}

// add takes one chunk's columns, at index z*16+x.
func (b *builder) add(cx, cz int32, columns *[Columns]uint32) {
	if cx < -maxChunkCoordinate || cx > maxChunkCoordinate || cz < -maxChunkCoordinate || cz > maxChunkCoordinate {
		b.stats.OutOfRange++
		return
	}
	key := cellKey(cx, cz)
	if _, held := b.layer.cells[key]; held {
		return
	}
	if *b.room <= 0 {
		b.stats.OverLimit++
		return
	}
	*b.room--
	b.stats.Chunks++

	var kinds [Columns]uint8
	var counts [maxKinds + 1]uint16
	same := true
	last, lastKind := columns[0], b.kind(columns[0])
	for i, id := range columns {
		// Runs of one biome are the rule, and a map lookup a column is not.
		if id != last {
			last, lastKind = id, b.kind(id)
			same = false
		}
		kinds[i] = lastKind
		counts[lastKind]++
	}
	if !same && len(b.layer.mixed) >= b.mixedLimit*Columns {
		b.stats.Coarsened++
		commonest := 0
		for k, n := range counts {
			if n > counts[commonest] {
				commonest = k
			}
		}
		kinds[0], same = uint8(commonest), true
	}
	if same {
		b.layer.cells[key] = uint32(kinds[0])
		return
	}
	b.layer.cells[key] = mixedFlag | uint32(len(b.layer.mixed)/Columns)
	b.layer.mixed = append(b.layer.mixed, kinds[:]...)
}

// index builds what search needs from the chunks: every kind in every
// chunk, and which of those join up into one stretch.
func (l *Layer) index(stats *Stats, limit int) {
	l.entries = l.entries[:0]
	for key, ref := range l.cells {
		cx, cz := int32(key>>32), int32(key)
		if ref&mixedFlag == 0 {
			l.entries = append(l.entries, entry{cx: cx, cz: cz, count: Columns, kind: uint8(ref)})
			continue
		}
		var counts [maxKinds + 1]uint16
		for _, k := range l.mixed[int(ref&^mixedFlag)*Columns:][:Columns] {
			counts[k]++
		}
		for k, n := range counts {
			if n > 0 {
				l.entries = append(l.entries, entry{cx: cx, cz: cz, count: n, kind: uint8(k)})
			}
		}
	}
	slices.SortFunc(l.entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.kind, b.kind), cmp.Compare(a.cz, b.cz), cmp.Compare(a.cx, b.cx))
	})
	if len(l.entries) > limit {
		stats.Unindexed += len(l.entries) - limit
		// The order is by kind, so cutting the end would lose whole
		// biomes; every pair past the bound is dropped evenly instead.
		keep := l.entries[:0]
		for i, e := range l.entries {
			if i*limit/len(l.entries) != (i+1)*limit/len(l.entries) {
				keep = append(keep, e)
			}
		}
		l.entries = keep
	}

	l.first = make([]int32, len(l.ids)+1)
	for _, e := range l.entries {
		l.first[int(e.kind)+1]++
	}
	for k := range l.ids {
		l.first[k+1] += l.first[k]
	}

	parent := make([]int32, len(l.entries))
	for i := range parent {
		parent[i] = int32(i)
	}
	var root func(int32) int32
	root = func(i int32) int32 {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, e := range l.entries {
		// Only the half of the neighbourhood that sorts after an entry is
		// looked at; the other half is done from the neighbour's side.
		for dz := int32(0); dz <= joinReach; dz++ {
			for dx := int32(-joinReach); dx <= joinReach; dx++ {
				if dz == 0 && dx <= 0 {
					continue
				}
				if near, ok := l.find(int(e.kind), e.cx+dx, e.cz+dz); ok {
					parent[root(near)] = root(int32(i))
				}
			}
		}
	}

	// Numbered in entry order, which keeps each kind's regions together.
	l.regions = l.regions[:0]
	l.firstRegion = make([]int32, len(l.ids)+1)
	number := make(map[int32]int32)
	for i := range l.entries {
		e := &l.entries[i]
		r := root(int32(i))
		id, seen := number[r]
		if !seen {
			id = int32(len(l.regions))
			number[r] = id
			l.regions = append(l.regions, region{minX: e.cx, minZ: e.cz, maxX: e.cx, maxZ: e.cz})
			l.firstRegion[int(e.kind)+1]++
		}
		e.region = id
		reg := &l.regions[id]
		reg.area += uint64(e.count)
		reg.chunks++
		reg.minX, reg.minZ = min(reg.minX, e.cx), min(reg.minZ, e.cz)
		reg.maxX, reg.maxZ = max(reg.maxX, e.cx), max(reg.maxZ, e.cz)
	}
	for k := range l.ids {
		l.firstRegion[k+1] += l.firstRegion[k]
	}
}
