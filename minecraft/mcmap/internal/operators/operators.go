// Package operators says who the map's admins are: the players the game's
// own permissions.json makes operators, read through the console bridge.
package operators

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/atomicfile"
)

type Source string

const (
	Live          Source = "live"
	LastKnownGood Source = "last_known_good"
	None          Source = "none"
)

const (
	CacheFor     = 10 * time.Second
	Window       = 24 * time.Hour
	ConfirmEvery = time.Hour
)

type Lister interface {
	Permissions(ctx context.Context) (map[string]string, error)
}

type file struct {
	XUIDs       []string  `json:"xuids"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

// List is the operator list with two fallbacks. While the bridge cannot be
// read, the last set read is used for a day from when it was last
// confirmed, so that an outage of the bridge does not also lock the owner
// out of the map he would debug it with. The cost is that a demotion
// waits for the bridge.
type List struct {
	path   string
	lister Lister
	now    func() time.Time
	log    *slog.Logger

	// refreshing is held for a lookup, so that requests arriving once the
	// cache has run out wait for one lookup instead of each making one.
	refreshing sync.Mutex

	mu      sync.Mutex
	live    map[string]bool
	askedAt time.Time
	source  Source
	kept    file
	failLog time.Time
}

func Open(path string, l Lister, now func() time.Time, log *slog.Logger) (*List, error) {
	o := &List{path: path, lister: l, now: now, log: log, source: None}
	kept, err := load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	o.kept = kept
	return o, nil
}

func load(path string) (file, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return file{}, err
	}
	var f file
	return f, json.Unmarshal(raw, &f)
}

func (o *List) fresh() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.live != nil && o.now().Sub(o.askedAt) < CacheFor
}

func (o *List) IsOperator(ctx context.Context, xuid string) (bool, Source) {
	if !o.fresh() {
		o.refreshing.Lock()
		if !o.fresh() {
			o.refresh(ctx)
		}
		o.refreshing.Unlock()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch o.source {
	case Live:
		return o.live[xuid], Live
	case LastKnownGood:
		return slices.Contains(o.kept.XUIDs, xuid), LastKnownGood
	}
	return false, None
}

func (o *List) Source() Source {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.source
}

func (o *List) Refresh(ctx context.Context) Source {
	o.refreshing.Lock()
	defer o.refreshing.Unlock()
	return o.refresh(ctx)
}

func (o *List) refresh(ctx context.Context) Source {
	perms, err := o.lister.Permissions(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	o.askedAt = now
	if err != nil {
		o.live = map[string]bool{}
		if now.Sub(o.failLog) >= time.Minute {
			o.failLog = now
			o.log.Warn("map operator lookup failed", "error_class", classOf(err))
		}
		if !o.kept.ConfirmedAt.IsZero() && now.Sub(o.kept.ConfirmedAt) < Window {
			o.source = LastKnownGood
			metricOperators.Set(float64(len(o.kept.XUIDs)))
		} else {
			o.source = None
			metricOperators.Set(0)
		}
		metricLookups.WithLabelValues(string(o.source)).Inc()
		return o.source
	}
	live, xuids := map[string]bool{}, []string{}
	for xuid, level := range perms {
		if level == "operator" {
			live[xuid] = true
			xuids = append(xuids, xuid)
		}
	}
	slices.Sort(xuids)
	o.live, o.source = live, Live
	metricOperators.Set(float64(len(xuids)))
	metricLookups.WithLabelValues(string(Live)).Inc()
	if !slices.Equal(xuids, o.kept.XUIDs) || now.Sub(o.kept.ConfirmedAt) >= ConfirmEvery {
		next := file{XUIDs: xuids, ConfirmedAt: now.UTC()}
		raw, _ := json.Marshal(next)
		if err := atomicfile.Write(o.path, raw); err != nil {
			o.log.Error("map operator list not kept", "error_class", "write")
		} else {
			o.kept = next
		}
	}
	return Live
}

// Run looks the list up now and then hourly, so that the gauge and the
// confirmation time stay current while nobody is using the map.
func (o *List) Run(ctx context.Context) {
	o.Refresh(ctx)
	tick := time.NewTicker(ConfirmEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			o.Refresh(ctx)
		}
	}
}

// classOf says what kind of failure it was and nothing a caller wrote.
func classOf(err error) string {
	switch {
	case errors.Is(err, ErrNoFile):
		return "no_file"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	return "unreachable"
}
