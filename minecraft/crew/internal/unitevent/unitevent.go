// Package unitevent carries the unit's account of what happened to it (a
// failed kit visit, a lost session) from where it is noticed to the control
// client that reports it. The producers are deep in the game loop and must not
// know about HTTP, so they emit here and whatever is listening takes it.
package unitevent

import (
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/sessionid"
)

// Event is the wire form the controller's unit-events route accepts. It has
// no position on purpose: the controller stores it where the public console's
// explainer can read it.
type Event struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	ReasonCode  string    `json:"reason_code,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	CrewVersion string    `json:"crew_version,omitempty"`
}

var (
	sink    atomic.Pointer[func(Event)]
	version atomic.Pointer[string]
)

// SetSink installs the listener, or removes it with nil. With none, events
// are dropped: a unit run without a controller has nowhere to send them.
func SetSink(f func(Event)) {
	if f == nil {
		sink.Store(nil)
		return
	}
	sink.Store(&f)
}

// SetVersion names the crew build stamped on every event.
func SetVersion(v string) { version.Store(&v) }

// Emit hands e to the listener, filling the time, session and build the
// producer left blank. It never blocks: the listener's own queue decides
// what to drop.
func Emit(e Event) {
	f := sink.Load()
	if f == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if e.SessionID == "" {
		e.SessionID = sessionid.Current()
	}
	if v := version.Load(); v != nil && e.CrewVersion == "" {
		e.CrewVersion = *v
	}
	(*f)(e)
}
