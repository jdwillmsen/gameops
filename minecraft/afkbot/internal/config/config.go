// Package config reads the bot's settings from the environment.
//
// It covers presence and nothing else. Configuration for a feature this
// program does not have would be the first thing to rot, so settings that
// moved to minecraft-server-agent were dropped rather than carried across.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Bedrock's maximum tick-distance, and the largest radius measured as granted
// in full (see ViewDistance below). Nothing above this was tried, so it is a
// verified ceiling rather than a known limit.
const maxTickDistance = 12

// Ceilings for the numeric environment variables. Each exists because the
// value is narrowed or range-bound somewhere the narrowing cannot report a
// problem.
const (
	// RequestChunkRadius carries MaxChunkRadius as a uint8, so anything above
	// 255 wraps silently. 64 rather than 255: it leaves room to try radii well
	// above the verified maxTickDistance, while a value big enough to ask a
	// server for an absurd amount of chunk data is far more likely a typo than
	// an intent.
	maxViewDistance uint8 = 64

	// A TCP/UDP port number is 16 bits.
	maxPort = 65535

	// One hour. The backoff ceiling is a wait before reconnecting, so a value
	// past this is indistinguishable from the bot never coming back.
	maxBackoffMs = 3_600_000
)

// Config is everything the bot needs to hold a player slot.
type Config struct {
	Host string
	Port int
	// Username is the token-cache key, not the gamertag other players see.
	// Xbox Live supplies the gamertag after login; this only decides which
	// cached token is used, which is why two bots with different values here
	// can appear under any two gamertags.
	Username string

	// ViewDistance is the chunk radius the bot asks for.
	//
	// The server grants what the client asks for rather than extending the
	// request to its own tick-distance, so this decides how much world the bot
	// actually holds. Nothing observable distinguishes a radius that covers the
	// farm from one that covers a fifth of it -- the farm produces either way --
	// which is why it defaults to the maximum rather than to a value that has
	// to be reasoned about (docs/decisions.md).
	//
	// uint8 because that is the width RequestChunkRadius.MaxChunkRadius uses.
	// Holding it at the protocol's width means the packet needs no narrowing
	// conversion, so there is no point where an out-of-range value could wrap
	// to a radius of zero -- it cannot be represented here in the first place.
	// ChunkRadius is int32 on the wire, and widening to it is always exact.
	ViewDistance uint8

	AuthCacheDir   string
	ReconnectMinMs int
	ReconnectMaxMs int
}

// Load reads the environment, returning an error rather than a partial
// config: a bot that starts with a missing host fails later, further from the
// cause.
func Load() (Config, error) {
	host, err := required("MC_HOST")
	if err != nil {
		return Config{}, err
	}
	username, err := required("MC_USERNAME")
	if err != nil {
		return Config{}, err
	}
	port, err := positiveInt("MC_PORT", 19132, maxPort)
	if err != nil {
		return Config{}, err
	}
	viewDistance, err := positiveUint8("MC_VIEW_DISTANCE", maxTickDistance, maxViewDistance)
	if err != nil {
		return Config{}, err
	}
	reconnectMin, err := positiveInt("RECONNECT_MIN_MS", 5000, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	reconnectMax, err := positiveInt("RECONNECT_MAX_MS", 300000, maxBackoffMs)
	if err != nil {
		return Config{}, err
	}
	if reconnectMax < reconnectMin {
		return Config{}, fmt.Errorf("RECONNECT_MAX_MS (%d) must be >= RECONNECT_MIN_MS (%d)", reconnectMax, reconnectMin)
	}

	return Config{
		Host:           host,
		Port:           port,
		Username:       username,
		ViewDistance:   viewDistance,
		AuthCacheDir:   stringDefault("AUTH_CACHE_DIR", "/data/auth"),
		ReconnectMinMs: reconnectMin,
		ReconnectMaxMs: reconnectMax,
	}, nil
}

func required(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("environment variable %s is required", name)
	}
	return v, nil
}

func stringDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// positiveUint8 parses a value the protocol carries as a uint8, at that width.
//
// ParseUint with a bitSize of 8 is the point: it refuses anything that would
// not survive the conversion, so the narrowing below cannot lose information.
// Checking an int and then converting cannot promise that -- MC_VIEW_DISTANCE=256
// once passed a `> 0` check, was stored as int32, and reached
// `uint8(cfg.ViewDistance)` as 0, leaving the bot connected and loading
// nothing. Parsing at the destination width removes the conversion that
// wrapped rather than guarding it from a distance.
func positiveUint8(name string, def, max uint8) (uint8, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(raw, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s must be an integer in 1..%d, got %q", name, max, raw)
	}
	v := uint8(n) // exact: ParseUint with bitSize 8 cannot return more than 255
	if v == 0 {
		return 0, fmt.Errorf("environment variable %s must be a positive integer, got %d", name, v)
	}
	if v > max {
		return 0, fmt.Errorf("environment variable %s must be at most %d, got %d", name, max, v)
	}
	return v, nil
}

// positiveInt rejects zero and negatives rather than silently falling back.
// A zero view distance would produce a bot that connects, looks healthy, and
// loads nothing -- the exact silent failure this rewrite has to avoid.
//
// max is a required argument for the same reason positiveUint8 exists: every
// caller feeds something range-bound further down, and a bound that holds only
// for the default is what let the view-distance wrap through.
func positiveInt(name string, def, max int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s must be an integer, got %q", name, raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("environment variable %s must be a positive integer, got %d", name, n)
	}
	if n > max {
		return 0, fmt.Errorf("environment variable %s must be at most %d, got %d", name, max, n)
	}
	return n, nil
}
