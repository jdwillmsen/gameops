package layers

import (
	"context"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// FloorMaps reads the chunks of the dimension and returns, for each cut, the surface
// the topography can be drawn from: for every column the top of the highest
// floor at or below the cut, counted as the game counts its own heightmap
// so that a heights.Map holds it.
//
// The Nether's own heightmap is the bedrock roof over all of it, which
// says nothing about the ground, and a cut at a level is the stand-in for
// it that does: the same terrain tiles are drawn at the same cut.
//
// Every chunk is read once, in one walk of the database, for all the cuts. A column with no floor under
// its cut has no surface in that map.
func FloorMaps(ctx context.Context, r *Reader, dim chunks.Dimension, cuts []int, at time.Time) ([]*heights.Map, error) {
	builders := make([]*heights.Builder, len(cuts))
	for i := range builders {
		builders[i] = heights.NewBuilder(at, -1)
	}
	floor, _ := heights.Floor(dim)
	var raw [heights.Columns]int16
	err := r.Walk(ctx, dim, func(col *Column) error {
		blocks := r.Registry.Blocks()
		for i, cut := range cuts {
			for z := range 16 {
				for x := range 16 {
					raw[z*16+x] = 0
					if s, ok := col.floor(blocks, x, z, cut, MinY(dim)); ok {
						raw[z*16+x] = int16(int32(s.Y) - floor + 1)
					}
				}
			}
			if err := builders[i].AddColumns(dim, col.Pos.X, col.Pos.Z, &raw); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	maps := make([]*heights.Map, len(cuts))
	for i, b := range builders {
		maps[i] = b.Map()
	}
	return maps, nil
}
