package server

import (
	"bytes"
	"encoding/json"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/trails"
)

const (
	plainsID, desertID, mushroomID, hellID = 1, 2, 14, 8
)

var biomesAt = time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC)

func chunkOf(id uint32) [biomes.Columns]uint32 {
	var c [biomes.Columns]uint32
	for i := range c {
		c[i] = id
	}
	return c
}

// biomeWorld is plains around the origin with desert to the east of block
// 8, a mushroom island far to the south-east, and a nether of one chunk:
//
//	overworld chunk 0, 0     plains west of x 8, desert from it
//	overworld chunk 1, 0     desert
//	overworld chunk 60, 60   mushroom_island (blocks 960 to 975)
//	nether chunk 0, 0        hell
func biomeWorld() *biomes.World {
	split := chunkOf(desertID)
	for i := range split {
		if i&15 < 8 {
			split[i] = plainsID
		}
	}
	return biomes.Build(biomesAt, map[chunks.Pos][biomes.Columns]uint32{
		{Dim: chunks.Overworld, X: 0, Z: 0}:   split,
		{Dim: chunks.Overworld, X: 1, Z: 0}:   chunkOf(desertID),
		{Dim: chunks.Overworld, X: 60, Z: 60}: chunkOf(mushroomID),
		{Dim: chunks.Nether, X: 0, Z: 0}:      chunkOf(hellID),
	})
}

func withBiomes(t *testing.T) *Server {
	t.Helper()
	s, _ := fixture(t)
	store := &biomes.Store{}
	store.Set(biomeWorld())
	s.Biomes = store
	return s
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return out
}

// Where the biomes are is the map, and where the players have been is more
// than the map. Every route this adds is behind the same gate as /api/map.
func TestBiomesSearchAndTrailsNeedASession(t *testing.T) {
	s, waypoints := withMarkers(t)
	store := &biomes.Store{}
	store.Set(biomeWorld())
	s.Biomes = store
	s.Structures = surveyed()
	recorder := trails.New(time.Hour, 100)
	recorder.Record("overworld", time.Now(), []live.Entity{{ID: "1", Name: "Dotablaze", X: 5, Y: 64, Z: 5}})
	s.Trails = recorder

	h := s.Handler()
	forged := &http.Cookie{Name: "__Host-mcmap_session", Value: "e30.nope"}
	paths := []string{
		"/api/biomes?dimension=overworld",
		"/api/biomes/at?dimension=overworld&x=0&z=0",
		"/api/biomes/nearest?dimension=overworld&biome=desert&x=0&z=0",
		"/api/biomes/region?dimension=overworld&x=0&z=0",
		"/api/biomes/tiles/overworld/0/0/0.png",
		"/api/biomes/tiles/overworld/0/0/0.png?biome=desert",
		"/api/search?q=a&dimension=overworld&x=0&z=0",
		"/api/trails?dimension=overworld",
	}
	for _, path := range paths {
		for name, cookies := range map[string][]*http.Cookie{"no session": nil, "a forged session": {forged}} {
			rec := do(h, "GET", path, "", cookies)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s with %s = %d, want 401", path, name, rec.Code)
			}
			body := rec.Body.String()
			for _, leak := range []string{"plains", "desert", "Dotablaze", "base", "PNG", "hits", "segments"} {
				if strings.Contains(body, leak) {
					t.Errorf("GET %s with %s: the refusal carries %q: %s", path, name, leak, body)
				}
			}
		}
		if rec := do(h, "GET", path, "", []*http.Cookie{session(s, steve)}); rec.Code != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200: %s", path, rec.Code, rec.Body)
		}
	}
	if len(waypoints.asked) != 1 || waypoints.asked[0] != steve.XUID {
		t.Errorf("the agent was asked for %v, want only the logged-in player once", waypoints.asked)
	}
}

func TestBiomeRoutesAreLeftOutWithoutASource(t *testing.T) {
	s, _ := fixture(t)
	for _, path := range []string{"/api/biomes?dimension=overworld", "/api/biomes/at?dimension=overworld&x=0&z=0", "/api/trails?dimension=overworld"} {
		rec := get(t, s, path)
		// The page's catch-all answers what no route does, and it holds no
		// such file.
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

type biomesListing struct {
	Extracted bool       `json:"extracted"`
	At        *time.Time `json:"at"`
	Version   string     `json:"version"`
	Biomes    []struct {
		ID      uint32 `json:"id"`
		Name    string `json:"name"`
		Label   string `json:"label"`
		Color   string `json:"color"`
		Known   bool   `json:"known"`
		Area    uint64 `json:"area"`
		Chunks  int    `json:"chunks"`
		Regions int    `json:"regions"`
	} `json:"biomes"`
	Tiles struct {
		MinZoom, MaxZoom, Size int
	} `json:"tiles"`
}

func TestBiomesListsWhatADimensionHolds(t *testing.T) {
	s := withBiomes(t)
	got := decodeBody[biomesListing](t, get(t, s, "/api/biomes?dimension=overworld"))
	if !got.Extracted || got.At == nil || !got.At.Equal(biomesAt) || got.Version == "" {
		t.Errorf("listing = %+v", got)
	}
	if got.Tiles.MinZoom != -12 || got.Tiles.MaxZoom != 4 || got.Tiles.Size != 256 {
		t.Errorf("tiles = %+v", got.Tiles)
	}
	if len(got.Biomes) != 3 {
		t.Fatalf("%d biomes, want 3: %+v", len(got.Biomes), got.Biomes)
	}
	desert, mushroom, plains := got.Biomes[0], got.Biomes[1], got.Biomes[2]
	if desert.Name != "desert" || desert.Area != 128+256 || desert.Chunks != 2 || desert.Regions != 1 || !desert.Known || desert.Color != "#fa9418" {
		t.Errorf("desert = %+v", desert)
	}
	if mushroom.Name != "mushroom_island" || mushroom.Label != "Mushroom Fields" || mushroom.ID != 14 || mushroom.Area != 256 {
		t.Errorf("mushroom island = %+v", mushroom)
	}
	if plains.Name != "plains" || plains.Area != 128 {
		t.Errorf("plains = %+v", plains)
	}
	nether := decodeBody[biomesListing](t, get(t, s, "/api/biomes?dimension=nether"))
	if len(nether.Biomes) != 1 || nether.Biomes[0].Name != "hell" || nether.Biomes[0].Label != "Nether Wastes" {
		t.Errorf("nether = %+v", nether.Biomes)
	}
	if end := get(t, s, "/api/biomes?dimension=end"); !strings.Contains(end.Body.String(), `"biomes":[]`) {
		t.Errorf("the end = %s, want an empty list", end.Body)
	}
	if rec := get(t, s, "/api/biomes?dimension=aether"); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown dimension = %d, want 400", rec.Code)
	}
}

func TestBiomesBeforeTheFirstReading(t *testing.T) {
	s, _ := fixture(t)
	s.Biomes = &biomes.Store{}
	if body := get(t, s, "/api/biomes?dimension=overworld").Body.String(); !strings.Contains(body, `"extracted":false`) || !strings.Contains(body, `"biomes":[]`) {
		t.Errorf("listing = %s", body)
	}
	if body := get(t, s, "/api/biomes/at?dimension=overworld&x=0&z=0").Body.String(); !strings.Contains(body, `"generated":false`) {
		t.Errorf("at = %s", body)
	}
	if body := get(t, s, "/api/biomes/nearest?dimension=overworld&biome=desert&x=0&z=0").Body.String(); !strings.Contains(body, `"hits":[]`) {
		t.Errorf("nearest = %s", body)
	}
	if body := get(t, s, "/api/biomes/region?dimension=overworld&x=0&z=0").Body.String(); !strings.Contains(body, `"found":false`) || !strings.Contains(body, `"rects":[]`) {
		t.Errorf("region = %s", body)
	}
	if rec := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png"); rec.Code != http.StatusNotFound {
		t.Errorf("tile = %d, want 404", rec.Code)
	}
	if rec := get(t, s, "/api/search?q=desert&dimension=overworld&x=0&z=0"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hits":[]`) {
		t.Errorf("search = %d %s", rec.Code, rec.Body)
	}
}

func tilePixel(t *testing.T, rec *httptest.ResponseRecorder, x, y int) color.NRGBA {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("tile = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestBiomeTilesAreDrawnWhereTheTerrainTilesAre(t *testing.T) {
	s := withBiomes(t)
	var (
		plains = color.NRGBA{0x8D, 0xB3, 0x60, 0xFF}
		desert = color.NRGBA{0xFA, 0x94, 0x18, 0xFF}
		clear  = color.NRGBA{}
	)
	rec := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png")
	for _, c := range []struct {
		x, y int
		want color.NRGBA
	}{{0, 0, plains}, {7, 15, plains}, {8, 0, desert}, {31, 15, desert}, {32, 0, clear}, {0, 16, clear}} {
		if got := tilePixel(t, rec, c.x, c.y); got != c.want {
			t.Errorf("pixel %d, %d = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	// Zoom -2 is four blocks to a pixel: the island at block 960 is pixel
	// 240 of tile 0.
	if got := tilePixel(t, get(t, s, "/api/biomes/tiles/overworld/-2/0/0.png"), 240, 240); got != (color.NRGBA{0xFF, 0x00, 0xFF, 0xFF}) {
		t.Errorf("the island at zoom -2 = %v", got)
	}
	// One biome picked out, by either of its names.
	for _, name := range []string{"desert", "Desert"} {
		rec := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png?biome="+name)
		if tilePixel(t, rec, 20, 5) != desert {
			t.Errorf("biome=%s does not keep the desert its colour", name)
		}
		if dim := tilePixel(t, rec, 2, 5); dim == plains || dim.A == 0 || dim.A == 255 {
			t.Errorf("biome=%s draws the plains as %v, want them dimmed", name, dim)
		}
	}

	for path, want := range map[string]int{
		"/api/biomes/tiles/overworld/0/9/9.png":              http.StatusNotFound,
		"/api/biomes/tiles/end/0/0/0.png":                    http.StatusNotFound,
		"/api/biomes/tiles/aether/0/0/0.png":                 http.StatusNotFound,
		"/api/biomes/tiles/overworld/0/0/0.webp":             http.StatusBadRequest,
		"/api/biomes/tiles/overworld/0/0/0":                  http.StatusBadRequest,
		"/api/biomes/tiles/overworld/5/0/0.png":              http.StatusBadRequest,
		"/api/biomes/tiles/overworld/-13/0/0.png":            http.StatusBadRequest,
		"/api/biomes/tiles/overworld/0/1048577/0.png":        http.StatusBadRequest,
		"/api/biomes/tiles/overworld/0/0/-1048577.png":       http.StatusBadRequest,
		"/api/biomes/tiles/overworld/zero/0/0.png":           http.StatusBadRequest,
		"/api/biomes/tiles/overworld/0/0/0.png?biome=narnia": http.StatusBadRequest,
		"/api/biomes/tiles/overworld/4/0/0.png":              http.StatusOK,
		// A pixel is the column under its middle, and at 4,096 blocks to a
		// pixel that is nowhere near these chunks.
		"/api/biomes/tiles/overworld/-12/0/0.png":            http.StatusNotFound,
		"/api/biomes/tiles/overworld/-3/0/0.png":             http.StatusOK,
		"/api/biomes/tiles/overworld/0/-1048576/1048576.png": http.StatusNotFound,
	} {
		if rec := get(t, s, path); rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestBiomeTilesAreKeptByTheBrowserForAsLongAsTheReadingStands(t *testing.T) {
	s := withBiomes(t)
	version := decodeBody[biomesListing](t, get(t, s, "/api/biomes?dimension=overworld")).Version

	plain := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png")
	etag := plain.Header().Get("ETag")
	if etag == "" || plain.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("without a version: ETag %q, Cache-Control %q", etag, plain.Header().Get("Cache-Control"))
	}
	pinned := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png?v="+version)
	if cc := pinned.Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("with the current version: Cache-Control %q", cc)
	}
	if stale := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png?v=old"); stale.Header().Get("Cache-Control") != "private, no-cache" {
		t.Errorf("with another version: Cache-Control %q", stale.Header().Get("Cache-Control"))
	}
	if other := get(t, s, "/api/biomes/tiles/overworld/0/0/0.png?biome=desert").Header().Get("ETag"); other == etag {
		t.Error("a tile picking a biome out carries the plain tile's tag")
	}

	req := httptest.NewRequest("GET", "/api/biomes/tiles/overworld/0/0/0.png", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("a matching tag = %d with %d bytes, want 304 and none", rec.Code, rec.Body.Len())
	}

	// A new reading is a new version, and the old tag no longer matches.
	later := biomeWorld()
	later.SnapshotAt = biomesAt.Add(15 * time.Minute)
	s.Biomes.(*biomes.Store).Set(later)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == etag {
		t.Errorf("after a new reading = %d with tag %q", rec.Code, rec.Header().Get("ETag"))
	}
}

func TestBiomeAtNamesTheBiomeOfABlock(t *testing.T) {
	s := withBiomes(t)
	type answer struct {
		Generated bool `json:"generated"`
		Biome     *struct {
			ID    uint32 `json:"id"`
			Name  string `json:"name"`
			Label string `json:"label"`
		} `json:"biome"`
	}
	for path, want := range map[string]string{
		"/api/biomes/at?dimension=overworld&x=7&z=3":        "plains",
		"/api/biomes/at?dimension=overworld&x=8&z=3":        "desert",
		"/api/biomes/at?dimension=overworld&x=7.9&z=3.2":    "plains",
		"/api/biomes/at?dimension=overworld&x=965&z=970":    "mushroom_island",
		"/api/biomes/at?dimension=nether&x=1&z=1":           "hell",
		"/api/biomes/at?dimension=overworld&x=-1&z=3":       "",
		"/api/biomes/at?dimension=overworld&x=-0.5&z=3":     "",
		"/api/biomes/at?dimension=end&x=0&z=0":              "",
		"/api/biomes/at?dimension=overworld&x=31999999&z=0": "",
	} {
		got := decodeBody[answer](t, get(t, s, path))
		switch {
		case want == "" && (got.Generated || got.Biome != nil):
			t.Errorf("GET %s = %+v, want nothing generated", path, got)
		case want != "" && (!got.Generated || got.Biome == nil || got.Biome.Name != want):
			t.Errorf("GET %s = %+v, want %s", path, got, want)
		}
	}
	for _, path := range []string{
		"/api/biomes/at?dimension=overworld&x=7",
		"/api/biomes/at?dimension=overworld&x=seven&z=3",
		"/api/biomes/at?dimension=overworld&x=NaN&z=3",
		"/api/biomes/at?dimension=overworld&x=1e300&z=3",
		"/api/biomes/at?dimension=overworld&x=32000001&z=3",
		"/api/biomes/at?x=7&z=3",
	} {
		if rec := get(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

type nearestAnswer struct {
	Biome struct {
		Name  string `json:"name"`
		Known bool   `json:"known"`
	} `json:"biome"`
	Hits []struct {
		X, Z     int32
		Distance float64
		Region   struct {
			Area                   uint64
			Chunks                 int
			MinX, MinZ, MaxX, MaxZ int32
		}
	} `json:"hits"`
	More int `json:"more"`
}

func TestBiomeNearestFindsABiomeByAnyOfItsNames(t *testing.T) {
	s := withBiomes(t)
	for _, name := range []string{"mushroom_island", "Mushroom+Fields", "mushroom%20fields", "14"} {
		got := decodeBody[nearestAnswer](t, get(t, s, "/api/biomes/nearest?dimension=overworld&x=0&z=0&biome="+name))
		if got.Biome.Name != "mushroom_island" || len(got.Hits) != 1 || got.More != 0 {
			t.Fatalf("biome=%s: %+v", name, got)
		}
		hit := got.Hits[0]
		if hit.X != 960 || hit.Z != 960 || hit.Distance != 1358 || hit.Region.Area != 256 || hit.Region.Chunks != 1 ||
			hit.Region.MinX != 960 || hit.Region.MaxX != 975 || hit.Region.MinZ != 960 || hit.Region.MaxZ != 975 {
			t.Errorf("biome=%s: hit = %+v", name, hit)
		}
	}
	// A biome the game has and this world does not is an empty answer.
	got := decodeBody[nearestAnswer](t, get(t, s, "/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=cherry_grove"))
	if got.Biome.Name != "cherry_grove" || got.Hits == nil || len(got.Hits) != 0 {
		t.Errorf("a biome not in this world = %+v", got)
	}
	for _, path := range []string{
		"/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=narnia",
		"/api/biomes/nearest?dimension=overworld&x=0&z=0",
		"/api/biomes/nearest?dimension=overworld&x=0&biome=desert",
		"/api/biomes/nearest?dimension=aether&x=0&z=0&biome=desert",
		"/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=desert&limit=0",
		"/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=desert&limit=-3",
		"/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=desert&limit=many",
	} {
		if rec := get(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

// However many stretches a biome has and however many are asked for, an
// answer carries at most fifty.
func TestBiomeNearestBoundsItsAnswer(t *testing.T) {
	s, _ := fixture(t)
	scattered := map[chunks.Pos][biomes.Columns]uint32{}
	for i := range int32(120) {
		scattered[chunks.Pos{Dim: chunks.Overworld, X: i * 4, Z: 0}] = chunkOf(desertID)
	}
	store := &biomes.Store{}
	store.Set(biomes.Build(biomesAt, scattered))
	s.Biomes = store

	for limit, want := range map[string]int{"": 10, "&limit=3": 3, "&limit=50": 50, "&limit=51": 50, "&limit=1000000": 50} {
		got := decodeBody[nearestAnswer](t, get(t, s, "/api/biomes/nearest?dimension=overworld&x=0&z=0&biome=desert"+limit))
		if len(got.Hits) != want || got.More != 120-want {
			t.Errorf("limit %q: %d hits and %d more, want %d and %d", limit, len(got.Hits), got.More, want, 120-want)
		}
		for i, h := range got.Hits {
			if h.X != int32(i)*64 {
				t.Fatalf("limit %q: hit %d at x %d, want nearest first", limit, i, h.X)
			}
		}
	}
}

func TestBiomeRegionGivesTheExtentOfTheStretchABlockIsIn(t *testing.T) {
	s := withBiomes(t)
	type answer struct {
		Found bool `json:"found"`
		Biome *struct {
			Name string `json:"name"`
		} `json:"biome"`
		Region *struct {
			Area                   uint64
			Chunks                 int
			MinX, MinZ, MaxX, MaxZ int32
		} `json:"region"`
		Rects     [][4]int32 `json:"rects"`
		RectsMore int        `json:"rectsMore"`
	}
	got := decodeBody[answer](t, get(t, s, "/api/biomes/region?dimension=overworld&x=20&z=4"))
	if !got.Found || got.Biome.Name != "desert" || got.Region.Area != 384 || got.Region.Chunks != 2 || got.RectsMore != 0 {
		t.Fatalf("desert = %+v", got)
	}
	if got.Region.MinX != 0 || got.Region.MaxX != 31 || got.Region.MinZ != 0 || got.Region.MaxZ != 15 {
		t.Errorf("box = %+v", got.Region)
	}
	if len(got.Rects) != 1 || got.Rects[0] != [4]int32{0, 0, 31, 15} {
		t.Errorf("rects = %v, want one row of two chunks", got.Rects)
	}
	// The same chunk, a different biome, a different stretch.
	if plains := decodeBody[answer](t, get(t, s, "/api/biomes/region?dimension=overworld&x=2&z=4")); plains.Biome.Name != "plains" || plains.Region.Area != 128 || plains.Region.MaxX != 15 {
		t.Errorf("plains = %+v %+v", plains.Biome, plains.Region)
	}
	if nowhere := decodeBody[answer](t, get(t, s, "/api/biomes/region?dimension=overworld&x=-500&z=4")); nowhere.Found || nowhere.Biome != nil || nowhere.Rects == nil {
		t.Errorf("ungenerated ground = %+v", nowhere)
	}
	if rec := get(t, s, "/api/biomes/region?dimension=overworld&x=2"); rec.Code != http.StatusBadRequest {
		t.Errorf("without z = %d, want 400", rec.Code)
	}
}

// Several biomes may be chosen at once: a list to draw, with nothing else
// drawn, or a list to leave out. What is left out is left clear, as
// ungenerated ground is, where one biome picked out dims the rest.
func TestBiomeTilesDrawAChosenSetOfBiomes(t *testing.T) {
	s := withBiomes(t)
	var (
		plains = color.NRGBA{0x8D, 0xB3, 0x60, 0xFF}
		desert = color.NRGBA{0xFA, 0x94, 0x18, 0xFF}
		clear  = color.NRGBA{}
	)
	const tile = "/api/biomes/tiles/overworld/0/0/0.png"
	for query, want := range map[string][2]color.NRGBA{
		"?biomes=desert":          {clear, desert},
		"?biomes=plains":          {plains, clear},
		"?biomes=desert,plains":   {plains, desert},
		"?biomes=Desert,desert":   {clear, desert},
		"?except=desert":          {plains, clear},
		"?except=plains,desert":   {clear, clear},
		"?except=mushroom_island": {plains, desert},
		"?biomes=mushroom_island": {clear, clear},
	} {
		rec := get(t, s, tile+query)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", query, rec.Code)
			continue
		}
		if got := [2]color.NRGBA{tilePixel(t, rec, 2, 5), tilePixel(t, rec, 20, 5)}; got != want {
			t.Errorf("%s draws the plains and the desert as %v, want %v", query, got, want)
		}
	}

	// Every name is one the game has, there are no more of them than the
	// limit, and a tile is asked for in one way at a time.
	most := strings.Repeat("desert,", maxTileBiomes-1) + "plains"
	for query, want := range map[string]int{
		"?biomes=":                     http.StatusBadRequest,
		"?except=":                     http.StatusBadRequest,
		"?biomes=desert,narnia":        http.StatusBadRequest,
		"?except=narnia":               http.StatusBadRequest,
		"?biomes=desert,":              http.StatusBadRequest,
		"?biomes=desert&except=plains": http.StatusBadRequest,
		"?biome=desert&biomes=plains":  http.StatusBadRequest,
		"?biome=desert&except=plains":  http.StatusBadRequest,
		"?biomes=" + most:              http.StatusOK,
		"?biomes=" + most + ",desert":  http.StatusBadRequest,
		"?except=" + most + ",desert":  http.StatusBadRequest,
		"?biome=":                      http.StatusOK,
	} {
		if rec := get(t, s, tile+query); rec.Code != want {
			t.Errorf("GET %.40s = %d, want %d", query, rec.Code, want)
		}
	}

	// The tag names the set, so one set's tile is never kept as another's;
	// and the same set is the same tile however it was written.
	tag := func(query string) string { return get(t, s, tile+query).Header().Get("ETag") }
	if a, b := tag("?biomes=desert,plains"), tag("?biomes=Plains,desert,plains"); a == "" || a != b {
		t.Errorf("one set of biomes has two tags: %q and %q", a, b)
	}
	seen := map[string]string{}
	for _, query := range []string{"", "?biome=desert", "?biomes=desert", "?except=desert", "?biomes=desert,plains", "?except=desert,plains", "?biomes=plains"} {
		got := tag(query)
		if other, dup := seen[got]; dup || got == "" {
			t.Errorf("%q carries the tag of %q: %s", query, other, got)
		}
		seen[got] = query
	}
	version := decodeBody[biomesListing](t, get(t, s, "/api/biomes?dimension=overworld")).Version
	if cc := get(t, s, tile+"?except=desert&v="+version).Header().Get("Cache-Control"); cc != "private, max-age=31536000, immutable" {
		t.Errorf("a set's tile asked for by its version: Cache-Control %q", cc)
	}
	req := httptest.NewRequest("GET", tile+"?biomes=desert", nil)
	req.Header.Set("If-None-Match", tag("?biomes=desert"))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("a set's tile asked for again by its tag = %d, want 304", rec.Code)
	}
}
