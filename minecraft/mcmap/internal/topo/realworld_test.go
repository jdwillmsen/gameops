package topo

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to measure what a tile costs, in time
// and in bytes, where the land is as untidy as land is:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/topo/
//
// Every tile of the world's grid that holds ground is drawn once in each
// set of styles. Nothing is written out: where the world's land is stays
// in the world's copy.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	store := &biomes.Store{}
	e := &biomes.Extractor{WorkDir: t.TempDir(), Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := e.Extract(context.Background(), filepath.Join(world, "db"), time.Now(), -1); err != nil {
		t.Fatal(err)
	}
	m := store.World().Heights()
	if m == nil {
		t.Fatal("the reading holds no heights")
	}
	for _, d := range chunks.Dimensions {
		if !Offered(d) {
			if _, ok := Tile(m, d, 0, 0, 0, Pick{Styles: allStyles}); ok {
				t.Errorf("%s is drawn though it is not offered", d.Name())
			}
			continue
		}
		// The tiles of a block to a pixel that hold ground, which are where a
		// finer zoom is drawn: of each, the one in its middle.
		var ground [][2]int
		for ty := -16_384/TileSize - 1; ty <= 16_384/TileSize+1; ty++ {
			for tx := -16_384/TileSize - 1; tx <= 16_384/TileSize+1; tx++ {
				if _, ok := Tile(m, d, 0, tx, ty, Pick{Styles: Tint}); ok {
					ground = append(ground, [2]int{tx, ty})
				}
			}
		}
		for _, c := range []struct {
			name   string
			styles Styles
		}{{"shade", Shade}, {"contours", Contours}, {"tint", Tint}, {"all three", allStyles}} {
			for zoom := MaxZoom; zoom >= MinZoom; zoom-- {
				var times []time.Duration
				var sizes []int
				draw := func(tx, ty int) {
					began := time.Now()
					if data, ok := Tile(m, d, zoom, tx, ty, Pick{Styles: c.styles}); ok {
						times = append(times, time.Since(began))
						sizes = append(sizes, len(data))
					}
				}
				if zoom > 0 {
					for _, g := range ground {
						draw(g[0]<<zoom+1<<zoom/2, g[1]<<zoom+1<<zoom/2)
					}
				} else {
					// A tile at this zoom covers span tiles of the finest across.
					span := 1 << -zoom
					lo, hi := -16_384/(TileSize*span)-1, 16_384/(TileSize*span)+1
					for ty := lo; ty <= hi; ty++ {
						for tx := lo; tx <= hi; tx++ {
							draw(tx, ty)
						}
					}
				}
				if len(times) == 0 {
					continue
				}
				slices.Sort(times)
				slices.Sort(sizes)
				var total time.Duration
				var bytes int
				for i := range times {
					total += times[i]
					bytes += sizes[i]
				}
				n := len(times)
				t.Logf("%-9s %-9s zoom %2d: %5d tiles; time mean %s, median %s, worst %s; bytes mean %d, median %d, worst %d",
					d.Name(), c.name, zoom, n, (total / time.Duration(n)).Round(10*time.Microsecond), times[n/2].Round(10*time.Microsecond), times[n-1].Round(10*time.Microsecond),
					bytes/n, sizes[n/2], sizes[n-1])
			}
		}
	}
}
