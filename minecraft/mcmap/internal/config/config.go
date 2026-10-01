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
}

// minTokenLength keeps a placeholder from standing in for a credential.
const minTokenLength = 16

// minRefresh keeps a mistyped interval from pausing world saving in a loop.
const minRefresh = time.Minute

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
	if c.Login && len(c.InternalToken) < minTokenLength {
		fail("INTERNAL_TOKEN must be at least %d characters; set AUTH_DISABLED=true to run with no login", minTokenLength)
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
	return c, errors.Join(errs...)
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
