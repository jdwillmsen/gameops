// Package config reads crew's settings from the environment, bounded at the
// widths the protocol and the OS will actually accept.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/mcproto"
)

const (
	maxPort      = 65535
	maxBackoffMs = 3_600_000
	// A worker needs the chunks it is working in, not a farm's radius. 32 is
	// generous for that and far inside RequestChunkRadius's uint8.
	maxViewDistance uint8 = 32
	// Every viewer holds a replay of the unit's world in flight; the cap keeps
	// a stray page reload loop from becoming crew's main cost.
	maxCameraViewers = 64
	// A wander is a few blocks' stroll in loaded chunks; past this it would
	// leave the area the unit knows.
	maxIdleRadius = 32
	// Bedrock refuses a longer display name at login.
	maxUsernameLen = 16
)

// A fixed number is the unit's identity; the username is only its name, so
// the number can stay put if a name is ever changed.
var unitIDPattern = regexp.MustCompile(`^crew-[0-9]{2,}$`)

type Config struct {
	// Host is a hostname for a RakNet server, or the scheme and host
	// (http://192.168.1.87) of a NetherNet server's signaling port; see
	// Address.
	Host     string
	Port     int
	Username string
	// UnitID is the unit's fixed identity, as opposed to its name.
	UnitID string
	// Offline joins with only a display name and no Microsoft sign-in. An
	// online-mode server rejects such a client, so it is for a lab server
	// with online mode off. A NetherNet server never admits one.
	Offline bool

	ViewDistance uint8

	AuthCacheDir   string
	ReconnectMinMs int
	ReconnectMaxMs int

	StatusAddr string

	CameraEnabled    bool
	CameraMaxViewers int
	// CameraAtlasDir holds the cached texture atlas; it belongs on the data
	// volume so a restart does not download the resource pack again.
	CameraAtlasDir string

	// TasksToken guards the task endpoints; empty leaves them unregistered.
	TasksToken   string
	TaskTimeoutS int

	// ControlURL and ControlToken connect the unit to crew-control; both are
	// set or neither.
	ControlURL   *url.URL
	ControlToken string

	// IdleWander lets a unit with no task look about and take short walks.
	IdleWander bool
	// IdleRadius bounds those walks, in blocks around where the unit went idle.
	IdleRadius int
	// Defend lets a unit fight hostile mobs and back away from players' blows.
	Defend bool
	// Harness opens the entity task templates and the entity list in the
	// state body. It is for a throwaway preview server, never the lab.
	Harness bool
	// Eat lets a hungry unit eat food from its hotbar.
	Eat bool
	// Role is the unit's job. A warrior role (vanguard, striker, ranger)
	// opts into the warrior engine and a squad; a builder role (mender,
	// builder) does neither. Empty leaves the reactive defence in charge;
	// anything else is refused rather than silently fighting on.
	Role Role
	// Lab says the unit is on the crew lab, which a builder role requires:
	// builders work in creative mode and must never run on the live server.
	Lab bool
	// Squad is the id of the squad a warrior joins through crew-control.
	// A unit without a warrior role never uses it.
	Squad string
	// TacticsControlDir is the tacticsctl state directory whose training
	// switch gates a lab unit's tactics profile. Empty, the default, means
	// the unit never fetches one and fights on the shipped behaviour.
	TacticsControlDir string
	// KitChest is the chest a unit without a weapon or food rearms from; nil
	// leaves the feature off. KitFood is how many food items one visit takes.
	KitChest *[3]int32
	KitFood  int

	// SkinRole is the unit's job (miner, chopper, ...), which picks its
	// outfit. A warrior's outfit follows Role instead. Empty or unknown gives
	// the plain name-coloured skin.
	SkinRole string
	// SkinDir holds hand-painted skins as <name>.png; a file there replaces
	// the generated outfit on the unit's next join. Empty turns it off.
	SkinDir string

	// WorkPlan is the unit's standing work, a JSON array of task bodies run in
	// rotation whenever it has nothing else; empty leaves it idle. Validated by
	// the task package, which owns the templates.
	WorkPlan string

	// JoinDelayS holds back the first connect, so a rollout of many units does
	// not log them all in on the same second.
	JoinDelayS int
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
	if n := utf8.RuneCountInString(username); n > maxUsernameLen {
		return Config{}, fmt.Errorf("MC_USERNAME is %d characters, at most %d", n, maxUsernameLen)
	}
	unitID := stringDefault("CREW_ID", username)
	if os.Getenv("CREW_ID") != "" && !unitIDPattern.MatchString(unitID) {
		return Config{}, fmt.Errorf("CREW_ID=%q: want crew- followed by at least two digits", unitID)
	}
	offline, err := boolDefault("MC_OFFLINE", false)
	if err != nil {
		return Config{}, err
	}
	port, err := intInRange("MC_PORT", 19132, 1, maxPort)
	if err != nil {
		return Config{}, err
	}
	address, err := dialAddress(host, port)
	if err != nil {
		return Config{}, err
	}
	if offline && mcproto.IsNetherNet(address) {
		return Config{}, errors.New("MC_OFFLINE=true cannot join a NetherNet server (MC_HOST is an http(s):// URL): " +
			"it turns an unsigned player away at the transport with error 37, so the unit needs a Microsoft sign-in; " +
			"unset MC_OFFLINE and sign the unit in, or point MC_HOST at a RakNet server")
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
	cameraEnabled, err := boolDefault("CAMERA_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cameraMaxViewers, err := intInRange("CAMERA_MAX_VIEWERS", 4, 1, maxCameraViewers)
	if err != nil {
		return Config{}, err
	}
	taskTimeout, err := intInRange("TASK_TIMEOUT_S", 900, 30, 3600)
	if err != nil {
		return Config{}, err
	}

	controlURL, controlToken, err := controlSettings()
	if err != nil {
		return Config{}, err
	}

	idleWander, err := boolDefault("IDLE_WANDER", true)
	if err != nil {
		return Config{}, err
	}
	idleRadius, err := intInRange("IDLE_RADIUS", 8, 1, maxIdleRadius)
	if err != nil {
		return Config{}, err
	}
	defend, err := boolDefault("DEFEND", true)
	if err != nil {
		return Config{}, err
	}
	harness, err := boolDefault("CREW_HARNESS", false)
	if err != nil {
		return Config{}, err
	}
	eat, err := boolDefault("EAT", true)
	if err != nil {
		return Config{}, err
	}
	role, err := roleSetting()
	if err != nil {
		return Config{}, err
	}
	lab, err := boolDefault("CREW_LAB", false)
	if err != nil {
		return Config{}, err
	}
	if role.IsBuilder() && !lab {
		return Config{}, fmt.Errorf("CREW_ROLE=%s: needs CREW_LAB=true, builder roles run only on the crew lab", role)
	}
	squad, err := squadSetting(role)
	if err != nil {
		return Config{}, err
	}
	kitChest, err := positionOptional("KIT_CHEST")
	if err != nil {
		return Config{}, err
	}
	kitFood, err := intInRange("KIT_FOOD", 8, 1, 64)
	if err != nil {
		return Config{}, err
	}
	joinDelay, err := intInRange("JOIN_DELAY_S", 0, 0, 600)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Host:           host,
		Port:           port,
		Username:       username,
		UnitID:         unitID,
		Offline:        offline,
		ViewDistance:   viewDistance,
		AuthCacheDir:   stringDefault("AUTH_CACHE_DIR", "/data/auth"),
		ReconnectMinMs: reconnectMin,
		ReconnectMaxMs: reconnectMax,
		StatusAddr:     statusAddr,

		CameraEnabled:    cameraEnabled,
		CameraMaxViewers: cameraMaxViewers,
		CameraAtlasDir:   stringDefault("CAMERA_ATLAS_DIR", "/data/atlas"),

		TasksToken:   strings.TrimSpace(os.Getenv("TASKS_TOKEN")),
		TaskTimeoutS: taskTimeout,

		ControlURL:   controlURL,
		ControlToken: controlToken,

		IdleWander: idleWander,
		IdleRadius: idleRadius,
		Defend:     defend,
		Harness:    harness,
		Eat:        eat,
		Role:       role,
		Lab:        lab,
		Squad:      squad,

		TacticsControlDir: strings.TrimSpace(os.Getenv("TACTICS_CONTROL_DIR")),

		KitChest:   kitChest,
		KitFood:    kitFood,
		SkinRole:   strings.ToLower(strings.TrimSpace(os.Getenv("CREW_UNIT_ROLE"))),
		SkinDir:    strings.TrimSpace(os.Getenv("SKIN_DIR")),
		WorkPlan:   strings.TrimSpace(os.Getenv("WORK_PLAN")),
		JoinDelayS: joinDelay,
	}, nil
}

// Address is what the Bedrock client dials. A Host that starts with http:// or
// https:// selects NetherNet and yields the signaling URL with Port appended
// unless the host already carries one; any other Host is a RakNet host:port.
// The agent applies the same rule to its MC_HOST and MC_PORT, so both agree on
// what a value means.
func (c Config) Address() string {
	addr, _ := dialAddress(c.Host, c.Port)
	return addr
}

// NetherNet reports whether Address names a NetherNet server.
func (c Config) NetherNet() bool { return mcproto.IsNetherNet(c.Address()) }

func dialAddress(host string, port int) (string, error) {
	lower := strings.ToLower(host)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return net.JoinHostPort(host, strconv.Itoa(port)), nil
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		// The value is left out of the message: a URL can carry credentials, and
		// this error reaches the log.
		return "", fmt.Errorf("MC_HOST must be a bare http(s)://host[:port] URL when it names a NetherNet server")
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	}
	u.Path = ""
	return u.String(), nil
}

// Role is a unit's job. Warrior and builder roles are separate kinds: only a
// warrior takes the fighting engine and a squad.
type Role string

// warriorRoles are kept in step with the life engine's, which this package
// does not import so as not to pull the engine into every binary that reads
// settings.
var warriorRoles = map[Role]bool{"vanguard": true, "striker": true, "ranger": true}

var builderRoles = map[Role]bool{"mender": true, "builder": true}

func (r Role) IsWarrior() bool { return warriorRoles[r] }

func (r Role) IsBuilder() bool { return builderRoles[r] }

// roleSetting reads CREW_ROLE: empty for no role, else a warrior or builder
// role.
func roleSetting() (Role, error) {
	role := Role(strings.TrimSpace(os.Getenv("CREW_ROLE")))
	if role == "" || role.IsWarrior() || role.IsBuilder() {
		return role, nil
	}
	return "", fmt.Errorf("CREW_ROLE=%q: want vanguard, striker, ranger, mender or builder", role)
}

// squadIDPattern mirrors the id shape the squad routes carry, so a bad
// CREW_SQUAD is refused here rather than as a hello the server rejects on
// every beat.
var squadIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)

// squadSetting reads CREW_SQUAD, the id of the squad a warrior joins. The
// default is alpha. Without a warrior role the value is still shape-checked,
// so a fleet-wide setting is honest, but never turns a non-warrior unit away.
func squadSetting(role Role) (string, error) {
	squad := strings.TrimSpace(os.Getenv("CREW_SQUAD"))
	if squad == "" {
		if !role.IsWarrior() {
			return "", nil
		}
		return "alpha", nil
	}
	if !squadIDPattern.MatchString(squad) {
		return "", fmt.Errorf("CREW_SQUAD=%q: want a lowercase id like alpha", squad)
	}
	return squad, nil
}

// positionOptional reads "x,y,z" block coordinates. Unset is nil. The error
// leaves the value out: it is a lab position, which logs never carry.
func positionOptional(name string) (*[3]int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%s: want x,y,z", name)
	}
	var pos [3]int32
	for i, p := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%s: want x,y,z as whole numbers", name)
		}
		pos[i] = int32(n)
	}
	return &pos, nil
}

// A URL without its token (or the reverse) would leave the unit silently
// disconnected from the controller that thinks it is managing it.
func controlSettings() (*url.URL, string, error) {
	raw := strings.TrimSpace(os.Getenv("CONTROL_URL"))
	token := strings.TrimSpace(os.Getenv("CONTROL_TOKEN"))
	switch {
	case raw == "" && token == "":
		return nil, "", nil
	case raw == "":
		return nil, "", errors.New("CONTROL_TOKEN is set but CONTROL_URL is not")
	case token == "":
		return nil, "", errors.New("CONTROL_URL is set but CONTROL_TOKEN is not")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("CONTROL_URL: want an http or https URL with a host")
	}
	return u, token, nil
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

func boolDefault(name string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s=%q: want true or false", name, raw)
	}
	return v, nil
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
