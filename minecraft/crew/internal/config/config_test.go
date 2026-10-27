package config

import (
	"strings"
	"testing"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("MC_HOST", "example.invalid")
	t.Setenv("MC_USERNAME", "crew")
}

func TestLoadAppliesDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 19132 || cfg.ViewDistance != 8 || cfg.AuthCacheDir != "/data/auth" ||
		cfg.ReconnectMinMs != 5000 || cfg.ReconnectMaxMs != 300000 {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestLoadRequiresHostAndUsername(t *testing.T) {
	for _, name := range []string{"MC_HOST", "MC_USERNAME"} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "  ")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v, want one naming %s", err, name)
			}
		})
	}
}

// The status API serves positions. Exposing it on every interface must be a
// choice someone writes down, not what an unset variable does.
func TestStatusAddrDefaultsToLoopback(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.StatusAddr != "127.0.0.1:8080" {
		t.Errorf("StatusAddr = %q, want 127.0.0.1:8080", cfg.StatusAddr)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"MC_VIEW_DISTANCE": "256",
		"MC_PORT":          "70000",
		"STATUS_ADDR":      "8080",
		"IDLE_RADIUS":      "0",
		"IDLE_WANDER":      "maybe",
		"DEFEND":           "sometimes",
		"EAT":              "yes please",
		"KIT_CHEST":        "1,2",
		"KIT_FOOD":         "0",
		"CREW_ROLE":        "wizard",
		"CREW_SQUAD":       "9lives",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%s: err = %v, want one naming %s", name, value, err, name)
			}
		})
	}
}

func TestLoadParsesTheWarriorRole(t *testing.T) {
	for _, role := range []string{"", "vanguard", "striker", "ranger"} {
		t.Run("role="+role, func(t *testing.T) {
			setRequired(t)
			t.Setenv("CREW_ROLE", role)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("CREW_ROLE=%q: %v", role, err)
			}
			if string(cfg.Role) != role {
				t.Errorf("Role = %q, want %q", cfg.Role, role)
			}
			if cfg.Role.IsWarrior() != (role != "") || cfg.Role.IsBuilder() {
				t.Errorf("Role %q: IsWarrior=%v IsBuilder=%v", role, cfg.Role.IsWarrior(), cfg.Role.IsBuilder())
			}
		})
	}
}

// A builder role is a different kind of unit, not a fifth warrior: it never
// takes the warrior path or a squad, whatever CREW_SQUAD says.
func TestLoadParsesTheBuilderRoles(t *testing.T) {
	for _, role := range []string{"mender", "builder"} {
		t.Run(role, func(t *testing.T) {
			setRequired(t)
			t.Setenv("CREW_ROLE", role)
			t.Setenv("CREW_LAB", "true")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("CREW_ROLE=%q: %v", role, err)
			}
			if string(cfg.Role) != role || !cfg.Role.IsBuilder() || cfg.Role.IsWarrior() {
				t.Errorf("Role %q: IsBuilder=%v IsWarrior=%v", cfg.Role, cfg.Role.IsBuilder(), cfg.Role.IsWarrior())
			}
			if cfg.Squad != "" {
				t.Errorf("Squad = %q, want none for a builder", cfg.Squad)
			}
			if !cfg.Lab {
				t.Error("Lab = false, want true")
			}
		})
	}
}

func TestLoadRefusesABuilderOffTheLab(t *testing.T) {
	for _, lab := range []string{"", "false", "yes"} {
		t.Run("lab="+lab, func(t *testing.T) {
			setRequired(t)
			t.Setenv("CREW_ROLE", "mender")
			t.Setenv("CREW_LAB", lab)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "CREW_LAB") {
				t.Fatalf("err = %v, want one naming CREW_LAB", err)
			}
		})
	}
}

func TestLoadLetsAWarriorRunOffTheLab(t *testing.T) {
	setRequired(t)
	t.Setenv("CREW_ROLE", "ranger")
	if cfg, err := Load(); err != nil || cfg.Lab {
		t.Fatalf("Load = Lab %v, %v; want a warrior unaffected by CREW_LAB", cfg.Lab, err)
	}
}

func TestLoadNamesAllFiveRolesWhenRefusing(t *testing.T) {
	setRequired(t)
	t.Setenv("CREW_ROLE", "wizard")
	_, err := Load()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, r := range []string{"vanguard", "striker", "ranger", "mender", "builder"} {
		if !strings.Contains(err.Error(), r) {
			t.Errorf("err = %q, want it to name %s", err, r)
		}
	}
}

// A warrior joins a squad without being told which one: the default is the
// design's first squad, so three warriors with no CREW_SQUAD still meet.
func TestLoadDefaultsAWarriorsSquadToAlpha(t *testing.T) {
	setRequired(t)
	t.Setenv("CREW_ROLE", "vanguard")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Squad != "alpha" {
		t.Fatalf("Squad = %q, want the default alpha", cfg.Squad)
	}
	t.Setenv("CREW_SQUAD", "bravo_2")
	if cfg, err = Load(); err != nil || cfg.Squad != "bravo_2" {
		t.Fatalf("CREW_SQUAD=bravo_2: %q, %v; want it carried through", cfg.Squad, err)
	}
	// Without a role there is no squad to join, but a fleet-wide setting is
	// still read, and still refused when it is not an id the routes take.
	t.Setenv("CREW_ROLE", "")
	t.Setenv("CREW_SQUAD", "bravo_2")
	if cfg, err = Load(); err != nil || cfg.Role != "" || cfg.Squad != "bravo_2" {
		t.Fatalf("no role: %q %q, %v; want the squad kept but unused", cfg.Role, cfg.Squad, err)
	}
	for _, bad := range []string{"Alpha", "9lives", "a b", strings.Repeat("x", 40)} {
		t.Setenv("CREW_SQUAD", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CREW_SQUAD") {
			t.Errorf("CREW_SQUAD=%q: err = %v, want one naming CREW_SQUAD", bad, err)
		}
	}
}

func TestLoadRejectsZeroViewDistance(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_VIEW_DISTANCE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("MC_VIEW_DISTANCE=0 was accepted")
	}
}

func TestLoadRejectsInvertedBackoff(t *testing.T) {
	setRequired(t)
	t.Setenv("RECONNECT_MIN_MS", "9000")
	t.Setenv("RECONNECT_MAX_MS", "1000")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "RECONNECT_MIN_MS") {
		t.Fatalf("err = %v, want one naming RECONNECT_MIN_MS", err)
	}
}

// The camera streams the unit's surroundings and look, so it must be an
// explicit choice rather than what an unset variable does.
func TestCameraIsOffByDefault(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CameraEnabled {
		t.Error("CameraEnabled is true with CAMERA_ENABLED unset")
	}
	if cfg.CameraMaxViewers != 4 {
		t.Errorf("CameraMaxViewers = %d, want 4", cfg.CameraMaxViewers)
	}
}

func TestCameraCanBeEnabled(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMERA_ENABLED", "true")
	t.Setenv("CAMERA_MAX_VIEWERS", "2")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.CameraEnabled || cfg.CameraMaxViewers != 2 {
		t.Errorf("camera = %v/%d, want true/2", cfg.CameraEnabled, cfg.CameraMaxViewers)
	}
}

func TestLoadRejectsBadCameraSettings(t *testing.T) {
	cases := map[string]string{
		"CAMERA_ENABLED":     "yes please",
		"CAMERA_MAX_VIEWERS": "0",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%s: err = %v, want one naming %s", name, value, err, name)
			}
		})
	}
}

func TestCameraAtlasDirDefaultsToTheDataVolume(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || cfg.CameraAtlasDir != "/data/atlas" {
		t.Fatalf("dir = %q, %v; want /data/atlas", cfg.CameraAtlasDir, err)
	}
	t.Setenv("CAMERA_ATLAS_DIR", "/var/cache/atlas")
	if cfg, _ = Load(); cfg.CameraAtlasDir != "/var/cache/atlas" {
		t.Errorf("dir = %q", cfg.CameraAtlasDir)
	}
}

// Offline skips authentication, which a real server would reject; it must be
// something a lab author writes down.
func TestOfflineIsOffByDefaultAndCanBeEnabled(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_OFFLINE", "")
	cfg, err := Load()
	if err != nil || cfg.Offline {
		t.Fatalf("default Offline = %v, %v; want false", cfg.Offline, err)
	}
	t.Setenv("MC_OFFLINE", "true")
	if cfg, err = Load(); err != nil || !cfg.Offline {
		t.Fatalf("MC_OFFLINE=true: Offline = %v, %v", cfg.Offline, err)
	}
	t.Setenv("MC_OFFLINE", "maybe")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "MC_OFFLINE") {
		t.Fatalf("err = %v, want one naming MC_OFFLINE", err)
	}
}

func TestTasksTokenAndTimeoutConfig(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TasksToken != "" || cfg.TaskTimeoutS != 900 {
		t.Errorf("defaults = %q, %d; want the endpoints off and 900", cfg.TasksToken, cfg.TaskTimeoutS)
	}

	t.Setenv("TASKS_TOKEN", " tok ")
	t.Setenv("TASK_TIMEOUT_S", "30")
	cfg, err = Load()
	if err != nil || cfg.TasksToken != "tok" || cfg.TaskTimeoutS != 30 {
		t.Errorf("set = %q, %d, %v", cfg.TasksToken, cfg.TaskTimeoutS, err)
	}

	for _, v := range []string{"29", "3601", "x"} {
		t.Setenv("TASK_TIMEOUT_S", v)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TASK_TIMEOUT_S") {
			t.Errorf("TASK_TIMEOUT_S=%s: err = %v", v, err)
		}
	}
}

func TestUnitIDDefaultsToTheUsername(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UnitID != "crew" {
		t.Errorf("UnitID = %q, want the username", cfg.UnitID)
	}
	t.Setenv("CREW_ID", "crew-02")
	if cfg, err = Load(); err != nil || cfg.UnitID != "crew-02" {
		t.Errorf("UnitID = %q, err %v, want crew-02", cfg.UnitID, err)
	}
}

func TestUnitIDMustBeCrewNN(t *testing.T) {
	for id, ok := range map[string]bool{"crew-1": false, "agate": false, "crew-01": true, "crew-123": true} {
		t.Run(id, func(t *testing.T) {
			setRequired(t)
			t.Setenv("CREW_ID", id)
			_, err := Load()
			if ok && err != nil {
				t.Errorf("rejected %q: %v", id, err)
			}
			if !ok && (err == nil || !strings.Contains(err.Error(), "CREW_ID")) {
				t.Errorf("err = %v, want one naming CREW_ID", err)
			}
		})
	}
}

func TestUsernameIsAtMost16Characters(t *testing.T) {
	setRequired(t)
	t.Setenv("MC_USERNAME", strings.Repeat("a", 17))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MC_USERNAME") {
		t.Errorf("err = %v, want one naming MC_USERNAME", err)
	}
	t.Setenv("MC_USERNAME", strings.Repeat("a", 16))
	if _, err := Load(); err != nil {
		t.Errorf("16 characters rejected: %v", err)
	}
}

func TestIdleLifeIsOnByDefaultAndConfigurable(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || !cfg.IdleWander || cfg.IdleRadius != 8 || !cfg.Defend {
		t.Fatalf("defaults = %+v, err %v", cfg, err)
	}
	t.Setenv("IDLE_WANDER", "false")
	t.Setenv("IDLE_RADIUS", "12")
	t.Setenv("DEFEND", "false")
	cfg, err = Load()
	if err != nil || cfg.IdleWander || cfg.IdleRadius != 12 || cfg.Defend {
		t.Fatalf("config = %+v, err %v", cfg, err)
	}
}

func TestEatIsOnByDefaultAndConfigurable(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || !cfg.Eat {
		t.Fatalf("defaults = %+v, err %v", cfg, err)
	}
	t.Setenv("EAT", "false")
	if cfg, err = Load(); err != nil || cfg.Eat {
		t.Fatalf("config = %+v, err %v", cfg, err)
	}
}

func TestKitChestIsOffUnlessAPositionIsGiven(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || cfg.KitChest != nil || cfg.KitFood != 8 {
		t.Fatalf("defaults = %+v, err %v", cfg, err)
	}
	t.Setenv("KIT_CHEST", " 12, 100 ,-30 ")
	t.Setenv("KIT_FOOD", "16")
	cfg, err = Load()
	if err != nil || cfg.KitChest == nil || *cfg.KitChest != [3]int32{12, 100, -30} || cfg.KitFood != 16 {
		t.Fatalf("config = %+v, err %v", cfg, err)
	}
}

func TestWorkPlanIsOptional(t *testing.T) {
	setRequired(t)
	if cfg, err := Load(); err != nil || cfg.WorkPlan != "" {
		t.Fatalf("default = %q, err %v", cfg.WorkPlan, err)
	}
	t.Setenv("JOIN_DELAY_S", "45")
	t.Setenv("WORK_PLAN", " [] ")
	if cfg, err := Load(); err != nil || cfg.WorkPlan != "[]" || cfg.JoinDelayS != 45 {
		t.Fatalf("set = %q, delay %d, err %v", cfg.WorkPlan, cfg.JoinDelayS, err)
	}
}

func TestKitChestRejectsWhatIsNotAPosition(t *testing.T) {
	for _, v := range []string{"1,2", "1,2,3,4", "a,b,c", "1,2,99999999999", "~1,2,3"} {
		setRequired(t)
		t.Setenv("KIT_CHEST", v)
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "KIT_CHEST") {
			t.Errorf("KIT_CHEST=%q: err = %v", v, err)
		} else if strings.Contains(err.Error(), v) && len(v) > 8 {
			t.Errorf("error echoes the position: %v", err)
		}
	}
}

func TestControlConfigIsBothOrNeither(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || cfg.ControlURL != nil || cfg.ControlToken != "" {
		t.Fatalf("unset = %+v, %v; want control off", cfg, err)
	}

	t.Setenv("CONTROL_URL", "http://crew-control:8080/")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CONTROL_TOKEN") {
		t.Fatalf("url only: err = %v, want one naming CONTROL_TOKEN", err)
	}

	t.Setenv("CONTROL_URL", "")
	t.Setenv("CONTROL_TOKEN", "tok")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CONTROL_URL") {
		t.Fatalf("token only: err = %v, want one naming CONTROL_URL", err)
	}

	t.Setenv("CONTROL_URL", "http://crew-control:8080/")
	cfg, err = Load()
	if err != nil || cfg.ControlURL == nil || cfg.ControlURL.Host != "crew-control:8080" || cfg.ControlToken != "tok" {
		t.Fatalf("both = %+v, %v", cfg, err)
	}

	for _, bad := range []string{"crew-control:8080", "ftp://x", "http://"} {
		t.Setenv("CONTROL_URL", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CONTROL_URL") {
			t.Errorf("CONTROL_URL=%q: err = %v, want a CONTROL_URL error", bad, err)
		}
	}
}

func TestTacticsControlDirDefaultsToOff(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	if err != nil || cfg.TacticsControlDir != "" {
		t.Fatalf("unset: %q, %v; want empty so no unit fetches a profile", cfg.TacticsControlDir, err)
	}
	t.Setenv("TACTICS_CONTROL_DIR", " /var/lib/crew-tactics ")
	cfg, err = Load()
	if err != nil || cfg.TacticsControlDir != "/var/lib/crew-tactics" {
		t.Fatalf("set: %q, %v", cfg.TacticsControlDir, err)
	}
}
