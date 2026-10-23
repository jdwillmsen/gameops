package squad

// The unit routes share the token the work routes use, and refuse anything
// that passed the ingress exactly as the work routes do. The operator's
// order route reuses the signed-in operator session instead.

// The package lives at the minecraft tree's shared internal so both sides
// reach it: crew-control serves it, and crew's offline tests run the real
// routes against the real engine. It names no crew-control type, so the
// console hands its roster and its auth in through Units and Gate.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxWait = 25 * time.Second
	minWait = time.Second
	// A hello, a status or a call-out is a few dozen bytes by contract; the
	// cap keeps a lost unit from becoming a place to park data.
	maxBodyLen = 1 << 10
	// A squad's display name is one line on the console.
	maxName = 24
	// A focus target is a mob name; anything longer is not one.
	maxTarget = 32
)

var (
	squadID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	// The message tables are closed: a unit gets exactly these, and a
	// kinds-not-in-the-table report is refused rather than round-tripped
	// with an unknown shape.
	roleRE    = regexp.MustCompile(`^(vanguard|striker|ranger)$`)
	orderRE   = regexp.MustCompile(`^(focus|hold|advance|regroup|retreat)$`)
	calloutRE = regexp.MustCompile(`^(low_health|need_heal|target_down|boss_phase)$`)
)

// Gate is the sign-in the operator route needs: it guards the route and
// names the operator who gave the order. crew-control passes its auth.Auth
// behind a small adapter; work.API takes the richer shape from it directly.
type Gate interface {
	Require(h http.Handler) http.Handler
	Operator(r *http.Request) (opID string, ok bool)
}

// Units is the roster lookup the unit routes need: a hello, a status or a
// call-out is only taken from a unit the console knows.
type Units interface {
	Has(unit string) bool
}

type API struct {
	store *Store
	units Units
	token [sha256.Size]byte
	now   func() time.Time

	// waits holds, per squad, the channels of open long polls; a new order
	// or call-out closes them all, so a waiting unit answers at once
	// instead of riding out its wait.
	mu    sync.Mutex
	waits map[string]map[chan struct{}]struct{}
}

// Routes registers the public reads and, with a token configured, the
// unit-facing writes. With no token the unit routes do not exist rather
// than exist to refuse, as the work routes do.
func Routes(mux *http.ServeMux, st *Store, units Units, token string) *API {
	a := &API{store: st, units: units, token: sha256.Sum256([]byte(token)), now: time.Now,
		waits: map[string]map[chan struct{}]struct{}{}}
	mux.HandleFunc("GET /v1/squads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, listBody{Squads: st.List(), Battalions: st.Battalions()})
	})
	mux.HandleFunc("GET /v1/squads/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := st.Get(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	if token == "" {
		return a
	}
	mux.HandleFunc("POST /v1/squads/{id}/hello", a.guard(a.hello))
	mux.HandleFunc("POST /v1/squads/{id}/status", a.guard(a.status))
	mux.HandleFunc("POST /v1/squads/{id}/callout", a.guard(a.callout))
	mux.HandleFunc("GET /v1/squads/{id}/orders", a.guard(a.orders))
	return a
}

// MountOperator registers the operator order route. Without sign-in on it
// still exists and answers 401, as the other operator routes do.
func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	mux.Handle("POST /v1/squads/{id}/order", g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, _ := g.Operator(r)
		a.order(w, r, op)
	})))
}

// guard enforces the shared-token, cluster-only contract of the work
// routes.
func (a *API) guard(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only cluster-internal traffic is meant to reach these routes.
		if r.Header.Get("X-Forwarded-For") != "" {
			http.NotFound(w, r)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(got))
		if !ok || subtle.ConstantTimeCompare(sum[:], a.token[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// decodeStrict reads one JSON body of at most maxBodyLen with the field
// table closed; a second value after the first is not part of any of our
// formats.
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyLen))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if _, e := dec.Token(); !errors.Is(e, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "bad body", http.StatusBadRequest)
		return false
	}
	return true
}

// helloBody is one unit's claim. The path names the squad it says it is
// joining; the body says who it is and what it will be there. The
// battalion fields nest the squad, data only.
type helloBody struct {
	Unit          string `json:"unit"`
	SquadName     string `json:"squad_name,omitempty"`
	Role          string `json:"role"`
	Battalion     string `json:"battalion,omitempty"`
	BattalionName string `json:"battalion_name,omitempty"`
}

func (a *API) hello(w http.ResponseWriter, r *http.Request) {
	var b helloBody
	if !decodeStrict(w, r, &b) {
		return
	}
	if !a.units.Has(b.Unit) ||
		!squadID.MatchString(r.PathValue("id")) ||
		!roleRE.MatchString(b.Role) ||
		utf8.RuneCountInString(b.SquadName) > maxName {
		http.Error(w, "bad hello", http.StatusBadRequest)
		return
	}
	a.store.Hello(Hello{
		Unit:          b.Unit,
		Squad:         r.PathValue("id"),
		SquadName:     b.SquadName,
		Role:          role(b.Role),
		Battalion:     b.Battalion,
		BattalionName: b.BattalionName,
	})
	w.WriteHeader(http.StatusNoContent)
}

// statusBody is one unit's own report of how it is. Alive is not a field:
// the store reads a zero heart as dead, and nothing else.
type statusBody struct {
	Unit   string   `json:"unit"`
	HP     float64  `json:"hp"`
	Food   int      `json:"food"`
	Weapon string   `json:"weapon"`
	Pos    position `json:"pos"`
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	var b statusBody
	if !decodeStrict(w, r, &b) {
		return
	}
	if !a.units.Has(b.Unit) || b.Food < 0 || b.Food > 20 {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	// A unit may report to the squad it belongs to without naming it; the
	// claim itself says where it sits after a restart of the store.
	a.store.Status(statusIn{Unit: b.Unit, Squad: r.PathValue("id"), HP: b.HP,
		Food: b.Food, Weapon: b.Weapon, Pos: b.Pos})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) callout(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Unit string `json:"unit"`
		Kind string `json:"kind"`
	}
	if !decodeStrict(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	if !a.units.Has(b.Unit) || !calloutRE.MatchString(b.Kind) {
		http.Error(w, "bad callout", http.StatusBadRequest)
		return
	}
	if !a.store.Has(id) {
		http.NotFound(w, r)
		return
	}
	if !a.store.Member(id, b.Unit) {
		http.Error(w, "not a member", http.StatusConflict)
		return
	}
	a.store.Callout(calloutIn{Squad: id, Kind: b.Kind, From: b.Unit}, a.now())
	// Calls and orders both advance the cursor, so both wake the waiters.
	a.broadcast(id)
	w.WriteHeader(http.StatusNoContent)
}

// orderBody is one operator instruction. Target points a focus order at a
// named threat; the other kinds carry none.
type orderBody struct {
	Kind   string `json:"kind"`
	Target string `json:"target,omitempty"`
}

func (a *API) order(w http.ResponseWriter, r *http.Request, op string) {
	var b orderBody
	if !decodeStrict(w, r, &b) {
		return
	}
	if !orderRE.MatchString(b.Kind) || utf8.RuneCountInString(b.Target) > maxTarget {
		http.Error(w, "bad order", http.StatusBadRequest)
		return
	}
	// An order must land on a squad that exists: an operator reading an
	// empty console should see that, not a 202 into nothing.
	id := r.PathValue("id")
	if !a.store.Has(id) {
		http.NotFound(w, r)
		return
	}
	a.store.Order(orderIn{Squad: id, Kind: b.Kind, Target: b.Target, By: op})
	a.broadcast(id)
	w.WriteHeader(http.StatusAccepted)
}

// orders long-polls the squad's messages: everything appended since the
// unit's cursor, the standing order and where the leader stands. A poll
// with nothing new waits up to the asked-for bound, and answers as soon as
// an order or call-out lands.
func (a *API) orders(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		http.Error(w, "bad since", http.StatusBadRequest)
		return
	}
	timer := time.NewTimer(parseWait(r.URL.Query().Get("wait")))
	defer timer.Stop()
	for {
		d, ok := a.store.Delta(id, since)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// A first poll (since=0), anything appended past the cursor, or the
		// standing order riding along answers at once: a joining unit must
		// see the leader and its orders without waiting this turn out.
		// The standing order is there for a unit that missed only the
		// leader's turn.
		if since == 0 || d.Cursor > since || len(d.Orders) > 0 || len(d.Callouts) > 0 {
			writeJSON(w, http.StatusOK, d)
			return
		}
		select {
		case <-a.waiter(id):
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			// Shutdown: nobody is left to read an answer.
			return
		}
	}
}

// parseSince reads a non-negative cursor; anything a unit cannot have is
// refused rather than quietly reinterpreted.
func parseSince(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := json.Number(s).Int64()
	if err != nil || n < 0 {
		return 0, errors.New("since must be a non-negative integer")
	}
	return n, nil
}

func parseWait(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return maxWait
	}
	return max(minWait, min(d, maxWait))
}

// waiter hands back the channel the next broadcast for that squad closes.
func (a *API) waiter(squad string) <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch := make(chan struct{})
	g, ok := a.waits[squad]
	if !ok {
		g = map[chan struct{}]struct{}{}
		a.waits[squad] = g
	}
	g[ch] = struct{}{}
	return ch
}

// broadcast closes every open poll for the squad. The closing of one of
// these channels unblocks exactly the goroutine waiting on it.
func (a *API) broadcast(squad string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ch := range a.waits[squad] {
		close(ch)
	}
	delete(a.waits, squad)
}

type listBody struct {
	Squads     []View      `json:"squads"`
	Battalions []Battalion `json:"battalions"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
