package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

type fakeHistory struct {
	days    []history.Day
	tiles   map[string]string
	markers map[string]string
}

func (f *fakeHistory) Days() []history.Day { return f.days }

func (f *fakeHistory) Tile(day, dimension string, tile render.Tile) (string, string, bool) {
	p, ok := f.tiles[fmt.Sprintf("%s/%s/%d/%d/%d", day, dimension, tile.Zoom, tile.X, tile.Y)]
	return p, "webp", ok
}

func (f *fakeHistory) MarkersOn(day, dimension string) (string, bool) {
	p, ok := f.markers[day+"/"+dimension]
	return p, ok
}

// What changed is asked of days really kept, in history_changes_test.go.
func (f *fakeHistory) Changes(string, string) (history.Changes, bool) {
	return history.Changes{}, false
}

const markersThen = `{"at":"2026-10-12T00:20:00Z","beds":[{"x":1,"y":64,"z":-2}],"containers":[],"mobs":[],"more":{"beds":0,"containers":0,"mobs":0}}`

func withHistory(t *testing.T) *Server {
	t.Helper()
	s, _ := fixture(t)
	blob := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(blob, []byte("RIFFas-it-was"), 0o644); err != nil {
		t.Fatal(err)
	}
	marked := filepath.Join(t.TempDir(), "markers")
	if err := os.WriteFile(marked, []byte(markersThen), 0o644); err != nil {
		t.Fatal(err)
	}
	s.History = &fakeHistory{
		days: []history.Day{{
			Day:      "2026-10-12",
			TakenAt:  time.Date(2026, 10, 12, 0, 20, 0, 0, time.UTC),
			Renderer: history.Stamp{SHA256: "aaaa", NetherTopY: 100},
			Dimensions: []history.DayDimension{{
				ID: "overworld", Info: render.Info{MinZoom: -6, MinRegionX: -13, Format: "webp"}, Tiles: 1, Changed: 1, Markers: true,
			}, {
				ID: "nether", Info: render.Info{MinZoom: -6, Format: "webp"},
			}},
		}},
		tiles:   map[string]string{"2026-10-12/overworld/0/37/-4": blob},
		markers: map[string]string{"2026-10-12/overworld": marked},
	}
	return s
}

func TestHistory_ListsTheDaysWithEachDimensionsExtent(t *testing.T) {
	rec := get(t, withHistory(t), "/api/history")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var out struct {
		Days []struct {
			Day        string
			TakenAt    time.Time
			Dimensions []struct {
				ID         string
				MinZoom    int
				MinRegionX int
				Format     string
				Tiles      int
				Changed    int
				Markers    *bool
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Days) != 1 || out.Days[0].Day != "2026-10-12" || out.Days[0].TakenAt.IsZero() {
		t.Fatalf("days = %+v", out.Days)
	}
	if d := out.Days[0].Dimensions[0]; d.ID != "overworld" || d.MinZoom != -6 || d.MinRegionX != -13 || d.Format != "webp" || d.Tiles != 1 || d.Changed != 1 {
		t.Errorf("dimension = %+v", d)
	}
	// Said only where it is so: a page reads its absence as none kept.
	if kept := out.Days[0].Dimensions[0].Markers; kept == nil || !*kept {
		t.Errorf("a dimension that kept its markers does not say so: %s", rec.Body)
	}
	if kept := out.Days[0].Dimensions[1].Markers; kept != nil {
		t.Errorf("a dimension that kept no markers says %v of them: %s", *kept, rec.Body)
	}
}

// With no day held yet the page is still told so in a list, not in a null
// it would have to know to look for.
func TestHistory_WithNoDaysTheListIsEmpty(t *testing.T) {
	s, _ := fixture(t)
	s.History = &fakeHistory{}
	if body := strings.TrimSpace(get(t, s, "/api/history").Body.String()); body != `{"days":[]}` {
		t.Errorf("body = %s", body)
	}
}

// A day's tile is the same bytes for as long as the day is held, so the
// browser never has to ask for it twice.
func TestHistory_ServesADaysTileAndLetsTheBrowserKeepIt(t *testing.T) {
	rec := get(t, withHistory(t), "/api/history/tiles/2026-10-12/overworld/0/37/-4.webp")
	if rec.Code != http.StatusOK || rec.Body.String() != "RIFFas-it-was" {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/webp" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Errorf("cache control = %q", got)
	}
}

func TestHistory_RefusesWhatIsNotATileOfADayHeld(t *testing.T) {
	s := withHistory(t)
	for path, want := range map[string]int{
		"/api/history/tiles/2026-10-12/overworld/0/37/-5.webp":      http.StatusNotFound,
		"/api/history/tiles/2026-10-11/overworld/0/37/-4.webp":      http.StatusNotFound,
		"/api/history/tiles/2026-10-12/moon/0/37/-4.webp":           http.StatusNotFound,
		"/api/history/tiles/2026-10-12/overworld/0/37/-4.png":       http.StatusBadRequest,
		"/api/history/tiles/2026-10-12/overworld/0/x/-4.webp":       http.StatusBadRequest,
		"/api/history/tiles/2026-10-12/overworld/99/37/-4.webp":     http.StatusBadRequest,
		"/api/history/tiles/2026-10-12/overworld/0/99999999/0.webp": http.StatusBadRequest,
	} {
		if got := get(t, s, path).Code; got != want {
			t.Errorf("%s = %d, want %d", path, got, want)
		}
	}
}

// A day's markers are the answer the map gave for them that day, byte for
// byte, and never change, so the browser never has to ask for them twice.
func TestHistory_ServesADaysMarkersAndLetsTheBrowserKeepThem(t *testing.T) {
	rec := get(t, withHistory(t), "/api/history/markers/2026-10-12?dimension=overworld")
	if rec.Code != http.StatusOK || rec.Body.String() != markersThen {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Errorf("cache control = %q", got)
	}
}

func TestHistory_RefusesMarkersOfWhatIsNotADimensionOfADayHeld(t *testing.T) {
	s := withHistory(t)
	for path, want := range map[string]int{
		"/api/history/markers/2026-10-12?dimension=nether":            http.StatusNotFound,
		"/api/history/markers/2026-10-11?dimension=overworld":         http.StatusNotFound,
		"/api/history/markers/..%2Fdays%2F2026-10-12?dimension=end":   http.StatusNotFound,
		"/api/history/markers/2026-10-12?dimension=moon":              http.StatusBadRequest,
		"/api/history/markers/2026-10-12?dimension=..%2Foverworld":    http.StatusBadRequest,
		"/api/history/markers/2026-10-12":                             http.StatusBadRequest,
		"/api/history/markers/2026-10-12/overworld":                   http.StatusNotFound,
		"/api/history/markers/":                                       http.StatusNotFound,
		"/api/history/markers/2026-10-12.json.gz?dimension=overworld": http.StatusNotFound,
	} {
		rec := get(t, s, path)
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
		if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
			t.Errorf("%s: a refusal may be kept for good: %q", path, got)
		}
	}
}

// Where a base was a month ago is as much the players' business alone as
// where it is now.
func TestHistory_NeedsASession(t *testing.T) {
	s := withHistory(t)
	login := withLogin(t)
	s.Sessions, s.Codes, s.InternalToken = login.Sessions, login.Codes, login.InternalToken
	h := s.Handler()
	for _, path := range []string{"/api/history", "/api/history/tiles/2026-10-12/overworld/0/37/-4.webp", "/api/history/markers/2026-10-12?dimension=overworld"} {
		if rec := do(h, "GET", path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
	}
}

func TestHistory_WithoutHistoryThereIsNothingToAskFor(t *testing.T) {
	s, _ := fixture(t)
	for _, path := range []string{"/api/history", "/api/history/markers/2026-10-12?dimension=overworld"} {
		if code := get(t, s, path).Code; code != http.StatusNotFound {
			t.Errorf("%s without history = %d", path, code)
		}
	}
}
