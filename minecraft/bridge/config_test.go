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
