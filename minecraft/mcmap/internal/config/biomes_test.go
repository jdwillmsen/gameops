package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_BiomesAndTrails(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.Biomes || c.Trails || c.TrailMaxAge != 24*time.Hour || c.TrailMaxPoints != 5000 {
		t.Errorf("defaults: biomes %v, trails %v for %s and %d points", c.Biomes, c.Trails, c.TrailMaxAge, c.TrailMaxPoints)
	}
	c, err = Load(with("BIOMES_ENABLED", "true", "TRAILS_ENABLED", "true", "TRAILS_MAX_AGE", "90m", "TRAILS_MAX_POINTS", "250"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Biomes || !c.Trails || c.TrailMaxAge != 90*time.Minute || c.TrailMaxPoints != 250 {
		t.Errorf("set: biomes %v, trails %v for %s and %d points", c.Biomes, c.Trails, c.TrailMaxAge, c.TrailMaxPoints)
	}
	// A trail is kept in memory for every player seen, so neither limit
	// can be set to nothing or to anything at all.
	for name, getenv := range map[string]func(string) string{
		"BIOMES_ENABLED":      with("BIOMES_ENABLED", "sometimes"),
		"TRAILS_ENABLED":      with("TRAILS_ENABLED", "sometimes"),
		"TRAILS_MAX_AGE":      with("TRAILS_ENABLED", "true", "TRAILS_MAX_AGE", "forever"),
		"TRAILS_MAX_AGE ":     with("TRAILS_ENABLED", "true", "TRAILS_MAX_AGE", "30s"),
		"TRAILS_MAX_AGE  ":    with("TRAILS_ENABLED", "true", "TRAILS_MAX_AGE", "169h"),
		"TRAILS_MAX_POINTS":   with("TRAILS_ENABLED", "true", "TRAILS_MAX_POINTS", "9"),
		"TRAILS_MAX_POINTS ":  with("TRAILS_ENABLED", "true", "TRAILS_MAX_POINTS", "50001"),
		"TRAILS_MAX_POINTS  ": with("TRAILS_ENABLED", "true", "TRAILS_MAX_POINTS", "lots"),
	} {
		if _, err := Load(getenv); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(name)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, edge := range [][]string{{"TRAILS_MAX_AGE", "1m"}, {"TRAILS_MAX_AGE", "168h"}, {"TRAILS_MAX_POINTS", "10"}, {"TRAILS_MAX_POINTS", "50000"}} {
		if _, err := Load(with(append([]string{"TRAILS_ENABLED", "true"}, edge...)...)); err != nil {
			t.Errorf("%s=%s is refused: %v", edge[0], edge[1], err)
		}
	}

	// A disabled feature does not fail startup over its limits, and the
	// documented values stay in place.
	c, err = Load(with("TRAILS_MAX_AGE", "forever", "TRAILS_MAX_POINTS", "lots"))
	if err != nil {
		t.Fatalf("limits of disabled trails are checked: %v", err)
	}
	if c.TrailMaxAge != 24*time.Hour || c.TrailMaxPoints != 5000 {
		t.Errorf("disabled trails: %s and %d points", c.TrailMaxAge, c.TrailMaxPoints)
	}
}
