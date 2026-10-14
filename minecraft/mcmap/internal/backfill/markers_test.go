package backfill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// scanner stands in for the marker scan: a dimension's markers are
// whatever the world's level.dat says, so a day's markers show which
// archive they were read from.
type scanner struct {
	t      *testing.T
	work   string
	failOn string
	read   []string
}

func (s *scanner) scan(_ context.Context, db, work string, at time.Time) (map[string][]byte, error) {
	if _, err := os.Stat(filepath.Join(db, "CURRENT")); err != nil {
		s.t.Errorf("the scan was given %s, which is not the world's database: %v", db, err)
	}
	if rel, err := filepath.Rel(filepath.Join(s.work, scratchDir), work); err != nil || !filepath.IsLocal(rel) {
		s.t.Errorf("the scan was to work in %s, outside the job's own directory", work)
	}
	said, err := os.ReadFile(filepath.Join(filepath.Dir(db), "level.dat"))
	if err != nil {
		return nil, err
	}
	s.read = append(s.read, string(said))
	if s.failOn != "" && strings.Contains(string(said), s.failOn) {
		return nil, errors.New("scan broke")
	}
	bodies := map[string][]byte{}
	for _, d := range render.Dimensions {
		bodies[d] = []byte(d + " markers of " + string(said) + " at " + at.Format(time.RFC3339))
	}
	return bodies, nil
}

func withScanner(t *testing.T, s *setup) *scanner {
	t.Helper()
	sc := &scanner{t: t, work: s.job.Work}
	s.job.Markers = sc.scan
	return sc
}

func markersOf(t *testing.T, s *history.Store, day, dimension string) (string, bool) {
	t.Helper()
	p, ok := s.MarkersOn(day, dimension)
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// unmarked is the days as listed with what they say of markers taken out,
// which is everything an amended day must still say.
func unmarked(s *history.Store) []history.Day {
	all := s.Days()
	for i := range all {
		// A copy: the list shares each day's dimensions with the store.
		all[i].Dimensions = slices.Clone(all[i].Dimensions)
		for j := range all[i].Dimensions {
			all[i].Dimensions[j].Markers = false
		}
	}
	return all
}

func TestADayIsKeptWithTheMarkersOfItsArchive(t *testing.T) {
	s := newSetup(t)
	sc := withScanner(t, s)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("the second")...)

	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 2 || r.Amended != 0 || r.Unmarked != 0 {
		t.Fatalf("result = %+v", r)
	}
	if got, ok := markersOf(t, s.store, "2026-10-01", "overworld"); !ok || got != "overworld markers of the first at 2026-10-01T04:00:07Z" {
		t.Errorf("the first day's markers = %q, %v", got, ok)
	}
	if got, ok := markersOf(t, s.store, "2026-10-02", "end"); !ok || got != "end markers of the second at 2026-10-02T04:00:09Z" {
		t.Errorf("the second day's markers in the end = %q, %v", got, ok)
	}
	if len(sc.read) != 2 {
		t.Errorf("%d scans, want one an archive", len(sc.read))
	}
	left, _ := os.ReadDir(s.job.Work)
	if len(left) != 0 {
		t.Errorf("the work directory holds %d entries after the run", len(left))
	}
}

// Every day from before markers were kept is one of these, and so is every
// day the map kept itself with markers off. The archive is unpacked and
// read, and not drawn: the tiles are the day and are held already.
func TestADayHeldWithoutMarkersIsGivenThoseOfItsArchiveAndIsNotDrawnAgain(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("the second")...)
	if _, err := s.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, drawn := unmarked(s.store), len(s.drawer.worlds)
	// A held day whose archive has since gone, and one never yet seen.
	if err := os.Remove(filepath.Join(s.archives, "fwb-20261001T040007Z.tar.gz")); err != nil {
		t.Fatal(err)
	}
	archive(t, s.archives, "fwb-20261003T040012Z.tar.gz", world("the third")...)
	sc := withScanner(t, s)

	r, err := s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 1 || r.Amended != 1 || r.Held != 0 || r.Failed != 0 || r.Unmarked != 0 {
		t.Fatalf("result = %+v, want one day kept and one given its markers", r)
	}
	if got := len(s.drawer.worlds) - drawn; got != 3 {
		t.Errorf("%d renders, want the three dimensions of the one new day", got)
	}
	if got, ok := markersOf(t, s.store, "2026-10-02", "nether"); !ok || got != "nether markers of the second at 2026-10-02T04:00:09Z" {
		t.Errorf("the held day's markers = %q, %v", got, ok)
	}
	if _, ok := markersOf(t, s.store, "2026-10-01", "overworld"); ok {
		t.Error("a day with no archive was given markers")
	}
	if after := unmarked(s.store); !reflect.DeepEqual(after[:2], before) {
		t.Errorf("the days held before, apart from their markers:\n got %+v\nwant %+v", after[:2], before)
	}
	if got := tileOf(t, s.store, "2026-10-02"); got != "overworld of the second" {
		t.Errorf("the held day's tile = %q", got)
	}
	if !strings.Contains(s.log.String(), "markers added") || !strings.Contains(s.log.String(), "fwb-20261002T040009Z.tar.gz") {
		t.Errorf("the log does not say which archive's markers were added:\n%s", s.log.String())
	}
	left, _ := os.ReadDir(s.job.Work)
	if len(left) != 0 {
		t.Errorf("the work directory holds %d entries after the run", len(left))
	}

	// Again: nothing lacks anything an archive could give it.
	scans := len(sc.read)
	r, err = s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Kept != 0 || r.Amended != 0 || r.Held != 2 {
		t.Fatalf("the run after = %+v, want both archives' days already held", r)
	}
	if len(sc.read) != scans || len(s.drawer.worlds) != drawn+3 {
		t.Errorf("the run after unpacked and read or drew an archive it had nothing to take from")
	}
}

func TestWithoutAMarkerScanDaysAreKeptAndLeftWithoutMarkers(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	for range 2 {
		if _, err := s.job.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := markersOf(t, s.store, "2026-10-01", "overworld"); ok {
		t.Error("a job with no marker scan kept markers")
	}
}

// The terrain is what takes the time, so markers that cannot be read cost
// the day those and not the day; the run says so, and the next one adds
// them without drawing anything.
func TestMarkersThatCannotBeReadCostTheDayOnlyThose(t *testing.T) {
	s := newSetup(t)
	sc := withScanner(t, s)
	sc.failOn = "the second"
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("the first")...)
	archive(t, s.archives, "fwb-20261002T040009Z.tar.gz", world("the second")...)

	r, err := s.job.Run(context.Background())
	if err == nil {
		t.Fatal("a run that could not read an archive's markers reported nothing wrong")
	}
	if r.Kept != 2 || r.Failed != 0 || r.Unmarked != 1 {
		t.Fatalf("result = %+v", r)
	}
	if got := tileOf(t, s.store, "2026-10-02"); got != "overworld of the second" {
		t.Errorf("the day's tile = %q", got)
	}
	if got, ok := markersOf(t, s.store, "2026-10-02", "overworld"); ok {
		t.Errorf("the day was kept with markers that were never read: %q", got)
	}
	if !strings.Contains(s.log.String(), "markers not read") || !strings.Contains(s.log.String(), "fwb-20261002T040009Z.tar.gz") {
		t.Errorf("the log does not name the archive whose markers were not read:\n%s", s.log.String())
	}

	sc.failOn = ""
	drawn := len(s.drawer.worlds)
	r, err = s.job.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Amended != 1 || r.Held != 1 || r.Kept != 0 || len(s.drawer.worlds) != drawn {
		t.Fatalf("the run after = %+v with %d renders, want the one day amended and nothing drawn", r, len(s.drawer.worlds)-drawn)
	}
	if got, _ := markersOf(t, s.store, "2026-10-02", "overworld"); got != "overworld markers of the second at 2026-10-02T04:00:09Z" {
		t.Errorf("the day's markers = %q", got)
	}
}

func TestAnArchiveThatCannotBeReadLeavesItsHeldDayAsItWasAndALaterOneIsTried(t *testing.T) {
	s := newSetup(t)
	archive(t, s.archives, "fwb-20261001T040007Z.tar.gz", world("morning")...)
	if _, err := s.job.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := s.store.Days()
	if err := os.WriteFile(filepath.Join(s.archives, "fwb-20261001T040007Z.tar.gz"), []byte("not an archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	withScanner(t, s)

	r, err := s.job.Run(context.Background())
	if err == nil || r.Unmarked != 1 || r.Amended != 0 || r.Failed != 0 {
		t.Fatalf("result = %+v, err = %v", r, err)
	}
	if !reflect.DeepEqual(s.store.Days(), before) {
		t.Errorf("the day changed:\n got %+v\nwant %+v", s.store.Days(), before)
	}

	archive(t, s.archives, "fwb-20261001T180000Z.tar.gz", world("evening")...)
	r, err = s.job.Run(context.Background())
	if err == nil || r.Unmarked != 1 || r.Amended != 1 {
		t.Fatalf("with a second archive of the day: result = %+v, err = %v", r, err)
	}
	if got, _ := markersOf(t, s.store, "2026-10-01", "overworld"); got != "overworld markers of evening at 2026-10-01T18:00:00Z" {
		t.Errorf("the day's markers = %q", got)
	}
	if got := tileOf(t, s.store, "2026-10-01"); got != "overworld of morning" {
		t.Errorf("the day's tile = %q: its terrain is the morning's still", got)
	}
}
