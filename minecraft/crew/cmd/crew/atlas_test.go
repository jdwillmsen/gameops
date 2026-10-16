package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/camera"
)

func atlasStatus(h *camera.Hub) int {
	rec := httptest.NewRecorder()
	camera.Mount(http.NotFoundHandler(), h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/camera/atlas.json", nil))
	return rec.Code
}

func newHub() *camera.Hub { return camera.New(camera.Options{MaxViewers: 1}) }

func TestAtlasIsServedOnceTheBuildSucceeds(t *testing.T) {
	built, err := atlas.Build("../../internal/atlas/testdata/pack")
	if err != nil {
		t.Fatal(err)
	}
	hub := newHub()
	var gotVersion, gotDir string
	l := &atlasLoader{dir: "/cache", hub: hub, log: logging.New(""), build: func(_ context.Context, v, d string) (*atlas.Atlas, error) {
		gotVersion, gotDir = v, d
		return built, nil
	}}
	l.load(context.Background(), "1.2.3.4")
	if atlasStatus(hub) != http.StatusOK || gotVersion != "1.2.3.4" || gotDir != "/cache" {
		t.Errorf("status %d, built for %q in %q", atlasStatus(hub), gotVersion, gotDir)
	}
	// Built once per process: a reconnect must not rebuild.
	gotVersion = ""
	l.load(context.Background(), "1.2.3.4")
	if gotVersion != "" {
		t.Error("atlas rebuilt after it was ready")
	}
}

func TestFailedBuildLeavesTheCameraFlatAndIsRetriedNextSession(t *testing.T) {
	hub := newHub()
	calls := 0
	l := &atlasLoader{dir: t.TempDir(), hub: hub, log: logging.New(""), build: func(context.Context, string, string) (*atlas.Atlas, error) {
		calls++
		return nil, errors.New("offline")
	}}
	l.load(context.Background(), "1.2.3.4")
	l.load(context.Background(), "1.2.3.4")
	if atlasStatus(hub) != http.StatusNotFound || calls != 2 {
		t.Errorf("status %d after %d attempts; want 404 and a retry", atlasStatus(hub), calls)
	}
}

func TestUnknownServerVersionBuildsNothing(t *testing.T) {
	l := &atlasLoader{hub: newHub(), log: logging.New(""), build: func(context.Context, string, string) (*atlas.Atlas, error) {
		t.Error("built without a server version")
		return nil, nil
	}}
	l.load(context.Background(), "")
}

// No network and nothing cached is the offline start: it must end in a plain
// error, not a hang or a panic.
func TestUnreachablePackSourceIsAnErrorNotACrash(t *testing.T) {
	build := ensureAtlas(atlas.Fetcher{BaseURL: "http://127.0.0.1:1"})
	if _, err := build(context.Background(), "1.2.3.4", t.TempDir()); err == nil {
		t.Error("build succeeded with no pack source")
	}
}
