package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/trails"
)

type trailsAnswer struct {
	Players []struct {
		Name     string       `json:"name"`
		Segments [][][4]int64 `json:"segments"`
	} `json:"players"`
	More          int `json:"more"`
	MaxAgeSeconds int `json:"maxAgeSeconds"`
	MaxPoints     int `json:"maxPoints"`
	Thinning      []struct {
		OlderThanSeconds int `json:"olderThanSeconds"`
		StepBlocks       int `json:"stepBlocks"`
	} `json:"thinning"`
}

func withTrails(t *testing.T) (*Server, time.Time) {
	t.Helper()
	s, _ := fixture(t)
	r := trails.New(time.Hour, 500)
	// Recent, since the handler reads the clock.
	began := time.Now().Add(-time.Minute).Truncate(time.Second)
	for i := range 6 {
		at := began.Add(time.Duration(i) * time.Second)
		r.Record("overworld", at, []live.Entity{
			{ID: "1", Name: "Dotablaze", X: float64(i * 10), Y: 64, Z: -5.5},
			{ID: "2", Name: "Steve", X: 900, Y: 70, Z: float64(i * 10)},
		})
	}
	r.Record("nether", began, []live.Entity{{ID: "3", Name: "Alex", X: 1, Y: 40, Z: 1}})
	s.Trails = r
	return s, began
}

func TestTrailsServeEachPlayersLinesInADimension(t *testing.T) {
	s, began := withTrails(t)
	rec := get(t, s, "/api/trails?dimension=overworld")
	got := decodeBody[trailsAnswer](t, rec)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if got.MaxAgeSeconds != 3600 || got.MaxPoints != 500 || got.More != 0 || len(got.Players) != 2 {
		t.Fatalf("answer = %+v", got)
	}
	if len(got.Thinning) != 1 || got.Thinning[0].OlderThanSeconds != 0 || got.Thinning[0].StepBlocks != 4 {
		t.Errorf("thinning = %+v, want only full detail, as nothing is held past an hour", got.Thinning)
	}
	first := got.Players[0]
	if first.Name != "Dotablaze" || len(first.Segments) != 1 || len(first.Segments[0]) != 6 {
		t.Fatalf("first trail = %+v", first)
	}
	// Each point is t, x, y, z, and -5.5 is in block -6.
	if p := first.Segments[0][5]; p != [4]int64{began.Unix() + 5, 50, 64, -6} {
		t.Errorf("last point = %v", p)
	}
	if got.Players[1].Name != "Steve" {
		t.Errorf("second trail is %s", got.Players[1].Name)
	}

	nether := decodeBody[trailsAnswer](t, get(t, s, "/api/trails?dimension=nether"))
	if len(nether.Players) != 1 || nether.Players[0].Name != "Alex" {
		t.Errorf("nether = %+v", nether.Players)
	}
	if end := get(t, s, "/api/trails?dimension=end"); !strings.Contains(end.Body.String(), `"players":[]`) {
		t.Errorf("the end = %s, want an empty list", end.Body)
	}

	one := decodeBody[trailsAnswer](t, get(t, s, "/api/trails?dimension=overworld&player=steve"))
	if len(one.Players) != 1 || one.Players[0].Name != "Steve" {
		t.Errorf("player=steve: %+v", one.Players)
	}
	recent := decodeBody[trailsAnswer](t, get(t, s, fmt.Sprintf("/api/trails?dimension=overworld&player=Steve&since=%d", began.Unix()+3)))
	if len(recent.Players) != 1 || len(recent.Players[0].Segments[0]) != 2 {
		t.Errorf("since the fourth point: %+v", recent.Players)
	}

	for _, path := range []string{
		"/api/trails",
		"/api/trails?dimension=aether",
		"/api/trails?dimension=overworld&since=yesterday",
		"/api/trails?dimension=overworld&since=-1",
		"/api/trails?dimension=overworld&player=" + strings.Repeat("x", 65),
	} {
		if rec := get(t, s, path); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}
