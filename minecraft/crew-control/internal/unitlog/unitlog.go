// Package unitlog is the narrow view of the stored unit event stream that the
// rule engines read. The rules are pure functions over these values, so the
// same code runs in production and in a replay test, and none of them can see
// a coordinate.
package unitlog

import (
	"encoding/json"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// Kinds the rules care about. task_* and died come from the poller today;
// kit_failed and session_error are what units are to emit once the closed
// failure vocabulary exists.
const (
	KindTaskStarted = "task_started"
	KindTaskDone    = "task_done"
	KindTaskFailed  = "task_failed"
	KindDied        = "died"
	KindKitFailed   = "kit_failed"
	KindSession     = "session_error"
	KindLowHunger   = "low_hunger"
	KindLowHealth   = "low_health"
)

// Event carries no position by construction.
type Event struct {
	ID   int64
	Unit string
	At   time.Time
	Kind string
	// Reason is the closed failure code of a failed task.
	Reason string
	// Cause is the id of the event this one names as its cause, or 0.
	Cause    int64
	Template string
	// Task is the id of the task the event is about, when it has one.
	Task string
	// Duration is how long the task ran; zero when unknown.
	Duration time.Duration
	// Idle marks a roll-up of idle polling, which is not work.
	Idle bool
	// Anchor is a coarse, coordinate-free position fingerprint that is equal
	// when a unit has not moved; empty when unknown.
	Anchor string
}

// payload holds the optional keys a stored event may carry.
type payload struct {
	Reason     string `json:"reason_code"`
	Cause      int64  `json:"cause_ref"`
	Template   string `json:"template"`
	DurationMS int64  `json:"duration_ms"`
	Idle       bool   `json:"idle"`
	Anchor     string `json:"anchor"`
}

// From converts a stored event. A payload that is not the expected object is
// treated as empty rather than failing the whole evaluation.
func From(e events.Event) Event {
	var p payload
	_ = json.Unmarshal(e.Payload, &p)
	return Event{
		ID: e.ID, Unit: e.Unit, At: e.At, Kind: e.Kind, Task: taskOf(e),
		Reason: p.Reason, Cause: p.Cause, Template: p.Template,
		Duration: time.Duration(p.DurationMS) * time.Millisecond,
		Idle:     p.Idle, Anchor: p.Anchor,
	}
}

// FromAll converts a slice, dropping nothing.
func FromAll(es []events.Event) []Event {
	out := make([]Event, len(es))
	for i, e := range es {
		out[i] = From(e)
	}
	return out
}

// Useful reports whether the event is a real completion: idle roll-ups are
// excluded so a hauler's polling never counts as throughput.
func (e Event) Useful() bool { return e.Kind == KindTaskDone && !e.Idle }

func taskOf(e events.Event) string {
	if e.TaskID == nil {
		return ""
	}
	return e.TaskID.String()
}
