package events

import (
	"errors"
	"regexp"
	"strconv"
)

// Reason codes and session ids are matched exactly, so the filter accepts only
// the shapes a unit can emit: anything else could not match a row and would
// read as empty history.
var (
	reasonCodeShape = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	sessionIDShape  = regexp.MustCompile(`^[0-9a-f]{4,32}$`)
)

func validOutcome(s string) bool {
	return s == OutcomeOK || s == OutcomeNoop || s == OutcomeFailed
}

// parseAnalyticFilters reads reason_code, outcome, session_id and cause_ref.
// A malformed value is an error, like every other filter.
func parseAnalyticFilters(get func(string) string, q *Query) error {
	if s := get("reason_code"); s != "" {
		if !reasonCodeShape.MatchString(s) {
			return errors.New("bad reason_code")
		}
		q.ReasonCode = s
	}
	if s := get("outcome"); s != "" {
		if !validOutcome(s) {
			return errors.New("unknown outcome")
		}
		q.Outcome = s
	}
	if s := get("session_id"); s != "" {
		if !sessionIDShape.MatchString(s) {
			return errors.New("bad session_id")
		}
		q.SessionID = s
	}
	if s := get("cause_ref"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 {
			return errors.New("bad cause_ref")
		}
		q.CauseRef = n
	}
	return nil
}
