package history

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

const properties = `var UnminedMapProperties = {
    minZoom: -6,
    maxZoom: 0,
    imageFormat: "webp",
    minRegionX: -13,
    minRegionZ: -43,
    maxRegionX: 21,
    maxRegionZ: 14,
}
`

var day1 = time.Date(2026, 10, 12, 0, 20, 0, 0, time.UTC)

// world is a maps directory the tests draw on and redraw.
type world struct {
	t    *testing.T
	maps string
	r    *render.Unmined
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, maps: t.TempDir(), r: &render.Unmined{}}
	for _, d := range render.Dimensions {
		dir := filepath.Join(w.maps, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func (w *world) draw(dimension string, tile render.Tile, content string) {
	w.t.Helper()
	p := w.r.TilePath(filepath.Join(w.maps, dimension), "webp", tile.Zoom, tile.X, tile.Y)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) erase(dimension string, tile render.Tile) {
	w.t.Helper()
	if err := os.Remove(w.r.TilePath(filepath.Join(w.maps, dimension), "webp", tile.Zoom, tile.X, tile.Y)); err != nil {
		w.t.Fatal(err)
	}
}

func open(t *testing.T, dir string, w *world) *Store {
	t.Helper()
	s, err := Open(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.MapsDir, s.Source, s.Renderer = w.maps, w.r, Stamp{SHA256: "aaaa", NetherTopY: 100}
	return s
}

func capture(t *testing.T, s *Store, at time.Time) Result {
	t.Helper()
	r, err := s.Capture(context.Background(), at)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func tileAt(t *testing.T, s *Store, day, dimension string, tile render.Tile) (string, bool) {
	t.Helper()
	p, format, ok := s.Tile(day, dimension, tile)
	if !ok {
		return "", false
	}
	if format != "webp" {
		t.Fatalf("format = %q", format)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

var (
	home = render.Tile{Zoom: 0, X: 37, Y: -4}
	far  = render.Tile{Zoom: -3, X: -2, Y: 5}
)

func TestADayKeepsItsTilesAfterTheMapIsRedrawn(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	w.draw("overworld", far, "hills")
	s := open(t, t.TempDir(), w)

	r := capture(t, s, day1)
	if r.Skipped || r.Day != "2026-10-12" || r.Tiles != 2 || r.Changed != 2 || r.NewBytes != int64(len("a hut")+len("hills")) {
		t.Fatalf("first capture = %+v", r)
	}
	w.draw("overworld", home, "a castle")

	if got, ok := tileAt(t, s, "2026-10-12", "overworld", home); !ok || got != "a hut" {
		t.Fatalf("the day's tile = %q, %v; the map being redrawn must not reach it", got, ok)
	}
	if _, ok := tileAt(t, s, "2026-10-12", "nether", home); ok {
		t.Fatal("a dimension with no tile there answered with one")
	}
	if _, ok := tileAt(t, s, "2026-10-11", "overworld", home); ok {
		t.Fatal("a day never captured answered with a tile")
	}
}

func TestOneDayIsCapturedOnce(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)
	w.draw("overworld", home, "a castle")

	if r := capture(t, s, day1.Add(6*time.Hour)); !r.Skipped {
		t.Fatalf("second capture of the day = %+v, want skipped", r)
	}
	if got, _ := tileAt(t, s, "2026-10-12", "overworld", home); got != "a hut" {
		t.Fatalf("the day's tile became %q", got)
	}
	if n := len(s.Days()); n != 1 {
		t.Fatalf("%d days held, want 1", n)
	}
}

func TestTheNextDayCostsOnlyWhatChanged(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	w.draw("overworld", far, "hills")
	w.draw("nether", far, "lava")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)

	w.draw("overworld", home, "a castle")
	w.erase("nether", far)
	r := capture(t, s, day1.Add(24*time.Hour))
	if r.Tiles != 2 || r.Changed != 1 || r.NewBytes != int64(len("a castle")) {
		t.Fatalf("second day = %+v, want 2 tiles, 1 changed, only the castle's bytes new", r)
	}
	if got, _ := tileAt(t, s, "2026-10-13", "overworld", home); got != "a castle" {
		t.Fatalf("second day's tile = %q", got)
	}
	if got, _ := tileAt(t, s, "2026-10-12", "overworld", home); got != "a hut" {
		t.Fatalf("first day's tile = %q", got)
	}
	if got, _ := tileAt(t, s, "2026-10-13", "overworld", far); got != "hills" {
		t.Fatalf("an unchanged tile on the second day = %q", got)
	}
	if _, ok := tileAt(t, s, "2026-10-13", "nether", far); ok {
		t.Fatal("a tile the map no longer has is still in the later day")
	}
	if _, ok := tileAt(t, s, "2026-10-12", "nether", far); !ok {
		t.Fatal("a tile the map later lost is gone from the day that had it")
	}
	if got, want := s.Bytes(), int64(len("a hut")+len("hills")+len("lava")+len("a castle")); got != want {
		t.Fatalf("held bytes = %d, want %d", got, want)
	}
}

func TestDaysAreListedOldestFirstAndSurviveARestart(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)
	w.draw("overworld", home, "a castle")
	capture(t, s, day1.Add(48*time.Hour))

	s = open(t, dir, w)
	days := s.Days()
	if len(days) != 2 || days[0].Day != "2026-10-12" || days[1].Day != "2026-10-14" {
		t.Fatalf("days after reopening = %+v", days)
	}
	if !days[0].TakenAt.Equal(day1) {
		t.Fatalf("takenAt = %v", days[0].TakenAt)
	}
	var overworld DayDimension
	for _, d := range days[1].Dimensions {
		if d.ID == "overworld" {
			overworld = d
		}
	}
	if overworld.Tiles != 1 || overworld.Changed != 1 || overworld.MinRegionX != -13 || overworld.Format != "webp" {
		t.Fatalf("overworld on the second day = %+v", overworld)
	}
	if got, _ := tileAt(t, s, "2026-10-12", "overworld", home); got != "a hut" {
		t.Fatalf("first day's tile after reopening = %q", got)
	}
	if got, want := s.Bytes(), int64(len("a hut")+len("a castle")); got != want {
		t.Fatalf("held bytes after reopening = %d, want %d", got, want)
	}
}

// failing stops a capture part-way, as a full volume or a kill would.
type failing struct {
	Source
	after int
}

func (f *failing) Tiles(out, format string, yield func(render.Tile, string) error) error {
	n := 0
	return f.Source.Tiles(out, format, func(tile render.Tile, p string) error {
		if n++; n > f.after {
			return errors.New("stopped")
		}
		return yield(tile, p)
	})
}

func TestACaptureThatStopsPartWayLeavesNoDay(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	w.draw("overworld", far, "hills")
	dir := t.TempDir()
	s := open(t, dir, w)
	s.Source = &failing{Source: w.r, after: 1}

	if _, err := s.Capture(context.Background(), day1); err == nil {
		t.Fatal("a capture that stopped part-way reported success")
	}
	if n := len(s.Days()); n != 0 {
		t.Fatalf("%d days held after a failed capture", n)
	}
	if n := len(open(t, dir, w).Days()); n != 0 {
		t.Fatalf("%d days found on reopening after a failed capture", n)
	}

	s.Source = w.r
	if r := capture(t, s, day1.Add(time.Hour)); r.Skipped || r.Tiles != 2 {
		t.Fatalf("the capture after the failed one = %+v", r)
	}
	if got, _ := tileAt(t, s, "2026-10-12", "overworld", far); got != "hills" {
		t.Fatalf("tile = %q", got)
	}
}

func TestADimensionNotRenderedFailsTheCapture(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	if err := os.Remove(filepath.Join(w.maps, "end", "unmined.map.properties.js")); err != nil {
		t.Fatal(err)
	}
	s := open(t, t.TempDir(), w)
	if _, err := s.Capture(context.Background(), day1); err == nil {
		t.Fatal("a day was captured with a dimension missing")
	}
}

func TestADayDrawnByADifferentRendererSaysSo(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)
	capture(t, s, day1.Add(24*time.Hour))
	s.Renderer.SHA256 = "bbbb"
	capture(t, s, day1.Add(48*time.Hour))

	days := s.Days()
	if days[0].RendererChanged || days[1].RendererChanged || !days[2].RendererChanged {
		t.Fatalf("rendererChanged = %v %v %v, want only the third", days[0].RendererChanged, days[1].RendererChanged, days[2].RendererChanged)
	}
}

func TestAManifestThisVersionCannotReadIsLeftOutNotGuessedAt(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)
	if err := writeManifest(filepath.Join(dir, daysDir), manifest{Version: manifestVersion + 1, Day: "2026-10-13", TakenAt: day1}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, daysDir, "2026-10-14"+manifestSuffix), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	days := open(t, dir, w).Days()
	if len(days) != 1 || days[0].Day != "2026-10-12" {
		t.Fatalf("days = %+v, want only the one this version wrote", days)
	}
}

func TestAnAddressThatIsNotADayFindsNothing(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)
	for _, day := range []string{"", "..", "../days/2026-10-12", "2026-10-12/..", "2026-10-12.json.gz"} {
		if _, _, ok := s.Tile(day, "overworld", home); ok {
			t.Errorf("day %q found a tile", day)
		}
	}
	if _, _, ok := s.Tile("2026-10-12", "../overworld", home); ok {
		t.Error("a dimension that is not one found a tile")
	}
}
