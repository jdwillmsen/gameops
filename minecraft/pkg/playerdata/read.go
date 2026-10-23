package playerdata

import (
	"bytes"
	"context"
	"fmt"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/util"
)

// maxPlayers bounds how many records are kept. A server of friends has a
// few dozen; the key space is the world's, and not this service's to trust.
const maxPlayers = 512

// recordPrefix is the key of a player's own record; the others that start
// with player_ are the account-to-record index.
var recordPrefix = []byte("player_server_")

// Stats is what a read found.
type Stats struct {
	Players int
	Skipped int
}

// Read reads every player record the open world at db holds. A record the
// decoder cannot make sense of costs that record, not the read.
func Read(ctx context.Context, db *leveldb.DB) (found map[int64]Record, stats Stats, err error) {
	// A record the decoder trips over must cost that record and not the
	// service reading it.
	defer func() {
		if p := recover(); p != nil {
			found, stats, err = nil, Stats{}, fmt.Errorf("player read panicked: %v", p)
		}
	}()
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
