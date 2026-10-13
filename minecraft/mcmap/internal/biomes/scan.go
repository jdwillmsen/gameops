package biomes

import (
	"context"
	"fmt"
	"time"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// maxRecord is the largest biome record read. The largest in the FWB world
// is 19 KB; a whole overworld chunk at sixteen bits a block is 200 KB.
const maxRecord = 1 << 20

// Scan reads the biomes of every chunk in a world, a snapshot taken at at
// in which the chunk count found censusChunks chunks, or -1 if it found
// nothing.
//
// It reads every key once, as the other readers of the world do. Seeking
// from one chunk's biome record to the next was measured and is no
// faster: the biome records are themselves most of what there is to read.
//
// The same records begin with the chunk's heightmap, which is kept as the
// surface heights in the same pass: reading the world again for them would
// cost as much as this does.
func Scan(ctx context.Context, db *leveldb.DB, at time.Time, censusChunks int) (*World, error) {
	w := &World{SnapshotAt: at, CensusChunks: censusChunks}
	room := MaxChunks
	var builders [3]*builder
	for i := range builders {
		builders[i] = newBuilder(&w.Stats, &room)
	}
	surfaces := heights.NewBuilder(at, censusChunks)
	it := db.NewIterator(nil, nil)
	defer it.Release()
	var columns [Columns]uint32
	for n := 0; it.Next(); n++ {
		if n%16384 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		k := it.Key()
		pos, tag, ok := chunks.RecordOf(k)
		// A sub-chunk's key is a byte longer and can carry the same tag.
		if !ok || tag != TagData3D || (len(k) != 9 && len(k) != 13) {
			continue
		}
		v := it.Value()
		if len(v) > maxRecord || surface(v, &columns) != nil {
			w.Stats.Malformed++
			continue
		}
		builders[pos.Dim].add(pos.X, pos.Z, &columns)
		// A record that parsed as biomes is longer than its heightmap.
		_ = surfaces.Add(pos.Dim, pos.X, pos.Z, v)
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("read world: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, b := range builders {
		w.layers[i] = b.layer
	}
	w.heights = surfaces.Map()
	w.finish()
	return w, nil
}

// Build makes a World of chunks whose columns are already in hand, each at
// index z*16+x, as if read from a snapshot taken at at. It is for tests of
// whatever asks a World questions.
func Build(at time.Time, columns map[chunks.Pos][Columns]uint32) *World {
	w := &World{SnapshotAt: at, CensusChunks: len(columns)}
	room := MaxChunks
	for i := range w.layers {
		b := newBuilder(&w.Stats, &room)
		for pos, c := range columns {
			if int(pos.Dim) == i {
				b.add(pos.X, pos.Z, &c)
			}
		}
		w.layers[i] = b.layer
	}
	w.finish()
	return w
}

// WithHeights sets the surface heights a World reports, and returns it. It
// is for tests of whatever asks a World questions, and for a World that
// Build made, which has none.
func (w *World) WithHeights(m *heights.Map) *World {
	w.heights = m
	return w
}

// finish builds what is worked out from the chunks, which is everything a
// saved world leaves out.
func (w *World) finish() { w.finishWithin(maxEntries) }

func (w *World) finishWithin(entries int) {
	w.Stats.Kinds, w.Stats.Unknown, w.Stats.Unindexed = 0, 0, 0
	seen := map[uint32]struct{}{}
	for _, l := range w.layers {
		l.index(&w.Stats, entries)
		for _, id := range l.ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			w.Stats.Kinds++
			if !Lookup(id).Known {
				w.Stats.Unknown++
			}
		}
	}
}
