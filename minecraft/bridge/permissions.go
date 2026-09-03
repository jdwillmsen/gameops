package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// PermissionEntry mirrors one object in the server's permissions.json.
type PermissionEntry struct {
	Permission string `json:"permission"`
	XUID       string `json:"xuid"`
}

// AllowlistEntry mirrors one object in the server's allowlist.json.
type AllowlistEntry struct {
	Name               string `json:"name"`
	XUID               string `json:"xuid"`
	IgnoresPlayerLimit bool   `json:"ignoresPlayerLimit"`
}

// ParsePermissions reads permissions.json into an xuid -> permission level
// map ("operator", "member", or "visitor"). Any XUID absent from the file
// falls back to the server's default-player-permission-level, which this
// package does not read (it lives in server.properties, not permissions.json
// or allowlist.json) — callers must treat a missing XUID as "unknown", not
// "visitor".
func ParsePermissions(r io.Reader) (map[string]string, error) {
	var entries []PermissionEntry
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse permissions.json: %w", err)
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.XUID == "" {
			continue
		}
		out[e.XUID] = e.Permission
	}
	return out, nil
}

// ParseAllowlist reads allowlist.json into a slice of entries.
func ParseAllowlist(r io.Reader) ([]AllowlistEntry, error) {
	var entries []AllowlistEntry
	if err := json.NewDecoder(r).Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse allowlist.json: %w", err)
	}
	return entries, nil
}
