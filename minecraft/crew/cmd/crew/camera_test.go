package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
)

func status(t *testing.T, cfg config.Config, path string) (int, bool) {
	t.Helper()
	tracker := belief.New(time.Now)
	hub := newCamera(cfg, tracker)
	rec := httptest.NewRecorder()
	statusHandler(tracker, "dev", hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, hub != nil
}

// The camera streams the unit's surroundings, so a default config must not
// build it or route to it.
func TestCameraIsAbsentByDefault(t *testing.T) {
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/camera", "/v1/camera/ws", "/v1/camera/stats"} {
		if code, built := status(t, cfg, path); code != http.StatusNotFound || built {
			t.Errorf("%s: status %d, hub built %v; want 404 and no hub", path, code, built)
		}
	}
	if code, _ := status(t, cfg, "/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d with the camera off", code)
	}
}

func TestCameraIsMountedWhenEnabled(t *testing.T) {
	cfg := config.Config{CameraEnabled: true, CameraMaxViewers: 1}
	if code, built := status(t, cfg, "/camera"); code != http.StatusOK || !built {
		t.Errorf("status %d, hub built %v; want 200 and a hub", code, built)
	}
}
