package invhistory

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

const (
	pageSize = 200
	// maxPages bounds one read to 5000 recorded changes: a window that long
	// is answered as truncated rather than scanned to the end.
	maxPages = 25
)

// Entry is one recorded change, as of when it was seen.
type Entry struct {
	At      time.Time `json:"at"`
	Changes []Change  `json:"changes"`
}

// Net is what an item's total moved over the whole window.
type Net struct {
	Item  string `json:"item"`
	Delta int    `json:"delta"`
}

// Report answers one unit's inventory history over a window.
type Report struct {
	Unit  string    `json:"unit"`
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	// Entries run newest first. Net sums them per item, omitting items that
	// came back to where they started.
	Entries []Entry `json:"entries"`
	Net     []Net   `json:"net"`
	// Truncated says the window held more changes than were read, so Net
	// covers only the newest part of it.
	Truncated bool `json:"truncated"`
}

// Read collects the unit's recorded changes in [since, until].
func Read(ctx context.Context, store events.Store, unit string, since, until time.Time) (Report, error) {
	rep := Report{Unit: unit, Since: since, Until: until, Entries: []Entry{}, Net: []Net{}}
	q := events.Query{Unit: unit, Kind: Kind, Since: since, Until: until, Limit: pageSize}
	for i := 0; ; i++ {
		page, err := store.Query(ctx, q)
		if err != nil {
			return Report{}, err
		}
		for _, e := range page.Events {
			var p payload
			if json.Unmarshal(e.Payload, &p) != nil || len(p.Changes) == 0 {
				continue
			}
			rep.Entries = append(rep.Entries, Entry{At: e.At, Changes: p.Changes})
		}
		if page.Next == nil {
			break
		}
		if i+1 >= maxPages {
			rep.Truncated = true
			break
		}
		q.Cursor = *page.Next
	}
	rep.Net = net(rep.Entries)
	return rep, nil
}

func net(entries []Entry) []Net {
	sum := map[string]int{}
	for _, e := range entries {
		for _, c := range e.Changes {
			sum[c.Item] += c.To - c.From
		}
	}
	out := []Net{}
	for item, d := range sum {
		if d != 0 {
			out = append(out, Net{Item: item, Delta: d})
		}
	}
	slices.SortFunc(out, func(a, b Net) int {
		if a.Delta != b.Delta {
			return b.Delta - a.Delta
		}
		switch {
		case a.Item < b.Item:
			return -1
		case a.Item > b.Item:
			return 1
		}
		return 0
	})
	return out
}
