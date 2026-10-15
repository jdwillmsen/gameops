package topo

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func TestNetherFloorsAreDrawnOnlyThroughFloorTileAndHaveBandsThatMeet(t *testing.T) {
	bands := Bands(chunks.Nether)
	if len(bands) == 0 || len(bands) > 16 || bands[0].From > 0 || bands[len(bands)-1].To < 127 {
		t.Fatalf("%d bands, which do not cover 0 to 127", len(bands))
	}
	for i, b := range bands[1:] {
		if b.From != bands[i].To+1 {
			t.Errorf("band %d starts at %d after one that ends at %d", i+1, b.From, bands[i].To)
		}
	}
	floors := world(t, chunks.Nether, -2, 2, func(x, z int32) (int32, bool) { return 40 + x/16, true })
	pick := Pick{Styles: allStyles}
	if _, ok := Tile(floors, chunks.Nether, 0, 0, 0, pick); ok {
		t.Error("Tile drew the nether, whose own heightmap is the roof")
	}
	data, ok := FloorTile(floors, chunks.Nether, 0, 0, 0, pick)
	if !ok || len(decode(t, data).Pix) == 0 {
		t.Fatal("FloorTile drew nothing")
	}
	if _, ok := FloorTile(floors, chunks.Overworld, 0, 0, 0, pick); ok {
		t.Error("FloorTile drew a dimension that has a heightmap of its own")
	}
	if _, ok := FloorTile(floors, chunks.Nether, MaxZoom+1, 0, 0, pick); ok {
		t.Error("FloorTile drew past the finest zoom")
	}
}
