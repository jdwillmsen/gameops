package trails

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
)

func at(r *Recorder, dimension string, when time.Time, x, z float64) {
	r.Record(dimension, when, []live.Entity{player("Alex", x, z)})
}

func TestStatsCountTimeAndDistanceFromEverySample(t *testing.T) {
	r := New(7*24*time.Hour, 1000)
	// Standing still keeps no points past the first, yet is time online.
	for i := range 11 {
		at(r, "overworld", start.Add(time.Duration(i)*10*time.Second), 5, 5)
	}
	// Then ten seconds of walking, ten blocks a second.
	walkAt := start.Add(110 * time.Second)
	for i := range 10 {
		at(r, "overworld", walkAt.Add(time.Duration(i)*time.Second), 5+float64(i)*10, 5)
	}
	got, ok := r.Stats("alex", walkAt.Add(10*time.Second))
	if !ok {
		t.Fatal("no stats for a player who was seen")
	}
	if got.OnlineSeconds != 100+10+9 {
		t.Errorf("online = %ds, want 119", got.OnlineSeconds)
	}
	if got.Blocks != 90 {
		t.Errorf("blocks = %d, want 90", got.Blocks)
	}
	if got.Name != "Alex" || got.LastDimension != "overworld" || got.LastSeen != walkAt.Add(9*time.Second).Unix() {
		t.Errorf("last seen = %+v", got)
	}
	if got.GapSeconds != 120 || got.WindowSeconds != 7*24*3600 {
		t.Errorf("window = %+v", got)
	}
}

func TestStatsDoNotCountASilenceLongerThanTheGapAsTimeOnline(t *testing.T) {
	r := New(time.Hour, 100)
	at(r, "overworld", start, 0, 0)
	at(r, "overworld", start.Add(OnlineGap), 0, 0)
	at(r, "overworld", start.Add(OnlineGap+OnlineGap+time.Second), 0, 0)
	got, _ := r.Stats("Alex", start.Add(5*time.Minute))
	if got.OnlineSeconds != int(OnlineGap.Seconds()) {
		t.Errorf("online = %ds, want only the first %v", got.OnlineSeconds, OnlineGap)
	}
}

func TestStatsLeaveAPortalOrATeleportOutOfTheDistance(t *testing.T) {
	r := New(time.Hour, 100)
	at(r, "overworld", start, 0, 0)
	at(r, "overworld", start.Add(time.Second), 10, 0)
	at(r, "overworld", start.Add(2*time.Second), 5000, 0)
	at(r, "nether", start.Add(3*time.Second), 625, 0)
	got, _ := r.Stats("Alex", start.Add(10*time.Second))
	if got.Blocks != 10 {
		t.Errorf("blocks = %d, want 10", got.Blocks)
	}
	if len(got.Dimensions) != 2 || got.Dimensions[0] != "overworld" || got.Dimensions[1] != "nether" || got.LastDimension != "nether" {
		t.Errorf("dimensions = %v, last %q", got.Dimensions, got.LastDimension)
	}
}

func TestStatsKeepEachDayApartAndLetGoOfThoseOutsideTheRetention(t *testing.T) {
	r := New(48*time.Hour, 1000)
	day1 := time.Date(2026, 10, 3, 23, 59, 0, 0, time.UTC)
	for i := range 3 {
		at(r, "overworld", day1.Add(time.Duration(i)*time.Second*30), float64(i*10), 0)
	}
	day2 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	at(r, "overworld", day2, 0, 0)
	at(r, "overworld", day2.Add(time.Minute), 0, 0)
	got, _ := r.Stats("Alex", day2.Add(time.Minute))
	if len(got.Days) != 2 || got.Days[0].Date != "2026-10-03" || got.Days[1].Date != "2026-10-04" {
		t.Fatalf("days = %+v", got.Days)
	}
	if got.Days[1].OnlineSeconds != 90 || got.Days[0].Blocks != 10 {
		t.Errorf("days = %+v", got.Days)
	}
	// A silence is counted in the day it ends in, so the half minute that
	// crosses midnight is the second day's.
	// Days later, the first has aged out and the player is not held at all
	// once their points have.
	if later, ok := r.Stats("Alex", day2.Add(49*time.Hour)); ok {
		t.Errorf("stats of a trail that aged out: %+v", later)
	}
}

func TestStatsOfAPlayerNeverSeenAreAbsent(t *testing.T) {
	if _, ok := New(time.Hour, 100).Stats("Herobrine", start); ok {
		t.Error("stats for nobody")
	}
}
