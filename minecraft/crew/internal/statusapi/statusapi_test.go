package statusapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

type fixed belief.Snapshot

func (f fixed) Snapshot() belief.Snapshot { return belief.Snapshot(f) }

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev"), "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d", rec.Code)
	}
}

func TestReadyzFollowsSpawn(t *testing.T) {
	if rec := get(t, Handler(fixed{}, "dev"), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unspawned readyz = %d, want 503", rec.Code)
	}
	if rec := get(t, Handler(fixed{Spawned: true}, "dev"), "/readyz"); rec.Code != http.StatusOK {
		t.Errorf("spawned readyz = %d, want 200", rec.Code)
	}
}

// A dead unit is connected and spawned but can do no work, and nothing else
// outside the process can tell.
func TestReadyzIsUnavailableWhileDead(t *testing.T) {
	if rec := get(t, Handler(fixed{Spawned: true, Dead: true}, "dev"), "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("dead readyz = %d, want 503", rec.Code)
	}
}

func TestStateReturnsTheSnapshotAndVersion(t *testing.T) {
	rec := get(t, Handler(fixed{Spawned: true, Health: 20, HealthKnown: true}, "1.2.3"), "/v1/state")
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
	Handler(fixed{}, "dev").ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/state", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/state = %d, want 405", rec.Code)
	}
}
