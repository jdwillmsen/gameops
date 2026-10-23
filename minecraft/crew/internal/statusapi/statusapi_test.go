package statusapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/task"
)

type tasksFixed []task.Task

func (f tasksFixed) List() []task.Task { return f }

type fixed belief.Snapshot

func (f fixed) Snapshot() belief.Snapshot { return belief.Snapshot(f) }

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev", Unit{}, nil), "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
}

func TestReadyzFollowsSpawn(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev", Unit{}, nil), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unspawned readyz = %d, want 503", rec.Code)
	}
	if rec := get(t, Handler(fixed{Spawned: true}, "dev", Unit{}, nil), "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("spawned readyz = %d, want 200", rec.Code)
	}
}

// A dead unit is connected and spawned but can do no work, and nothing else
// outside the process can tell.
func TestReadyzIsUnavailableWhileDead(t *testing.T) {
	if rec := get(t, Handler(fixed{Spawned: true, Dead: true}, "dev", Unit{}, nil), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("dead readyz = %d, want 503", rec.Code)
	}
}

func TestStateReturnsTheSnapshotAndVersion(t *testing.T) {
	rec := get(t, Handler(fixed{Spawned: true, Health: 20, HealthKnown: true}, "1.2.3", Unit{}, nil), "/v1/state")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("state = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body struct {
		Version string          `json:"version"`
		State   belief.Snapshot `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != "1.2.3" || !body.State.Spawned || body.State.Health != 20 {
		t.Errorf("body = %+v", body)
	}
}

func TestWritesAreRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(fixed{}, "dev", Unit{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/state = %d, want 405", rec.Code)
	}
}

func TestMetricsServesTheProcessRegistry(t *testing.T) {
	rec := get(t, Handler(fixed{}, "dev", Unit{}, nil), "/metrics")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Errorf("metrics = %d, body lacks go_goroutines", rec.Code)
	}
}

func TestStateCarriesTheRunningTaskAndTheLastFive(t *testing.T) {
	var all tasksFixed
	for i := 0; i < 7; i++ {
		all = append(all, task.Task{ID: string(rune('a' + i)), State: task.Done})
	}
	all[2] = task.Task{ID: "run", Template: "gather_and_deposit", State: task.Running, Skill: "mine", Item: "minecraft:log", Qty: 8, Progress: 3}
	all[6] = task.Task{ID: "bad", State: task.Failed, Reason: "no path"}

	rec := get(t, Handler(fixed{}, "dev", Unit{}, all), "/v1/state")
	var body struct {
		Tasks struct {
			Current *task.Task  `json:"current"`
			Recent  []task.Task `json:"recent"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if c := body.Tasks.Current; c == nil || c.ID != "run" || c.Progress != 3 || c.Qty != 8 || c.Skill != "mine" {
		t.Errorf("current = %+v", c)
	}
	var ids []string
	for _, r := range body.Tasks.Recent {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, ","); got != "bad,f,e,d,b" {
		t.Errorf("recent = %s, want the newest five without the running one", got)
	}
	if body.Tasks.Recent[0].Reason != "no path" {
		t.Errorf("failure reason lost: %+v", body.Tasks.Recent[0])
	}
}

func TestStateReportsNoTasksWithoutARunner(t *testing.T) {
	rec := get(t, Handler(fixed{}, "dev", Unit{}, nil), "/v1/state")
	if got := strings.TrimSpace(rec.Body.String()); !strings.Contains(got, `"tasks":{"current":null,"recent":[]}`) {
		t.Errorf("body = %s", got)
	}
}

func TestStateNamesTheUnit(t *testing.T) {
	rec := get(t, Handler(fixed{}, "dev", Unit{ID: "crew-02", Name: "Basalt"}, nil), "/v1/state")
	var body struct {
		Version string `json:"version"`
		Unit    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"unit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Unit.ID != "crew-02" || body.Unit.Name != "Basalt" || body.Version != "dev" {
		t.Errorf("body = %+v", body)
	}
}

type fightFixed struct {
	s  life.FightSummary
	ok bool
}

func (f fightFixed) Stats() (life.FightSummary, bool) { return f.s, f.ok }

func TestStateCarriesAWarriorsFightCounts(t *testing.T) {
	fight := fightFixed{ok: true, s: life.FightSummary{
		Total:   life.FightStats{Swings: 12, HitsLanded: 7, DamageTaken: 9.5, Deaths: 1},
		Current: life.FightStats{Swings: 4, HitsLanded: 2, DamageTaken: 3},
	}}
	rec := get(t, Handler(fixed{}, "dev", Unit{}, nil, WithFight(fight)), "/v1/state")
	var body struct {
		Tasks struct {
			Fight *life.FightSummary `json:"fight"`
		} `json:"tasks"`
	}
	raw := rec.Body.String()
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if body.Tasks.Fight == nil || *body.Tasks.Fight != fight.s {
		t.Fatalf("fight = %+v, want %+v", body.Tasks.Fight, fight.s)
	}
	for _, key := range []string{`"swings":12`, `"hits_landed":7`, `"damage_taken":9.5`, `"deaths":1`} {
		if !strings.Contains(raw, key) {
			t.Errorf("body lacks %s: %s", key, raw)
		}
	}
}

func TestStateOmitsFightCountsForAUnitThatKeepsNone(t *testing.T) {
	for name, h := range map[string]http.Handler{
		"no source":     Handler(fixed{}, "dev", Unit{}, nil),
		"not a fighter": Handler(fixed{}, "dev", Unit{}, nil, WithFight(fightFixed{})),
	} {
		if body := get(t, h, "/v1/state").Body.String(); strings.Contains(body, `"fight"`) {
			t.Errorf("%s: body carries fight counts: %s", name, body)
		}
	}
}

func TestMetricsExposeTheFightCounters(t *testing.T) {
	body := get(t, Handler(fixed{}, "dev", Unit{}, nil), "/metrics").Body.String()
	for _, name := range []string{"crew_swings_total", "crew_hits_landed_total", "crew_damage_taken_total"} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics lack %s", name)
		}
	}
}
