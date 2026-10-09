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

func TestLoad_Live(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Live || c.LivePollWait != 2*time.Second || c.LiveTTL != 10*time.Second || c.LiveKeepalive != 15*time.Second || c.LiveMaxEntities != 1000 {
		t.Errorf("live defaults = %+v", c)
	}
	c, err = Load(with("LIVE_ENABLED", "false", "LIVE_POLL_WAIT", "25s", "LIVE_TTL", "30s", "LIVE_KEEPALIVE", "20s", "LIVE_MAX_ENTITIES", "250"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Live || c.LivePollWait != 25*time.Second || c.LiveTTL != 30*time.Second || c.LiveKeepalive != 20*time.Second || c.LiveMaxEntities != 250 {
		t.Errorf("live overrides = %+v", c)
	}
}

// The load balancer cuts a connection that is silent for 30 seconds. A
// keepalive at or past that would drop every quiet stream, so it is not a
// value the setting accepts.
func TestLoad_LiveKeepaliveStaysUnderThePathsIdleTimeout(t *testing.T) {
	for _, v := range []string{"21s", "29s", "30s", "31s", "1m", "0s", "-5s", "500ms", "15"} {
		if _, err := Load(with("LIVE_KEEPALIVE", v)); err == nil {
			t.Errorf("accepted LIVE_KEEPALIVE=%s", v)
		}
	}
	for _, v := range []string{"1s", "15s", "20s"} {
		if _, err := Load(with("LIVE_KEEPALIVE", v)); err != nil {
			t.Errorf("LIVE_KEEPALIVE=%s: %v", v, err)
		}
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
		"live enabled not a boolean":    with("LIVE_ENABLED", "on please"),
		"live poll wait not a duration": with("LIVE_POLL_WAIT", "2"),
		"live poll wait zero":           with("LIVE_POLL_WAIT", "0s"),
		"live poll wait past the cap":   with("LIVE_POLL_WAIT", "26s"),
		"live ttl not a duration":       with("LIVE_TTL", "ten"),
		"live ttl under a sample":       with("LIVE_TTL", "500ms"),
		"live ttl absurd":               with("LIVE_TTL", "24h"),
		"live max entities zero":        with("LIVE_MAX_ENTITIES", "0"),
		"live max entities negative":    with("LIVE_MAX_ENTITIES", "-1"),
		"live max entities not a count": with("LIVE_MAX_ENTITIES", "lots"),
		"live max entities absurd":      with("LIVE_MAX_ENTITIES", "1000000"),
		// A bad value is refused even with the layer off, so turning it on
		// later is not where the typo is found.
		"live off with a bad ttl": with("LIVE_ENABLED", "false", "LIVE_TTL", "ten"),
	}
	for name, getenv := range cases {
		if _, err := Load(getenv); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLoad_Structures(t *testing.T) {
	c, err := Load(with())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Structures || c.StructureSeed != nil || !c.StructureSeedSearch {
		t.Errorf("defaults: structures %v, seed %v, search %v", c.Structures, c.StructureSeed, c.StructureSeedSearch)
	}
	c, err = Load(with("STRUCTURES_ENABLED", "false", "STRUCTURE_SEED", "4294967295", "STRUCTURE_SEED_SEARCH", "false"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Structures || c.StructureSeed == nil || *c.StructureSeed != 4294967295 || c.StructureSeedSearch {
		t.Errorf("overrides: structures %v, seed %v", c.Structures, c.StructureSeed)
	}
	// Zero is a seed, and must not be read as "none given".
	if c, err = Load(with("STRUCTURE_SEED", "0")); err != nil || c.StructureSeed == nil || *c.StructureSeed != 0 {
		t.Errorf("a seed of 0: %v, %v", c.StructureSeed, err)
	}
	// A whole world seed is cut to the half the game places structures by,
	// and a newline from the file it was read out of is not part of it.
	for raw, want := range map[string]uint32{
		"4294967296":            0,
		"3000000000000000001\n": 3000000000000000001 & 0xffffffff,
		"-5":                    0xfffffffb,
		" 7 ":                   7,
	} {
		if c, err = Load(with("STRUCTURE_SEED", raw)); err != nil || c.StructureSeed == nil || *c.StructureSeed != want {
			t.Errorf("a world seed: %v, %v, want %d", c.StructureSeed, err, want)
		}
	}
	for name, getenv := range map[string]func(string) string{
		"enabled not a boolean": with("STRUCTURES_ENABLED", "maybe"),
		"search not a boolean":  with("STRUCTURE_SEED_SEARCH", "maybe"),
		"seed over 64 bits":     with("STRUCTURE_SEED", "9223372036854775808"),
		"seed not a number":     with("STRUCTURE_SEED", "0x1234"),
	} {
		_, err := Load(getenv)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "9223372036854775808") || strings.Contains(err.Error(), "0x1234") {
			t.Errorf("%s: the refusal repeats the value: %v", name, err)
		}
	}
}
