package auth

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	maxReports   = 20
	reportWindow = time.Minute
)

// The page reports only words from these lists. An unlisted error name is
// logged as "other" and an unlisted kind or stage is refused, so nothing a
// stranger types ever reaches the log.
var (
	reportKinds  = map[string]bool{"login": true, "register": true}
	reportStages = map[string]bool{"begin": true, "credential": true, "finish": true}
	// "network" and "refused" are the page's own words for a fetch that never
	// answered and an answer it showed as an error; the rest are the
	// DOMException names a WebAuthn call rejects with.
	reportErrors = map[string]bool{
		"network": true, "refused": true,
		"NotAllowedError": true, "SecurityError": true, "InvalidStateError": true, "NotSupportedError": true,
		"AbortError": true, "ConstraintError": true, "UnknownError": true, "DataError": true,
		"EncodingError": true, "OperationError": true, "TypeError": true,
	}
)

// reportLimit bounds client reports for the whole server, not per caller:
// the route is open to anyone and the proxy hides the caller's address.
// It is separate from the invite throttle so a flood of reports cannot
// lock an invited operator out.
type reportLimit struct {
	mu   sync.Mutex
	now  func() time.Time
	seen []time.Time
}

func (l *reportLimit) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := l.now().Add(-reportWindow)
	i := 0
	for i < len(l.seen) && !l.seen[i].After(cut) {
		i++
	}
	l.seen = l.seen[i:]
	if len(l.seen) >= maxReports {
		return false
	}
	l.seen = append(l.seen, l.now())
	return true
}

// report logs where in the browser a ceremony failed. Those failures never
// reach a ceremony route, so without this the server cannot tell a person
// who tried and was refused by their browser from one who never tried.
func (a *Auth) report(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		rejectStateChanging(w, r)
		return
	}
	if !a.reports.allow() {
		fail(w, http.StatusTooManyRequests, "slow_down")
		return
	}
	raw, ok := readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind  string `json:"kind"`
		Stage string `json:"stage"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &req) != nil || !reportKinds[req.Kind] || !reportStages[req.Stage] {
		fail(w, http.StatusBadRequest, "bad_body")
		return
	}
	if !reportErrors[req.Error] {
		req.Error = "other"
	}
	a.event("client_ceremony_failed", logging.Fields{"kind": req.Kind, "stage": req.Stage, "error": req.Error})
	w.WriteHeader(http.StatusNoContent)
}
