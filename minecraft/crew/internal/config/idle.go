package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
)

// Mode is what the unit is for: work, as every lab unit runs, or idle, a unit
// that only holds its chunks where an AFK bot used to (docs/idle-mode.md).
type Mode string

const (
	ModeWork Mode = "work"
	ModeIdle Mode = "idle"
)

// An idle unit holds a farm's radius, as the AFK bot it replaces did.
const idleViewDistance uint8 = 12

const (
	// Ten minutes. The poll interval is how long a park or resume takes to
	// reach the unit, and parking is meant to work mid-game.
	maxPollMs = 600_000
	// One second. Below this, "poll the agent" becomes "hammer the agent"
	// for a responsiveness gain nobody watching a park or resume would see.
	minPollMs = 1_000
)

// The shared contract's actor id shape, checked here so a typo fails at
// startup rather than as a 404 on every poll.
var actorIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// idleRefused are the settings that give a unit work, orders or a place to go.
// An idle unit refuses them rather than ignoring them: an ignored value reads
// as honoured to whoever set it.
var idleRefused = []string{
	"TASKS_TOKEN", "WORK_PLAN", "CONTROL_URL", "CREW_ROLE", "CREW_SQUAD",
	"CREW_HARNESS", "CREW_LAB", "KIT_CHEST", "INV_CHEST", "HOME_POS",
}

// idleBehaviours are forced off in idle mode whatever their defaults, and
// refused when set to true: several default to on for a worker.
var idleBehaviours = []string{
	"IDLE_WANDER", "DEFEND", "EAT", "RECOVER_DROPS", "NIGHT_SHELTER",
	"INV_MANAGE", "KIT_GEAR", "COMBAT_BASELINE",
}

// idleOnly are the settings only an idle unit reads. A worker refuses them:
// crew-control owns when a worker joins, and a second owner of that decision
// is what the 2026-10-14 decision avoided.
var idleOnly = []string{
	"FACING", "STOW", "SEAT",
	"PRESENCE_URL", "PRESENCE_TOKEN", "PRESENCE_ACTOR_ID", "PRESENCE_DEFAULT", "PRESENCE_POLL_MS",
}

// Presence configures the desired-state reconciler that lets the server agent
// park an idle unit.
type Presence struct {
	URL     string
	Token   string
	ActorID string
	// Default is acted on until the agent first answers, so a unit that
	// starts during an agent outage still does what git says.
	Default presenceapi.State
	PollMs  int
}

// Enabled reports whether the unit should ask the agent before connecting.
func (p Presence) Enabled() bool { return p.URL != "" }

// Idle reports whether the unit runs in idle mode.
func (c Config) Idle() bool { return c.Mode == ModeIdle }

func modeSetting() (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(os.Getenv("CREW_MODE")))); m {
	case "", ModeWork:
		return ModeWork, nil
	case ModeIdle:
		return ModeIdle, nil
	default:
		return "", fmt.Errorf("CREW_MODE=%q: want work or idle", m)
	}
}

// checkModeSettings refuses what the mode does not use. It runs before the
// settings are parsed, so the refusal names the setting rather than a parse
// error in it.
func checkModeSettings(mode Mode) error {
	set := func(name string) bool { return strings.TrimSpace(os.Getenv(name)) != "" }
	if mode == ModeWork {
		for _, name := range idleOnly {
			if set(name) {
				return fmt.Errorf("%s is set but CREW_MODE is work; it is read only in idle mode", name)
			}
		}
		return nil
	}
	for _, name := range idleRefused {
		if set(name) {
			return fmt.Errorf("%s is set but CREW_MODE is idle; an idle unit takes no work", name)
		}
	}
	for _, name := range idleBehaviours {
		if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name))); err == nil && v {
			return fmt.Errorf("%s=true but CREW_MODE is idle; an idle unit does nothing of its own", name)
		}
	}
	return nil
}

// idleSettings reads what only an idle unit has.
func idleSettings(c *Config) error {
	var err error
	if c.Facing, c.HasFacing, err = parseFacing(os.Getenv("FACING")); err != nil {
		return err
	}
	if c.Stow, err = parseStow(os.Getenv("STOW")); err != nil {
		return err
	}
	if c.Seat, c.HasSeat, err = parseSeat(os.Getenv("SEAT")); err != nil {
		return err
	}
	// The seat's click has to describe an empty hand, which only the stow
	// makes known.
	c.Stow = c.Stow || c.HasSeat
	c.Presence, err = loadPresence()
	return err
}

// forceIdle turns off every behaviour an idle unit must not have, including
// the ones that default to on.
func forceIdle(c *Config) {
	c.IdleWander, c.Defend, c.Eat, c.RecoverDrops, c.NightShelter = false, false, false, false, false
	c.InvManage, c.InvChest, c.KitGear = false, nil, false
	c.Combat.Baseline = false
}

// loadPresence reads nothing beyond PRESENCE_URL while it is unset, so a
// stray variable on a unit deployed without the agent cannot stop it starting.
//
// No error here quotes PRESENCE_TOKEN: startup errors go to the pod log.
func loadPresence() (Presence, error) {
	raw := strings.TrimSpace(os.Getenv("PRESENCE_URL"))
	if raw == "" {
		return Presence{}, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		msg := "environment variable PRESENCE_URL must be an http or https URL"
		// Redacted keeps a userinfo password out of an error that goes
		// straight to the pod log.
		if u != nil {
			msg = fmt.Sprintf("%s, got %q", msg, u.Redacted())
		}
		return Presence{}, errors.New(msg)
	}
	token, err := presenceRequired("PRESENCE_TOKEN")
	if err != nil {
		return Presence{}, err
	}
	actorID, err := presenceRequired("PRESENCE_ACTOR_ID")
	if err != nil {
		return Presence{}, err
	}
	if !actorIDPattern.MatchString(actorID) {
		return Presence{}, fmt.Errorf("environment variable PRESENCE_ACTOR_ID must match %s", actorIDPattern)
	}
	def, err := presenceRequired("PRESENCE_DEFAULT")
	if err != nil {
		return Presence{}, err
	}
	state := presenceapi.State(def)
	if !state.Valid() {
		return Presence{}, fmt.Errorf("environment variable PRESENCE_DEFAULT must be %q or %q", presenceapi.StatePresent, presenceapi.StateParked)
	}
	pollMs, err := pollSetting()
	if err != nil {
		return Presence{}, err
	}
	return Presence{
		URL:     strings.TrimRight(raw, "/"),
		Token:   token,
		ActorID: actorID,
		Default: state,
		PollMs:  pollMs,
	}, nil
}

func presenceRequired(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("environment variable %s is required", name)
	}
	return v, nil
}

func pollSetting() (int, error) {
	const name = "PRESENCE_POLL_MS"
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 10000, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("environment variable %s must be an integer, got %q", name, raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("environment variable %s must be a positive integer, got %d", name, n)
	}
	if n > maxPollMs {
		return 0, fmt.Errorf("environment variable %s must be at most %d, got %d", name, maxPollMs, n)
	}
	if n < minPollMs {
		return 0, fmt.Errorf("environment variable %s must be at least %d, got %d", name, minPollMs, n)
	}
	return n, nil
}

// parseFacing reads FACING: a compass point or a yaw in degrees. Empty means
// no facing is held.
func parseFacing(raw string) (yaw float32, set bool, err error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "":
		return 0, false, nil
	case "south":
		return 0, true, nil
	case "west":
		return 90, true, nil
	case "north":
		return 180, true, nil
	case "east":
		return 270, true, nil
	}
	deg, perr := strconv.ParseFloat(raw, 32)
	if perr != nil || math.IsNaN(deg) || deg < 0 || deg >= 360 {
		return 0, false, fmt.Errorf("FACING: %q is not north, south, east, west or a yaw in [0, 360)", raw)
	}
	return float32(deg), true, nil
}

// parseStow reads STOW. Empty means off.
func parseStow(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("STOW: %q is not a boolean", raw)
	}
	return v, nil
}

// parseSeat reads SEAT as x,y,z block coordinates. Empty means no seat. The
// errors leave the value out: a seat on FWB is a base's location, which logs
// never carry.
func parseSeat(raw string) (seat [3]float32, set bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return seat, false, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return seat, false, errors.New("SEAT: want x,y,z")
	}
	for i, part := range parts {
		v, perr := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if perr != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return seat, false, errors.New("SEAT: want x,y,z as numbers")
		}
		seat[i] = float32(v)
	}
	return seat, true, nil
}
