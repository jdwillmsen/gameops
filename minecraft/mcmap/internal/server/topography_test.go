package server

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/topo"
)

// withLand is biomeWorld with heights: the overworld's first four by four
// chunks fall away to the north-west a block in four, so that they are lit
// by the light there is; the end's first chunk is an island with a gap of
// void beside it; and the nether's is its roof.
func withLand(t *testing.T) *Server {
	t.Helper()
	s := withLogin(t)
	b := heights.NewBuilder(biomesAt, 4)
	add := func(d chunks.Dimension, cx, cz int32, f func(x, z int) int16) {
		var cols [heights.Columns]int16
		for i := range cols {
			cols[i] = f(i%16+int(cx)*16, i/16+int(cz)*16)
		}
		if err := b.AddColumns(d, cx, cz, &cols); err != nil {
			t.Fatal(err)
		}
	}
	for cz := range int32(4) {
		for cx := range int32(4) {
			add(chunks.Overworld, cx, cz, func(x, z int) int16 { return int16(130 + (x+z)/4) })
		}
	}
	add(chunks.End, 0, 0, func(x, z int) int16 {
		if x > 8 {
			return 0
		}
		return 60
	})
	add(chunks.Nether, 0, 0, func(x, z int) int16 { return 128 })
	store := &biomes.Store{}
	store.Set(biomeWorld().WithHeights(b.Map()))
	s.Biomes = store
	return s
}

// fetch asks as a logged-in viewer does.
func fetch(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	return do(s.Handler(), "GET", path, "", []*http.Cookie{session(s, steve)})
}

const (
	tile       = "/api/topography/tiles/overworld/0/0/0.png"
	allStyles  = "?styles=hillshade,contours,tint"
	listingURL = "/api/topography?dimension=overworld"
)

func TestTopographyRoutesNeedASession(t *testing.T) {
	s := withLand(t)
	h := s.Handler()
	forged := &http.Cookie{Name: "__Host-mcmap_session", Value: "e30.nope"}
	for _, path := range []string{listingURL, tile + allStyles, tile + "?styles=tint&hide=1&v=x"} {
		for name, cookies := range map[string][]*http.Cookie{"no session": nil, "a forged session": {forged}} {
			rec := do(h, "GET", path, "", cookies)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with %s = %d, want 401", path, name, rec.Code)
			}
			if body := rec.Body.String(); strings.Contains(body, "PNG") || strings.Contains(body, "bands") || strings.Contains(body, "version") {
				t.Errorf("GET %s with %s: the refusal carries something of the overlay: %s", path, name, body)
			}
		}
		if rec := do(h, "GET", path, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200: %s", path, rec.Code, rec.Body)
		}
	}
}

func TestTopographyRoutesAreLeftOutWithoutASource(t *testing.T) {
	s, _ := fixture(t)
	for _, path := range []string{listingURL, tile + allStyles} {
		if rec := get(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

type topographyListing struct {
	Offered bool   `json:"offered"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
	Tiles   struct {
		MinZoom int `json:"minZoom"`
		MaxZoom int `json:"maxZoom"`
		Size    int `json:"size"`
	} `json:"tiles"`
	Intervals  []int `json:"intervals"`
	HeavyEvery int   `json:"heavyEvery"`
	Bands      []struct {
		From   int    `json:"from"`
		To     int    `json:"to"`
		Colour string `json:"colour"`
	} `json:"bands"`
}

func TestTopographyListingSaysWhatEachDimensionHas(t *testing.T) {
	s := withLand(t)
	for _, c := range []struct {
		dimension string
		offered   bool
	}{{"overworld", true}, {"end", true}, {"nether", false}} {
		got := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension="+c.dimension))
		if got.Offered != c.offered || got.Ready != c.offered {
			t.Errorf("%s: offered %v and ready %v, want %v", c.dimension, got.Offered, got.Ready, c.offered)
		}
		if got.Tiles.Size != 256 || got.Tiles.MinZoom != topo.MinZoom || got.Tiles.MaxZoom != topo.MaxZoom {
			t.Errorf("%s: tiles %+v", c.dimension, got.Tiles)
		}
		if !c.offered {
			if len(got.Bands) != 0 || len(got.Intervals) != 0 || got.Version != "" {
				t.Errorf("%s says something of what it does not draw: %+v", c.dimension, got)
			}
			continue
		}
		if got.Version == "" || got.HeavyEvery != 5 || len(got.Intervals) != 1-topo.MinZoom || got.Intervals[0] != 5 {
			t.Errorf("%s: %+v", c.dimension, got)
		}
		if len(got.Bands) < 4 {
			t.Fatalf("%s: %d bands", c.dimension, len(got.Bands))
		}
		for i, b := range got.Bands {
			// The band that is not drawn has no colour to say.
			if (len(b.Colour) != 7 || b.Colour[0] != '#') && b.Colour != "" || (i > 0 && b.From != got.Bands[i-1].To+1) {
				t.Errorf("%s: band %d is %+v", c.dimension, i, b)
			}
		}
	}
	if rec := fetch(t, s, "/api/topography?dimension=aether"); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown dimension = %d, want 400", rec.Code)
	}
	if rec := fetch(t, s, "/api/topography"); rec.Code != http.StatusBadRequest {
		t.Errorf("no dimension = %d, want 400", rec.Code)
	}
}

func TestTopographyIsNotReadyBeforeTheHeightsAre(t *testing.T) {
	empty := withLogin(t)
	empty.Biomes = &biomes.Store{}
	unread := withLogin(t)
	unread.Biomes = &biomes.Store{}
	unread.Biomes.(*biomes.Store).Set(biomeWorld())
	for name, s := range map[string]*Server{"a reading without heights": unread, "no reading": empty} {
		got := decodeBody[topographyListing](t, fetch(t, s, listingURL))
		if !got.Offered || got.Ready || got.Version != "" {
			t.Errorf("%s: %+v", name, got)
		}
		rec := fetch(t, s, tile+allStyles)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "private, max-age=60" {
			t.Errorf("%s: a tile is %d with Cache-Control %q, want a 404 kept for a minute", name, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

func pixel(t *testing.T, rec *httptest.ResponseRecorder, x, y int) color.NRGBA {
	t.Helper()
	return tilePixel(t, rec, x, y)
}

func TestTopographyTilesAreDrawnWhereTheTerrainTilesAre(t *testing.T) {
	s := withLand(t)
	// The ground falls to the north-west a quarter of a block a block: it
	// faces the light, which is the first thing of a hillshade.
	shade := pixel(t, fetch(t, s, tile+"?styles=hillshade"), 32, 32)
	if shade.A == 0 || shade.R < 200 {
		t.Errorf("the slope facing the light is %v", shade)
	}
	if beyond := pixel(t, fetch(t, s, tile+"?styles=hillshade"), 100, 100); beyond.A != 0 {
		t.Errorf("ground that is not generated is drawn %v", beyond)
	}
	tint := pixel(t, fetch(t, s, tile+"?styles=tint"), 32, 32)
	if tint.A == 0 {
		t.Errorf("the tint is %v", tint)
	}
	// One picture for any of the three: asking for all is one request.
	if both := pixel(t, fetch(t, s, tile+allStyles), 32, 32); both == shade || both == tint || both.A == 0 {
		t.Errorf("all three are %v, the same as one alone", both)
	}
	// Zoom -2 is four blocks to a pixel: the same ground is a sixteenth.
	if got := pixel(t, fetch(t, s, "/api/topography/tiles/overworld/-2/0/0.png?styles=tint"), 8, 8); got.A == 0 {
		t.Errorf("zoom -2 is %v where the ground is", got)
	}
}

func TestTopographyTilesDrawEveryDimensionButTheNether(t *testing.T) {
	s := withLand(t)
	for path, want := range map[string]int{
		"/api/topography/tiles/overworld/0/0/0.png?styles=tint": http.StatusOK,
		"/api/topography/tiles/end/0/0/0.png?styles=tint":       http.StatusOK,
		// The nether has a chunk of heights and is refused all the same.
		"/api/topography/tiles/nether/0/0/0.png?styles=tint": http.StatusNotFound,
		"/api/topography/tiles/aether/0/0/0.png?styles=tint": http.StatusNotFound,
		// Nothing generated there.
		"/api/topography/tiles/overworld/0/9/9.png?styles=tint": http.StatusNotFound,
		"/api/topography/tiles/end/0/1/0.png?styles=tint":       http.StatusNotFound,
	} {
		if rec := fetch(t, s, path); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
	// The island is drawn and the void beside it is not.
	end := fetch(t, s, "/api/topography/tiles/end/0/0/0.png?styles=tint")
	if pixel(t, end, 4, 4).A == 0 {
		t.Error("the island of the end is not drawn")
	}
	if got := pixel(t, end, 12, 4); got.A != 0 {
		t.Errorf("the void of the end is drawn %v", got)
	}
	// What is missing is not kept long, in case it is there at the next reading.
	missing := fetch(t, s, "/api/topography/tiles/overworld/0/9/9.png?styles=tint")
	if missing.Header().Get("Cache-Control") != "private, max-age=60" || missing.Header().Get("ETag") != "" {
		t.Errorf("a tile that is not there carries %v", missing.Header())
	}
}

func TestTopographyTilesRefuseABadAddressOrPick(t *testing.T) {
	s := withLand(t)
	base := "/api/topography/tiles/overworld/"
	for path, why := range map[string]string{
		base + "5/0/0.png?styles=tint":                           "a zoom nearer than the finest",
		base + "-6/0/0.png?styles=tint":                          "a zoom wider than the widest",
		base + "0/0/0.webp?styles=tint":                          "another format",
		base + "0/0/0?styles=tint":                               "no format",
		base + "0/1048577/0.png?styles=tint":                     "a tile far past any world",
		base + "0/a/0.png?styles=tint":                           "a tile that is not a number",
		base + "0/0/0.png":                                       "no styles",
		base + "0/0/0.png?styles=":                               "empty styles",
		base + "0/0/0.png?styles=relief":                         "a style there is not",
		base + "0/0/0.png?styles=tint,":                          "a style left empty",
		base + "0/0/0.png?styles=tint&styles=":                   "styles twice",
		base + "0/0/0.png?styles=tint&hide=a":                    "a band that is not a number",
		base + "0/0/0.png?styles=tint&hide=-1":                   "a band below the first",
		base + "0/0/0.png?styles=tint&hide=99":                   "a band past the last",
		base + "0/0/0.png?styles=tint&hide=1,":                   "a band left empty",
		base + "0/0/0.png?styles=" + strings.Repeat("tint,", 20): "styles too long",
	} {
		if rec := fetch(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s (%s) = %d, want 400", path, why, rec.Code)
		}
	}
	for path, why := range map[string]string{
		base + "0/0/0.png?styles=tint,hillshade,tint": "a style named twice",
		base + "0/0/0.png?styles=tint&hide=":          "no band hidden",
		base + "0/0/0.png?styles=tint&hide=2,2,3":     "a band named twice",
		base + "-5/0/0.png?styles=hillshade":          "the widest zoom",
		base + "4/0/0.png?styles=hillshade":           "the finest zoom",
	} {
		if rec := fetch(t, s, path); rec.Code != http.StatusOK {
			t.Errorf("GET %s (%s) = %d, want 200: %s", path, why, rec.Code, rec.Body)
		}
	}
}

func TestTopographyTilesAreKeptAsLongAsTheReadingStands(t *testing.T) {
	s := withLand(t)
	version := decodeBody[topographyListing](t, fetch(t, s, listingURL)).Version
	plain := fetch(t, s, tile+"?styles=tint")
	etag := plain.Header().Get("ETag")
	if etag == "" || plain.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("without a version: ETag %q, Cache-Control %q", etag, plain.Header().Get("Cache-Control"))
	}
	if cc := fetch(t, s, tile+"?styles=tint&v="+version).Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("with the current version: Cache-Control %q", cc)
	}
	if cc := fetch(t, s, tile+"?styles=tint&v=old").Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Errorf("with another version: Cache-Control %q", cc)
	}
	tag := func(query string) string { return fetch(t, s, tile+query).Header().Get("ETag") }
	if tag("?styles=hillshade") == etag || tag(allStyles) == etag || tag("?styles=tint&hide=1") == etag {
		t.Error("a tile drawn another way carries the plain tile's tag")
	}
	// The same pick, however it is written, is one tag; and what is hidden
	// of a tint that is not drawn is not another picture.
	if tag("?styles=contours,tint,hillshade") != tag(allStyles) || tag("?styles=tint&hide=3,1,1") != tag("?styles=tint&hide=1,3") {
		t.Error("the same pick written another way carries another tag")
	}
	if tag("?styles=hillshade&hide=2") != tag("?styles=hillshade") {
		t.Error("a band hidden from a tile with no tint makes it another picture")
	}

	req := httptest.NewRequest("GET", tile+"?styles=tint", nil)
	req.AddCookie(session(s, steve))
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("a matching tag = %d with %d bytes, want 304 and none", rec.Code, rec.Body.Len())
	}

	// A new reading is a new version.
	moved := heights.NewBuilder(biomesAt.Add(15*time.Minute), 4)
	var cols [heights.Columns]int16
	for i := range cols {
		cols[i] = 130
	}
	if err := moved.AddColumns(chunks.Overworld, 0, 0, &cols); err != nil {
		t.Fatal(err)
	}
	s.Biomes.(*biomes.Store).Set(biomeWorld().WithHeights(moved.Map()))
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == etag {
		t.Errorf("after a new reading = %d with tag %q", rec.Code, rec.Header().Get("ETag"))
	}
}

func TestAHiddenBandIsLeftOutOfTheTile(t *testing.T) {
	s := withLand(t)
	listing := decodeBody[topographyListing](t, fetch(t, s, listingURL))
	// The ground at 32, 32 is 81 high: 130 stored is 65, and the slope adds
	// a quarter of 64. It is in the band of the tint that has 81.
	band := -1
	for i, b := range listing.Bands {
		if 81 >= b.From && 81 <= b.To {
			band = i
		}
	}
	shown := pixel(t, fetch(t, s, tile+"?styles=tint"), 32, 32)
	left := pixel(t, fetch(t, s, tile+"?styles=tint&hide="+strconv.Itoa(band)), 32, 32)
	if shown.A == 0 || left.A != 0 {
		t.Errorf("the band is drawn %v, and %v with it hidden", shown, left)
	}
	if _, err := png.Decode(bytes.NewReader(fetch(t, s, tile+"?styles=tint&hide="+strconv.Itoa(band)).Body.Bytes())); err != nil {
		t.Errorf("a tile with every band hidden is not a picture: %v", err)
	}
}
