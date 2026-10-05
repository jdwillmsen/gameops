package config

import (
	"strings"
	"testing"
)

func TestLoad_Markers(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Markers || c.AgentURL != "" {
		t.Errorf("defaults: markers %v, agent %q; want on and none", c.Markers, c.AgentURL)
	}
	c, err = Load(with("MARKERS_ENABLED", "false", "AGENT_URL", "http://agent:8080"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Markers || c.AgentURL != "http://agent:8080" {
		t.Errorf("set: markers %v, agent %q", c.Markers, c.AgentURL)
	}
	for name, getenv := range map[string]func(string) string{
		"MARKERS_ENABLED": with("MARKERS_ENABLED", "sometimes"),
		"AGENT_URL":       with("AGENT_URL", "agent:8080"),
		// Waypoints are a player's own, and the agent is asked with the
		// internal token: neither exists without the login.
		"AGENT_URL needs the login": env("BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB", "AUTH_DISABLED", "true", "AGENT_URL", "http://agent:8080"),
	} {
		if _, err := Load(getenv); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
