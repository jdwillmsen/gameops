package squad

// The console's squad stream is a public read: it carries only what anonymous
// viewers may see. Positions and the operator id on an order stay out, so the
// same snapshot can drive every viewer's panel.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
)

const (
	defaultStreamEvery     = 2 * time.Second
	defaultStreamKeepalive = 15 * time.Second
	streamWriteTimeout     = 5 * time.Second
	// The console panel is a glance, not an audit log; the store keeps more.
	consoleLogDepth = 12
	maxStreams      = 64
)

// consoleList is one snapshot event. Battalions stay for parity with the
// list route; the panel does not need them yet.
type consoleList struct {
	Squads     []consoleSquad `json:"squads"`
	Battalions []Battalion    `json:"battalions"`
}

type consoleSquad struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Leader    string           `json:"leader"`
	Battalion string           `json:"battalion,omitempty"`
	Members   []consoleMember  `json:"members"`
	Order     *consoleOrder    `json:"order,omitempty"`
	Callouts  []consoleCallout `json:"callouts"`
}

// consoleMember has no position: the panel only needs what to draw and which
// camera to open.
type consoleMember struct {
	Unit   string    `json:"unit"`
	Role   string    `json:"role"`
	HP     float64   `json:"hp"`
	Food   int       `json:"food"`
	Weapon string    `json:"weapon"`
	Alive  bool      `json:"alive"`
	At     time.Time `json:"at,omitempty"`
}

// consoleOrder drops By: who issued an order is operator data.
type consoleOrder struct {
	Kind   string    `json:"kind"`
	Target string    `json:"target,omitempty"`
	At     time.Time `json:"at"`
}

type consoleCallout struct {
	Kind string    `json:"kind"`
	From string    `json:"from"`
	At   time.Time `json:"at"`
}

// streamSquads serves GET /v1/squads/stream. The store is small and every
// view is a pure render, so a per-connection tick is simpler than wiring a
// second fan-out; a quiet fleet still gets a keepalive copy so the ingress
// does not time the stream out.
func (a *API) streamSquads(w http.ResponseWriter, r *http.Request) {
	if !a.acquireStream() {
		http.Error(w, "too many viewers", http.StatusServiceUnavailable)
		return
	}
	defer a.releaseStream()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	var last []byte
	var sentAt time.Time
	send := func(force bool) bool {
		data, err := json.Marshal(a.store.consoleSnapshot())
		if err != nil {
			return false
		}
		if !force && bytes.Equal(data, last) && time.Since(sentAt) < a.streamKeepalive {
			return true
		}
		if err := writeSquadEvent(w, rc, data); err != nil {
			return false
		}
		last = data
		sentAt = time.Now()
		return true
	}
	if !send(true) {
		return
	}

	ticker := time.NewTicker(a.streamEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send(false) {
				return
			}
		}
	}
}

func (a *API) acquireStream() bool {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.streams >= maxStreams {
		return false
	}
	a.streams++
	return true
}

func (a *API) releaseStream() {
	a.streamMu.Lock()
	a.streams--
	a.streamMu.Unlock()
}

// writeSquadEvent bounds a stalled viewer's write exactly as the unit stream
// does, so a slow console cannot hold the slot forever.
func writeSquadEvent(w http.ResponseWriter, rc *http.ResponseController, data []byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: squads\ndata: %s\n\n", data); err != nil {
		return err
	}
	return rc.Flush()
}

// consoleSnapshot renders every live squad in one lock so an event cannot
// pair one squad's members with another's order.
func (st *Store) consoleSnapshot() consoleList {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	ids := make([]string, 0, len(st.squads))
	for id := range st.squads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]consoleSquad, 0, len(ids))
	for _, id := range ids {
		out = append(out, st.consoleSquadLocked(st.squads[id], now))
	}
	return consoleList{Squads: out, Battalions: st.battalionsLocked()}
}

// consoleSquad renders one squad in the console-safe shape the public
// single-squad read serves, redacted exactly as the stream is: no member
// position and no operator id. ok is false for a squad the store has
// never heard of.
func (st *Store) consoleSquad(id string) (consoleSquad, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok {
		return consoleSquad{}, false
	}
	return st.consoleSquadLocked(sq, now), true
}

// consoleSquadLocked is the redaction of one squad's view. Callers hold
// the mutex.
func (st *Store) consoleSquadLocked(sq *squadState, now time.Time) consoleSquad {
	v := st.render(sq, now)
	cs := consoleSquad{
		ID:        v.ID,
		Name:      v.Name,
		Leader:    v.Leader,
		Battalion: v.Battalion,
		Members:   make([]consoleMember, 0, len(v.Members)),
	}
	for _, m := range v.Members {
		cs.Members = append(cs.Members, consoleMember{
			Unit:   m.Unit,
			Role:   m.Role,
			HP:     m.HP,
			Food:   m.Food,
			Weapon: m.Weapon,
			Alive:  m.Alive,
			At:     m.At,
		})
	}
	if n := len(sq.orders); n > 0 {
		last := sq.orders[n-1]
		cs.Order = &consoleOrder{Kind: last.kind, Target: last.target, At: last.at}
	}
	from := max(0, len(sq.callouts)-consoleLogDepth)
	for i := len(sq.callouts) - 1; i >= from; i-- {
		c := sq.callouts[i]
		cs.Callouts = append(cs.Callouts, consoleCallout{Kind: c.kind, From: c.from, At: c.at})
	}
	return cs
}
