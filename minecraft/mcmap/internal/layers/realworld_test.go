package layers

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to see what the decoder makes of what
// real play wrote, and what a tile costs:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/layers/
//
// Nothing is written out: where the world's blocks are stays in the copy.
// cmd/layerbench measures the whole of it.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	dbDir := filepath.Join(world, "db")
	set, err := chunks.Scan(context.Background(), dbDir, filepath.Join(t.TempDir(), "scan"))
	if err != nil {
		t.Fatal(err)
	}
	db, closeDB, err := chunks.OpenView(dbDir, filepath.Join(t.TempDir(), "view"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	r := &Renderer{Reader: Reader{DB: db, Registry: NewRegistry()}}

	for _, d := range chunks.Dimensions {
		var positions []chunks.Pos
		for p := range set {
			if p.Dim == d {
				positions = append(positions, p)
			}
		}
		slices.SortFunc(positions, func(a, b chunks.Pos) int {
			if a.X != b.X {
				return int(a.X - b.X)
			}
			return int(a.Z - b.Z)
		})
		// Every fifty-first chunk, for a spread over the world without
		// reading all of it.
		var read, unreadable, withSlices int
		began := time.Now()
		col := new(Column)
		for i := 0; i < len(positions); i += 51 {
			found, err := r.Read(positions[i], col)
			read++
			if err != nil {
				unreadable++
			}
			if found {
				withSlices++
			}
		}
		t.Logf("%-9s %d chunks read in %s; %d hold slices, %d had a slice that would not read",
			d.Name(), read, time.Since(began).Round(time.Millisecond), withSlices, unreadable)
		if unreadable != 0 {
			t.Errorf("%s: %d chunks had a slice that is laid out in a way this does not read", d.Name(), unreadable)
		}
	}

	// A block that is drawn is not left to a guess: every kind met in what
	// was read has a colour.
	var guessed []string
	for _, b := range r.Registry.Blocks() {
		if b.Source == Guessed && b.Flags&(Air|Plant) == 0 {
			guessed = append(guessed, b.Name)
		}
	}
	t.Logf("%d kinds of block met; solid or liquid ones with only a guessed colour: %v", len(r.Registry.Blocks()), guessed)
	if len(guessed) > 12 {
		t.Errorf("%d kinds of block have no colour of their own", len(guessed))
	}

	for _, c := range []struct {
		spec Spec
		zoom int
	}{
		{Spec{Dim: chunks.Overworld, Cut: 0, Thickness: 16, Style: Floor}, 0},
		{Spec{Dim: chunks.Overworld, Cut: 0, Thickness: 16, Style: Floor}, -1},
		{Spec{Dim: chunks.Nether, Cut: 66, Thickness: 67, Style: Floor}, 0},
		{Spec{Dim: chunks.Nether, Cut: 100, Thickness: 101, Style: Cut}, -1},
	} {
		// The tile with the most chunks in it.
		counts := map[[2]int]int{}
		across := 16 << -c.zoom
		for p := range set {
			if p.Dim == c.spec.Dim {
				counts[[2]int{floorDiv(int(p.X), across), floorDiv(int(p.Z), across)}]++
			}
		}
		var best [2]int
		for k, n := range counts {
			if n > counts[best] || n == counts[best] && (k[0] < best[0] || k[0] == best[0] && k[1] < best[1]) {
				best = k
			}
		}
		_, tm, ok, err := r.Tile(c.spec, c.zoom, best[0], best[1])
		if err != nil || !ok {
			t.Errorf("%s zoom %d: ok %v, error %v", c.spec.Dim.Name(), c.zoom, ok, err)
			continue
		}
		t.Logf("%-9s cut %3d zoom %2d: %d chunks, %d slices; read %s, paint %s, encode %s; %d bytes",
			c.spec.Dim.Name(), c.spec.Cut, c.zoom, tm.Chunks, tm.Slices, tm.Read.Round(time.Millisecond),
			tm.Paint.Round(time.Millisecond), tm.Encode.Round(time.Millisecond), tm.Bytes)
	}
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
