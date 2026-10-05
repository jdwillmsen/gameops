package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
)

type fakeStructures struct {
	survey   structures.Survey
	surveyed bool
}

func (f *fakeStructures) Last() (structures.Survey, bool) { return f.survey, f.surveyed }

// A seed with every byte different, so that any spelling of it in a
// response is recognisable.
const secretSeed = int64(0x1122334455667788)

func surveyed() *fakeStructures {
	return &fakeStructures{surveyed: true, survey: structures.Survey{
		At: renderedAt,
		Layers: map[chunks.Dimension]structures.Layer{
			chunks.Overworld: {
				Recorded: []structures.Structure{{Kind: structures.Monument, Box: structures.Box{MinX: 27, MinY: 39, MinZ: 5243, MaxX: 84, MaxY: 61, MaxZ: 5300}, Areas: 15}},
			},
			chunks.Nether: {
				Recorded:      []structures.Structure{{Kind: structures.Fortress, Box: structures.Box{MinX: 74, MinY: 48, MinZ: -450, MaxX: 231, MaxY: 72, MaxZ: -286}, Areas: 165}},
				Predicted:     []structures.Prediction{{Kind: structures.Fortress, X: 536, Z: 216}, {Kind: structures.Fortress, X: -600, Z: -2808, Generated: true}},
				PredictedMore: 3,
			},
		},
		Check:            structures.Check{State: structures.SeedVerified, Agree: 19, Findings: []string{"fortress predicted at nether -600, -2808"}, Total: 1},
		Level:            leveldat.Level{Seed: secretSeed, SpawnX: 40, SpawnZ: -72, SpawnY: 32767},
		HasLevel:         true,
		StructureSeed:    0x55667788,
		HasStructureSeed: true,
	}}
}

type structuresResponse struct {
	Surveyed      bool                    `json:"surveyed"`
	Recorded      []structures.Structure  `json:"recorded"`
	RecordedMore  int                     `json:"recordedMore"`
	Predicted     []structures.Prediction `json:"predicted"`
	PredictedMore int                     `json:"predictedMore"`
	Prediction    string                  `json:"prediction"`
	Spawn         *struct {
		X, Z int32
		Y    *int32
	} `json:"spawn"`
}

func structuresOf(t *testing.T, s *Server, path string, cookies ...*http.Cookie) (structuresResponse, string) {
	t.Helper()
	rec := do(s.Handler(), "GET", path, "", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var got structuresResponse
	body := rec.Body.String()
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	return got, body
}

// Where every fortress and monument is, is behind the same gate as the map.
func TestStructuresRequireASession(t *testing.T) {
	s := withLogin(t)
	s.Structures = surveyed()
	h := s.Handler()
	forged := &http.Cookie{Name: "__Host-mcmap_session", Value: "e30.nope"}
	for name, cookies := range map[string][]*http.Cookie{"no session": nil, "a forged session": {forged}} {
		rec := do(h, "GET", "/api/structures?dimension=nether", "", cookies)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("with %s = %d, want 401", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "fortress") {
			t.Errorf("with %s the refusal names a structure: %s", name, rec.Body)
		}
	}
	got, _ := structuresOf(t, s, "/api/structures?dimension=nether", session(s, steve))
	if len(got.Recorded) != 1 || len(got.Predicted) != 2 {
		t.Errorf("with a session: %+v", got)
	}
}

func TestStructuresKeepRecordedAndPredictedApartByDimension(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = surveyed()

	nether, _ := structuresOf(t, s, "/api/structures?dimension=nether")
	if !nether.Surveyed || nether.Prediction != structures.SeedVerified || nether.PredictedMore != 3 {
		t.Errorf("nether = %+v", nether)
	}
	if len(nether.Recorded) != 1 || nether.Recorded[0].Kind != structures.Fortress || nether.Recorded[0].MaxX != 231 || nether.Recorded[0].Areas != 165 {
		t.Errorf("nether recorded = %+v", nether.Recorded)
	}
	if len(nether.Predicted) != 2 || nether.Predicted[0].X != 536 || nether.Predicted[0].Generated || !nether.Predicted[1].Generated {
		t.Errorf("nether predicted = %+v", nether.Predicted)
	}
	// The spawn is the overworld's.
	if nether.Spawn != nil {
		t.Errorf("nether carries a spawn: %+v", nether.Spawn)
	}

	overworld, _ := structuresOf(t, s, "/api/structures?dimension=overworld")
	if len(overworld.Recorded) != 1 || overworld.Recorded[0].Kind != structures.Monument || len(overworld.Predicted) != 0 {
		t.Errorf("overworld = %+v", overworld)
	}
	// 32767 is what a world stores before it has worked the height out.
	if overworld.Spawn == nil || overworld.Spawn.X != 40 || overworld.Spawn.Z != -72 || overworld.Spawn.Y != nil {
		t.Errorf("spawn = %+v", overworld.Spawn)
	}

	// A dimension with nothing in it is empty lists, not nulls.
	_, body := structuresOf(t, s, "/api/structures?dimension=end")
	if !strings.Contains(body, `"recorded":[]`) || !strings.Contains(body, `"predicted":[]`) {
		t.Errorf("end = %s", body)
	}
}

// With the seed, a seed map shows a player everything the world has yet to
// generate. It stays on the server, in every form.
func TestStructuresNeverCarryTheSeed(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = surveyed()
	for _, d := range []string{"overworld", "nether", "end"} {
		_, body := structuresOf(t, s, "/api/structures?dimension="+d)
		for _, spelling := range []string{"1234605616436508552", "1432778632", "11223344", "55667788", "eed", "finding", "-600, -2808"} {
			if strings.Contains(strings.ToLower(body), spelling) {
				t.Errorf("%s: the response contains %q: %s", d, spelling, body)
			}
		}
	}
}

func TestStructuresBeforeTheFirstSurvey(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = &fakeStructures{}
	got, body := structuresOf(t, s, "/api/structures?dimension=overworld")
	if got.Surveyed || got.Prediction != structures.SeedUnknown || got.Spawn != nil {
		t.Errorf("before a survey: %+v", got)
	}
	if !strings.Contains(body, `"recorded":[]`) {
		t.Errorf("body = %s", body)
	}
}

func TestStructuresRejectAnUnknownDimension(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = surveyed()
	for _, path := range []string{"/api/structures", "/api/structures?dimension=", "/api/structures?dimension=moon", "/api/structures?dimension=dimension-3"} {
		if rec := do(s.Handler(), "GET", path, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

// The survey bounds its own lists; this is the bound on the response, which
// must hold whatever it is handed.
func TestStructuresBoundTheResponse(t *testing.T) {
	s, _ := fixture(t)
	source := surveyed()
	layer := source.survey.Layers[chunks.Nether]
	for i := range int32(structures.MaxPerLayer + 500) {
		layer.Recorded = append(layer.Recorded, structures.Structure{Kind: structures.Fortress, Box: structures.Box{MinX: i, MaxX: i}})
		layer.Predicted = append(layer.Predicted, structures.Prediction{Kind: structures.Fortress, X: i})
	}
	source.survey.Layers[chunks.Nether] = layer
	s.Structures = source

	got, body := structuresOf(t, s, "/api/structures?dimension=nether")
	if len(got.Recorded) != structures.MaxPerLayer || got.RecordedMore != 501 {
		t.Errorf("recorded %d (+%d), want %d (+501)", len(got.Recorded), got.RecordedMore, structures.MaxPerLayer)
	}
	if len(got.Predicted) != structures.MaxPerLayer || got.PredictedMore != 3+502 {
		t.Errorf("predicted %d (+%d), want %d (+505)", len(got.Predicted), got.PredictedMore, structures.MaxPerLayer)
	}
	if len(body) > 512<<10 {
		t.Errorf("a full response is %d bytes", len(body))
	}
}

func TestStructuresDisabledRegistersNoRoute(t *testing.T) {
	s, _ := fixture(t)
	rec := do(s.Handler(), "GET", "/api/structures?dimension=overworld", "", nil)
	// The page's catch-all answers, with no structures in it.
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "recorded") {
		t.Errorf("structures served with none configured: %d %s", rec.Code, rec.Body)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/structures with none configured = %d, want 404", rec.Code)
	}
}
