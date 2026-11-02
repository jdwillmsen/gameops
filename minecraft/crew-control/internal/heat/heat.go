// Package heat aggregates unit positions into density grids over the lab
// map. Samples are folded into per-minute counters the moment they arrive, so
// memory is bounded by retention and the cell caps, never by how long the
// service has run or how often units are sampled.
package heat

import (
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

const (
	// CellSize is the grid edge in blocks, matching a quarter chunk.
	CellSize = 4

	sliceLen  = time.Minute
	Retention = 6 * time.Hour

	// A slice holds at most this many cells per unit and layer; a unit that
	// somehow touches more in a minute loses the overflow, counted in Dropped.
	maxCellsPerSlice = 256

	// Travel is drawn along the segment between two samples, but only for
	// plausible walking: a longer jump is a teleport or a respawn, and a
	// gap in samples means the path between them is unknown.
	maxTravelGap  = 10 * time.Second
	maxTravelSpan = 64.0
	travelStep    = 2.0
)

type Layer string

const (
	Position Layer = "position"
	Mining   Layer = "mining"
	Travel   Layer = "travel"
	// Yield weights a cell by the items a unit gained there, where Mining
	// counts the time spent gathering.
	Yield Layer = "yield"
)

var layers = []Layer{Position, Mining, Travel, Yield}

const numLayers = 4

func ValidLayer(l Layer) bool { return slices.Contains(layers, l) }

// Windows are the only spans a client may ask for, which also keeps the
// work per request bounded.
var Windows = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": Retention}

type cell struct{ X, Z int32 }

type bucket map[cell]uint32

type slice struct {
	at      int64 // unix minute
	counts  map[string]*[numLayers]bucket
	samples map[string]uint32
	// Overflow is kept per unit and layer so a query reports only what
	// fell out of the grid it is answering.
	dropped map[string]*[numLayers]uint32
}

type last struct {
	at      time.Time
	x, z    float64
	present bool
}

// Store is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	slices map[int64]*slice
	last   map[string]last
	// OnDrop, when set before Run, hears of each sample skipped because the
	// unit stood outside the overworld. It runs on the sampling loop and must
	// not block.
	OnDrop func(telemetry.Position)
}

func New() *Store {
	return &Store{slices: map[int64]*slice{}, last: map[string]last{}}
}

func layerIdx(l Layer) int {
	for i, x := range layers {
		if x == l {
			return i
		}
	}
	return 0
}

func cellOf(x, z float64) cell {
	return cell{int32(math.Floor(x / CellSize)), int32(math.Floor(z / CellSize))}
}

// Add folds one sample in. mining marks a sample taken while the unit was on
// a gathering task: units do not report individual block breaks, so time
// spent gathering is the closest honest measure of where mining happens.
func (s *Store) Add(unit string, at time.Time, x, z float64, mining bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, b := s.bucketsLocked(unit, at)
	sl.samples[unit]++
	s.bump(sl, unit, b, Position, cellOf(x, z), 1)
	if mining {
		s.bump(sl, unit, b, Mining, cellOf(x, z), 1)
	}
	if p := s.last[unit]; p.present && at.After(p.at) && at.Sub(p.at) <= maxTravelGap {
		dx, dz := x-p.x, z-p.z
		if d := math.Hypot(dx, dz); d >= 1 && d <= maxTravelSpan {
			n := int(math.Ceil(d / travelStep))
			var prev cell
			for i := 1; i <= n; i++ {
				f := float64(i) / float64(n)
				c := cellOf(p.x+dx*f, p.z+dz*f)
				// Consecutive steps inside one cell count once, so a path's
				// weight is the cells it crosses and not its sampling rate.
				if i > 1 && c == prev {
					continue
				}
				prev = c
				s.bump(sl, unit, b, Travel, c, 1)
			}
		}
	}
	s.last[unit] = last{at: at, x: x, z: z, present: true}
}

// AddYield credits items gained by a unit to the cell it stood in. It is not
// a position sample, so it leaves the sample count and the travel line alone.
func (s *Store) AddYield(unit string, at time.Time, x, z float64, items int) {
	if items <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sl, b := s.bucketsLocked(unit, at)
	s.bump(sl, unit, b, Yield, cellOf(x, z), uint32(items))
}

// bucketsLocked finds the minute slice for at, and the unit's layers in it,
// creating both as needed.
func (s *Store) bucketsLocked(unit string, at time.Time) (*slice, *[numLayers]bucket) {
	minute := at.Unix() / 60
	sl := s.slices[minute]
	if sl == nil {
		sl = &slice{at: minute, counts: map[string]*[numLayers]bucket{}, samples: map[string]uint32{}, dropped: map[string]*[numLayers]uint32{}}
		s.slices[minute] = sl
		s.pruneLocked(at)
	}
	b := sl.counts[unit]
	if b == nil {
		b = &[numLayers]bucket{}
		sl.counts[unit] = b
	}
	return sl, b
}

// Forget drops a unit's last position, so a respawn or an offline gap does
// not draw a line across the map.
func (s *Store) Forget(unit string) {
	s.mu.Lock()
	delete(s.last, unit)
	s.mu.Unlock()
}

func (s *Store) bump(sl *slice, unit string, b *[numLayers]bucket, l Layer, c cell, by uint32) {
	i := layerIdx(l)
	if b[i] == nil {
		b[i] = bucket{}
	}
	if _, ok := b[i][c]; !ok && len(b[i]) >= maxCellsPerSlice {
		d := sl.dropped[unit]
		if d == nil {
			d = &[numLayers]uint32{}
			sl.dropped[unit] = d
		}
		d[i]++
		return
	}
	b[i][c] += by
}

func (s *Store) pruneLocked(now time.Time) {
	cut := now.Add(-Retention).Unix() / 60
	for k := range s.slices {
		if k <= cut {
			delete(s.slices, k)
		}
	}
}

// Grid is a density answer. Cells are [cellX, cellZ, count], sorted by x
// then z, so equal inputs give equal bytes.
type Grid struct {
	Cell   int       `json:"cell"`
	Layer  Layer     `json:"layer"`
	Window string    `json:"window"`
	Unit   string    `json:"unit"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	// Samples is the position samples behind the grid, or for the yield layer
	// the items gained.
	Samples uint64     `json:"samples"`
	Max     uint32     `json:"max"`
	Dropped uint64     `json:"dropped"`
	Cells   [][3]int32 `json:"cells"`
}

// Query sums the slices inside the window for one unit, or for the whole
// fleet when unit is empty.
func (s *Store) Query(unit string, layer Layer, window string, now time.Time) Grid {
	d := Windows[window]
	g := Grid{Cell: CellSize, Layer: layer, Window: window, Unit: unit, Since: now.Add(-d), Until: now, Cells: [][3]int32{}}
	from := g.Since.Unix() / 60
	to := now.Unix() / 60
	idx := layerIdx(layer)
	sum := map[cell]uint32{}

	s.mu.Lock()
	for k, sl := range s.slices {
		if k < from || k > to {
			continue
		}
		for u, b := range sl.counts {
			if unit != "" && u != unit {
				continue
			}
			// A yield answer counts items, below, not position samples.
			if layer != Yield {
				g.Samples += uint64(sl.samples[u])
			}
			if d := sl.dropped[u]; d != nil {
				g.Dropped += uint64(d[idx])
			}
			for c, n := range b[idx] {
				sum[c] += n
			}
		}
	}
	s.mu.Unlock()

	for c, n := range sum {
		if layer == Yield {
			g.Samples += uint64(n)
		}
		g.Cells = append(g.Cells, [3]int32{c.X, c.Z, int32(n)})
		if n > g.Max {
			g.Max = n
		}
	}
	sort.Slice(g.Cells, func(i, j int) bool {
		if g.Cells[i][0] != g.Cells[j][0] {
			return g.Cells[i][0] < g.Cells[j][0]
		}
		return g.Cells[i][1] < g.Cells[j][1]
	})
	return g
}
