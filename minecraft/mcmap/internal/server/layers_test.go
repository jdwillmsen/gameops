package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// fakeLayers answers as the drawing of a tile would, and says what it was
// asked for.
type fakeLayers struct {
	mu      sync.Mutex
	version string
	err     error
	empty   bool
	asked   []layers.Spec
	zooms   []int
}

func (f *fakeLayers) Version() string { return f.version }

func (f *fakeLayers) Tile(_ context.Context, spec layers.Spec, zoom, x, y int) (layers.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked, f.zooms = append(f.asked, spec), append(f.zooms, zoom)
	if f.err != nil {
		return layers.Result{}, f.err
	}
	if f.empty {
		return layers.Result{}, nil
	}
	return layers.Result{Data: []byte("\x89PNG tile"), Drawn: true, Timings: layers.Timings{Read: 40 * time.Millisecond, Paint: 5 * time.Millisecond, Encode: time.Millisecond}}, nil
}

func withLayers(t *testing.T) (*Server, *fakeLayers) {
	t.Helper()
	s := withLogin(t)
	f := &fakeLayers{version: "v1"}
	s.Layers = f
	s.NetherTop = 100
	s.NetherLevels = []render.NetherLevel{{TopY: 66, Label: "Mid"}, {TopY: 34, Label: "Low"}}
	return s, f
}

type layersListing struct {
	Offered bool   `json:"offered"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
	Tiles   struct {
		MinZoom int `json:"minZoom"`
		MaxZoom int `json:"maxZoom"`
		Size    int `json:"size"`
	} `json:"tiles"`
	Styles []struct {
		ID, Label, Says string
	} `json:"styles"`
	Levels []struct {
		ID    string `json:"id"`
		Y     int    `json:"y"`
		Label string `json:"label"`
		Band  int    `json:"band"`
	} `json:"levels"`
	Bands []int `json:"bands"`
}

func TestLayerRoutesNeedASessionAndAreLeftOutWithoutASource(t *testing.T) {
	s, _ := withLayers(t)
	h := s.Handler()
	paths := []string{"/api/layers?dimension=overworld", "/api/layers/tiles/overworld/0/0/0.png?level=0&style=floor"}
	for _, path := range paths {
		if rec := do(h, "GET", path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no session = %d, want 401", path, rec.Code)
		}
		if rec := do(h, "GET", path, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200: %s", path, rec.Code, rec.Body)
		}
	}
	off, _ := fixture(t)
	for _, path := range paths {
		if rec := get(t, off, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with the feature off = %d, want 404", path, rec.Code)
		}
	}
}

func TestLayerListingSaysWhatEachDimensionIsOfferedAt(t *testing.T) {
	s, f := withLayers(t)
	got := decodeBody[layersListing](t, fetch(t, s, "/api/layers?dimension=overworld"))
	if !got.Offered || !got.Ready || got.Version != "v1" || got.Tiles.Size != 256 || got.Tiles.MinZoom != -1 || got.Tiles.MaxZoom != 0 {
		t.Errorf("overworld: %+v", got)
	}
	var ids []string
	for _, l := range got.Levels {
		ids = append(ids, l.ID)
		if l.Band != 16 {
			t.Errorf("the overworld's level %s looks at %d blocks", l.ID, l.Band)
		}
	}
	if strings.Join(ids, ",") != "62,40,16,0,-16,-32,-48" || len(got.Bands) != 3 || len(got.Styles) != 2 || got.Styles[0].ID != "floor" {
		t.Errorf("levels %v, bands %v, styles %v", ids, got.Bands, got.Styles)
	}

	nether := decodeBody[layersListing](t, fetch(t, s, "/api/layers?dimension=nether"))
	if len(nether.Levels) != 3 || nether.Levels[0].ID != "100" || nether.Levels[0].Label != render.DefaultNetherLabel || nether.Levels[1].Label != "Mid" || nether.Levels[2].Y != 34 || nether.Levels[0].Band != 0 || len(nether.Bands) != 0 {
		t.Errorf("nether: %+v", nether)
	}
	end := decodeBody[layersListing](t, fetch(t, s, "/api/layers?dimension=end"))
	if len(end.Levels) != 1 || end.Levels[0].Y != 255 {
		t.Errorf("end: %+v", end)
	}

	f.version = ""
	if before := decodeBody[layersListing](t, fetch(t, s, "/api/layers?dimension=overworld")); before.Ready || before.Version != "" || !before.Offered {
		t.Errorf("before the world is open: %+v", before)
	}
	if rec := fetch(t, s, "/api/layers?dimension=moon"); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown dimension = %d", rec.Code)
	}
}

func TestLayerTileIsDrawnForTheCutAskedForAndNoOther(t *testing.T) {
	s, f := withLayers(t)
	rec := fetch(t, s, "/api/layers/tiles/overworld/-1/3/-4.png?level=-16&style=cut&v=v1")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if got := rec.Header().Get("Server-Timing"); !strings.Contains(got, "read;dur=40.0") {
		t.Errorf("a drawn tile says where the time went: %q", got)
	}
	want := layers.Spec{Dim: chunks.Overworld, Cut: -16, Thickness: 16, Style: layers.Cut}
	if len(f.asked) != 1 || f.asked[0] != want || f.zooms[0] != -1 {
		t.Errorf("asked for %+v at %v, want %+v", f.asked, f.zooms, want)
	}

	// The nether's levels look at everything under the cut, and the cut is
	// one of those configured.
	fetch(t, s, "/api/layers/tiles/nether/0/0/0.png?level=66&style=floor")
	if got := f.asked[1]; got.Cut != 66 || got.Thickness != 67 || got.Style != layers.Floor || got.Dim != chunks.Nether {
		t.Errorf("nether: %+v", got)
	}
	// An explicit band is one of the few there are, on a level that has one.
	fetch(t, s, "/api/layers/tiles/overworld/0/0/0.png?level=0&style=floor&band=64")
	if got := f.asked[2]; got.Thickness != 64 || got.Cut != 0 {
		t.Errorf("band 64: %+v", got)
	}
	fetch(t, s, "/api/layers/tiles/overworld/0/0/0.png?level=0&style=floor&band=all")
	if got := f.asked[3]; got.Thickness != 65 {
		t.Errorf("band all: %+v", got)
	}
}

func TestLayerTileRefusesWhatItWasNotOffered(t *testing.T) {
	s, f := withLayers(t)
	for name, path := range map[string]string{
		"a level the overworld does not have": "/api/layers/tiles/overworld/0/0/0.png?level=63&style=floor",
		"the nether's level not configured":   "/api/layers/tiles/nether/0/0/0.png?level=50&style=floor",
		"no level":                            "/api/layers/tiles/overworld/0/0/0.png?style=floor",
		"a level that is not a number":        "/api/layers/tiles/overworld/0/0/0.png?level=abc&style=floor",
		"no style":                            "/api/layers/tiles/overworld/0/0/0.png?level=0",
		"a style there is not":                "/api/layers/tiles/overworld/0/0/0.png?level=0&style=xray",
		"a band that is not one of the few":   "/api/layers/tiles/overworld/0/0/0.png?level=0&style=cut&band=17",
		"a band on a level that has none":     "/api/layers/tiles/nether/0/0/0.png?level=100&style=cut&band=16",
		"a zoom past the finest":              "/api/layers/tiles/overworld/1/0/0.png?level=0&style=cut",
		"a zoom past the widest":              "/api/layers/tiles/overworld/-2/0/0.png?level=0&style=cut",
		"the wrong extension":                 "/api/layers/tiles/overworld/0/0/0.webp?level=0&style=cut",
		"a tile past the world":               "/api/layers/tiles/overworld/0/99999999/0.png?level=0&style=cut",
	} {
		if rec := fetch(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rec.Code)
		}
	}
	if rec := fetch(t, s, "/api/layers/tiles/moon/0/0/0.png?level=0&style=cut"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown dimension = %d, want 404", rec.Code)
	}
	if len(f.asked) != 0 {
		t.Errorf("a refused request was drawn: %+v", f.asked)
	}
}

func TestLayerTileIsKeptForever_ByItsVersion(t *testing.T) {
	s, f := withLayers(t)
	url := "/api/layers/tiles/overworld/0/0/0.png?level=0&style=floor"
	versioned := fetch(t, s, url+"&v=v1")
	if cc := versioned.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
		t.Errorf("a tile asked for by its version: %q", cc)
	}
	unversioned := fetch(t, s, url)
	if cc := unversioned.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || !strings.Contains(cc, "no-cache") {
		t.Errorf("a tile asked for with no version: %q", cc)
	}
	etag := unversioned.Header().Get("ETag")
	if etag == "" || etag == fetch(t, s, "/api/layers/tiles/overworld/0/0/0.png?level=0&style=cut").Header().Get("ETag") {
		t.Errorf("etag %q is not of the style", etag)
	}
	drawn := len(f.asked)
	again := do(s.Handler(), "GET", url, "", []*http.Cookie{session(s, steve)}, "If-None-Match", etag)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || len(f.asked) != drawn {
		t.Errorf("a revalidation was %d with %d bytes, and drew %d more", again.Code, again.Body.Len(), len(f.asked)-drawn)
	}
	f.version = "v2"
	if stale := do(s.Handler(), "GET", url, "", []*http.Cookie{session(s, steve)}, "If-None-Match", etag); stale.Code != http.StatusOK {
		t.Errorf("a new version was answered %d from the old etag", stale.Code)
	}
}

func TestLayerTileSaysWhyItHasNone(t *testing.T) {
	s, f := withLayers(t)
	url := "/api/layers/tiles/overworld/0/0/0.png?level=0&style=floor"
	f.empty = true
	if rec := fetch(t, s, url); rec.Code != http.StatusNotFound || rec.Header().Get("ETag") != "" || !strings.Contains(rec.Header().Get("Cache-Control"), "max-age=60") {
		t.Errorf("nothing in the band: %d %v", rec.Code, rec.Header())
	}
	f.empty = false
	for _, c := range []struct {
		err  error
		want int
	}{
		{layers.ErrNotReady, http.StatusServiceUnavailable},
		{context.DeadlineExceeded, http.StatusServiceUnavailable},
		{errors.New("disk"), http.StatusInternalServerError},
	} {
		f.err = c.err
		rec := fetch(t, s, url)
		if rec.Code != c.want || rec.Header().Get("ETag") != "" || strings.Contains(rec.Body.String(), "disk") {
			t.Errorf("%v: %d %q", c.err, rec.Code, rec.Body)
		}
		if c.want == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
			t.Errorf("%v: no Retry-After", c.err)
		}
	}
	f.err = nil
	f.version = ""
	if rec := fetch(t, s, url); rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("before the world is open: %d", rec.Code)
	}
	// A viewer who has gone is not answered and not logged as a failure.
	f.version, f.err = "v1", context.Canceled
	req := httptest.NewRequest("GET", url, nil)
	req.AddCookie(session(s, steve))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("a cancelled request was answered with %q", rec.Body)
	}
}
