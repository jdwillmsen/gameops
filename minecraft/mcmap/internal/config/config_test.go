package config

import (
	"strings"
	"testing"
	"time"
)

func env(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

var required = []string{"BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB"}

func with(extra ...string) func(string) string {
	return env(append(append([]string{}, required...), extra...)...)
}

func TestLoad_Defaults(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.DataDir != "/data" || c.Refresh != 15*time.Minute ||
		c.ChunkProcessors != 1 || c.NetherTopY != 100 || len(c.Quiet) != 0 ||
		c.UnminedURL != DefaultUnminedURL || c.UnminedSHA256 != DefaultUnminedSHA256 {
		t.Errorf("defaults = %+v", c)
	}
	if c.BridgeURL != "http://bridge:8766" || c.BridgeToken != "tok" || c.Level != "FWB" {
		t.Errorf("required values = %+v", c)
	}
}

func TestLoad_Overrides(t *testing.T) {
	c, err := Load(with("REFRESH_INTERVAL", "5m", "QUIET_UTC", "03:50-05:10,05:30-06:10", "RENDER_CHUNK_PROCESSORS", "4",
		"NETHER_TOP_Y", "90", "UNMINED_SHA256", strings.Repeat("a", 64), "HTTP_ADDR", ":9000", "DATA_DIR", "/var/map"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Refresh != 5*time.Minute || len(c.Quiet) != 2 || c.ChunkProcessors != 4 || c.NetherTopY != 90 ||
		c.UnminedSHA256 != strings.Repeat("a", 64) || c.HTTPAddr != ":9000" || c.DataDir != "/var/map" {
		t.Errorf("overrides = %+v", c)
	}
}

func TestLoad_Rejects(t *testing.T) {
	cases := map[string]func(string) string{
		"no bridge url":           env("BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB"),
		"no bridge token":         env("BRIDGE_URL", "http://b", "LEVEL_NAME", "FWB"),
		"no level name":           env("BRIDGE_URL", "http://b", "BRIDGE_TOKEN", "tok"),
		"bridge url not http":     env("BRIDGE_URL", "bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB"),
		"level name with a slash": with("LEVEL_NAME", "../FWB"),
		"refresh not a duration":  with("REFRESH_INTERVAL", "15"),
		// Each cycle pauses world saving; a typo must not turn that into a
		// loop that never lets the server write.
		"refresh under a minute":        with("REFRESH_INTERVAL", "10s"),
		"bad quiet window":              with("QUIET_UTC", "4-5"),
		"chunk processors zero":         with("RENDER_CHUNK_PROCESSORS", "0"),
		"chunk processors not a number": with("RENDER_CHUNK_PROCESSORS", "many"),
		"sha256 wrong length":           with("UNMINED_SHA256", "abc"),
		"sha256 not hex":                with("UNMINED_SHA256", strings.Repeat("z", 64)),
		"renderer url not https":        with("UNMINED_URL", "http://unmined.net/x"),
	}
	for name, getenv := range cases {
		if _, err := Load(getenv); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
