// Package config reads crew's settings from the environment, bounded at the
// widths the protocol and the OS will actually accept.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	maxPort      = 65535
	maxBackoffMs = 3_600_000
	// A worker needs the chunks it is working in, not a farm's radius. 32 is
	// generous for that and far inside RequestChunkRadius's uint8.
	maxViewDistance uint8 = 32
)

type Config struct {
	Host     string
	Port     int
	Username string

	ViewDistance uint8

	AuthCacheDir   string
	ReconnectMinMs int
	ReconnectMaxMs int

	StatusAddr string
}

func Load() (Config, error) {
	host, err := required("MC_HOST")
	if err != nil {
		return Config{}, err
	}
	username, err := required("MC_USERNAME")
	if err != nil {
		return Config{}, err
	}
	port, err := intInRange("MC_PORT", 19132, 1, maxPort)
	if err != nil {
		return Config{}, err
	}
	viewDistance, err := uint8InRange("MC_VIEW_DISTANCE", 8, 1, maxViewDistance)
	if err != nil {
		return Config{}, err
	}
	reconnectMin, err := intInRange("RECONNECT_MIN_MS", 5000, 1, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	reconnectMax, err := intInRange("RECONNECT_MAX_MS", 300000, 1, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	if reconnectMin > reconnectMax {
		return Config{}, fmt.Errorf("RECONNECT_MIN_MS (%d) exceeds RECONNECT_MAX_MS (%d)", reconnectMin, reconnectMax)
	}
	statusAddr := stringDefault("STATUS_ADDR", "127.0.0.1:8080")
	if _, _, err := net.SplitHostPort(statusAddr); err != nil {
		return Config{}, fmt.Errorf("STATUS_ADDR %q: %w", statusAddr, err)
	}

	return Config{
		Host:           host,
		Port:           port,
		Username:       username,
		ViewDistance:   viewDistance,
		AuthCacheDir:   stringDefault("AUTH_CACHE_DIR", "/data/auth"),
		ReconnectMinMs: reconnectMin,
		ReconnectMaxMs: reconnectMax,
		StatusAddr:     statusAddr,
	}, nil
}

func required(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", errors.New(name + " is required")
	}
	return v, nil
}

func stringDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func intInRange(name string, def, min, max int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s=%q: want an integer in [%d, %d]", name, raw, min, max)
	}
	return n, nil
}

// Parsed at uint8 width so a value the packet cannot carry is refused here
// rather than wrapping on the way in.
func uint8InRange(name string, def, min, max uint8) (uint8, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(raw, 10, 8)
	if err != nil || uint8(n) < min || uint8(n) > max {
		return 0, fmt.Errorf("%s=%q: want an integer in [%d, %d]", name, raw, min, max)
	}
	return uint8(n), nil
}
