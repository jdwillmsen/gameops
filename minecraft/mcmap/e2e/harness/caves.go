package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers/layerstest"
)

// A made-up world of sub-chunks to draw the cave views from, as the game
// writes them: four by four chunks of the overworld with a cave in the
// stone, and one chunk of the nether with a floor and a shelf. Nothing in it
// is a real place.
func writeCaveWorld(db *leveldb.DB) error {
	for cz := range int32(4) {
		for cx := range int32(4) {
			err := layerstest.Chunk(db, chunks.Pos{Dim: chunks.Overworld, X: cx, Z: cz}, 63, func(x, y, z int) string {
				switch {
				case y < 0:
					return "deepslate"
				case x < 10 && y >= 10 && y <= 14:
					return "air"
				case y < 41:
					return "stone"
				case y == 41:
					return "grass_block"
				}
				return "air"
			})
			if err != nil {
				return err
			}
		}
	}
	return layerstest.Chunk(db, chunks.Pos{Dim: chunks.Nether, X: 0, Z: 0}, 127, func(x, y, z int) string {
		switch {
		case y == 0 || y >= 126:
			return "bedrock"
		case y >= 100:
			return "netherrack"
		case x < 8 && y <= 70:
			return "netherrack"
		case y <= 30:
			return "netherrack"
		}
		return "air"
	})
}

// caves opens the made-up world for the cave views and reads the nether's
// floors from it, as the service does after a snapshot.
func caves(ctx context.Context, dir string, at time.Time, biomeStore *biomes.Store, levels []int) (*layers.Service, *layers.FloorStore, error) {
	db := filepath.Join(dir, "cave-world", "db")
	handle, err := leveldb.OpenFile(db, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := writeCaveWorld(handle); err != nil {
		handle.Close()
		return nil, nil, err
	}
	if err := handle.Close(); err != nil {
		return nil, nil, err
	}
	// Left as the mirror leaves a world: closed, with no lock.
	if err := os.Remove(filepath.Join(db, "LOCK")); err != nil {
		return nil, nil, err
	}
	service := layers.NewService(layers.Config{
		WorkDir:    filepath.Join(dir, "layers", "serve"),
		CacheBytes: 16 << 20,
		MaxRenders: 2,
		Biomes: func() layers.Biomes {
			if w := biomeStore.World(); w != nil {
				return w
			}
			return nil
		},
	})
	if err := service.Refresh(db, "harness"); err != nil {
		return nil, nil, err
	}
	store := &layers.FloorStore{}
	floors := &layers.FloorExtractor{WorkDir: filepath.Join(dir, "layers", "floors"), Levels: levels, Store: store}
	floors.Logger = slog.New(slog.DiscardHandler)
	if _, err := floors.Extract(ctx, db, at, -1); err != nil {
		return nil, nil, err
	}
	return service, store, nil
}

// lostWorld is a chunk count that found some chunks gone: a block of the
// overworld at the far edge of the made-up map.
type lostWorld struct{ at time.Time }

func (l lostWorld) Last() (chunks.Report, bool) {
	return chunks.Report{At: l.at, Present: map[chunks.Dimension]int{chunks.Overworld: 16}, Missing: map[chunks.Dimension]int{chunks.Overworld: 4}, Lost: map[chunks.Dimension]int{chunks.Overworld: 4}}, true
}

func (lostWorld) Acknowledge(time.Time) error { return nil }

func (lostWorld) Missing() chunks.Set {
	out := chunks.Set{}
	for _, p := range [][2]int32{{5, 1}, {6, 1}, {5, 2}, {6, 2}} {
		out[chunks.Pos{Dim: chunks.Overworld, X: p[0], Z: p[1]}] = struct{}{}
	}
	return out
}
