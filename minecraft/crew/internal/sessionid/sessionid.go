// Package sessionid names one connection of the unit to the game server, so
// log lines and reports from before and after a reconnect can be told apart
// without parsing timestamps.
package sessionid

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
)

var current atomic.Pointer[string]

// Begin starts a new session and returns its id. The id is random rather than
// a counter because a restarted process would otherwise reuse the ids of the
// one before it.
func Begin() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	current.Store(&id)
	return id
}

// Current is the id of the latest session, empty before the first connect.
func Current() string {
	if p := current.Load(); p != nil {
		return *p
	}
	return ""
}
