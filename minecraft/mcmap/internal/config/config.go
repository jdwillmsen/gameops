// Package config reads the map service's settings from the environment.
package config

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"
)

// The renderer build this service was verified against. The address always
// serves the newest dev build, so the digest is what actually pins it.
const (
	DefaultUnminedURL    = "https://unmined.net/download/unmined-cli-linux-x64-dev/"
	DefaultUnminedSHA256 = "a47ec942a6d4a0f2e68323ed6c4da3221fe9d09353c2132188042776f96e47d7"
)

// The revision of Mojang's published samples the mob icons are read from:
// the commit tagged v1.26.50.4, the release nearest the game server's own
// 1.26.5x line. It is a commit, not the tag, because a tag can be moved and
// what is fetched should not change unless this does.
const DefaultIconsRef = "46ba6ea985fb5a92d79a9419198f10dda14c199d"

var iconsRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

type Config struct {
	HTTPAddr string
	// DataDir holds the world mirror, the rendered tiles and the installed
	// renderer; all of it can be rebuilt, but only slowly.
	DataDir string

	BridgeURL   string
	BridgeToken string
	// Level is the world's directory name on the server.
	Level string

	Refresh time.Duration
	Quiet   schedule.Quiet

	UnminedURL      string
	UnminedSHA256   string
	ChunkProcessors int
	NetherTopY      int

	// InternalAddr serves metrics and the agent's login claims. It is a
	// separate listener so that publishing HTTPAddr cannot publish these.
	InternalAddr string
	// Login is whether the map is behind a login. It is on unless
	// AUTH_DISABLED says otherwise by name.
	Login         bool
	InternalToken string
	SessionTTL    time.Duration

	// Live is whether players and mobs are drawn on the map as they move.
	// Off, nothing asks the bridge for them and the page is not offered a
	// stream.
	Live bool
	// LivePollWait is how long the bridge may hold one request for records
	// open.
	LivePollWait time.Duration
	// LiveTTL is how old a position may be and still be drawn.
	LiveTTL time.Duration
	// LiveKeepalive is the longest a live stream stays silent.
	LiveKeepalive time.Duration
	// LiveMaxEntities caps each of players and mobs, per dimension, in what
	// is sent to a browser.
	LiveMaxEntities int

	// Markers is whether beds, containers and named mobs are read from each
	// snapshot and drawn.
	Markers bool
	// AgentURL is the server agent, which holds each player's waypoints.
	// Empty leaves waypoints off the map.
	AgentURL string

	// Icons is whether live markers are drawn as mob icons and player
	// heads. Off, nothing is fetched and the page draws dots.
	Icons bool
	// IconsRef is the tag or commit of the published samples the mob icons
	// are fetched at.
	IconsRef string

	// Structures is whether the world's structures are read and served.
	Structures bool
	// StructureSeed, when set, is the 32 bits structure placement is
	// seeded with, for a world whose level.dat does not hold them. A whole
	// world seed given for it has been cut to its low 32 bits.
	StructureSeed *uint32
	// StructureSeedSearch is whether a seed the world's records refute is
	// worked out from those records, which is about an hour of one CPU.
	StructureSeedSearch bool

	// Biomes is whether the world's biomes are read and served.
	Biomes bool
	// Trails is whether players' recent positions are kept and served. It
	// needs the live layer, which is where the positions come from.
	Trails bool
	// TrailMaxAge is how long a trail point is kept, and TrailMaxPoints
	// the most kept for one player.
	TrailMaxAge    time.Duration
	TrailMaxPoints int
}

// minTokenLength keeps a placeholder from standing in for a credential.
const minTokenLength = 16

// minRefresh keeps a mistyped interval from pausing world saving in a loop.
const minRefresh = time.Minute

const (
	// maxLivePollWait is the longest wait the bridge honours; it shortens a
	// longer one without saying so.
	maxLivePollWait = 25 * time.Second

	// pathIdleTimeout is the shortest idle timeout between this service and
	// a browser: the load balancer's. A stream silent for this long is cut.
	pathIdleTimeout = 30 * time.Second
	// maxLiveKeepalive leaves a third of that for a keepalive that is
	// written late or delivered slowly. Reaching the timeout itself would
	// drop every quiet stream, so the setting cannot be raised that far.
	maxLiveKeepalive = pathIdleTimeout * 2 / 3

	maxLiveEntities = 10_000

	maxTrailAge    = 7 * 24 * time.Hour
	maxTrailPoints = 50_000
)

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		HTTPAddr:      or(getenv("HTTP_ADDR"), ":8080"),
		DataDir:       or(getenv("DATA_DIR"), "/data"),
		BridgeURL:     getenv("BRIDGE_URL"),
		BridgeToken:   getenv("BRIDGE_TOKEN"),
		Level:         getenv("LEVEL_NAME"),
		UnminedURL:    or(getenv("UNMINED_URL"), DefaultUnminedURL),
		UnminedSHA256: strings.ToLower(or(getenv("UNMINED_SHA256"), DefaultUnminedSHA256)),
		InternalAddr:  or(getenv("INTERNAL_ADDR"), ":9090"),
		InternalToken: getenv("INTERNAL_TOKEN"),
	}
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if u, err := url.Parse(c.BridgeURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fail("BRIDGE_URL must be an http(s) URL, got %q", c.BridgeURL)
	}
	if c.BridgeToken == "" {
		fail("BRIDGE_TOKEN is required")
	}
	if c.Level == "" || strings.ContainsAny(c.Level, `/\`) || c.Level == "." || c.Level == ".." {
		fail("LEVEL_NAME must be the world's directory name, got %q", c.Level)
	}
	if u, err := url.Parse(c.UnminedURL); err != nil || u.Scheme != "https" || u.Host == "" {
		fail("UNMINED_URL must be an https URL, got %q", c.UnminedURL)
	}
	if raw, err := hex.DecodeString(c.UnminedSHA256); err != nil || len(raw) != 32 {
		fail("UNMINED_SHA256 must be 64 hex characters")
	}

	// The map shows where every base is, so the absence of a login is
	// something to ask for, never something to fall into.
	disabled, err := strconv.ParseBool(or(getenv("AUTH_DISABLED"), "false"))
	if err != nil {
		fail("AUTH_DISABLED must be true or false")
	}
	c.Login = !disabled
	if (c.Login || c.InternalToken != "") && len(c.InternalToken) < minTokenLength {
		fail("INTERNAL_TOKEN must be at least %d characters; set AUTH_DISABLED=true and leave it unset to run with no login", minTokenLength)
	}
	if c.SessionTTL, err = time.ParseDuration(or(getenv("SESSION_TTL"), "168h")); err != nil || c.SessionTTL <= 0 {
		fail("SESSION_TTL must be a positive duration")
	}

	if c.Refresh, err = time.ParseDuration(or(getenv("REFRESH_INTERVAL"), "15m")); err != nil {
		fail("REFRESH_INTERVAL: %v", err)
	} else if c.Refresh < minRefresh {
		fail("REFRESH_INTERVAL must be at least %s, got %s", minRefresh, c.Refresh)
	}
	if c.Quiet, err = schedule.ParseQuiet(getenv("QUIET_UTC")); err != nil {
		fail("QUIET_UTC: %v", err)
	}
	if c.ChunkProcessors, err = strconv.Atoi(or(getenv("RENDER_CHUNK_PROCESSORS"), "1")); err != nil || c.ChunkProcessors < 1 || c.ChunkProcessors > 64 {
		fail("RENDER_CHUNK_PROCESSORS must be between 1 and 64")
	}
	// The nether is 128 blocks tall with bedrock at both ends.
	if c.NetherTopY, err = strconv.Atoi(or(getenv("NETHER_TOP_Y"), "100")); err != nil || c.NetherTopY < 1 || c.NetherTopY > 127 {
		fail("NETHER_TOP_Y must be between 1 and 127")
	}

	if c.Live, err = strconv.ParseBool(or(getenv("LIVE_ENABLED"), "true")); err != nil {
		fail("LIVE_ENABLED must be true or false")
	}
	if c.LivePollWait, err = time.ParseDuration(or(getenv("LIVE_POLL_WAIT"), "2s")); err != nil || c.LivePollWait < 100*time.Millisecond || c.LivePollWait > maxLivePollWait {
		fail("LIVE_POLL_WAIT must be a duration between 100ms and %s", maxLivePollWait)
	}
	// The pack samples once a second, so a shorter life than that would
	// blink every marker off between samples.
	if c.LiveTTL, err = time.ParseDuration(or(getenv("LIVE_TTL"), "10s")); err != nil || c.LiveTTL < 2*time.Second || c.LiveTTL > 10*time.Minute {
		fail("LIVE_TTL must be a duration between 2s and 10m")
	}
	if c.LiveKeepalive, err = time.ParseDuration(or(getenv("LIVE_KEEPALIVE"), "15s")); err != nil || c.LiveKeepalive < time.Second || c.LiveKeepalive > maxLiveKeepalive {
		fail("LIVE_KEEPALIVE must be a duration between 1s and %s: connections idle for %s are cut on the way to the browser", maxLiveKeepalive, pathIdleTimeout)
	}
	if c.LiveMaxEntities, err = strconv.Atoi(or(getenv("LIVE_MAX_ENTITIES"), "1000")); err != nil || c.LiveMaxEntities < 1 || c.LiveMaxEntities > maxLiveEntities {
		fail("LIVE_MAX_ENTITIES must be between 1 and %d", maxLiveEntities)
	}

	if c.Markers, err = strconv.ParseBool(or(getenv("MARKERS_ENABLED"), "true")); err != nil {
		fail("MARKERS_ENABLED must be true or false")
	}
	if c.AgentURL = getenv("AGENT_URL"); c.AgentURL != "" {
		if u, err := url.Parse(c.AgentURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail("AGENT_URL must be the http(s) address of the server agent, got %q", c.AgentURL)
		}
		// The agent is asked with the internal token, and waypoints belong
		// to whoever is logged in.
		if !c.Login {
			fail("AGENT_URL needs the login: without one there is no player to show waypoints to")
		}
	}

	if c.Icons, err = strconv.ParseBool(or(getenv("ICONS_ENABLED"), "true")); err != nil {
		fail("ICONS_ENABLED must be true or false")
	}
	// It becomes part of an address, so it is held to what a tag or a
	// commit can be.
	if c.IconsRef = or(getenv("ICONS_REF"), DefaultIconsRef); !iconsRef.MatchString(c.IconsRef) {
		fail("ICONS_REF must be a tag or commit of the samples repository, got %q", c.IconsRef)
	}

	if c.Structures, err = strconv.ParseBool(or(getenv("STRUCTURES_ENABLED"), "true")); err != nil {
		fail("STRUCTURES_ENABLED must be true or false")
	}
	if c.StructureSeedSearch, err = strconv.ParseBool(or(getenv("STRUCTURE_SEED_SEARCH"), "true")); err != nil {
		fail("STRUCTURE_SEED_SEARCH must be true or false")
	}
	if raw := strings.TrimSpace(getenv("STRUCTURE_SEED")); raw != "" {
		// A whole world seed is taken as the game takes it, by its low 32
		// bits, so the number the server prints can be given as it is. The
		// value is left out of the message: it is as good as the seed.
		if seed, err := strconv.ParseInt(raw, 10, 64); err != nil {
			fail("STRUCTURE_SEED must be a whole number: a world seed, or the 32 bits of one from 0 to 4294967295")
		} else {
			// The low four bytes, taken as bytes so that cutting the seed
			// down reads as meant and not as a conversion left unchecked.
			seed32 := binary.LittleEndian.Uint32(binary.LittleEndian.AppendUint64(nil, uint64(seed)))
			c.StructureSeed = &seed32
		}
	}

	// Unlike the older features these start off: the biome pass costs about
	// ten seconds of a CPU per reading, and an image update must not switch
	// that on by itself.
	if c.Biomes, err = strconv.ParseBool(or(getenv("BIOMES_ENABLED"), "false")); err != nil {
		fail("BIOMES_ENABLED must be true or false")
	}
	if c.Trails, err = strconv.ParseBool(or(getenv("TRAILS_ENABLED"), "false")); err != nil {
		fail("TRAILS_ENABLED must be true or false")
	}
	c.TrailMaxAge, c.TrailMaxPoints = 24*time.Hour, 5000
	// Trails are held in memory for every player seen, so both limits have
	// a ceiling: at the top of each, 64 players come to about 100 MB. A
	// disabled feature does not fail startup over its limits.
	if c.Trails {
		if c.TrailMaxAge, err = time.ParseDuration(or(getenv("TRAILS_MAX_AGE"), "24h")); err != nil || c.TrailMaxAge < time.Minute || c.TrailMaxAge > maxTrailAge {
			fail("TRAILS_MAX_AGE must be a duration between 1m and %s", maxTrailAge)
		}
		if c.TrailMaxPoints, err = strconv.Atoi(or(getenv("TRAILS_MAX_POINTS"), "5000")); err != nil || c.TrailMaxPoints < 10 || c.TrailMaxPoints > maxTrailPoints {
			fail("TRAILS_MAX_POINTS must be between 10 and %d", maxTrailPoints)
		}
	}
	return c, errors.Join(errs...)
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
