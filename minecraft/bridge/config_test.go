package main

import (
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BRIDGE_TOKEN", "tok")
	t.Setenv("CONSOLE_PASSWORD", "pw")
}

func TestLoadConfig_CommandTimeoutDefaultsTo2s(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CommandTimeout != 2*time.Second {
		t.Errorf("CommandTimeout = %v, want 2s default", cfg.CommandTimeout)
	}
}

func TestLoadConfig_CommandTimeoutFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("COMMAND_TIMEOUT_MS", "500")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CommandTimeout != 500*time.Millisecond {
		t.Errorf("CommandTimeout = %v, want 500ms", cfg.CommandTimeout)
	}
}

func TestLoadConfig_CommandTimeoutInvalidIsAnError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("COMMAND_TIMEOUT_MS", "not-a-number")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig with an invalid COMMAND_TIMEOUT_MS returned no error")
	}
}

func TestLoadConfig_CommandTimeoutZeroIsRejected(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("COMMAND_TIMEOUT_MS", "0")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig with COMMAND_TIMEOUT_MS=0 returned no error — a zero timeout would make every console write fail instantly")
	}
}

func TestLoadConfig_ConsoleOriginDefault(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ConsoleOrigin != defaultConsoleOrigin {
		t.Errorf("ConsoleOrigin = %q, want %q", cfg.ConsoleOrigin, defaultConsoleOrigin)
	}
}

func TestLoadConfig_ConsoleOriginFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CONSOLE_ORIGIN", "https://console.example.test:8443")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ConsoleOrigin != "https://console.example.test:8443" {
		t.Errorf("ConsoleOrigin = %q, want the configured value", cfg.ConsoleOrigin)
	}
}

func TestLoadConfig_ConsoleOriginInvalidIsAnError(t *testing.T) {
	for _, raw := range []string{"example.test", "https://", "https://example.test/console"} {
		t.Run(raw, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("CONSOLE_ORIGIN", raw)

			if _, err := LoadConfig(); err == nil {
				t.Fatalf("LoadConfig with CONSOLE_ORIGIN=%q returned no error — a malformed origin fails every handshake, and the reconnect loop would never say why", raw)
			}
		})
	}
}

func TestLoadConfig_KickableDefaultsToNobody(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Kickable.Len() != 0 {
		t.Errorf("Kickable.Len = %d, want 0 when BRIDGE_KICKABLE is unset", cfg.Kickable.Len())
	}
}

func TestLoadConfig_KickableFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BRIDGE_KICKABLE", "AfkBotOne,Afk Bot Two")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, name := range []string{"AfkBotOne", "Afk Bot Two"} {
		if !cfg.Kickable.Contains(name) {
			t.Errorf("Kickable.Contains(%q) = false, want true", name)
		}
	}
}

func TestLoadConfig_KickableUnsafeEntryIsAnError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BRIDGE_KICKABLE", "AfkBotOne,@a")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig with a selector in BRIDGE_KICKABLE returned no error — the operator would believe it was applied")
	}
}
