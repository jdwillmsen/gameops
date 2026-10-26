package explorer

import (
	"context"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// scanCap bounds how much history a store without a snapshot is paged for
// one request: the explorer is an in-memory scan, and a month of rows would
// not fit a request's budget. The answer says when it was cut.
const scanCap = 20000

// Window narrows what a Source reads before the filter runs. A source that
// can push it down (a database) should; the in-memory one only trims.
type Window struct {
	Since, Until time.Time
	// AfterID keeps only events stored after this one, which is how the
	// live tail asks for what is new.
	AfterID int64
}

// Loaded is what a source read. Truncated means older events were left out.
type Loaded struct {
	Events    []events.Event
	Truncated bool
}

// Source is the explorer's storage interface. The in-memory implementation
// scans; a Postgres one would translate Window and the filter into SQL.
type Source interface {
	Load(ctx context.Context, w Window) (Loaded, error)
}

// StoreSource reads an events.Store: in one copy when it can snapshot, and
// by paging newest-first otherwise.
type StoreSource struct{ Store events.Store }

func (s StoreSource) Load(ctx context.Context, w Window) (Loaded, error) {
	if sn, ok := s.Store.(events.Snapshotter); ok {
		if all, ok := sn.Snapshot(); ok {
			out := all[:0]
			for _, e := range all {
				if e.ID > w.AfterID {
					out = append(out, e)
				}
			}
			return Loaded{Events: out}, nil
		}
	}
	q := events.Query{Since: w.Since, Until: w.Until, Limit: 200}
	var out []events.Event
	for {
		page, err := s.Store.Query(ctx, q)
		if err != nil {
			return Loaded{}, err
		}
		for _, e := range page.Events {
			if e.ID > w.AfterID {
				out = append(out, e)
			}
		}
		if page.Next == nil {
			return Loaded{Events: out}, nil
		}
		if len(out) >= scanCap {
			return Loaded{Events: out, Truncated: true}, nil
		}
		q.Cursor = *page.Next
	}
}
