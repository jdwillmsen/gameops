package heat

import (
	"context"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

const (
	DefaultEvery = 2 * time.Second
	MinEvery     = time.Second
)

// Source is the part of the poller the sampler reads.
type Source interface {
	Positions() []telemetry.Position
	Snapshot() []telemetry.Status
}

// Sample records one reading of every unit. Only the overworld is mapped:
// coordinates in another dimension would land on unrelated lab cells.
func (s *Store) Sample(src Source, now time.Time) {
	status := map[string]telemetry.Status{}
	for _, st := range src.Snapshot() {
		status[st.ID] = st
	}
	for _, p := range src.Positions() {
		st := status[p.ID]
		if !p.Known || p.Dim != 0 || !st.Online || st.Dead {
			s.Forget(p.ID)
			continue
		}
		mining := st.Task != nil && strings.HasPrefix(st.Task.Template, "gather")
		s.Add(p.ID, now, float64(p.X), float64(p.Z), mining)
	}
}

// Run samples until ctx ends. A non-positive or too-short every is clamped,
// since the cell caps assume a sample rate of about a second at most.
func (s *Store) Run(ctx context.Context, src Source, every time.Duration) {
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
			s.Sample(src, now)
		}
	}
}
