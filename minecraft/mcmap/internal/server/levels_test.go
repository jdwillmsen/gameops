package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// renderedNether puts a rendered nether map in maps/<dir>, with one tile
// whose bytes say which directory it came from.
func renderedNether(t *testing.T, maps, dir string) {
	t.Helper()
	out := filepath.Join(maps, dir)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
		t.Fatal(err)
	}
	tile := (&render.Unmined{}).TilePath(out, "webp", 0, 1, 1)
	if err := os.MkdirAll(filepath.Dir(tile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tile, []byte("tile of "+dir), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withLevels(t *testing.T, rendered ...string) *Server {
	t.Helper()
	s, maps := fixture(t)
	for _, dir := range rendered {
		renderedNether(t, maps, dir)
	}
	s.NetherTop = 100
	s.NetherLevels = []render.NetherLevel{{TopY: 66, Label: "Mid"}, {TopY: 34, Label: "Low"}}
	return s
}

type levelsAnswer struct {
	Dimensions []struct {
		ID       string `json:"id"`
		Rendered bool   `json:"rendered"`
		Levels   []struct {
			ID         string     `json:"id"`
			Y          int        `json:"y"`
			Label      string     `json:"label"`
			RenderedAt *time.Time `json:"renderedAt"`
		} `json:"levels"`
	} `json:"dimensions"`
}

func levelsOf(t *testing.T, s *Server) levelsAnswer {
	t.Helper()
	rec := get(t, s, "/api/map")
	var got levelsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return got
}

func TestMapAPI_TellsNoLevelsWhereNoneAreConfigured(t *testing.T) {
	s, maps := fixture(t)
	renderedNether(t, maps, "nether")
	got := levelsOf(t, s)
	if len(got.Dimensions) != 3 || got.Dimensions[1].ID != "nether" || !got.Dimensions[1].Rendered {
		t.Fatalf("dimensions = %+v, want a rendered nether second of three", got.Dimensions)
	}
	for _, d := range got.Dimensions {
		if len(d.Levels) != 0 {
			t.Errorf("a deployment with no extra levels is told of levels of %s: %+v", d.ID, d.Levels)
		}
	}
}

// The levels are drawn after the dimensions, so each says when it was drawn
// itself; the default cut's time is the nether's own.
func TestMapAPI_EachLevelSaysWhenItWasDrawn(t *testing.T) {
	s := withLevels(t, "nether", "nether-66", "nether-34")
	top, mid := renderedAt.Add(time.Hour), renderedAt.Add(2*time.Hour)
	s.Status.MarkRendered("nether", top)
	s.Status.MarkRendered("nether-66", mid)

	levels := levelsOf(t, s).Dimensions[1].Levels
	if len(levels) != 3 {
		t.Fatalf("levels = %+v", levels)
	}
	if levels[0].RenderedAt == nil || !levels[0].RenderedAt.Equal(top) {
		t.Errorf("default level drawn at %v, want %v", levels[0].RenderedAt, top)
	}
	if levels[1].RenderedAt == nil || !levels[1].RenderedAt.Equal(mid) {
		t.Errorf("level 66 drawn at %v, want %v", levels[1].RenderedAt, mid)
	}
	if levels[2].RenderedAt != nil {
		t.Errorf("level 34, whose render time is not known, says %v", levels[2].RenderedAt)
	}
}

func TestMapAPI_ListsTheLevelsThatHaveBeenDrawn(t *testing.T) {
	s := withLevels(t, "nether", "nether-66")
	got := levelsOf(t, s)

	for _, d := range got.Dimensions {
		if d.ID != "nether" && len(d.Levels) != 0 {
			t.Errorf("%s has levels %+v", d.ID, d.Levels)
		}
	}
	nether := got.Dimensions[1]
	// nether-34 is configured but has no map yet, so it is not offered.
	if len(nether.Levels) != 2 ||
		nether.Levels[0].ID != "default" || nether.Levels[0].Y != 100 || nether.Levels[0].Label != "Top" ||
		nether.Levels[1].ID != "66" || nether.Levels[1].Y != 66 || nether.Levels[1].Label != "Mid" {
		t.Errorf("levels = %+v", nether.Levels)
	}
}

func TestMapAPI_NoLevelsUntilAnExtraOneIsDrawn(t *testing.T) {
	s := withLevels(t, "nether")
	if levels := levelsOf(t, s).Dimensions[1].Levels; len(levels) != 0 {
		t.Errorf("levels = %+v, want none", levels)
	}
}

func TestTiles_ALevelIsServedFromItsOwnDirectory(t *testing.T) {
	s := withLevels(t, "nether", "nether-66", "nether-34")

	for path, want := range map[string]string{
		"/tiles/nether/0/1/1.webp":                 "tile of nether",
		"/tiles/nether/0/1/1.webp?level=66":        "tile of nether-66",
		"/tiles/nether/0/1/1.webp?level=34":        "tile of nether-34",
		"/tiles/nether/0/1/1.webp?level=":          "tile of nether",
		"/tiles/nether/0/1/1.webp?other=66":        "tile of nether",
		"/tiles/nether/0/1/1.webp?level=66&level=": "tile of nether-66",
	} {
		rec := get(t, s, path)
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("GET %s = %d %q, want %q", path, rec.Code, rec.Body.String(), want)
		}
	}
}

// What a request says is only ever compared with what is configured; none
// of it reaches a path.
func TestTiles_ALevelThatIsNotConfiguredIsNotThere(t *testing.T) {
	s := withLevels(t, "nether", "nether-66", "nether-50", "overworld")
	for _, level := range []string{
		"50", "default", "066", "+66", "66 ", "6", "-66", "0", "..", "../overworld", "..%2foverworld", "%2e%2e", "66/..", "nether-66", "66\x00", "66/../66",
	} {
		path := "/tiles/nether/0/1/1.webp?level=" + url.QueryEscape(level)
		if rec := get(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d %q, want 404", path, rec.Code, rec.Body.String())
		}
	}
}

func TestTiles_OnlyTheNetherHasLevels(t *testing.T) {
	s := withLevels(t, "nether-66")
	for _, path := range []string{"/tiles/overworld/0/37/-4.webp?level=66", "/tiles/end/0/37/-4.webp?level=66"} {
		if rec := get(t, s, path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	// The overworld's own tile is untouched by there being levels.
	if rec := get(t, s, "/tiles/overworld/0/37/-4.webp"); rec.Code != http.StatusOK {
		t.Errorf("overworld tile = %d", rec.Code)
	}
}

func TestTiles_ALevelNotDrawnYetIsNotFound(t *testing.T) {
	s := withLevels(t, "nether")
	if rec := get(t, s, "/tiles/nether/0/1/1.webp?level=66"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
