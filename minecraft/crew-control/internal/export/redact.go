// Package export builds the bounded, redacted event window an operator
// downloads to hand to an offline tool. It is the one place unit-originated
// strings are kept away from a reader that may treat them as instructions.
package export

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// RedactionVersion changes whenever the allow-list or a shape below does, so
// a stored window can be told from one produced under other rules.
const RedactionVersion = 1

// Line is the whole of what leaves the service for one event. It is a struct
// of allow-listed fields: a column added to the event type is not copied
// until someone adds it here, so a new column cannot leak by default.
type Line struct {
	ID         int64     `json:"id"`
	TS         time.Time `json:"ts"`
	Unit       string    `json:"unit"`
	Kind       string    `json:"kind"`
	Template   string    `json:"template,omitempty"`
	State      string    `json:"state"`
	ReasonCode string    `json:"reason_code,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Count      int       `json:"count"`
	CauseRef   *int64    `json:"cause_ref,omitempty"`
}

// The strings a line may carry are matched against closed shapes with no
// whitespace, no punctuation but _ and -, and a length cap: free text, an
// address or a token cannot take that shape, so it is dropped rather than
// scrubbed, and a scrubber's missed case cannot leak.
var (
	unitShape     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,23}$`)
	kindShape     = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)
	templateShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// maxRolledCount bounds a roll-up's own count; a payload is the unit's input
// and must not be able to inflate a line.
const maxRolledCount = 100000

// Redact turns one stored event into its exportable line. ok is false for an
// event whose unit or kind is not of an exportable shape: such an event is
// left out and counted by the caller, never exported in a degraded form.
func Redact(e events.Event) (Line, bool) {
	if !unitShape.MatchString(e.Unit) || !kindShape.MatchString(e.Kind) {
		return Line{}, false
	}
	l := Line{ID: e.ID, TS: e.At.UTC(), Unit: e.Unit, Kind: e.Kind, Count: 1, CauseRef: e.CauseRef}
	switch e.Severity {
	case events.SevOK, events.SevInfo, events.SevWarn, events.SevCrit:
		l.State = e.Severity
	default:
		l.State = "unknown"
	}
	switch e.Outcome {
	case events.OutcomeOK, events.OutcomeNoop, events.OutcomeFailed:
		l.Outcome = e.Outcome
	}
	if e.ReasonCode != "" {
		l.ReasonCode = "other"
		if events.KnownReasonCode(e.ReasonCode) {
			l.ReasonCode = e.ReasonCode
		}
	}
	// Only two keys are read from the payload, so the rest of it, which
	// holds feed text and the unit's own words, is never looked at.
	var p struct {
		Template string `json:"template"`
		Count    int    `json:"count"`
	}
	if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &p) == nil {
		if templateShape.MatchString(p.Template) {
			l.Template = p.Template
		}
		if p.Count > 1 && p.Count <= maxRolledCount {
			l.Count = p.Count
		}
	}
	return l, true
}

// merge run-length encodes consecutive identical lines. Lines are identical
// when they differ only in id and time; the merged line keeps the first of
// them, so its id and ts still name a real event.
func merge(in []Line) []Line {
	out := make([]Line, 0, len(in))
	for _, l := range in {
		if n := len(out); n > 0 && sameLine(out[n-1], l) {
			out[n-1].Count += l.Count
			continue
		}
		out = append(out, l)
	}
	return out
}

func sameLine(a, b Line) bool {
	if (a.CauseRef == nil) != (b.CauseRef == nil) || (a.CauseRef != nil && *a.CauseRef != *b.CauseRef) {
		return false
	}
	return a.Unit == b.Unit && a.Kind == b.Kind && a.Template == b.Template && a.State == b.State &&
		a.ReasonCode == b.ReasonCode && a.Outcome == b.Outcome
}
