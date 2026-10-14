package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/config"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

const rendered = `var UnminedMapProperties = {
    minZoom: -6,
    maxZoom: 0,
    imageFormat: "webp",
    minRegionX: -1,
    minRegionZ: -1,
    maxRegionX: 1,
    maxRegionZ: 1,
}
`

// drawnMaps is a maps directory with every dimension rendered and one tile
// in the overworld, which is as much as a day needs to be kept.
func drawnMaps(t *testing.T) (string, *render.Unmined) {
	t.Helper()
	maps, r := t.TempDir(), &render.Unmined{}
	for _, d := range render.Dimensions {
		if err := os.MkdirAll(filepath.Join(maps, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(maps, d, "unmined.map.properties.js"), []byte(rendered), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tile := r.TilePath(filepath.Join(maps, "overworld"), "webp", 0, 3, -4)
	if err := os.MkdirAll(filepath.Dir(tile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tile, []byte("a hut"), 0o644); err != nil {
		t.Fatal(err)
	}
	return maps, r
}

func TestHistoryDisabledOpensNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	past, err := openHistory(config.Config{HistoryDir: dir}, t.TempDir(), &render.Unmined{}, markers.NewStore(), slog.New(slog.DiscardHandler))
	if err != nil || past != nil {
		t.Fatalf("history off gave %v, %v", past, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a directory was made for a history that is off")
	}
}

// The day is kept with what the map was serving over it, and that is the
// markers' own store: nothing else in the service knows them.
func TestADayIsKeptWithTheMarkersTheMapIsServing(t *testing.T) {
	maps, r := drawnMaps(t)
	marked := markers.NewStore()
	cfg := config.Config{History: true, HistoryDir: t.TempDir(), UnminedSHA256: "aaaa", NetherTopY: 100}
	past, err := openHistory(cfg, maps, r, marked, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 12, 0, 20, 0, 0, time.UTC)

	// Before the first scan there is nothing true to keep.
	first, err := past.Capture(t.Context(), at)
	if err != nil || first.Tiles != 1 || first.Markers != 0 {
		t.Fatalf("a capture before any scan = %+v, %v; want the day kept without markers", first, err)
	}

	marked.Set(at.Add(24*time.Hour), markers.World{chunks.Overworld: {Beds: []markers.Marker{{X: 1, Y: 64, Z: -2}}}})
	second, err := past.Capture(t.Context(), at.Add(24*time.Hour))
	if err != nil || second.Markers != len(render.Dimensions) {
		t.Fatalf("a capture after a scan = %+v, %v; want every dimension's markers", second, err)
	}
	path, ok := past.MarkersOn("2026-10-13", "overworld")
	if !ok {
		t.Fatal("the day has no markers for the overworld")
	}
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if served, _, _ := marked.Dimension("overworld"); string(kept) != string(served) {
		t.Errorf("kept %s, and the map was serving %s", kept, served)
	}
	if days := past.Days(); days[1].Renderer.SHA256 != "aaaa" || days[1].Renderer.NetherTopY != 100 {
		t.Errorf("the day's renderer = %+v", days[1].Renderer)
	}
}

func TestWithMarkersOffADayIsKeptWithoutThem(t *testing.T) {
	maps, r := drawnMaps(t)
	past, err := openHistory(config.Config{History: true, HistoryDir: t.TempDir()}, maps, r, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := past.Capture(t.Context(), time.Date(2026, 10, 12, 0, 20, 0, 0, time.UTC))
	if err != nil || kept.Tiles != 1 || kept.Markers != 0 {
		t.Fatalf("capture = %+v, %v", kept, err)
	}
}
