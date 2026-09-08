// Package config reads the bot's settings from the environment.
//
// Deliberately smaller than the TypeScript configuration it replaces. Chat
// answering moved to minecraft-server-agent on 2026-09-08, so MC_ANSWER_* and
// MC_LLM_* are gone rather than carried across dead: porting configuration for
// a feature this program no longer has would be the first thing to rot.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// Low on purpose. The server's own tick-distance governs what simulates
	// around a player, so a bot holding a farm loaded does not need to see
	// far -- it needs to be present. Asking for a large radius would cost
	// bandwidth and server work for chunks nobody looks at.
	ViewDistance int32

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
	port, err := positiveInt("MC_PORT", 19132)
	if err != nil {
		return Config{}, err
	}
	viewDistance, err := positiveInt("MC_VIEW_DISTANCE", 4)
	if err != nil {
		return Config{}, err
	}
	reconnectMin, err := positiveInt("RECONNECT_MIN_MS", 5000)
	if err != nil {
		return Config{}, err
	}
	reconnectMax, err := positiveInt("RECONNECT_MAX_MS", 300000)
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
		ViewDistance:   int32(viewDistance),
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

// positiveInt rejects zero and negatives rather than silently falling back.
// A zero view distance would produce a bot that connects, looks healthy, and
// loads nothing -- the exact silent failure this rewrite has to avoid.
func positiveInt(name string, def int) (int, error) {
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
	return n, nil
}
