package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/config"
)

func status(t *testing.T, cfg config.Config, path string) (int, bool) {
	t.Helper()
	tracker := belief.New(time.Now)
	hub, _ := newCamera(cfg, tracker, logging.New(""))
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

// The drift counter must be on the status port before any drift, or an alert
// on its rate has nothing to compare against.
func TestMetricsShowTheDriftCounterOncePerVersion(t *testing.T) {
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
	t.Setenv("CAMERA_ENABLED", "true")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	tracker := belief.New(time.Now)
	hub, beginSession := newCamera(cfg, tracker, logging.New(""))
	// Nothing listens here, so the version is unknown; the series must exist
	// all the same.
	beginSession(context.Background(), "127.0.0.1:1")

	rec := httptest.NewRecorder()
	statusHandler(tracker, "dev", hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	want := `crew_camera_unmapped_block_hashes_total{server_version="unknown"} 0`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("/metrics lacks %q", want)
	}
}
