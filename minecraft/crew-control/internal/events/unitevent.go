package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// unitKinds are the events a unit emits about itself, as opposed to the ones
// the poller derives from its status. The list is closed: a unit cannot
// introduce a kind the console has no colour or meaning for.
var unitKinds = map[string]string{
	"kit_failed":    SevWarn,
	"died":          SevWarn,
	"respawned":     SevInfo,
	"session_error": SevWarn,
	"movement_clip": SevWarn,
}

// reasonCodes mirrors the unit's closed catalogue. A code outside it is
// stored as other: an unknown code is a unit newer than this controller, and
// refusing the event would lose the failure it reports.
var reasonCodes = map[string]bool{
	"missing_tool": true, "kit_chunk_not_loaded": true, "kit_no_path": true, "kit_chest_timeout": true,
	"kit_visit_timeout": true, "session_lost": true, "deposit_rejected": true, "no_pickup": true,
	"no_reachable_target": true, "trap_climb_failed": true, "inventory_desync": true,
	"chunk_not_loaded": true, "no_path": true, "other": true,
	// The clipping reasons of movement_clip, from the unit's act package.
	"predicted_inside_block": true, "corrected_inside_block": true, "corrected_in_free_space": true,
}

// KnownReasonCode reports whether code is in the units' closed catalogue.
func KnownReasonCode(code string) bool { return reasonCodes[code] }

// UnitEvent is a unit's own report of something that happened to it. It has no
// position field on purpose.
type UnitEvent struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	ReasonCode  string    `json:"reason_code"`
	Reason      string    `json:"reason"`
	SessionID   string    `json:"session_id"`
	CrewVersion string    `json:"crew_version"`
	Attempt     *int      `json:"attempt"`
}

var ErrUnitEvent = errors.New("events: bad unit event")

// FromUnit validates a unit's event and turns it into a row for unit. now
// stands in for a missing or implausible timestamp: the controller's clock is
// the one history is ordered by.
func FromUnit(unit string, u UnitEvent, now time.Time) (Event, error) {
	sev, ok := unitKinds[u.Kind]
	if !ok {
		return Event{}, fmt.Errorf("%w: kind %q", ErrUnitEvent, u.Kind)
	}
	if u.SessionID != "" && !sessionIDShape.MatchString(u.SessionID) {
		return Event{}, fmt.Errorf("%w: session_id", ErrUnitEvent)
	}
	if u.Attempt != nil && (*u.Attempt < 0 || *u.Attempt > 1<<15-1) {
		return Event{}, fmt.Errorf("%w: attempt", ErrUnitEvent)
	}
	at := u.At
	if at.IsZero() || at.After(now.Add(time.Minute)) || at.Before(now.Add(-CauseWindow)) {
		at = now
	}
	code := u.ReasonCode
	if code != "" && !reasonCodes[code] {
		code = "other"
	}
	// The text is a unit's own words and may name places, so it stays short
	// and goes only into the payload an operator reads.
	payload, _ := json.Marshal(map[string]string{"reason_text": boundText(u.Reason)})
	e := Event{Unit: unit, At: at, Kind: u.Kind, Severity: sev, Payload: payload,
		ReasonCode: code, SessionID: u.SessionID, Attempt: u.Attempt}
	if len(u.CrewVersion) <= 32 && !strings.ContainsAny(u.CrewVersion, " \n\r\t") {
		e.CrewVersion = u.CrewVersion
	}
	return e, nil
}

func boundText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > ReasonTextMax {
		s = s[:ReasonTextMax]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
