package replay

import (
	"context"
	"math"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

const (
	// DefaultEvery is the coarse sampling interval; the console interpolates
	// between samples.
	DefaultEvery = 2 * time.Second
	// MinEvery guards the retention arithmetic, which assumes at most a few
	// samples a second across the fleet.
	MinEvery = time.Second

	// A unit that has not moved is stored once in a while, not every tick.
	// The console treats a longer gap than the keepalive allows as "no data"
	// instead of interpolating across it, so the two must stay in step.
	Keepalive = 30 * time.Second
	// moved is the smallest step in blocks that counts as movement.
	moved = 0.25
)

// Source is the part of the poller the recorder reads.
type Source interface {
	Positions() []telemetry.Position
	Snapshot() []telemetry.Status
}

type last struct {
	x, y, z float64
	at      time.Time
}

// Recorder turns the poller's positions into stored samples. A standing unit
// would otherwise write the same row every tick for the whole retention
// window, and idle is the usual state.
type Recorder struct {
	store Store
	seen  map[string]last
}

func NewRecorder(store Store) *Recorder { return &Recorder{store: store, seen: map[string]last{}} }

// Sample returns what is worth storing at now. Offline, dead and unlocated
// units are forgotten, so the first sample after they return is always kept.
func (r *Recorder) Sample(src Source, now time.Time) []Sample {
	status := map[string]telemetry.Status{}
	for _, st := range src.Snapshot() {
		status[st.ID] = st
	}
	var out []Sample
	for _, p := range src.Positions() {
		st := status[p.ID]
		if !p.Known || p.Dim != 0 || !st.Online || st.Dead {
			delete(r.seen, p.ID)
			continue
		}
		s := Sample{Unit: p.ID, At: now, X: float64(p.X), Y: float64(p.Y), Z: float64(p.Z)}
		if prev, ok := r.seen[p.ID]; ok && now.Sub(prev.at) < Keepalive &&
			math.Abs(s.X-prev.x) < moved && math.Abs(s.Y-prev.y) < moved && math.Abs(s.Z-prev.z) < moved {
			continue
		}
		r.seen[p.ID] = last{s.X, s.Y, s.Z, now}
		out = append(out, s)
	}
	return out
}

// Run samples until ctx ends. A too-short every is clamped.
func (r *Recorder) Run(ctx context.Context, src Source, every time.Duration) {
	if every < MinEvery {
		every = DefaultEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if batch := r.Sample(src, now); len(batch) > 0 {
				actx, cancel := context.WithTimeout(ctx, queryTimeout)
				_ = r.store.Append(actx, batch)
				cancel()
			}
		}
	}
}
