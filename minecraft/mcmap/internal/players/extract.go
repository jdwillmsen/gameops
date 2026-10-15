package players

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/df-mc/goleveldb/leveldb/util"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

var (
	metricPlayers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_player_records",
		Help: "Player records read from the world at the last scan.",
	})
	metricFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_player_scan_failures_total",
		Help: "Player scans that could not read the world or ran out of time.",
	})
)

// maxPlayers bounds how many records are kept. A server of friends has a
// few dozen; the key space is the world's, and not this service's to trust.
const maxPlayers = 512

// recordPrefix is the key of a player's own record; the others that start
// with player_ are the account-to-record index.
var recordPrefix = []byte("player_server_")

// Stats is what a scan found.
type Stats struct {
	Players int
	Skipped int
}

// Extractor reads a freshly mirrored world's player records into a Store.
type Extractor struct {
	// WorkDir is where a scan builds its view of the world, as the other
	// readers' is: on the mirror's filesystem and shared with no one.
	WorkDir string
	Store   *Store
	// Timeout bounds one scan; zero means no bound.
	Timeout time.Duration
}

// Extract reads the records of the snapshot at dbDir, taken at at. A scan
// that fails leaves the store as it was.
func (e *Extractor) Extract(ctx context.Context, dbDir string, at time.Time) (Stats, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	found, stats, err := e.scan(ctx, dbDir)
	if err != nil {
		metricFailures.Inc()
		return Stats{}, err
	}
	e.Store.Set(at, found)
	metricPlayers.Set(float64(stats.Players))
	return stats, nil
}

func (e *Extractor) scan(ctx context.Context, dbDir string) (found map[int64]Record, stats Stats, err error) {
	// A record the decoder cannot make sense of must cost that record and
	// not the service.
	defer func() {
		if p := recover(); p != nil {
			found, err = nil, fmt.Errorf("player scan panicked: %v", p)
		}
	}()
	db, done, err := chunks.OpenView(dbDir, e.WorkDir)
	if err != nil {
		return nil, Stats{}, err
	}
	defer done()
	found = map[int64]Record{}
	it := db.NewIterator(util.BytesPrefix(recordPrefix), nil)
	defer it.Release()
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, Stats{}, err
		}
		if !bytes.HasPrefix(it.Key(), recordPrefix) || len(found) >= maxPlayers {
			stats.Skipped++
			continue
		}
		id, record, err := Decode(it.Value())
		if err != nil {
			stats.Skipped++
			continue
		}
		found[id] = record
	}
	if err := it.Error(); err != nil {
		return nil, Stats{}, err
	}
	stats.Players = len(found)
	return found, stats, nil
}
