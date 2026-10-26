package events

import (
	"time"
)

// CauseWindow is how far back a failure looks for what explained it. Past
// this a kit failure is a different incident, and a link would assert a
// cause the data cannot support.
const CauseWindow = 30 * time.Minute

// Outcomes of a task row. A noop is a poll that found nothing to do; it is
// stored only as a roll-up, never one row per poll.
const (
	OutcomeOK     = "ok"
	OutcomeNoop   = "noop"
	OutcomeFailed = "failed"
)

// causeKinds are the unit-side events that explain a task failure that
// follows them: a unit without its kit, a lost session or a death fails
// whatever it tries next.
var causeKinds = map[string]bool{"kit_failed": true, "session_error": true, "died": true}

// ReasonTextMax bounds the free-text reason kept beside the code. The text is
// a unit's own words and is never exported to a model; only the code is.
const ReasonTextMax = 120

// Outcome is how a finished-task event ended, empty for events that are not
// the end of a task. It uses the same idle rule as the roll-up, so an
// outcome of noop and a collapsed poll are one definition.
func Outcome(kind, template string, took time.Duration) string {
	switch kind {
	case "task_failed":
		return OutcomeFailed
	case "task_done":
		if IsIdlePoll(kind, template, took) {
			return OutcomeNoop
		}
		return OutcomeOK
	}
	return ""
}

// Enrich fills the analytic columns of a polled event from what the poller
// knew about it.
func Enrich(e *Event, h Hint) {
	e.ReasonCode = h.ReasonCode
	e.Outcome = Outcome(e.Kind, h.Template, h.Took)
	if h.Took > 0 {
		ms := int(h.Took.Milliseconds())
		e.DurationMS = &ms
	}
}

// LinkCause returns the id of the latest cause-kind event on e's unit within
// CauseWindow before e, or nil. Only a failed task is linked: a success
// after a kit failure is not explained by it. The caller passes events it has
// already stored, so the link is always to a row that exists.
func LinkCause(e Event, prior []Event) *int64 {
	if e.Kind != "task_failed" {
		return nil
	}
	var best *Event
	for i := range prior {
		p := &prior[i]
		if p.Unit != e.Unit || !causeKinds[p.Kind] || p.ID == 0 || p.At.After(e.At) || e.At.Sub(p.At) > CauseWindow {
			continue
		}
		if best == nil || p.At.After(best.At) || (p.At.Equal(best.At) && p.ID > best.ID) {
			best = p
		}
	}
	if best == nil {
		return nil
	}
	id := best.ID
	return &id
}
