package server

import (
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// missingCensus is a chunk census that knows which chunks are gone.
type missingCensus struct {
	at      time.Time
	counted bool
	gone    chunks.Set
	asked   int
}

func (m *missingCensus) Last() (chunks.Report, bool) {
	missing := map[chunks.Dimension]int{}
	for p := range m.gone {
		missing[p.Dim]++
	}
	return chunks.Report{At: m.at, Missing: missing}, m.counted
}
func (m *missingCensus) Acknowledge(time.Time) error { return nil }
func (m *missingCensus) Missing() chunks.Set {
	m.asked++
	return m.gone
}

func withLost(t *testing.T) (*Server, *missingCensus) {
	t.Helper()
	s := withLogin(t)
	c := &missingCensus{at: time.Unix(1_700_000_000, 0), counted: true, gone: chunks.Set{
		{Dim: chunks.Overworld, X: 1, Z: 2}:    {},
		{Dim: chunks.Overworld, X: 17, Z: 2}:   {},
		{Dim: chunks.Overworld, X: -1, Z: -1}:  {},
		{Dim: chunks.Nether, X: 40, Z: 40}:     {},
		{Dim: chunks.Overworld, X: 900, Z: 90}: {},
	}}
	s.Chunks = c
	// The lost chunks are part of the feature the flag turns on.
	s.Layers = &fakeLayers{version: "v1"}
	return s, c
}

func TestLostListingSaysWhatIsMissingInEachDimension(t *testing.T) {
	s, c := withLost(t)
	got := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld"))
	if !got.Offered || got.Missing != 4 || got.Version == "" || got.Tiles.Size != 256 || got.Tiles.MinZoom != lostMinZoom {
		t.Errorf("overworld: %+v", got)
	}
	if end := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=end")); !end.Offered || end.Missing != 0 {
		t.Errorf("end: %+v", end)
	}
	// The set is indexed once for a count, not for each request.
	fetch(t, s, "/api/lost?dimension=nether")
	fetch(t, s, "/api/lost/tiles/overworld/0/0/0.png")
	if c.asked != 1 {
		t.Errorf("the missing chunks were read %d times", c.asked)
	}
	// A newer count is read again.
	c.at = c.at.Add(time.Minute)
	if again := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld")); again.Version == got.Version || c.asked != 2 {
		t.Errorf("after a newer count: version %q, read %d times", again.Version, c.asked)
	}
	if rec := fetch(t, s, "/api/lost?dimension=moon"); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown dimension = %d", rec.Code)
	}
}

func TestLostIsNotOfferedBeforeACountOrWithACensusThatKeepsNoRecord(t *testing.T) {
	s, c := withLost(t)
	c.counted = false
	if got := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld")); got.Offered {
		t.Errorf("before a count: %+v", got)
	}
	if rec := fetch(t, s, "/api/lost/tiles/overworld/0/0/0.png"); rec.Code != http.StatusNotFound {
		t.Errorf("a tile before a count = %d", rec.Code)
	}
	s.Chunks = damaged()
	if got := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld")); got.Offered {
		t.Errorf("with a census that cannot say: %+v", got)
	}
}

func TestLostRoutesNeedASession(t *testing.T) {
	s, _ := withLost(t)
	for _, path := range []string{"/api/lost?dimension=overworld", "/api/lost/tiles/overworld/0/0/0.png"} {
		if rec := do(s.Handler(), "GET", path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no session = %d", path, rec.Code)
		}
	}
}

func lostPixels(t *testing.T, s *Server, path string) map[color.NRGBA]int {
	t.Helper()
	rec := fetch(t, s, path)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	img, err := png.Decode(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	out := map[color.NRGBA]int{}
	for y := range 256 {
		for x := range 256 {
			if c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA); c.A != 0 {
				out[c]++
			}
		}
	}
	return out
}

func TestLostTileMarksTheChunksThatAreMissingAndNothingElse(t *testing.T) {
	s, _ := withLost(t)
	// At a block to a pixel a chunk is 16 by 16 pixels, and tile 0, 0 has
	// the chunk at 1, 2 and no other; the hatch is a minority of it.
	marks := lostPixels(t, s, "/api/lost/tiles/overworld/0/0/0.png")
	total := 0
	for _, n := range marks {
		total += n
	}
	if total != 256 || marks[lostFill] == 0 || marks[lostHatch] == 0 || marks[lostHatch] > marks[lostFill] {
		t.Errorf("one chunk drawn as %v", marks)
	}
	// The chunk beside it, at 17, 2, is in the tile east of that one.
	if east := lostPixels(t, s, "/api/lost/tiles/overworld/0/1/0.png"); len(east) == 0 {
		t.Error("the chunk at 17, 2 is not marked")
	}
	// Chunk -1, -1 is in the tile north-west of the origin and at its
	// last chunk.
	if nw := lostPixels(t, s, "/api/lost/tiles/overworld/0/-1/-1.png"); nw[lostFill]+nw[lostHatch] != 256 {
		t.Errorf("chunk -1, -1 drawn as %v", nw)
	}
	for name, path := range map[string]string{
		"a tile with none in it":           "/api/lost/tiles/overworld/0/5/5.png",
		"a dimension with none of its own": "/api/lost/tiles/end/0/0/0.png",
	} {
		if rec := fetch(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
}

func TestLostTileAtWideZoomsMarksAChunkWithAPixelAtLeast(t *testing.T) {
	s, _ := withLost(t)
	// At zoom -3 a pixel is 8 blocks and a chunk is two pixels.
	two := lostPixels(t, s, "/api/lost/tiles/overworld/-3/0/0.png")
	if two[lostFill] != 8 || len(two) != 1 {
		t.Errorf("two chunks of two pixels each at zoom -3: %v", two)
	}
	// At -5 a chunk is a pixel, kept as a mark and not rounded to nothing.
	wide := lostPixels(t, s, "/api/lost/tiles/overworld/-5/0/0.png")
	if wide[lostFill]+wide[lostHatch] == 0 {
		t.Error("a chunk was lost at the widest zoom")
	}
	// And in a close view it is a large square of its own.
	close := lostPixels(t, s, "/api/lost/tiles/overworld/4/1/2.png")
	if close[lostFill]+close[lostHatch] != 256*256 {
		t.Errorf("at the finest zoom the chunk fills the tile: %d pixels", close[lostFill]+close[lostHatch])
	}
}

func TestLostTileIsKeptByTheCountItWasDrawnFrom(t *testing.T) {
	s, c := withLost(t)
	url := "/api/lost/tiles/overworld/0/0/0.png"
	versioned := fetch(t, s, url+"?v="+lostVersion(s.lost()))
	if cc := versioned.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("by its version: %q", cc)
	}
	etag := fetch(t, s, url).Header().Get("ETag")
	if rec := do(s.Handler(), "GET", url, "", []*http.Cookie{session(s, steve)}, "If-None-Match", etag); rec.Code != http.StatusNotModified {
		t.Errorf("a revalidation = %d", rec.Code)
	}
	c.at = c.at.Add(time.Hour)
	if rec := do(s.Handler(), "GET", url, "", []*http.Cookie{session(s, steve)}, "If-None-Match", etag); rec.Code != http.StatusOK {
		t.Errorf("a tile of a newer count from the old etag = %d", rec.Code)
	}
}

func TestLostTileRefusesABadAddress(t *testing.T) {
	s, _ := withLost(t)
	for _, path := range []string{
		"/api/lost/tiles/overworld/5/0/0.png", "/api/lost/tiles/overworld/-6/0/0.png", "/api/lost/tiles/overworld/0/0/0.webp",
		"/api/lost/tiles/overworld/0/x/0.png", "/api/lost/tiles/overworld/0/99999999/0.png",
	} {
		if rec := fetch(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
	if rec := fetch(t, s, "/api/lost/tiles/moon/0/0/0.png"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown dimension = %d", rec.Code)
	}
}

func TestLostRoutesAreLeftOutWhereTheViewsAreOff(t *testing.T) {
	s, _ := withLost(t)
	s.Layers = nil
	for _, path := range []string{"/api/lost?dimension=overworld", "/api/lost/tiles/overworld/0/0/0.png"} {
		if rec := fetch(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with LAYERS_ENABLED off = %d, want 404", path, rec.Code)
		}
	}
}

// An acknowledgement forgets what was missing without the count's time
// changing; what the map marks has to follow it.
func TestLostFollowsAnAcknowledgementThatLeavesTheCountsTimeAlone(t *testing.T) {
	s, c := withLost(t)
	before := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld"))
	url := "/api/lost/tiles/overworld/0/0/0.png"
	if rec := fetch(t, s, url); rec.Code != http.StatusOK {
		t.Fatalf("a tile with a chunk missing = %d", rec.Code)
	}
	etag := fetch(t, s, url).Header().Get("ETag")
	c.gone = chunks.Set{}
	after := decodeBody[lostJSON](t, fetch(t, s, "/api/lost?dimension=overworld"))
	if after.Missing != 0 || after.Version == before.Version {
		t.Errorf("after the acknowledgement: %d missing, version %q was %q", after.Missing, after.Version, before.Version)
	}
	if rec := fetch(t, s, url); rec.Code != http.StatusNotFound {
		t.Errorf("a tile after the acknowledgement = %d, want 404", rec.Code)
	}
	if rec := do(s.Handler(), "GET", url, "", []*http.Cookie{session(s, steve)}, "If-None-Match", etag); rec.Code == http.StatusNotModified {
		t.Error("the old etag still matches")
	}
}
