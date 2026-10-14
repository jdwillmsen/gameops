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
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
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

	checkRealHeights(t, e, w)

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

// checkRealHeights measures what the heights cost and checks that they are
// what the biome reading was of, and that the file brings them back.
func checkRealHeights(t *testing.T, e *Extractor, w *World) {
	t.Helper()
	h := w.Heights()
	if h == nil {
		t.Fatal("the reading holds no heights")
	}
	saved, err := os.Stat(e.heightsFile())
	if err != nil {
		t.Fatal(err)
	}
	var before, held runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	back, err := heights.Load(e.heightsFile())
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(started)
	runtime.GC()
	runtime.ReadMemStats(&held)
	t.Logf("heights: %d MiB estimated, %d KiB on the volume, loaded in %s holding %d KiB more heap", h.Bytes()>>20, saved.Size()>>10, took.Round(time.Millisecond), (held.HeapInuse-before.HeapInuse)>>10)
	runtime.KeepAlive(back)
	checkRealSurfaceCost(t, e.Store, w)

	for _, d := range chunks.Dimensions {
		if h.Chunks(d) != len(w.layers[d].cells) || back.Chunks(d) != h.Chunks(d) {
			t.Errorf("%s: %d chunks of heights, %d of biomes, %d loaded", d.Name(), h.Chunks(d), len(w.layers[d].cells), back.Chunks(d))
		}
		floor, _ := heights.Floor(d)
		top := map[chunks.Dimension]int32{chunks.Overworld: 319, chunks.Nether: 127, chunks.End: 255}[d]
		known, void, out := 0, 0, 0
		var cells [heights.Columns]int16
		for key := range w.layers[d].cells {
			cx, cz := int32(key>>32), int32(key)
			if !h.Rect(d, cx*16, cz*16, 16, 16, cells[:]) {
				void += heights.Columns
				continue
			}
			for _, y := range cells {
				switch {
				case y == heights.Unknown:
					void++
				case int32(y) < floor || int32(y) > top:
					out++
				default:
					known++
				}
			}
		}
		t.Logf("%s: %d columns with a surface, %d without, %d outside the dimension", d.Name(), known, void, out)
		if out != 0 {
			t.Errorf("%s: %d surfaces outside the dimension", d.Name(), out)
		}
		var a, b [heights.TileSize * heights.TileSize]int16
		for _, zoom := range []int{0, -2, -4} {
			started := time.Now()
			tiles := 0
			for tx := -2; tx < 2; tx++ {
				for ty := -2; ty < 2; ty++ {
					if h.Tile(d, zoom, tx, ty, &a) {
						tiles++
						back.Tile(d, zoom, tx, ty, &b)
						if a != b {
							t.Errorf("%s: a tile came back different from the volume", d.Name())
						}
					}
				}
			}
			if tiles > 0 {
				t.Logf("%s zoom %d: %d tiles of heights, %s each with the check", d.Name(), zoom, tiles, (time.Since(started) / time.Duration(tiles)).Round(time.Microsecond))
			}
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

// checkRealSurfaceCost measures what telling the surface under a frame of
// live entities costs: a thousand lookups at columns spread over the
// world, which is what each sample of a thousand mobs asks, once.
func checkRealSurfaceCost(t *testing.T, store *Store, w *World) {
	t.Helper()
	l := w.layers[chunks.Overworld]
	type column struct{ x, z int32 }
	var at []column
	for key := range l.cells {
		if len(at) == 1000 {
			break
		}
		at = append(at, column{int32(key>>32)*16 + 7, int32(key)*16 + 3})
	}
	const rounds = 200
	known := 0
	started := time.Now()
	for range rounds {
		known = 0
		for _, c := range at {
			if _, ok := store.Surface("overworld", c.x, c.z); ok {
				known++
			}
		}
	}
	each := time.Since(started) / time.Duration(rounds*len(at))
	t.Logf("surface under %d columns: %d known, %s a lookup, %s for a frame of %d", len(at), known, each, (each * time.Duration(len(at))).Round(time.Microsecond), len(at))

	// And the markers' answer, written out again with a surface on each:
	// the most a dimension's answer may hold, over columns of the world.
	var layer markers.Layer
	for i := 0; i < 3000 && i < len(at); i++ {
		c := at[i%len(at)]
		layer.Beds = append(layer.Beds, markers.Marker{X: c.x, Y: 64, Z: c.z, Colour: "red"})
		layer.Containers = append(layer.Containers, markers.Marker{X: c.x + 1, Y: 40, Z: c.z, Kind: "chest"})
		layer.Mobs = append(layer.Mobs, markers.Marker{X: c.x + 2, Y: 70, Z: c.z, Kind: "cat", Name: "A named cat", ID: "1234567890"})
	}
	marked := markers.NewStore()
	marked.Set(time.Now(), markers.World{chunks.Overworld: &layer})
	body, _, _ := marked.Dimension("overworld")
	var out []byte
	started = time.Now()
	for range 20 {
		out, _ = markers.WithSurface(body, func(x, z int32) (int32, bool) { return w.Heights().Surface(chunks.Overworld, x, z) })
	}
	t.Logf("markers answer of %d markers: %d bytes, %d with surfaces; %s to write out again", 3*len(layer.Beds), len(body), len(out), (time.Since(started) / 20).Round(time.Microsecond))
}
