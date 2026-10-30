package squad

// A lab squad's duel is the signal the combat engine fights players by:
// who the squad's units may hunt, and whether the fight is on. Like the
// tactics profile it rides the unit token and the squad's poll, but it
// lapses on its own, because a go that outlived the director that sent it
// would leave the units hunting each other with nobody watching.

import (
	"errors"
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
	// A duel in force is renewed by the director on every poll; one nobody
	// renews lapses after this, so a dead director or a failed withdrawal
	// leaves the units hunting for under a minute, not a whole round. It
	// must stay well above the director's poll so one lost beat ends nothing.
	duelTTL = 45 * time.Second
)

var (
	errNoDuelSquad = errors.New("squad: not a lab squad the store holds")
	errNotCrewUnit = errors.New("squad: a duel names someone who is not a member of a lab squad")
)

// UnitNamer says which in-game name a roster unit plays under. A duel names
// players, and only the roster can tell a crew unit from a human, so a
// Units value that cannot name its units makes the store refuse every duel
// that names anyone.
type UnitNamer interface {
	NameOf(unit string) (string, bool)
}

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

// memberNames is the in-game names of the squad's current members.
func memberNames(sq *squadState, nameOf func(string) (string, bool)) map[string]bool {
	out := make(map[string]bool, len(sq.members))
	for unit := range sq.members {
		if n, ok := nameOf(unit); ok {
			out[n] = true
		}
	}
	return out
}

// namesCrew fails closed: every fighter must be a current member of this
// squad and every enemy a current member of some lab squad, by the in-game
// name the roster gives the member. Without a namer nobody can be named.
func (st *Store) namesCrew(own *squadState, d duelWire) bool {
	if len(d.Fighters) == 0 && len(d.Enemies) == 0 {
		return true
	}
	if st.nameOf == nil {
		return false
	}
	fighters := memberNames(own, st.nameOf)
	for _, n := range d.Fighters {
		if !fighters[n] {
			return false
		}
	}
	lab := map[string]bool{}
	for id, sq := range st.squads {
		if labSquad(id) {
			for n := range memberNames(sq, st.nameOf) {
				lab[n] = true
			}
		}
	}
	for _, n := range d.Enemies {
		if !lab[n] {
			return false
		}
	}
	return true
}

// LabCrew is the in-game names of every current member of a lab squad, in
// name order. Empty when the roster cannot name units.
func (st *Store) LabCrew() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	out := []string{}
	if st.nameOf == nil {
		return out
	}
	seen := map[string]bool{}
	for id, sq := range st.squads {
		if !labSquad(id) {
			continue
		}
		for n := range memberNames(sq, st.nameOf) {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	slices.Sort(out)
	return out
}

func sameDuel(a, b duelWire) bool {
	return a.State == b.State && a.Round == b.Round && slices.Equal(a.Fighters, b.Fighters) &&
		slices.Equal(a.Enemies, b.Enemies) && slices.Equal(a.Eliminated, b.Eliminated)
}

// SetDuel stores the squad's duel state, already checked at the door. It
// refuses a squad the store does not hold or one not marked lab, and a duel
// that names anyone who is not a lab squad member. changed is false when the
// duel is the one already in force: that only renews its lease, so a
// director's heartbeat costs no revision bump and no refetch by the units.
// A changed duel bumps the squad's revision so a poll parked on the old one
// answers and carries the new duelSeq.
func (st *Store) SetDuel(id string, d duelWire) (changed bool, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok || !labSquad(id) {
		return false, errNoDuelSquad
	}
	if !st.namesCrew(sq, d) {
		return false, errNotCrewUnit
	}
	if sq.duel != nil && now.Sub(sq.duelAt) <= duelTTL && sameDuel(*sq.duel, d) {
		sq.duelAt = now
		return false, nil
	}
	sq.duel, sq.duelAt = &d, now
	sq.duelSeq++
	sq.rev++
	return true, nil
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

// duelAnswer is what a unit reads: the stored duel plus Crew, the in-game
// names of every lab squad member right now. Crew is the server's word, never
// the director's: it is not part of the PUT body, so a director cannot name
// someone into it, and a unit that hunts only crew names stays off players
// even if a duel got stored with one.
type duelAnswer struct {
	duelWire
	Crew []string `json:"crew"`
}

// getDuel serves the squad's duel. No duel answers 404: the unit reads
// that as "no fight", the same as a server without the route.
func (a *API) getDuel(w http.ResponseWriter, r *http.Request) {
	d, ok := a.store.Duel(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, duelAnswer{duelWire: d, Crew: a.store.LabCrew()})
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
	changed, err := a.store.SetDuel(id, d)
	switch {
	case errors.Is(err, errNotCrewUnit):
		http.Error(w, "a duel may name only lab squad members", http.StatusForbidden)
		return
	case err != nil:
		http.NotFound(w, r)
		return
	}
	if changed {
		a.broadcast(id)
	}
	w.WriteHeader(http.StatusNoContent)
}
