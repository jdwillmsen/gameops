package statusapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
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
	if rec := get(t, Handler(fixed{}, "dev", nil), "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
}

func TestReadyzFollowsSpawn(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev", nil), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unspawned readyz = %d, want 503", rec.Code)
	}
	if rec := get(t, Handler(fixed{Spawned: true}, "dev", nil), "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("spawned readyz = %d, want 200", rec.Code)
	}
}

// A dead unit is connected and spawned but can do no work, and nothing else
// outside the process can tell.
func TestReadyzIsUnavailableWhileDead(t *testing.T) {
	if rec := get(t, Handler(fixed{Spawned: true, Dead: true}, "dev", nil), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("dead readyz = %d, want 503", rec.Code)
	}
}

func TestStateReturnsTheSnapshotAndVersion(t *testing.T) {
	rec := get(t, Handler(fixed{Spawned: true, Health: 20, HealthKnown: true}, "1.2.3", nil), "/v1/state")
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
	Handler(fixed{}, "dev", nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/state = %d, want 405", rec.Code)
	}
}

func TestMetricsServesTheProcessRegistry(t *testing.T) {
	rec := get(t, Handler(fixed{}, "dev", nil), "/metrics")
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

	rec := get(t, Handler(fixed{}, "dev", all), "/v1/state")
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
	rec := get(t, Handler(fixed{}, "dev", nil), "/v1/state")
	if got := strings.TrimSpace(rec.Body.String()); !strings.Contains(got, `"tasks":{"current":null,"recent":[]}`) {
		t.Errorf("body = %s", got)
	}
}
