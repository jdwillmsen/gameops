package backfill

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
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

// drawer stands in for the renderer: a tile is whatever the world's
// level.dat says, so a day's tile shows which archive it was drawn from.
type drawer struct {
	render.Unmined
	prepareErr error
	failOn     string
	worlds     []string
}

func (d *drawer) Prepare(context.Context) error { return d.prepareErr }

func (d *drawer) Render(_ context.Context, world, dimension, out string) error {
	d.worlds = append(d.worlds, world)
	said, err := os.ReadFile(filepath.Join(world, "level.dat"))
	if err != nil {
		return err
	}
	if d.failOn != "" && strings.Contains(string(said), d.failOn) {
		return errors.New("render broke")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
		return err
	}
	tile := d.TilePath(out, "webp", 0, 37, -4)
	if err := os.MkdirAll(filepath.Dir(tile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(tile, []byte(dimension+" of "+string(said)), 0o644)
}

type file struct{ name, content string }

func archive(t *testing.T, dir, name string, files ...file) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func world(said string) []file {
	return []file{{"./FWB/level.dat", said}, {"./FWB/db/CURRENT", "MANIFEST-000001\n"}}
}

type setup struct {
	archives string
	store    *history.Store
	drawer   *drawer
	job      Job
	log      *bytes.Buffer
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	store, err := history.Open(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	d := &drawer{}
	store.Source, store.Renderer = d, history.Stamp{SHA256: "aaaa", NetherTopY: 100}
	s := &setup{archives: t.TempDir(), store: store, drawer: d, log: &log}
	s.job = Job{Archives: s.archives, Work: t.TempDir(), Level: "FWB", Store: store, Renderer: d, Logger: logger}
	return s
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func days(s *history.Store) []string {
	var out []string
	for _, d := range s.Days() {
		out = append(out, d.Day)
	}
	return out
}

func tileOf(t *testing.T, s *history.Store, day string) string {
	t.Helper()
	p, _, ok := s.Tile(day, "overworld", render.Tile{Zoom: 0, X: 37, Y: -4})
	if !ok {
		t.Fatalf("no tile for %s", day)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEachArchiveBecomesTheDayItWasTakenOn(t *testing.T) {
	s := newSetup(t)
	// Out of order on purpose: a day is counted against the day before it,
	// so the oldest archive has to be drawn first.
	archive(t, s.archives, "fwb-20261003T040012Z.tar.gz", world("the third")...)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("the first")...)

	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 3 || r.Held != 0 || r.Failed != 0 {
		t.Fatalf("result = %+v", r)
	}
	if got := strings.Join(days(s.store), " "); got != "2026-10-01 2026-10-02 2026-10-03" {
		t.Fatalf("days = %s", got)
	}
	if got := tileOf(t, s.store, "2026-10-03"); got != "overworld of the third" {
		t.Errorf("the third day's tile = %q", got)
	}
	if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of the first" {
		t.Errorf("the first day's tile = %q", got)
	}
	all := s.store.Days()
	if !all[0].TakenAt.Equal(mustTime("2026-10-01T04:00:07Z")) {
		t.Errorf("takenAt = %v, want the archive's own time", all[0].TakenAt)
	}
	// The second world is the first again, so its tiles are already held.
	if all[1].Dimensions[0].Changed != 0 || all[2].Dimensions[0].Changed != 1 {
		t.Errorf("changed = %d then %d, want 0 then 1", all[1].Dimensions[0].Changed, all[2].Dimensions[0].Changed)
	}
}

// The job is run by hand and may be run again: after a failure, or when
// more archives are there than last time.
func TestADayAlreadyHeldIsLeftAloneAndNotDrawnAgain(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	if _, err := s.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	drawn := len(s.drawer.worlds)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("the second")...)

	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Held != 1 {
		t.Fatalf("result = %+v, want one kept and one already held", r)
	}
	if got := len(s.drawer.worlds) - drawn; got != 3 {
		t.Errorf("%d renders on the second run, want the three dimensions of the one new day", got)
	}
}

func TestTwoArchivesOfOneDayAreOneDayTheEarlier(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("morning")...)
	archive(t, s.archives, "fwb-20261001T180000Z.tar.gz", world("evening")...)
	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Held != 1 {
		t.Fatalf("result = %+v", r)
	}
	if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of morning" {
		t.Errorf("tile = %q", got)
	}
}

func TestAnArchiveThatCannotBeDrawnCostsItsDayAndNoOther(t *testing.T) {
	s := newSetup(t)
	s.drawer.failOn = "broken"
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("broken")...)
	if err := os.WriteFile(filepath.Join(s.archives, "fwb-20261003T040000Z.tar.gz"), []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive(t, s.archives, "fwb-20261004T040009Z.tar.gz", world("the fourth")...)

	r, err := s.job.Run(context.Background())
	if err == nil {
		t.Fatal("a run that lost two days reported nothing wrong")
	}
	if r.Kept != 2 || r.Failed != 2 {
		t.Fatalf("result = %+v", r)
	}
	if got := strings.Join(days(s.store), " "); got != "2026-10-01 2026-10-04" {
		t.Fatalf("days = %s", got)
	}
	if !strings.Contains(s.log.String(), "fwb-20261002T040009Z.tar.gz") || !strings.Contains(s.log.String(), "fwb-20261003T040000Z.tar.gz") {
		t.Errorf("the log does not name the archives that failed:\n%s", s.log.String())
	}
}

func TestWhatIsNotAnArchiveOfTheWorldIsPassedOver(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	for _, name := range []string{".fwb-20261002T040007Z.tar.gz.partial", "fwb-20261002T040007Z.tar.gz.meta", "notes.txt", "fwb-2026.tar.gz", "fwb-20261332T040007Z.tar.gz"} {
		if err := os.WriteFile(filepath.Join(s.archives, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Failed != 0 {
		t.Fatalf("result = %+v", r)
	}
}

func TestAnArchiveCannotWriteOutsideItsWorkDirectory(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", file{"../escaped", "x"}, file{"FWB/level.dat", "the first"})
	if _, err := s.job.Run(context.Background()); err == nil {
		t.Fatal("an archive naming a path outside itself was drawn")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.job.Work), "escaped")); err == nil {
		t.Fatal("the archive wrote outside the work directory")
	}
	if n := len(s.store.Days()); n != 0 {
		t.Fatalf("%d days kept", n)
	}
}

func TestAnArchiveWithoutTheLevelIsNotADay(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", file{"Other/level.dat", "x"})
	r, err := s.job.Run(context.Background())
	if err == nil || r.Failed != 1 {
		t.Fatalf("result = %+v, err = %v", r, err)
	}
}

// With no renderer there is nothing to do for any archive, so none is
// unpacked to find that out.
func TestARendererThatCannotRunStopsTheJobBeforeAnythingIsUnpacked(t *testing.T) {
	s := newSetup(t)
	s.drawer.prepareErr = errors.New("no renderer")
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	if _, err := s.job.Run(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	left, _ := os.ReadDir(s.job.Work)
	if len(left) != 0 {
		t.Errorf("the work directory holds %d entries", len(left))
	}
}

func TestTheWorkDirectoryIsLeftEmpty(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	if _, err := s.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(s.job.Work)
	if len(left) != 0 {
		t.Errorf("the work directory holds %d entries after the run", len(left))
	}
}
