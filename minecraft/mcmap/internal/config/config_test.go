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

var required = []string{"BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB", "INTERNAL_TOKEN", "internal-token-0123456789"}

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
	if c.InternalAddr != ":9090" || !c.Login || c.InternalToken != "internal-token-0123456789" || c.SessionTTL != 7*24*time.Hour {
		t.Errorf("login defaults = %+v", c)
	}
}

// The map shows where every base is. Forgetting the login's settings must
// stop the service, not serve the map to whoever finds it; running open has
// to be asked for by name.
func TestLoad_LoginIsRequiredUnlessTurnedOffExplicitly(t *testing.T) {
	bare := []string{"BRIDGE_URL", "http://bridge:8766", "BRIDGE_TOKEN", "tok", "LEVEL_NAME", "FWB"}
	if _, err := Load(env(bare...)); err == nil {
		t.Error("started with no login and no explicit AUTH_DISABLED")
	}
	if _, err := Load(env(append(bare, "INTERNAL_TOKEN", "short")...)); err == nil {
		t.Error("accepted a 5-character internal token")
	}
	c, err := Load(env(append(bare, "AUTH_DISABLED", "true")...))
	if err != nil || c.Login {
		t.Errorf("AUTH_DISABLED=true: %+v, %v", c, err)
	}
	// With no login the token still guards the internal API, which can clear
	// the lost-chunks alert, so a short one is refused there too.
	if _, err := Load(env(append(bare, "AUTH_DISABLED", "true", "INTERNAL_TOKEN", "short")...)); err == nil {
		t.Error("accepted a short INTERNAL_TOKEN with the login off")
	}
	if _, err := Load(env(append(bare, "AUTH_DISABLED", "yes please")...)); err == nil {
		t.Error("accepted AUTH_DISABLED with a value that is not a boolean")
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
		"no bridge url":              with("BRIDGE_URL", ""),
		"no bridge token":            with("BRIDGE_TOKEN", ""),
		"no level name":              with("LEVEL_NAME", ""),
		"bridge url not http":        with("BRIDGE_URL", "bridge:8766"),
		"session ttl not a duration": with("SESSION_TTL", "30d"),
		"level name with a slash":    with("LEVEL_NAME", "../FWB"),
		"refresh not a duration":     with("REFRESH_INTERVAL", "15"),
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
