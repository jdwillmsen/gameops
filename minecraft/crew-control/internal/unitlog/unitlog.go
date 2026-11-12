// Package unitlog is the narrow view of the stored unit event stream that the
// rule engines read. The rules are pure functions over these values, so the
// same code runs in production and in a replay test, and none of them can see
// a coordinate.
package unitlog

import (
	"encoding/json"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/dimension"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
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
	// KindEffectGained and KindEffectLost carry the closed effect name and
	// the amplifier in the payload, never a duration.
	KindEffectGained = "effect_gained"
	KindEffectLost   = "effect_lost"
	// KindThreatHigh is several hostile mobs close to the unit; the payload
	// has counts only.
	KindThreatHigh = "threat_high"
	// KindFightEnded closes a warrior's run of fighting; the payload has the
	// counts and an outcome from a closed list.
	KindFightEnded = "fight_ended"
	// KindRubberbanding is the server repeatedly overruling a unit's
	// movement; the payload has the correction count and window only.
	KindRubberbanding = "rubberbanding"
	// KindOnline and KindOffline mark a unit's connection coming and going;
	// the rules that need exposure time read them.
	KindOnline  = "online"
	KindOffline = "offline"
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
	// World is the roster slug of the unit's world and Dim the closed name of
	// its dimension, both empty on an event stored before they were recorded.
	World, Dim string
}

const other = "other"

// templates mirrors the units' closed task template list. A name outside it
// is a unit newer than this controller or free text, and reads as "other" so
// it cannot carry a player name, position or token into a public sentence.
var templates = map[string]bool{
	"gather_and_deposit": true, "haul": true, "withdraw": true, "deposit": true, "mine_shaft": true,
	"goto": true, "inspect": true, "observe_blocks": true, "place_block": true, "craft": true,
	"repair_build": true, "build_blueprint": true, "use_bed": true, "shelter": true,
}

// safeReason keeps a reason code only if it is in the closed catalogue, so a
// free-text reason never reaches a sentence an anonymous caller reads.
func safeReason(r string) string {
	if r == "" || events.KnownReasonCode(r) {
		return r
	}
	return other
}

// safeDim keeps a dimension name only if it is in the closed list.
func safeDim(d string) string {
	if d == "" {
		return ""
	}
	for _, n := range dimension.Names {
		if n == d {
			return d
		}
	}
	return other
}

// safeWorld drops a world that is not shaped like a roster slug.
func safeWorld(w string) string {
	if roster.ValidWorld(w) {
		return w
	}
	return ""
}

func safeTemplate(t string) string {
	if t == "" || templates[t] {
		return t
	}
	return other
}

// payload holds the optional keys a stored event may carry. The analytic
// columns of a stored row win over the same key here; the payload form is
// what an older row or a replay fixture has.
type payload struct {
	Reason     string `json:"reason_code"`
	Cause      int64  `json:"cause_ref"`
	Template   string `json:"template"`
	DurationMS int64  `json:"duration_ms"`
	Idle       bool   `json:"idle"`
	Anchor     string `json:"anchor"`
	// Task holds an id that is not a UUID, which the row's task_id cannot.
	Task  string `json:"task"`
	World string `json:"world"`
	Dim   string `json:"dim"`
}

// From converts a stored event. A payload that is not the expected object is
// treated as empty rather than failing the whole evaluation.
func From(e events.Event) Event {
	var p payload
	_ = json.Unmarshal(e.Payload, &p)
	reason := p.Reason
	if e.ReasonCode != "" {
		reason = e.ReasonCode
	}
	cause := p.Cause
	if e.CauseRef != nil {
		cause = *e.CauseRef
	}
	ms := p.DurationMS
	if e.DurationMS != nil {
		ms = int64(*e.DurationMS)
	}
	task := taskOf(e)
	if task == "" {
		task = p.Task
	}
	return Event{
		ID: e.ID, Unit: e.Unit, At: e.At, Kind: e.Kind, Task: task,
		Reason: safeReason(reason), Cause: cause, Template: safeTemplate(p.Template),
		Duration: time.Duration(ms) * time.Millisecond,
		Idle:     p.Idle || e.Outcome == events.OutcomeNoop, Anchor: p.Anchor,
		World: safeWorld(p.World), Dim: safeDim(p.Dim),
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
