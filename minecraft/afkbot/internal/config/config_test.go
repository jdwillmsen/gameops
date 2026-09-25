package config

import (
	"strings"
	"testing"

	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

// setRequired supplies the two variables Load refuses to run without, so each
// test can speak only about the one it is actually exercising.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "bot")
}

// The regression this package exists to prevent. MC_VIEW_DISTANCE=256 passed
// the old `n > 0` check, was stored as int32, and reached the packet's uint8
// MaxChunkRadius as 0 -- so the bot connected, reported healthy and requested a
// chunk radius of zero. Nothing observed the failure until someone noticed the
// mob farms had stopped.
//
// ViewDistance is a uint8 now, so 256 cannot be represented rather than merely
// being refused. This test outlives that change on purpose: it pins the
// behaviour at the boundary, not the mechanism enforcing it.
func TestLoadRejectsViewDistanceThatWouldWrapToZero(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_VIEW_DISTANCE", "256")

	_, err := Load()
	if err == nil {
		t.Fatal("MC_VIEW_DISTANCE=256 was accepted; it used to wrap to a chunk radius of 0")
	}
	if !strings.Contains(err.Error(), "MC_VIEW_DISTANCE") {
		t.Fatalf("error should name the offending variable, got %q", err)
	}
}

// 255 and 256 fail by different routes now -- 256 cannot be parsed into a
// uint8 at all, while 255 parses and is then refused for exceeding
// maxViewDistance. Both must fail, and a change that collapsed one into the
// other would be invisible to a test that only tried 256.
func TestLoadRejectsViewDistanceAboveCeilingButWithinUint8(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_VIEW_DISTANCE", "255")

	if _, err := Load(); err == nil {
		t.Fatal("MC_VIEW_DISTANCE=255 was accepted; it is within uint8 but above the ceiling")
	}
}

// Every bound is checked at its edge rather than well past it: an off-by-one
// in the comparison is the failure this table is shaped to catch.
func TestLoadBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		value   string
		wantErr bool
	}{
		{"view distance at the ceiling", "MC_VIEW_DISTANCE", "64", false},
		{"view distance above the ceiling", "MC_VIEW_DISTANCE", "65", true},
		{"port at the ceiling", "MC_PORT", "65535", false},
		{"port above the ceiling", "MC_PORT", "65536", true},
		{"backoff at the ceiling", "RECONNECT_MAX_MS", "3600000", false},
		{"backoff above the ceiling", "RECONNECT_MAX_MS", "3600001", true},
		{"zero is still refused", "MC_VIEW_DISTANCE", "0", true},
		{"negative is still refused", "MC_VIEW_DISTANCE", "-1", true},
		{"non-numeric is still refused", "MC_VIEW_DISTANCE", "twelve", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tc.env, tc.value)

			_, err := Load()
			if tc.wantErr && err == nil {
				t.Fatalf("%s=%s was accepted, expected a refusal", tc.env, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("%s=%s was refused: %v", tc.env, tc.value, err)
			}
		})
	}
}

// An unset variable still falls back to its default, which is the path every
// real deployment takes -- the chart sets none of these.
func TestLoadDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with only the required variables set: %v", err)
	}
	if cfg.ViewDistance != maxTickDistance {
		t.Errorf("ViewDistance = %d, want the %d default", cfg.ViewDistance, maxTickDistance)
	}
	if cfg.Port != 19132 {
		t.Errorf("Port = %d, want the 19132 default", cfg.Port)
	}
}

// The ordering check must survive the new bound: both values are legal on
// their own, and only their relationship is wrong.
func TestLoadRejectsBackoffCeilingBelowFloor(t *testing.T) {
	setRequired(t)
	t.Setenv("RECONNECT_MIN_MS", "10000")
	t.Setenv("RECONNECT_MAX_MS", "5000")

	if _, err := Load(); err == nil {
		t.Fatal("RECONNECT_MAX_MS below RECONNECT_MIN_MS was accepted")
	}
}

// setPresence supplies a complete, valid presence configuration so each test
// can break exactly one variable.
func setPresence(t *testing.T) {
	t.Helper()
	t.Setenv("PRESENCE_URL", "http://fwb-server-agent:8080/")
	t.Setenv("PRESENCE_TOKEN", "ZZZZZZZZZZZZZZZZZZZZ")
	t.Setenv("PRESENCE_ACTOR_ID", "afk-bot-1")
	t.Setenv("PRESENCE_DEFAULT", "present")
}

// Feature off must mean today's bot. A deployment without the agent may still
// carry a stray PRESENCE_* variable, and that must not stop the bot starting.
func TestLoadPresenceOffWhenURLUnset(t *testing.T) {
	setRequired(t)
	t.Setenv("PRESENCE_DEFAULT", "bogus")
	t.Setenv("PRESENCE_POLL_MS", "not-a-number")
	t.Setenv("PRESENCE_ACTOR_ID", "Not Valid")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with PRESENCE_URL unset refused other PRESENCE_* variables: %v", err)
	}
	if cfg.Presence.Enabled() {
		t.Error("presence enabled with PRESENCE_URL unset")
	}
	if cfg.Presence != (Presence{}) {
		t.Errorf("Presence = %+v, want the zero value", cfg.Presence)
	}
}

func TestLoadPresenceEnabled(t *testing.T) {
	setRequired(t)
	setPresence(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Presence{
		URL:     "http://fwb-server-agent:8080",
		Token:   "ZZZZZZZZZZZZZZZZZZZZ",
		ActorID: "afk-bot-1",
		Default: presenceapi.StatePresent,
		PollMs:  10000,
	}
	if cfg.Presence != want {
		t.Errorf("Presence = %+v, want %+v", cfg.Presence, want)
	}
	if !cfg.Presence.Enabled() {
		t.Error("presence not enabled with PRESENCE_URL set")
	}
}

// With the feature on, a missing or malformed setting would leave the bot
// guessing whether it may connect, so each one fails startup instead.
func TestLoadPresenceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		value string
	}{
		{"token missing", "PRESENCE_TOKEN", ""},
		{"actor id missing", "PRESENCE_ACTOR_ID", ""},
		{"actor id not a slug", "PRESENCE_ACTOR_ID", "AFK_Bot"},
		{"actor id too long", "PRESENCE_ACTOR_ID", "a" + strings.Repeat("b", 63)},
		{"default missing", "PRESENCE_DEFAULT", ""},
		{"default not a state", "PRESENCE_DEFAULT", "absent"},
		{"url not http", "PRESENCE_URL", "ftp://fwb-server-agent"},
		{"url without host", "PRESENCE_URL", "http://"},
		{"poll zero", "PRESENCE_POLL_MS", "0"},
		{"poll above the ceiling", "PRESENCE_POLL_MS", "600001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			setPresence(t)
			t.Setenv(tc.env, tc.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("%s=%q was accepted", tc.env, tc.value)
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Errorf("error should name %s, got %q", tc.env, err)
			}
		})
	}
}

func TestLoadPresencePollAtTheCeiling(t *testing.T) {
	setRequired(t)
	setPresence(t)
	t.Setenv("PRESENCE_POLL_MS", "600000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("PRESENCE_POLL_MS=600000 was refused: %v", err)
	}
	if cfg.Presence.PollMs != 600000 {
		t.Errorf("PollMs = %d, want 600000", cfg.Presence.PollMs)
	}
}

// Startup errors land in the pod log; the token must not.
func TestLoadPresenceErrorsNeverEchoTheToken(t *testing.T) {
	for _, env := range []string{"PRESENCE_ACTOR_ID", "PRESENCE_DEFAULT"} {
		t.Run(env, func(t *testing.T) {
			setRequired(t)
			setPresence(t)
			t.Setenv(env, "ZZZZZZZZZZZZZZZZZZZZ")

			_, err := Load()
			if err == nil {
				t.Fatalf("%s set to the token was accepted", env)
			}
			if strings.Contains(err.Error(), "ZZZZZZZZZZZZZZZZZZZZ") {
				t.Errorf("error echoes a value that was set to the token: %q", err)
			}
		})
	}
}
