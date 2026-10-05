// Package config reads the map service's settings from the environment.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
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
	return c, errors.Join(errs...)
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
