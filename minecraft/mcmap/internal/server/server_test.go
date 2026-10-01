package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
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

var renderedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// fixture is a maps directory where only the overworld has been rendered,
// with one tile at zoom 0 (37,-4) — negative on one axis, since that is
// where the directory bucketing is easy to get wrong.
func fixture(t *testing.T) (*Server, string) {
	t.Helper()
	maps := t.TempDir()
	r := &render.Unmined{}
	ow := filepath.Join(maps, "overworld")
	if err := os.MkdirAll(ow, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ow, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
		t.Fatal(err)
	}
	tile := r.TilePath(ow, "webp", 0, 37, -4)
	if err := os.MkdirAll(filepath.Dir(tile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tile, []byte("RIFFwebp-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := &worker.Status{}
	status.MarkRendered("overworld", renderedAt)
	return &Server{
		Renderer: r,
		MapsDir:  maps,
		World:    "FWB",
		Status:   status,
		Refresh:  15 * time.Minute,
		Static:   fstest.MapFS{"index.html": {Data: []byte("<html>map</html>")}},
	}, maps
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestMapAPI_DescribesRenderedAndPendingDimensions(t *testing.T) {
	s, _ := fixture(t)
	rec := get(t, s, "/api/map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		World          string `json:"world"`
		RefreshSeconds int    `json:"refreshSeconds"`
		Dimensions     []struct {
			ID         string     `json:"id"`
			Rendered   bool       `json:"rendered"`
			RenderedAt *time.Time `json:"renderedAt"`
			MinZoom    int        `json:"minZoom"`
			MaxRegionX int        `json:"maxRegionX"`
			Format     string     `json:"format"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if got.World != "FWB" || got.RefreshSeconds != 900 || len(got.Dimensions) != 3 {
		t.Fatalf("got %+v", got)
	}
	ow, nether := got.Dimensions[0], got.Dimensions[1]
	if ow.ID != "overworld" || !ow.Rendered || ow.MinZoom != -6 || ow.MaxRegionX != 21 || ow.Format != "webp" ||
		ow.RenderedAt == nil || !ow.RenderedAt.Equal(renderedAt) {
		t.Errorf("overworld = %+v", ow)
	}
	// A dimension still waiting for its first render is listed, so the page
	// can say so, but carries no extent to draw.
	if nether.ID != "nether" || nether.Rendered || nether.RenderedAt != nil {
		t.Errorf("nether = %+v", nether)
	}
}

func TestTiles(t *testing.T) {
	s, _ := fixture(t)

	rec := get(t, s, "/tiles/overworld/0/37/-4.webp")
	if rec.Code != http.StatusOK || rec.Body.String() != "RIFFwebp-bytes" {
		t.Fatalf("status = %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Last-Modified") == "" || rec.Header().Get("Cache-Control") == "" {
		t.Errorf("headers = %v: tiles must be cacheable and revalidatable", rec.Header())
	}

	for path, want := range map[string]int{
		"/tiles/overworld/0/38/-4.webp":      http.StatusNotFound, // not generated
		"/tiles/nether/0/0/0.webp":           http.StatusNotFound, // dimension not rendered yet
		"/tiles/aether/0/0/0.webp":           http.StatusNotFound,
		"/tiles/overworld/0/x/0.webp":        http.StatusBadRequest,
		"/tiles/overworld/zero/0/0.webp":     http.StatusBadRequest,
		"/tiles/overworld/0/37/-4.png":       http.StatusBadRequest,
		"/tiles/overworld/0/99999999/0.webp": http.StatusBadRequest,
		"/tiles/overworld/-99/0/0.webp":      http.StatusBadRequest,
		"/tiles/overworld/0/37/..%2f..%2fx":  http.StatusBadRequest,
		"/tiles/..%2foverworld/0/37/-4.webp": http.StatusNotFound,
	} {
		if rec := get(t, s, path); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestTiles_RevalidationSendsNoBody(t *testing.T) {
	s, _ := fixture(t)
	first := get(t, s, "/tiles/overworld/0/37/-4.webp")

	req := httptest.NewRequest("GET", "/tiles/overworld/0/37/-4.webp", nil)
	req.Header.Set("If-Modified-Since", first.Header().Get("Last-Modified"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("revalidation = %d with %d body bytes, want 304 and none", rec.Code, rec.Body.Len())
	}
}

func TestStaticAndHealth(t *testing.T) {
	s, _ := fixture(t)
	if rec := get(t, s, "/"); rec.Code != http.StatusOK || rec.Body.String() != "<html>map</html>" {
		t.Errorf("GET / = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(t, s, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("GET /healthz = %d", rec.Code)
	}

}

// The page will be reachable from the internet. It loads nothing from
// anywhere else, so the policy can say exactly that.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	s, _ := fixture(t)
	for _, path := range []string{"/", "/api/map", "/tiles/overworld/0/37/-4.webp", "/tiles/overworld/0/38/-4.webp"} {
		h := get(t, s, path).Header()
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", path, csp)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers = %v", path, h)
		}
	}
}

// What went wrong is for the logs. The page only needs to know that
// something did, and the detail names internal addresses and paths.
func TestMapAPI_ReportsThatSomethingIsWrongWithoutSayingWhat(t *testing.T) {
	s, _ := fixture(t)
	s.Status.MarkProblem("snapshot")
	body := get(t, s, "/api/map").Body.String()
	if !strings.Contains(body, `"problem":"snapshot"`) {
		t.Errorf("body = %s", body)
	}
	for _, leak := range []string{"http://", "svc.cluster.local", "/data"} {
		if strings.Contains(body, leak) {
			t.Errorf("body leaks %q: %s", leak, body)
		}
	}
}

func TestStatic_DirectoriesAreNotListed(t *testing.T) {
	s, _ := fixture(t)
	s.Static = fstest.MapFS{
		"index.html":             {Data: []byte("<html>map</html>")},
		"lib/leaflet/leaflet.js": {Data: []byte("js")},
	}
	for _, path := range []string{"/lib/", "/lib/leaflet/"} {
		if rec := get(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d (%q), want 404", path, rec.Code, rec.Body.String())
		}
	}
	if rec := get(t, s, "/lib/leaflet/leaflet.js"); rec.Code != http.StatusOK {
		t.Errorf("GET a file under it = %d", rec.Code)
	}
}
