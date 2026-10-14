package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/biomes"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

// hill gives chunks 0, 0 and 0, -1 of the overworld a surface at y 70 and
// chunk 0, 0 of the nether its roof, at 127. The overworld's chunk 0, -3 is
// not generated.
func hill(t *testing.T, s *Server) *biomes.Store {
	t.Helper()
	b := heights.NewBuilder(biomesAt, 2)
	flat := func(raw int16) *[heights.Columns]int16 {
		var c [heights.Columns]int16
		for i := range c {
			c[i] = raw
		}
		return &c
	}
	// Stored counted from the floor: -64 for the overworld, 0 below.
	for pos, raw := range map[chunks.Pos]int16{{Dim: chunks.Overworld}: 70 + 64 + 1, {Dim: chunks.Overworld, Z: -1}: 70 + 64 + 1, {Dim: chunks.Nether}: 128} {
		if err := b.AddColumns(pos.Dim, pos.X, pos.Z, flat(raw)); err != nil {
			t.Fatal(err)
		}
	}
	store := &biomes.Store{}
	store.Set(biomeWorld().WithHeights(b.Map()))
	s.Biomes = store
	return store
}

type surfaced struct {
	Beds       []markers.Marker `json:"beds"`
	Containers []markers.Marker `json:"containers"`
	Mobs       []markers.Marker `json:"mobs"`
}

func surfaceOfMarker(m markers.Marker) any {
	if m.Surface == nil {
		return nil
	}
	return *m.Surface
}

func TestMarkers_SayTheSurfaceUnderEachWhereTheWorldKnowsIt(t *testing.T) {
	s, _ := withMarkers(t)
	hill(t, s)
	h, cookies := s.Handler(), []*http.Cookie{session(s, steve)}

	rec := do(h, "GET", "/api/markers?dimension=overworld", "", cookies)
	var got surfaced
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("%d %v", rec.Code, err)
	}
	// The bed and the chest are over the hill; the cat is in a chunk the
	// world has not generated.
	if surfaceOfMarker(got.Beds[0]) != int32(70) || surfaceOfMarker(got.Containers[0]) != int32(70) || surfaceOfMarker(got.Mobs[0]) != nil {
		t.Errorf("surfaces = %v %v %v, want 70 70 and none", surfaceOfMarker(got.Beds[0]), surfaceOfMarker(got.Containers[0]), surfaceOfMarker(got.Mobs[0]))
	}
	if got.Beds[0].Y != 64 || got.Containers[0].Name != hostile {
		t.Errorf("the markers' own fields changed on the way: %+v", got)
	}

	// The nether's heightmap is its roof, which says nothing of a bed in it.
	rec = do(h, "GET", "/api/markers?dimension=nether", "", cookies)
	if strings.Contains(rec.Body.String(), `"s":`) {
		t.Errorf("a nether marker carries a surface: %s", rec.Body)
	}
}

func TestMarkers_AreSentAgainWhenTheSurfaceReadingChanges(t *testing.T) {
	s, _ := withMarkers(t)
	h, cookies := s.Handler(), []*http.Cookie{session(s, steve)}
	bare := do(h, "GET", "/api/markers?dimension=overworld", "", cookies)
	if strings.Contains(bare.Body.String(), `"s":`) {
		t.Fatalf("a service with no biomes put a surface on a marker: %s", bare.Body)
	}

	hill(t, s)
	with := do(h, "GET", "/api/markers?dimension=overworld", "", cookies)
	etag := with.Header().Get("ETag")
	if etag == "" || etag == bare.Header().Get("ETag") || !strings.Contains(with.Body.String(), `"s":70`) {
		t.Fatalf("with a reading: tag %q (was %q), body %s", etag, bare.Header().Get("ETag"), with.Body)
	}
	if again := do(h, "GET", "/api/markers?dimension=overworld", "", cookies, "If-None-Match", etag); again.Code != http.StatusNotModified {
		t.Errorf("an unchanged answer = %d, want 304", again.Code)
	}
	// A tag from before the reading is not still current once it arrives.
	if old := do(h, "GET", "/api/markers?dimension=overworld", "", cookies, "If-None-Match", bare.Header().Get("ETag")); old.Code != http.StatusOK {
		t.Errorf("the tag of the markers without surfaces = %d, want the new answer", old.Code)
	}
}

func TestHistory_ServesADaysMarkersWithTheSurfaceNowAndOnlyLetsTheBrowserCheckThem(t *testing.T) {
	s := withHistory(t)
	hill(t, s)
	rec := get(t, s, "/api/history/markers/2026-10-12?dimension=overworld")
	var got surfaced
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil || len(got.Beds) != 1 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	// The bed of that day stood at 1, -2, which is in chunk 0, -1.
	if surfaceOfMarker(got.Beds[0]) != int32(70) || got.Containers == nil || got.Mobs == nil {
		t.Errorf("day's markers = %+v", got)
	}
	// The surface is of now, so a copy kept for good could outlive it.
	if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") || rec.Header().Get("ETag") == "" {
		t.Errorf("Cache-Control %q, ETag %q", cc, rec.Header().Get("ETag"))
	}
	if again := do(s.Handler(), "GET", "/api/history/markers/2026-10-12?dimension=overworld", "", nil, "If-None-Match", rec.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Errorf("an unchanged day = %d, want 304", again.Code)
	}
}

func TestStructures_SayTheSurfaceAtTheMiddleOfEachRecordedBoxAndNothingOfAPrediction(t *testing.T) {
	s := withLogin(t)
	survey := surveyed()
	// A box whose middle is over the hill, and the survey's others, which
	// are not.
	survey.survey.Layers[chunks.Overworld] = structures.Layer{
		Recorded: []structures.Structure{
			{Kind: structures.Village, Box: structures.Box{MinX: 0, MinY: 60, MinZ: 0, MaxX: 14, MaxY: 80, MaxZ: 14}},
			{Kind: structures.Monument, Box: structures.Box{MinX: 4000, MinY: 39, MinZ: 6000, MaxX: 4047, MaxY: 61, MaxZ: 6047}},
		},
		Predicted: []structures.Prediction{{Kind: structures.Village, X: 5, Z: 5}},
	}
	s.Structures = survey
	hill(t, s)
	cookies := []*http.Cookie{session(s, steve)}

	rec := do(s.Handler(), "GET", "/api/structures?dimension=overworld", "", cookies)
	var got struct {
		Recorded  []struct{ Kind, S any } `json:"recorded"`
		Predicted []map[string]any        `json:"predicted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("%d %v", rec.Code, err)
	}
	var raw struct {
		Recorded []map[string]any `json:"recorded"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if len(raw.Recorded) != 2 || raw.Recorded[0]["s"] != float64(70) || raw.Recorded[1]["s"] != nil || raw.Recorded[0]["minY"] != float64(60) {
		t.Errorf("recorded = %v", raw.Recorded)
	}
	if len(got.Predicted) != 1 || got.Predicted[0]["s"] != nil {
		t.Errorf("predicted = %v", got.Predicted)
	}

	// The reading arriving after the survey changes the answer, though the
	// survey has not.
	s.Biomes = nil
	bare := do(s.Handler(), "GET", "/api/structures?dimension=overworld", "", cookies)
	if strings.Contains(bare.Body.String(), `"s":`) || bare.Header().Get("ETag") == rec.Header().Get("ETag") {
		t.Errorf("without a reading: %s", bare.Body)
	}
}
