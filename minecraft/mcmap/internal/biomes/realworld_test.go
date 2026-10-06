package biomes

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to check the reading against places
// whose biome is not in doubt, and to measure it:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/biomes/
//
// MCMAP_REAL_PLACES names a file of places in that world whose biome is not in
// doubt, one to a line: what it is, the dimension, x, z, and the biome wanted
// there (deep ocean for a monument, since the generator builds one nowhere
// else; nothing, for a place that is only reported). The file is kept out of
// the repository: where a world's structures are is enough to work out the
// seed that placed them.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	e := &Extractor{WorkDir: t.TempDir(), Store: &Store{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// The heap is sampled while the reading runs, since what matters is
	// the most it holds at once and not what is left afterwards.
	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	stats, err := e.Extract(context.Background(), filepath.Join(world, "db"), started, -1)
	took := time.Since(started)
	close(stop)
	<-sampled
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	saved, err := os.Stat(e.file())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("took %s; heap peaked at %d MiB and holds %d MiB afterwards; %d MiB allocated in all; saved file %d KiB",
		took.Round(time.Millisecond), peak.Load()>>20, after.HeapInuse>>20, (after.TotalAlloc-before.TotalAlloc)>>20, saved.Size()>>10)
	t.Logf("stats %+v", stats)
	w := e.Store.World()

	started = time.Now()
	again, err := load(e.file())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded from the volume in %s", time.Since(started).Round(time.Millisecond))

	for _, d := range chunks.Dimensions {
		l := w.layers[d]
		t.Logf("%s: %d chunks, %d of them column by column, %d kinds, %d chunk-and-kind pairs, %d regions",
			d.Name(), len(l.cells), len(l.mixed)/Columns, len(l.ids), len(l.entries), len(l.regions))
		for _, p := range w.Present(d) {
			t.Logf("  %-34s id %3d  %10d blocks  %6d chunks  %5d regions", p.Name, p.ID, p.Area, p.Chunks, p.Regions)
		}
	}

	places := realPlaces(t)
	for _, p := range places {
		got, ok := w.At(p.d, p.x, p.z)
		agrees := ok && got.Name == p.want
		switch p.want {
		case "deep ocean":
			agrees = ok && strings.HasPrefix(got.Name, "deep_") && strings.HasSuffix(got.Name, "ocean")
		case "nether":
			agrees = ok && (got.ID == 8 || (got.ID >= 178 && got.ID <= 181))
		}
		t.Logf("%-22s %-9s %6d, %6d: %s", p.what, p.d.Name(), p.x, p.z, got.Name)
		if p.want != "" && !agrees {
			t.Errorf("%s at %s %d, %d is in %q (found %v), want %s", p.what, p.d.Name(), p.x, p.z, got.Name, ok, p.want)
		}
		if back, _ := again.At(p.d, p.x, p.z); back != got {
			t.Errorf("the saved world answers %q there", back.Name)
		}
	}
	spawn, _ := w.At(chunks.Overworld, 0, 0)
	t.Logf("the world spawn, 0, 0: %s", spawn.Name)

	for _, zoom := range []int{0, -2, -4, -6} {
		started := time.Now()
		tiles, bytes := 0, 0
		for tx := -2; tx < 2; tx++ {
			for ty := -2; ty < 2; ty++ {
				if data, ok := w.Tile(chunks.Overworld, zoom, tx, ty, nil); ok {
					tiles++
					bytes += len(data)
				}
			}
		}
		if tiles > 0 {
			t.Logf("zoom %d: %d tiles in %s, %s and %d bytes each", zoom, tiles, time.Since(started).Round(time.Microsecond), (time.Since(started) / time.Duration(tiles)).Round(time.Microsecond), bytes/tiles)
		}
	}
	for _, name := range []string{"mushroom_island", "deep_ocean", "river", "cherry_grove"} {
		id, _ := Resolve(name)
		started := time.Now()
		hits, more := w.Nearest(chunks.Overworld, id, 0, 0, 5)
		t.Logf("nearest %s to 0, 0 in %s: %d more", name, time.Since(started).Round(time.Microsecond), more)
		for _, h := range hits {
			t.Logf("  %6d, %6d  %5.0f blocks away  %8d blocks in %5d chunks", h.X, h.Z, h.Distance, h.Region.Area, h.Region.Chunks)
		}
	}
}

type place struct {
	what string
	d    chunks.Dimension
	x, z int32
	want string
}

// realPlaces reads the places MCMAP_REAL_PLACES lists, or none if it names
// no file.
func realPlaces(t *testing.T) []place {
	t.Helper()
	name := os.Getenv("MCMAP_REAL_PLACES")
	if name == "" {
		t.Log("MCMAP_REAL_PLACES names no file, so no place is checked")
		return nil
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var places []place
	for n, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// what;dimension;x;z;want
		f := strings.Split(line, ";")
		if len(f) != 5 {
			t.Fatalf("%s:%d: want what;dimension;x;z;biome", name, n+1)
		}
		d := slices.IndexFunc(chunks.Dimensions, func(d chunks.Dimension) bool { return d.Name() == strings.TrimSpace(f[1]) })
		x, errX := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 32)
		z, errZ := strconv.ParseInt(strings.TrimSpace(f[3]), 10, 32)
		if d < 0 || errX != nil || errZ != nil {
			t.Fatalf("%s:%d: not a dimension and two whole numbers", name, n+1)
		}
		places = append(places, place{strings.TrimSpace(f[0]), chunks.Dimensions[d], int32(x), int32(z), strings.TrimSpace(f[4])})
	}
	return places
}
