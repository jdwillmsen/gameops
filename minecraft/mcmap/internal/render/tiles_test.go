package render

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTiles_FindsEveryTileWhereTilePathPutsIt(t *testing.T) {
	out := t.TempDir()
	u := &Unmined{}
	want := map[Tile]string{}
	for _, tile := range []Tile{{0, 0, 0}, {0, 37, -4}, {-6, -1, -1}, {-2, -10, 10}, {0, -11, 9}} {
		p := u.TilePath(out, "webp", tile.Zoom, tile.X, tile.Y)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("tile"), 0o644); err != nil {
			t.Fatal(err)
		}
		want[tile] = p
	}
	// What else the renderer leaves beside its tiles is not one.
	for _, other := range []string{"tiles/zoom.0/0/0/tile.0.0.jpeg", "tiles/zoom.0/0/0/tile.0.webp", "tiles/zoom.0/0/0/notes.txt", "tiles/cache/0/0/tile.0.0.webp", "unmined.map.properties.js"} {
		p := filepath.Join(out, filepath.FromSlash(other))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := map[Tile]string{}
	if err := u.Tiles(out, "webp", func(tile Tile, p string) error { got[tile] = p; return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tiles = %v, want %v", got, want)
	}
}

func TestTiles_ADimensionWithNoTilesHasNoneAndIsNotAnError(t *testing.T) {
	calls := 0
	if err := (&Unmined{}).Tiles(t.TempDir(), "webp", func(Tile, string) error { calls++; return nil }); err != nil || calls != 0 {
		t.Errorf("err = %v after %d tiles", err, calls)
	}
}
