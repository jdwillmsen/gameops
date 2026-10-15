package layers

import (
	"context"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/topo"
)

// A made-up nether: bedrock at the bottom and the top, a floor of
// netherrack at 30 and a shelf of it at 70 on the west half of each chunk.
func shelves(x, y, z int) string {
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
}

func TestFloorMaps_AreTheTopOfTheHighestFloorAtOrUnderEachCut(t *testing.T) {
	f := newFake(t)
	for _, p := range []chunks.Pos{{Dim: chunks.Nether, X: 0, Z: 0}, {Dim: chunks.Nether, X: -1, Z: 2}} {
		f.chunk(p, 127, shelves)
	}
	// A chunk of another dimension is not in them.
	f.chunk(chunks.Pos{Dim: chunks.Overworld, X: 0, Z: 0}, 15, upTo(10, "stone"))
	r := f.reader()
	at := time.Unix(1_700_000_000, 0)
	maps, err := FloorMaps(context.Background(), &r, chunks.Nether, []int{100, 66, 34}, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != 3 || maps[0].Chunks(chunks.Nether) != 2 || maps[0].Chunks(chunks.Overworld) != 0 || !maps[0].SnapshotAt.Equal(at) {
		t.Fatalf("%d maps, %d chunks, taken at %v", len(maps), maps[0].Chunks(chunks.Nether), maps[0].SnapshotAt)
	}
	for _, c := range []struct {
		name  string
		level int
		x, z  int32
		y     int32
		has   bool
	}{
		{"the shelf from the top level", 0, 3, 3, 70, true},
		{"the floor from the top level", 0, 12, 3, 30, true},
		{"the shelf is solid at the middle level, which has no floor above 30 over it", 1, 3, 3, 0, false},
		{"the floor from the middle level", 1, 12, 3, 30, true},
		{"the floor from the low level", 2, 12, 3, 30, true},
		{"the second chunk", 0, -16 + 12, 32 + 3, 30, true},
		{"a chunk there is not", 0, 100, 100, 0, false},
	} {
		y, ok := maps[c.level].At(chunks.Nether, c.x, c.z)
		if ok != c.has || (ok && y != c.y) {
			t.Errorf("%s: %d, %v; want %d, %v", c.name, y, ok, c.y, c.has)
		}
	}
}

func TestFloorMaps_StopWhenAskedTo(t *testing.T) {
	f := newFake(t)
	f.chunk(chunks.Pos{Dim: chunks.Nether, X: 0, Z: 0}, 127, shelves)
	r := f.reader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FloorMaps(ctx, &r, chunks.Nether, []int{100}, time.Now()); err == nil {
		t.Error("a cancelled pass returned maps")
	}
}

// The floors are what the topography is drawn from in the Nether, where
// the game's own heightmap is the roof.
func TestFloorMaps_DriveTheNethersTopography(t *testing.T) {
	f := newFake(t)
	// A slope of floor across the chunk, so that there is relief to light.
	f.chunk(chunks.Pos{Dim: chunks.Nether, X: 0, Z: 0}, 127, func(x, y, z int) string {
		switch {
		case y == 0 || y >= 126:
			return "bedrock"
		case y >= 100:
			return "netherrack"
		case y <= 30+x:
			return "netherrack"
		}
		return "air"
	})
	r := f.reader()
	maps, err := FloorMaps(context.Background(), &r, chunks.Nether, []int{100}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pick := topo.Pick{Styles: topo.Shade | topo.Contours | topo.Tint}
	if data, ok := topo.FloorTile(maps[0], chunks.Nether, 0, 0, 0, pick); !ok || len(data) == 0 {
		t.Error("the nether's floors drew no tile")
	}
	if _, ok := topo.Tile(maps[0], chunks.Nether, 0, 0, 0, pick); ok {
		t.Error("a map of floors was drawn as the nether's heightmap")
	}
	if _, ok := topo.FloorTile(maps[0], chunks.Overworld, 0, 0, 0, pick); ok {
		t.Error("floors were drawn for the overworld")
	}
}
