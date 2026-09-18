package config

import (
	"strings"
	"testing"
)

// setRequired supplies the two variables Load refuses to run without, so each
// test can speak only about the one it is actually exercising.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "bot")
}

// The regression this package exists to prevent. MC_VIEW_DISTANCE=256 passed
// the old `n > 0` check, was stored as int32, and reached
// `uint8(cfg.ViewDistance)` as 0 -- so the bot connected, reported healthy and
// requested a chunk radius of zero. Nothing observed the failure until someone
// noticed the mob farms had stopped.
func TestLoadRejectsViewDistanceThatWouldWrapToZero(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_VIEW_DISTANCE", "256")

	_, err := Load()
	if err == nil {
		t.Fatal("MC_VIEW_DISTANCE=256 was accepted; it wraps to a chunk radius of 0 at the uint8 conversion")
	}
	if !strings.Contains(err.Error(), "MC_VIEW_DISTANCE") {
		t.Fatalf("error should name the offending variable, got %q", err)
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
