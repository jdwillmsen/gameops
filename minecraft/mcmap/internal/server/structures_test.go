package server

import (
	"encoding/json"
	"net/http"
	"slices"
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

// surveyed is a survey of a world that is nobody's. Every box and site in
// it is made up: where a real structure stands follows from its world's
// seed, so no real one's position belongs in a test.
func surveyed() *fakeStructures {
	return &fakeStructures{surveyed: true, survey: structures.Survey{
		At: renderedAt,
		Layers: map[chunks.Dimension]structures.Layer{
			chunks.Overworld: {
				Recorded: []structures.Structure{
					{Kind: structures.Monument, Box: structures.Box{MinX: 4000, MinY: 39, MinZ: 6000, MaxX: 4047, MaxY: 61, MaxZ: 6047}, Areas: 15},
					{Kind: structures.Village, Box: structures.Box{MinX: -2600, MinY: 60, MinZ: 1800, MaxX: -2550, MaxY: 80, MaxZ: 1860},
						Village: &structures.VillageFacts{Counted: true, Villagers: 12, Golems: 1, Cats: 3, Beds: 11, Bells: 1, JobSites: 4}},
					{Kind: structures.Village, Box: structures.Box{MinX: 640, MinY: 60, MinZ: -800, MaxX: 704, MaxY: 84, MaxZ: -736}, Village: &structures.VillageFacts{}},
				},
			},
			chunks.Nether: {
				Recorded:      []structures.Structure{{Kind: structures.Fortress, Box: structures.Box{MinX: 40, MinY: 50, MinZ: -200, MaxX: 120, MaxY: 70, MaxZ: -130}, Areas: 165}},
				Predicted:     []structures.Prediction{{Kind: structures.Fortress, X: 640, Z: 320}, {Kind: structures.Fortress, X: -480, Z: -960, Generated: true}},
				PredictedMore: 3,
			},
		},
		Check: structures.Check{State: structures.SeedVerified, Agree: 19, Findings: []string{"fortress predicted at nether -480, -960"}, Total: 1,
			Kinds: map[structures.Kind]structures.KindCheck{
				structures.Fortress: {State: structures.SeedVerified, Agree: 11, Built: 8, Empty: 1, Findings: 1},
				structures.Monument: {State: structures.SeedVerified, Agree: 11},
				structures.WitchHut: {State: structures.SeedUnverified, Agree: 1},
			}},
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
	Kinds         map[structures.Kind]struct {
		State    string `json:"state"`
		Agree    int    `json:"agree"`
		Disagree int    `json:"disagree"`
	} `json:"kinds"`
	Spawn *struct {
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
		for _, dimension := range []string{"nether", "overworld"} {
			rec := do(h, "GET", "/api/structures?dimension="+dimension, "", cookies)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with %s = %d, want 401", dimension, name, rec.Code)
			}
			// A village is where somebody's villagers are.
			for _, kind := range []string{"fortress", "monument", "village"} {
				if strings.Contains(rec.Body.String(), kind) {
					t.Errorf("%s with %s: the refusal names a %s: %s", dimension, name, kind, rec.Body)
				}
			}
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
	if len(nether.Recorded) != 1 || nether.Recorded[0].Kind != structures.Fortress || nether.Recorded[0].MaxX != 120 || nether.Recorded[0].Areas != 165 {
		t.Errorf("nether recorded = %+v", nether.Recorded)
	}
	if len(nether.Predicted) != 2 || nether.Predicted[0].X != 640 || nether.Predicted[0].Generated || !nether.Predicted[1].Generated {
		t.Errorf("nether predicted = %+v", nether.Predicted)
	}
	// Each dimension says how its own kinds fared, and no other's.
	if k := nether.Kinds[structures.Fortress]; len(nether.Kinds) != 1 || k.State != structures.SeedVerified || k.Agree != 11 {
		t.Errorf("nether kinds = %+v", nether.Kinds)
	}
	// The spawn is the overworld's.
	if nether.Spawn != nil {
		t.Errorf("nether carries a spawn: %+v", nether.Spawn)
	}

	overworld, _ := structuresOf(t, s, "/api/structures?dimension=overworld")
	if len(overworld.Recorded) != 3 || overworld.Recorded[0].Kind != structures.Monument || len(overworld.Predicted) != 0 {
		t.Errorf("overworld = %+v", overworld)
	}
	if k := overworld.Kinds[structures.WitchHut]; len(overworld.Kinds) != 2 || k.State != structures.SeedUnverified || k.Agree != 1 {
		t.Errorf("overworld kinds = %+v", overworld.Kinds)
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

// A village goes out as one more recorded structure: a kind and a box, which
// is all the page needs to draw one, and beside them what the world counted
// in it. Nothing a player typed and nothing that names one is in a village's
// records as they are read, so nothing of the sort can be in the answer.
func TestStructuresServeAVillageAsARecordedStructure(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = surveyed()
	rec := do(s.Handler(), "GET", "/api/structures?dimension=overworld", "", nil)
	var got struct {
		Recorded []map[string]json.RawMessage `json:"recorded"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Recorded) != 3 {
		t.Fatalf("recorded = %s (%v)", rec.Body, err)
	}
	fields := func(m map[string]json.RawMessage) string {
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		slices.Sort(names)
		return strings.Join(names, " ")
	}
	// What the page reads of any recorded structure, and has since before
	// there were villages.
	if f := fields(got.Recorded[0]); f != "areas kind maxX maxY maxZ minX minY minZ" {
		t.Errorf("a monument's fields = %s", f)
	}
	for i, want := range map[int]string{
		1: `{"counted":true,"villagers":12,"golems":1,"cats":3,"beds":11,"bells":1,"jobSites":4}`,
		// Not counted yet is said outright; the zeros alone would read
		// as a village with nobody in it.
		2: `{"counted":false,"villagers":0,"golems":0,"cats":0,"beds":0,"bells":0,"jobSites":0}`,
	} {
		village := got.Recorded[i]
		if f := fields(village); f != "kind maxX maxY maxZ minX minY minZ village" {
			t.Errorf("a village's fields = %s", f)
		}
		if string(village["kind"]) != `"village"` || string(village["village"]) != want {
			t.Errorf("village %d = %s %s, want %s", i, village["kind"], village["village"], want)
		}
	}
	if string(got.Recorded[1]["minX"]) != "-2600" || string(got.Recorded[1]["maxZ"]) != "1860" {
		t.Errorf("box = %v", got.Recorded[1])
	}
}

// With the seed, a seed map shows a player everything the world has yet to
// generate. It stays on the server, in every form.
func TestStructuresNeverCarryTheSeed(t *testing.T) {
	s, _ := fixture(t)
	s.Structures = surveyed()
	for _, d := range []string{"overworld", "nether", "end"} {
		_, body := structuresOf(t, s, "/api/structures?dimension="+d)
		for _, spelling := range []string{"1234605616436508552", "1432778632", "11223344", "55667788", "eed", "finding", "-480, -960"} {
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

	// Villages are the largest thing on the layer, and a layer of nothing
	// else, each with every number as long as it can be, is still bounded.
	layer = structures.Layer{}
	for range structures.MaxPerLayer + 500 {
		layer.Recorded = append(layer.Recorded, structures.Structure{
			Kind: structures.Village, Box: structures.Box{MinX: -31_999_000, MinY: -31_999_000, MinZ: -31_999_000, MaxX: -31_999_000, MaxY: -31_999_000, MaxZ: -31_999_000},
			Village: &structures.VillageFacts{Counted: true, Villagers: 10_000, Golems: 10_000, Cats: 10_000, Beds: 10_000, Bells: 10_000, JobSites: 10_000},
		})
	}
	source.survey.Layers[chunks.Overworld] = layer
	got, body = structuresOf(t, s, "/api/structures?dimension=overworld")
	if len(got.Recorded) != structures.MaxPerLayer || got.RecordedMore != 500 {
		t.Errorf("villages %d (+%d), want %d (+500)", len(got.Recorded), got.RecordedMore, structures.MaxPerLayer)
	}
	if len(body) > 640<<10 {
		t.Errorf("a full response of villages is %d bytes", len(body))
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
