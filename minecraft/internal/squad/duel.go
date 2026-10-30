package squad

// A lab squad's duel is the signal the combat engine fights players by:
// who the squad's units may hunt, and whether the fight is on. Like the
// tactics profile it rides the unit token and the squad's poll, but it
// lapses on its own, because a go that outlived the director that sent it
// would leave the units hunting each other with nobody watching.

import (
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	duelIdle  = "idle"
	duelArmed = "armed"
	duelGo    = "go"
	duelOver  = "over"

	// A duel names two lines of a few units each; sixteen is far above any
	// arena the lab builds.
	maxDuelNames = 16
	// A player name is a gamertag, at most 16 characters; the cap leaves
	// room for the lab's longer unit names.
	maxDuelName = 32
	// A duel with sixteen names a side is still a few KiB.
	maxDuelLen = 4 << 10
	// A round outlasts the director's longest round timeout by this margin
	// before the squad forgets it was ever told to fight.
	duelTTL = 12 * time.Minute
)

// duelWire is the duel state a director PUTs and a unit GETs. Fighters are
// the squad's own side, Enemies the other: a unit fights only when it is a
// fighter, so a squad-mate that is not in the roster never joins in.
type duelWire struct {
	State      string   `json:"state"`
	Round      string   `json:"round,omitempty"`
	Fighters   []string `json:"fighters,omitempty"`
	Enemies    []string `json:"enemies,omitempty"`
	Eliminated []string `json:"eliminated,omitempty"`
}

func validDuelNames(names []string) bool {
	if len(names) > maxDuelNames {
		return false
	}
	seen := map[string]bool{}
	for _, n := range names {
		// The names reach selectors on the director's side, so a quote or
		// backslash is no name even though it prints.
		if n == "" || utf8.RuneCountInString(n) > maxDuelName || seen[n] ||
			strings.ContainsAny(n, `"\`) || strings.TrimFunc(n, unicode.IsSpace) != n {
			return false
		}
		for _, r := range n {
			if !unicode.IsPrint(r) {
				return false
			}
		}
		seen[n] = true
	}
	return true
}

// valid is the closed shape of a duel: a known state, bounded printable
// names, an eliminated list that is a subset of the two sides, and the
// two sides present for the states that fight — an armed or go duel with
// nobody to hunt is a malformed one, not a harmless one.
func (d duelWire) valid() bool {
	switch d.State {
	case duelIdle, duelOver:
	case duelArmed, duelGo:
		if len(d.Fighters) == 0 || len(d.Enemies) == 0 {
			return false
		}
	default:
		return false
	}
	if !shortPrintable(d.Round, maxLabel) ||
		!validDuelNames(d.Fighters) || !validDuelNames(d.Enemies) || !validDuelNames(d.Eliminated) {
		return false
	}
	for _, n := range d.Fighters {
		if slices.Contains(d.Enemies, n) {
			return false
		}
	}
	for _, n := range d.Eliminated {
		if !slices.Contains(d.Fighters, n) && !slices.Contains(d.Enemies, n) {
			return false
		}
	}
	return true
}

// SetDuel stores the squad's duel state, already checked at the door. ok
// is false for a squad the store does not hold or one not marked lab. It
// bumps the squad's revision so a poll parked on the old one answers and
// carries the new duelSeq.
func (st *Store) SetDuel(id string, d duelWire) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok || !labSquad(id) {
		return false
	}
	sq.duel, sq.duelAt = &d, now
	sq.duelSeq++
	sq.rev++
	return true
}

// Duel is the squad's duel state while it is still in force. ok is false
// for a squad the store does not hold, one nobody started a duel on, or
// one whose duel lapsed.
func (st *Store) Duel(id string) (duelWire, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok || sq.duel == nil || now.Sub(sq.duelAt) > duelTTL {
		return duelWire{}, false
	}
	return *sq.duel, true
}

// getDuel serves the squad's duel. No duel answers 404: the unit reads
// that as "no fight", the same as a server without the route.
func (a *API) getDuel(w http.ResponseWriter, r *http.Request) {
	d, ok := a.store.Duel(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// putDuel stores a duel state. Only a lab squad takes one, whatever token
// the caller holds: a production squad must never be told to hunt players.
func (a *API) putDuel(w http.ResponseWriter, r *http.Request) {
	var d duelWire
	if !decodeStrict(w, r, maxDuelLen, &d) {
		return
	}
	if !d.valid() {
		http.Error(w, "bad duel", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if !a.store.Has(id) {
		http.NotFound(w, r)
		return
	}
	if !labSquad(id) {
		http.Error(w, "not a lab squad", http.StatusForbidden)
		return
	}
	if !a.store.SetDuel(id, d) {
		http.NotFound(w, r)
		return
	}
	a.broadcast(id)
	w.WriteHeader(http.StatusNoContent)
}
