package alerts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Config holds the rule thresholds. The zero value is not usable; start from
// Defaults.
type Config struct {
	// StuckFor is how long a unit must stay in the poller's stuck state
	// before the alert fires. The poller's own stuck detection already waits
	// for a quiet spell, so zero alerts on the tick it flags.
	StuckFor time.Duration
	// Failures is the consecutive failed tasks that fire task_failures.
	Failures int
	// HungerBelow fires low_food at or under this hunger value.
	HungerBelow float32
	// SilentFor is how long a unit may be unreachable before unit_silent.
	SilentFor time.Duration
	// MuteMax caps a single mute.
	MuteMax time.Duration
}

func Defaults() Config {
	return Config{StuckFor: 0, Failures: 3, HungerBelow: 6, SilentFor: 2 * time.Minute, MuteMax: 24 * time.Hour}
}

type wireConfig struct {
	StuckFor    *string  `json:"stuck_for"`
	Failures    *int     `json:"failures"`
	HungerBelow *float32 `json:"hunger_below"`
	SilentFor   *string  `json:"silent_for"`
	MuteMax     *string  `json:"mute_max"`
}

// ParseConfig reads the CREW_ALERTS JSON object over the defaults. Blank
// keeps every default; an unknown key or an out-of-range value is an error so
// a typo in the chart cannot silently leave a rule at its default.
func ParseConfig(raw string) (Config, error) {
	cfg := Defaults()
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	var w wireConfig
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return cfg, fmt.Errorf("CREW_ALERTS: %w", err)
	}
	dur := func(name string, s *string, dst *time.Duration, min time.Duration) error {
		if s == nil {
			return nil
		}
		d, err := time.ParseDuration(*s)
		if err != nil || d < min {
			return fmt.Errorf("CREW_ALERTS: %s must be a duration of at least %s", name, min)
		}
		*dst = d
		return nil
	}
	if err := dur("stuck_for", w.StuckFor, &cfg.StuckFor, 0); err != nil {
		return cfg, err
	}
	if err := dur("silent_for", w.SilentFor, &cfg.SilentFor, time.Second); err != nil {
		return cfg, err
	}
	if err := dur("mute_max", w.MuteMax, &cfg.MuteMax, time.Minute); err != nil {
		return cfg, err
	}
	if w.Failures != nil {
		if *w.Failures < 1 {
			return cfg, fmt.Errorf("CREW_ALERTS: failures must be at least 1")
		}
		cfg.Failures = *w.Failures
	}
	if w.HungerBelow != nil {
		if *w.HungerBelow < 0 || *w.HungerBelow > 20 {
			return cfg, fmt.Errorf("CREW_ALERTS: hunger_below must be 0 to 20")
		}
		cfg.HungerBelow = *w.HungerBelow
	}
	return cfg, nil
}
