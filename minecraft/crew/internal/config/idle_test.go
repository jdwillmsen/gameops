package config

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
)

func setIdle(t *testing.T) {
	t.Helper()
	setRequired(t)
	t.Setenv("CREW_MODE", "idle")
}

func TestModeDefaultsToWork(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeWork || cfg.Idle() {
		t.Errorf("mode = %q, want work", cfg.Mode)
	}
}

func TestModeRejectsAnUnknownValue(t *testing.T) {
	setRequired(t)
	t.Setenv("CREW_MODE", "afk")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CREW_MODE") {
		t.Fatalf("err = %v, want one naming CREW_MODE", err)
	}
}

// Several behaviours default to on for a worker, so idle mode has to turn
// them off rather than merely refuse an explicit true.
func TestIdleModeForcesEveryBehaviourOff(t *testing.T) {
	setIdle(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Idle() {
		t.Fatal("CREW_MODE=idle did not select idle mode")
	}
	if cfg.IdleWander || cfg.Defend || cfg.Eat || cfg.RecoverDrops || cfg.NightShelter ||
		cfg.InvManage || cfg.KitGear || cfg.Combat.Baseline {
		t.Errorf("behaviours left on in idle mode: %+v", cfg)
	}
}

func TestIdleModeAcceptsBehavioursSetToFalse(t *testing.T) {
	setIdle(t)
	for _, name := range idleBehaviours {
		t.Setenv(name, "false")
	}
	if _, err := Load(); err != nil {
		t.Fatalf("explicit false refused: %v", err)
	}
}

func TestIdleModeRefusesABehaviourTurnedOn(t *testing.T) {
	for _, name := range idleBehaviours {
		t.Run(name, func(t *testing.T) {
			setIdle(t)
			t.Setenv(name, "true")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

func TestIdleModeRefusesWorkSettings(t *testing.T) {
	values := map[string]string{
		"TASKS_TOKEN": "t", "WORK_PLAN": "[]", "CONTROL_URL": "http://control", "CREW_ROLE": "vanguard",
		"CREW_SQUAD": "alpha", "CREW_HARNESS": "false", "CREW_LAB": "false", "KIT_CHEST": "1,2,3",
		"INV_CHEST": "1,2,3", "HOME_POS": "1,2,3",
	}
	for _, name := range idleRefused {
		t.Run(name, func(t *testing.T) {
			setIdle(t)
			t.Setenv(name, values[name])
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

func TestIdleModeViewDistanceDefaultsToTwelveAndKeepsTheCeiling(t *testing.T) {
	setIdle(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ViewDistance != 12 {
		t.Errorf("view distance = %d, want 12", cfg.ViewDistance)
	}
	t.Setenv("MC_VIEW_DISTANCE", "33")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MC_VIEW_DISTANCE") {
		t.Errorf("33 accepted in idle mode: %v", err)
	}
}

func TestWorkModeRefusesIdleSettings(t *testing.T) {
	for _, name := range idleOnly {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "1")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

// setPresence supplies a complete, valid presence configuration so each test
// can break exactly one variable.
func setPresence(t *testing.T) {
	t.Helper()
	t.Setenv("PRESENCE_URL", "http://fwb-server-agent:8080/")
	t.Setenv("PRESENCE_TOKEN", "Fake-Presence-Tok")
	t.Setenv("PRESENCE_ACTOR_ID", "afk-bot-1")
	t.Setenv("PRESENCE_DEFAULT", "present")
}

// A deployment without the agent may still carry a stray PRESENCE_*
// variable, and that must not stop the unit starting.
func TestLoadPresenceOffWhenURLUnset(t *testing.T) {
	setIdle(t)
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
	setIdle(t)
	setPresence(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Presence{
		URL:     "http://fwb-server-agent:8080",
		Token:   "Fake-Presence-Tok",
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

// With the feature on, a missing or malformed setting would leave the unit
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
		{"poll below the floor", "PRESENCE_POLL_MS", "999"},
		{"poll above the ceiling", "PRESENCE_POLL_MS", "600001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setIdle(t)
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

func TestLoadPresencePollAtTheBounds(t *testing.T) {
	for _, v := range []string{"1000", "600000"} {
		t.Run(v, func(t *testing.T) {
			setIdle(t)
			setPresence(t)
			t.Setenv("PRESENCE_POLL_MS", v)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("PRESENCE_POLL_MS=%s was refused: %v", v, err)
			}
			if got := cfg.Presence.PollMs; got == 0 || v != strconv.Itoa(got) {
				t.Errorf("PollMs = %d, want %s", got, v)
			}
		})
	}
}

// A password embedded in PRESENCE_URL must never reach the pod log through
// the config error that names the rejected value.
func TestLoadPresenceURLErrorRedactsUserinfo(t *testing.T) {
	setIdle(t)
	setPresence(t)
	t.Setenv("PRESENCE_URL", "ftp://bot:Fake-Presence-Tok@fwb-server-agent")

	_, err := Load()
	if err == nil {
		t.Fatal("ftp:// PRESENCE_URL was accepted")
	}
	if strings.Contains(err.Error(), "Fake-Presence-Tok") {
		t.Errorf("error echoes the URL's password: %q", err)
	}
	if !strings.Contains(err.Error(), "PRESENCE_URL") {
		t.Errorf("error should name PRESENCE_URL, got %q", err)
	}
}

// Startup errors land in the pod log; the token must not.
func TestLoadPresenceErrorsNeverEchoTheToken(t *testing.T) {
	for _, env := range []string{"PRESENCE_ACTOR_ID", "PRESENCE_DEFAULT"} {
		t.Run(env, func(t *testing.T) {
			setIdle(t)
			setPresence(t)
			t.Setenv(env, "Fake-Presence-Tok")

			_, err := Load()
			if err == nil {
				t.Fatalf("%s set to the token was accepted", env)
			}
			if strings.Contains(err.Error(), "Fake-Presence-Tok") {
				t.Errorf("error echoes a value that was set to the token: %q", err)
			}
		})
	}
}

func TestParseFacing(t *testing.T) {
	cases := []struct {
		in   string
		yaw  float32
		set  bool
		fail bool
	}{
		{"", 0, false, false},
		{"west", 90, true, false},
		{" North ", 180, true, false},
		{"east", 270, true, false},
		{"south", 0, true, false},
		{"45.5", 45.5, true, false},
		{"360", 0, false, true},
		{"-1", 0, false, true},
		{"up", 0, false, true},
	}
	for _, c := range cases {
		yaw, set, err := parseFacing(c.in)
		if (err != nil) != c.fail || yaw != c.yaw || set != c.set {
			t.Errorf("parseFacing(%q) = %v, %v, %v", c.in, yaw, set, err)
		}
	}
}

func TestFacingIsReadInIdleMode(t *testing.T) {
	setIdle(t)
	t.Setenv("FACING", "north")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasFacing || cfg.Facing != 180 {
		t.Errorf("facing = %v %v, want 180", cfg.HasFacing, cfg.Facing)
	}
	t.Setenv("FACING", "up")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FACING") {
		t.Errorf("err = %v, want one naming FACING", err)
	}
}

func TestStowAndSeatDefaultOff(t *testing.T) {
	setIdle(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Stow || cfg.HasSeat || cfg.HasFacing {
		t.Fatalf("stow=%v hasSeat=%v hasFacing=%v, want all off", cfg.Stow, cfg.HasSeat, cfg.HasFacing)
	}
}

func TestStowAndSeatParse(t *testing.T) {
	setIdle(t)
	t.Setenv("STOW", "true")
	t.Setenv("SEAT", "10, 64.5,-3")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stow || !cfg.HasSeat || cfg.Seat != [3]float32{10, 64.5, -3} {
		t.Fatalf("cfg = stow %v seat %v %v", cfg.Stow, cfg.HasSeat, cfg.Seat)
	}
}

func TestSeatBringsTheStow(t *testing.T) {
	setIdle(t)
	t.Setenv("SEAT", "1,2,3")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stow {
		t.Error("SEAT without STOW left the hands unknown")
	}
}

func TestStowAndSeatRejectGarbage(t *testing.T) {
	for _, tc := range []struct{ env, value string }{
		{"STOW", "maybe"}, {"SEAT", "1,2"}, {"SEAT", "1,2,x"}, {"SEAT", "1,2,NaN"},
	} {
		t.Run(tc.env+"="+tc.value, func(t *testing.T) {
			setIdle(t)
			t.Setenv(tc.env, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.env) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// A seat on FWB is a base's location, so its error leaves the value out.
func TestSeatErrorDoesNotEchoThePosition(t *testing.T) {
	setIdle(t)
	t.Setenv("SEAT", "1234,64,-5678,9")
	_, err := Load()
	if err == nil || strings.Contains(err.Error(), "1234") {
		t.Errorf("err = %v, want one without the position", err)
	}
}
