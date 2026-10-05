package live

import (
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

type listKey struct {
	dimension string
	kind      Kind
}

// building is a list whose parts are still arriving.
type building struct {
	gen   int64
	parts [][]Entity
	have  []bool
	got   int
	more  int
}

// list is one kind of entity in one dimension, whole, as of one sample.
type list struct {
	gen   int64
	at    time.Time
	items []Entity
	more  int
}

// Frame is everything to draw in one dimension.
type Frame struct {
	// At is when the bridge received the newest sample in the frame.
	At      time.Time
	Players []Entity
	Mobs    []Entity
	// More and MorePlayers are how many were left out, by the pack's cap
	// and this service's together.
	More, MorePlayers int
	// Stale means there is nothing recent enough to draw.
	Stale bool
}

// Store keeps the latest whole list of players and of mobs for each
// dimension. A list split across several records replaces the one before it
// only once every part is in, so the page never draws half of a sample.
type Store struct {
	// TTL is how old a list may be and still be drawn. Past it the entities
	// are gone from the frame, which is how positions disappear when the
	// pack stops reporting.
	TTL time.Duration
	// Max caps each list. What it cuts is added to the frame's count of
	// entities left out.
	Max int

	mu       sync.Mutex
	started  bool
	newest   int64
	building map[listKey]*building
	lists    map[listKey]list
	dirty    map[string]bool
	// shown is how many of a dimension's lists were fresh in the frame last
	// handed out for it, so that one going stale is itself a change.
	shown map[string]int
}

// Apply takes one record, received by the bridge at at, and reports what
// became of it.
func (s *Store) Apply(rec Record, at time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.building == nil {
		s.building, s.lists = map[listKey]*building{}, map[listKey]list{}
		s.dirty, s.shown = map[string]bool{}, map[string]int{}
	}

	// Generations only count up, so a lower one is a new server process
	// counting from the start. Lists half built from the old one will never
	// be finished, and no generation of the new one is a repeat.
	if s.started && rec.Gen < s.newest {
		for k := range s.building {
			metricFrames.WithLabelValues(resultIncomplete).Inc()
			delete(s.building, k)
		}
		for k, l := range s.lists {
			l.gen = -1
			s.lists[k] = l
		}
	}
	s.started, s.newest = true, rec.Gen
	if rec.Kind == Tick {
		return resultHeartbeat
	}

	k := listKey{rec.Dimension, rec.Kind}
	if held, ok := s.lists[k]; ok && held.gen == rec.Gen {
		return resultDropped
	}
	b := s.building[k]
	if b != nil && b.gen != rec.Gen {
		// The list it belonged to is abandoned, and what was drawn before it
		// stays drawn until something whole replaces it or it ages out.
		metricFrames.WithLabelValues(resultIncomplete).Inc()
		b = nil
	}
	if b == nil {
		b = &building{gen: rec.Gen, parts: make([][]Entity, rec.Parts), have: make([]bool, rec.Parts)}
		s.building[k] = b
	}
	if rec.Parts != len(b.parts) || b.have[rec.Part] {
		return resultDropped
	}
	b.parts[rec.Part], b.have[rec.Part] = rec.Items, true
	b.got++
	b.more = max(b.more, rec.More)
	if b.got < len(b.parts) {
		return resultBuffered
	}

	delete(s.building, k)
	var items []Entity
	for _, part := range b.parts {
		items = append(items, part...)
	}
	more := b.more
	if s.Max > 0 && len(items) > s.Max {
		more += len(items) - s.Max
		items = items[:s.Max:s.Max]
	}
	s.lists[k] = list{gen: rec.Gen, at: at, items: items, more: more}
	s.dirty[rec.Dimension] = true
	metricEntities.WithLabelValues(rec.Dimension, string(rec.Kind)).Set(float64(len(items)))
	return resultApplied
}

func (s *Store) fresh(k listKey, now time.Time) (list, bool) {
	l, ok := s.lists[k]
	return l, ok && now.Sub(l.at) < s.TTL
}

// Snapshot is what there is to draw in a dimension at now. The slices are
// shared with the store and must not be changed.
func (s *Store) Snapshot(dimension string, now time.Time) Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, _ := s.snapshot(dimension, now)
	return f
}

func (s *Store) snapshot(dimension string, now time.Time) (f Frame, fresh int) {
	if players, ok := s.fresh(listKey{dimension, Players}, now); ok {
		f.Players, f.MorePlayers, f.At = players.items, players.more, players.at
		fresh++
	}
	if mobs, ok := s.fresh(listKey{dimension, Mobs}, now); ok {
		f.Mobs, f.More = mobs.items, mobs.more
		if mobs.at.After(f.At) {
			f.At = mobs.at
		}
		fresh++
	}
	f.Stale = fresh == 0
	return f, fresh
}

// Changed returns a frame for each dimension that is not what it was when
// this was last called: a list was replaced, or one aged out. Calling it is
// what marks them seen.
func (s *Store) Changed(now time.Time) map[string]Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out map[string]Frame
	for _, dimension := range render.Dimensions {
		f, fresh := s.snapshot(dimension, now)
		if !s.dirty[dimension] && fresh == s.shown[dimension] {
			continue
		}
		delete(s.dirty, dimension)
		s.shown[dimension] = fresh
		if out == nil {
			out = map[string]Frame{}
		}
		out[dimension] = f
	}
	return out
}
