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
