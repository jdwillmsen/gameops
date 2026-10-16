package config

import (
	"strings"
	"testing"
)

func TestLoad_Layers(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.Layers || c.LayersCacheMB != 64 || c.LayersMaxRenders != 2 {
		t.Errorf("defaults: %v, %d MB, %d renders", c.Layers, c.LayersCacheMB, c.LayersMaxRenders)
	}
	c, err = Load(with("LAYERS_ENABLED", "true", "LAYERS_CACHE_MB", "128", "LAYERS_MAX_RENDERS", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Layers || c.LayersCacheMB != 128 || c.LayersMaxRenders != 1 {
		t.Errorf("set: %v, %d MB, %d renders", c.Layers, c.LayersCacheMB, c.LayersMaxRenders)
	}
	for name, getenv := range map[string]func(string) string{
		"LAYERS_ENABLED":     with("LAYERS_ENABLED", "maybe"),
		"LAYERS_CACHE_MB":    with("LAYERS_ENABLED", "true", "LAYERS_CACHE_MB", "0"),
		"LAYERS_CACHE_MB ":   with("LAYERS_ENABLED", "true", "LAYERS_CACHE_MB", "2000"),
		"LAYERS_MAX_RENDERS": with("LAYERS_ENABLED", "true", "LAYERS_MAX_RENDERS", "9"),
	} {
		if _, err := Load(getenv); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(name)) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A feature that is off does not fail startup over its limits.
	if _, err := Load(with("LAYERS_CACHE_MB", "lots")); err != nil {
		t.Errorf("the limits of the feature off were checked: %v", err)
	}
}
