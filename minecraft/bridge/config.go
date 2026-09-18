package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

// defaultConsoleOrigin is the Origin header sent on the console handshake.
// mc-server-runner compares it against WEBSOCKET_ALLOWED_ORIGINS by exact
// string equality, so the default has to be a value an operator can paste
// into that list verbatim.
//
// The scheme is deliberately not http/https: a browser only ever mints an
// origin for a scheme it can load a page from, so no website can forge this
// one. Allow-listing it therefore keeps the origin check meaningful against
// Cross-Site WebSocket Hijacking, which allow-listing something like
// http://localhost would not.
const defaultConsoleOrigin = "mc-console-bridge://sidecar"

// Config holds every environment-derived setting for the bridge.
type Config struct {
	// HTTPAddr is the bind address for the bridge's own HTTP API.
	HTTPAddr string
	// BridgeToken authenticates callers of the bridge's HTTP API (bearer token).
	BridgeToken string

	// ConsoleAddr is the bedrock_server websocket console, e.g. "127.0.0.1:8765".
	ConsoleAddr string
	// ConsolePassword is the WEBSOCKET_PASSWORD configured on the server container.
	ConsolePassword string
	// ConsoleOrigin is the Origin header sent on the console handshake. The
	// server matches it against WEBSOCKET_ALLOWED_ORIGINS when its origin
	// check is enabled, and ignores it entirely when the check is off.
	ConsoleOrigin string
	// CommandTimeout bounds how long SendCommand waits for the console write
	// to complete, and caps the window it then spends collecting the
	// command's output.
	CommandTimeout time.Duration

	// DataDir is where the server's permissions.json and allowlist.json live
	// (the mounted /data volume).
	DataDir string
}

func requiredEnv(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// positiveMillisOr parses an environment variable as a positive integer
// count of milliseconds, falling back to def on an unset or empty value. A
// present-but-invalid value is an error rather than a silent fallback.
func positiveMillisOr(name string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer number of milliseconds, got %q", name, raw)
	}
	return time.Duration(ms) * time.Millisecond, nil
}

// originOr parses an environment variable as a serialized origin
// (scheme://host), falling back to def on an unset or empty value. A
// present-but-invalid value is an error rather than a silent fallback: a
// malformed Origin fails every handshake, and a startup error names the
// cause where an endless reconnect loop would not.
func originOr(name, def string) (string, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%s must be an origin of the form scheme://host[:port], got %q", name, raw)
	}
	return raw, nil
}

// LoadConfig reads configuration from the environment. It fails closed:
// missing required values are an error, not a silently-empty default.
func LoadConfig() (Config, error) {
	token, err := requiredEnv("BRIDGE_TOKEN")
	if err != nil {
		return Config{}, err
	}
	consolePassword, err := requiredEnv("CONSOLE_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	commandTimeout, err := positiveMillisOr("COMMAND_TIMEOUT_MS", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	consoleOrigin, err := originOr("CONSOLE_ORIGIN", defaultConsoleOrigin)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddr:        envOr("HTTP_ADDR", ":8080"),
		BridgeToken:     token,
		ConsoleAddr:     envOr("CONSOLE_ADDR", "127.0.0.1:8765"),
		ConsolePassword: consolePassword,
		ConsoleOrigin:   consoleOrigin,
		CommandTimeout:  commandTimeout,
		DataDir:         envOr("DATA_DIR", "/data"),
	}, nil
}
