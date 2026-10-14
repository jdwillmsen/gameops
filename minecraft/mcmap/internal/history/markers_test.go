package history

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

// served stands in for the markers the map is serving, one answer a
// dimension, which a test changes as a scan would.
type served map[string]string

func (m served) of(dimension string) ([]byte, bool) {
	body, ok := m[dimension]
	return []byte(body), ok
}

func markersOn(t *testing.T, s *Store, day, dimension string) (string, bool) {
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

func marked(day Day) map[string]bool {
	out := map[string]bool{}
	for _, d := range day.Dimensions {
		out[d.ID] = d.Markers
	}
	return out
}

func TestADayKeepsTheMarkersTheMapServedThen(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	now := served{"overworld": `{"beds":[{"x":1}]}`, "nether": `{"beds":[]}`, "end": `{"beds":[]}`}
	dir := t.TempDir()
	s := open(t, dir, w)
	s.Markers = now.of

	r := capture(t, s, day1)
	if want := int64(len("a hut") + len(now["overworld"]) + len(now["nether"])); r.Markers != 3 || r.NewBytes != want {
		t.Fatalf("first capture = %+v, want three dimensions' markers and %d new bytes: the nether's and the end's are one blob", r, want)
	}
	now["overworld"] = `{"beds":[{"x":1},{"x":2}]}`
	capture(t, s, day1.Add(24*time.Hour))

	for _, s := range []*Store{s, open(t, dir, w)} {
		if got, ok := markersOn(t, s, "2026-10-12", "overworld"); !ok || got != `{"beds":[{"x":1}]}` {
			t.Errorf("the first day's markers = %q, %v; what the map serves now must not reach them", got, ok)
		}
		if got, ok := markersOn(t, s, "2026-10-13", "overworld"); !ok || got != now["overworld"] {
			t.Errorf("the second day's markers = %q, %v", got, ok)
		}
		if got, ok := markersOn(t, s, "2026-10-12", "end"); !ok || got != `{"beds":[]}` {
			t.Errorf("the first day's markers in the end = %q, %v", got, ok)
		}
		if got := marked(s.Days()[0]); !got["overworld"] || !got["nether"] || !got["end"] {
			t.Errorf("the first day is listed as keeping markers for %v, want all three", got)
		}
		if got, want := s.Bytes(), int64(len("a hut")+len(`{"beds":[{"x":1}]}`)+len(`{"beds":[]}`)+len(now["overworld"])); got != want {
			t.Errorf("held bytes = %d, want %d", got, want)
		}
	}
}

// Markers switched off, and markers no scan has read yet: neither is a
// reason to lose the day's terrain.
func TestADayWithNoMarkersToKeepIsKeptWithoutThem(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)

	if r := capture(t, s, day1); r.Skipped || r.Tiles != 1 || r.Markers != 0 {
		t.Fatalf("a capture with markers off = %+v", r)
	}
	s.Markers = served{"nether": `{"beds":[]}`}.of
	if r := capture(t, s, day1.Add(24*time.Hour)); r.Skipped || r.Tiles != 1 || r.Markers != 1 {
		t.Fatalf("a capture with only the nether's markers read = %+v", r)
	}

	s = open(t, dir, w)
	days := s.Days()
	if got := marked(days[0]); got["overworld"] || got["nether"] || got["end"] {
		t.Errorf("a day kept without markers is listed as keeping them: %v", got)
	}
	if got := marked(days[1]); got["overworld"] || !got["nether"] || got["end"] {
		t.Errorf("the second day is listed as keeping markers for %v, want the nether alone", got)
	}
	if _, ok := markersOn(t, s, "2026-10-12", "overworld"); ok {
		t.Error("a day kept without markers answered with some")
	}
	if _, ok := markersOn(t, s, "2026-10-13", "overworld"); ok {
		t.Error("a dimension kept without markers answered with some")
	}
	if got, _ := tileAt(t, s, "2026-10-12", "overworld", home); got != "a hut" {
		t.Errorf("the day's tile = %q", got)
	}
}

func TestMarkersAreFoundOnlyByADayHeldAndADimensionOfIt(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	s.Markers = served{"overworld": `{"beds":[]}`, "nether": `{"beds":[]}`, "end": `{"beds":[]}`}.of
	capture(t, s, day1)
	for _, day := range []string{"", "..", "../days/2026-10-12", "2026-10-12/..", "2026-10-12.json.gz", "2026-10-11"} {
		if _, ok := s.MarkersOn(day, "overworld"); ok {
			t.Errorf("day %q found markers", day)
		}
	}
	for _, dimension := range []string{"", "../overworld", "moon"} {
		if _, ok := s.MarkersOn("2026-10-12", dimension); ok {
			t.Errorf("dimension %q found markers", dimension)
		}
	}
}

func manifestBytes(t *testing.T, dir, day string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, daysDir, day+manifestSuffix))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// What is added to a day long held is its markers and nothing else: the
// tiles, when it was taken, what drew it and how much of it had changed
// are the day, and are as they were.
func TestMarkersAddedToADayHeldLeaveTheRestOfItAsItWas(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	w.draw("overworld", far, "hills")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)
	w.draw("overworld", home, "a castle")
	capture(t, s, day1.Add(24*time.Hour))
	before := s.Days()
	// Asked for first, so the day is one a tile request has already read.
	tileAt(t, s, "2026-10-13", "overworld", home)

	then := map[string][]byte{"overworld": []byte(`{"beds":[{"x":1}]}`), "nether": []byte(`{"beds":[]}`), "end": []byte(`{"beds":[]}`)}
	added, err := s.AddMarkers("2026-10-13", then)
	if err != nil || added != 3 {
		t.Fatalf("added %d, %v; want the three dimensions", added, err)
	}

	for _, s := range []*Store{s, open(t, dir, w)} {
		after := s.Days()
		if got, ok := markersOn(t, s, "2026-10-13", "overworld"); !ok || got != `{"beds":[{"x":1}]}` {
			t.Errorf("the day's markers = %q, %v", got, ok)
		}
		if got := marked(after[1]); !got["overworld"] || !got["nether"] || !got["end"] {
			t.Errorf("the day is listed as keeping markers for %v, want all three", got)
		}
		// A copy: the list shares each day's dimensions with the store.
		after[1].Dimensions = slices.Clone(after[1].Dimensions)
		for i := range after[1].Dimensions {
			after[1].Dimensions[i].Markers = false
		}
		if !reflect.DeepEqual(after, before) {
			t.Errorf("the days apart from the markers:\n got %+v\nwant %+v", after, before)
		}
		if got, _ := tileAt(t, s, "2026-10-13", "overworld", home); got != "a castle" {
			t.Errorf("the day's tile = %q", got)
		}
		if got, _ := tileAt(t, s, "2026-10-13", "overworld", far); got != "hills" {
			t.Errorf("the day's unchanged tile = %q", got)
		}
		if _, ok := markersOn(t, s, "2026-10-12", "overworld"); ok {
			t.Error("the day before was given markers too")
		}
	}
	if got, want := s.Bytes(), int64(len("a hut")+len("hills")+len("a castle")+len(`{"beds":[{"x":1}]}`)+len(`{"beds":[]}`)); got != want {
		t.Errorf("held bytes = %d, want %d", got, want)
	}
}

// The job that adds them is run by hand and may be run again, and the
// markers a day has are the day's: a later reading does not replace them.
func TestMarkersAddedTwiceAreAddedOnce(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)
	if _, err := s.AddMarkers("2026-10-12", map[string][]byte{"overworld": []byte(`{"beds":[{"x":1}]}`)}); err != nil {
		t.Fatal(err)
	}
	written := manifestBytes(t, dir, "2026-10-12")
	stat, err := os.Stat(filepath.Join(dir, daysDir, "2026-10-12"+manifestSuffix))
	if err != nil {
		t.Fatal(err)
	}

	added, err := s.AddMarkers("2026-10-12", map[string][]byte{"overworld": []byte(`{"beds":[{"x":9}]}`)})
	if err != nil || added != 0 {
		t.Fatalf("the second time added %d, %v; want nothing", added, err)
	}
	again, err := os.Stat(filepath.Join(dir, daysDir, "2026-10-12"+manifestSuffix))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(stat, again) || string(manifestBytes(t, dir, "2026-10-12")) != string(written) {
		t.Error("a day that lacked nothing was written again")
	}
	if got, _ := markersOn(t, s, "2026-10-12", "overworld"); got != `{"beds":[{"x":1}]}` {
		t.Errorf("the day's markers became %q", got)
	}

	// The dimensions it still lacks are another matter.
	added, err = s.AddMarkers("2026-10-12", map[string][]byte{"overworld": []byte(`{"beds":[{"x":9}]}`), "end": []byte(`{"beds":[]}`)})
	if err != nil || added != 1 {
		t.Fatalf("with the end's as well added %d, %v; want the end's alone", added, err)
	}
	if got, ok := markersOn(t, open(t, dir, w), "2026-10-12", "end"); !ok || got != `{"beds":[]}` {
		t.Errorf("the end's markers = %q, %v", got, ok)
	}
}

func TestMarkersAreNotAddedToADayThatIsNotHeld(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)
	for _, day := range []string{"2026-10-11", "", "../days/2026-10-12"} {
		if added, err := s.AddMarkers(day, map[string][]byte{"overworld": []byte(`{}`)}); err == nil || added != 0 {
			t.Errorf("day %q: added %d, %v; want a refusal", day, added, err)
		}
	}
	if n := len(open(t, dir, w).Days()); n != 1 {
		t.Errorf("%d days held, want the one", n)
	}
}

// A day whose markers could not be written is still a day: the terrain is
// what cannot be had again.
func TestMarkersThatCannotBeWrittenDoNotCostTheDay(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	s := open(t, dir, w)
	body := `{"beds":[{"x":1}]}`
	s.Markers = served{"overworld": body}.of
	// The shard the markers' blob would go in is a file, so it cannot.
	sum := hashOf(body)
	if err := os.WriteFile(filepath.Join(dir, blobsDir, sum[:2]), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.draw("overworld", home, "a hut")

	r, err := s.Capture(t.Context(), day1)
	if err != nil || r.Skipped || r.Tiles != 1 || r.Markers != 0 {
		t.Fatalf("capture = %+v, %v; want the day kept without markers", r, err)
	}
	if _, ok := markersOn(t, s, "2026-10-12", "overworld"); ok {
		t.Error("markers that were never written are answered for")
	}
}

func hashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
