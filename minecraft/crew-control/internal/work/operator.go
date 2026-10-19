package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

const (
	historyLen   = 50
	maxTaskBody  = 4 << 10
	maxHaulItems = 8
	maxHaulFrom  = 4
	maxQty       = 256
	stoppedNote  = "stopped the unit"

	minXZ, maxXZ = -500, 499
	minY, maxY   = -64, 320
)

// Gate is the part of auth.Auth the operator routes need.
type Gate interface {
	Require(h http.Handler) http.Handler
	Operator(r *http.Request) (auth.Operator, bool)
}

// View is an item as an operator sees it. The body carries coordinates, so
// it goes only to signed-in responses and is never logged.
type View struct {
	ID       uuid.UUID       `json:"id"`
	Unit     string          `json:"unit"`
	Kind     string          `json:"kind"`
	Body     json.RawMessage `json:"body"`
	State    string          `json:"state"`
	Progress int             `json:"progress"`
	Reason   string          `json:"reason"`
	Operator string          `json:"operator"`
	Created  time.Time       `json:"created"`
	Updated  time.Time       `json:"updated"`
}

func view(it Item) View {
	return View{it.ID, it.Unit, it.Kind, it.Body, it.State, it.Progress, it.Reason, it.Operator, it.Created, it.Updated}
}

func views(items []Item) []View {
	out := make([]View, len(items))
	for i, it := range items {
		out[i] = view(it)
	}
	return out
}

// Feed renders the work event of a unit: its recent items, newest first.
func (a *API) Feed(ctx context.Context, unit string) ([]byte, error) {
	items, err := a.store.Recent(ctx, unit, historyLen)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Unit  string `json:"unit"`
		Items []View `json:"items"`
	}{unit, views(items)})
}

// MountOperator registers the routes a signed-in operator drives units with.
// Without sign-in configured they still exist and answer 401.
func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	post := func(h func(http.ResponseWriter, *http.Request, string, string)) http.Handler {
		return g.Require(a.unitRoute(func(w http.ResponseWriter, r *http.Request, unit string) {
			op, _ := g.Operator(r)
			h(w, r, unit, op.ID)
		}))
	}
	mux.Handle("POST /v1/work/{unit}/tasks", post(a.queueTask))
	mux.Handle("POST /v1/work/{unit}/goto", post(a.queueGoto))
	mux.Handle("POST /v1/work/{unit}/stop", post(a.stop))
	mux.Handle("POST /v1/work/{unit}/items/{id}/cancel", post(a.cancelItem))
	mux.HandleFunc("GET /v1/work/{unit}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			opError(w, http.StatusUnauthorized, "signed_out")
			return
		}
		a.unitRoute(a.history).ServeHTTP(w, r)
	})
}

func (a *API) unitRoute(h func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unit := r.PathValue("unit")
		if _, ok := a.units.Get(unit); !ok {
			opError(w, http.StatusNotFound, "no such unit")
			return
		}
		h(w, r, unit)
	})
}

func opJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func opError(w http.ResponseWriter, status int, msg string) {
	opJSON(w, status, map[string]string{"error": msg})
}

func opStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, ErrUnavailable):
		opError(w, http.StatusServiceUnavailable, "queue unavailable")
	case errors.Is(err, ErrInvalid):
		opError(w, http.StatusBadRequest, "invalid request")
	default:
		opError(w, http.StatusInternalServerError, "queue error")
	}
}

// changed wakes the unit's poll and tells signed-in streams.
func (a *API) changed(unit string) {
	a.Wake(unit)
	a.notify(unit)
}

func (a *API) history(w http.ResponseWriter, r *http.Request, unit string) {
	data, err := a.Feed(r.Context(), unit)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(data, '\n'))
}

func (a *API) queueTask(w http.ResponseWriter, r *http.Request, unit, op string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTaskBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			opError(w, http.StatusRequestEntityTooLarge, "task too large")
			return
		}
		opError(w, http.StatusBadRequest, "bad task")
		return
	}
	a.enqueueTask(w, r, unit, op, body)
}

func (a *API) queueGoto(w http.ResponseWriter, r *http.Request, unit, op string) {
	var p struct {
		X *int `json:"x"`
		Y *int `json:"y"`
		Z *int `json:"z"`
	}
	if err := decodeStrict(http.MaxBytesReader(w, r.Body, maxTaskBody), &p); err != nil || p.X == nil || p.Y == nil || p.Z == nil {
		opError(w, http.StatusBadRequest, "bad goto")
		return
	}
	body, _ := json.Marshal(map[string]any{"template": "goto", "to": point{p.X, p.Y, p.Z}})
	a.enqueueTask(w, r, unit, op, body)
}

func (a *API) enqueueTask(w http.ResponseWriter, r *http.Request, unit, op string, body []byte) {
	// The message names the rule, never the value: coordinates stay out of
	// responses that may be logged by a proxy.
	if err := validateTemplate(body); err != nil {
		opError(w, http.StatusBadRequest, err.Error())
		return
	}
	it, err := a.store.Enqueue(r.Context(), unit, KindTask, body, op)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	a.changed(unit)
	opJSON(w, http.StatusAccepted, view(it))
}

// stopUnit cancels everything open, then queues the stop, so the stop itself
// is not among what was cancelled.
func (a *API) stopUnit(ctx context.Context, unit, op string) (cancelled []Item, stop Item, err error) {
	cancelled, err = a.store.CancelOpen(ctx, unit)
	if err != nil {
		return nil, Item{}, err
	}
	// If this fails after CancelOpen succeeded the caller answers 503, yet the
	// unit keeps running its current work until the next stop gets through.
	stop, err = a.store.Enqueue(ctx, unit, KindCommand, json.RawMessage(`{"name":"stop"}`), op)
	return cancelled, stop, err
}

func (a *API) stop(w http.ResponseWriter, r *http.Request, unit, op string) {
	cancelled, stop, err := a.stopUnit(r.Context(), unit, op)
	if len(cancelled) > 0 || err == nil {
		a.changed(unit)
	}
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	opJSON(w, http.StatusAccepted, struct {
		View
		Cancelled int `json:"cancelled"`
	}{view(stop), len(cancelled)})
}

type cancelled struct {
	View
	Note string `json:"note,omitempty"`
}

func (a *API) cancelItem(w http.ResponseWriter, r *http.Request, unit, op string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		opError(w, http.StatusNotFound, "no such item")
		return
	}
	recent, err := a.store.Recent(r.Context(), unit, historyLen)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	var it Item
	found := false
	for _, c := range recent {
		if c.ID == id {
			it, found = c, true
			break
		}
	}
	if !found {
		opError(w, http.StatusNotFound, "no such item")
		return
	}
	if terminal(it.State) {
		opJSON(w, http.StatusOK, cancelled{View: view(it)})
		return
	}
	if it.State == Queued {
		// Conditional, because the unit may take the item after the lookup.
		got, ok, err := a.store.CancelQueued(r.Context(), unit, id)
		if err != nil {
			opStoreError(w, r, err)
			return
		}
		if ok {
			a.changed(unit)
			opJSON(w, http.StatusOK, cancelled{View: view(got)})
			return
		}
		it = got
		if terminal(it.State) {
			opJSON(w, http.StatusOK, cancelled{View: view(it)})
			return
		}
	}
	// The unit has this item and has no per-item cancel yet, so the only way
	// to end it is stopping the unit, which also cancels every other open
	// item. The note tells the operator that happened.
	open, _, err := a.stopUnit(r.Context(), unit, op)
	if len(open) > 0 || err == nil {
		a.changed(unit)
	}
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	for _, c := range open {
		if c.ID == id {
			it = c
		}
	}
	it.State = Cancelled
	opJSON(w, http.StatusAccepted, cancelled{view(it), stoppedNote})
}

func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

type point struct {
	X *int `json:"x"`
	Y *int `json:"y"`
	Z *int `json:"z"`
}

func (p *point) check() error {
	if p == nil || p.X == nil || p.Y == nil || p.Z == nil {
		return errors.New("a position needs x, y and z")
	}
	if *p.X < minXZ || *p.X > maxXZ || *p.Z < minXZ || *p.Z > maxXZ || *p.Y < minY || *p.Y > maxY {
		return errors.New("a position is outside the allowed area")
	}
	return nil
}

var gatherItems = map[string]bool{"cobblestone": true, "oak_log": true}

func checkQty(q int) error {
	if q < 1 || q > maxQty {
		return fmt.Errorf("qty must be 1 to %d", maxQty)
	}
	return nil
}

// validateTemplate is a bounds-and-shape check only. The unit validates in
// full and reports a bad task failed with its reason, so a rule added there
// need not be mirrored here.
func validateTemplate(body []byte) error {
	var head struct {
		Template string `json:"template"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return errors.New("task must be a JSON object")
	}
	strict := func(v any) error {
		if err := decodeStrict(bytes.NewReader(body), v); err != nil {
			return errors.New("task has a missing, misspelt or mistyped field")
		}
		return nil
	}
	switch head.Template {
	case "goto":
		var t struct {
			Template string `json:"template"`
			To       *point `json:"to"`
		}
		if err := strict(&t); err != nil {
			return err
		}
		return t.To.check()
	case "gather_and_deposit":
		var t struct {
			Template string `json:"template"`
			Item     string `json:"item"`
			Qty      int    `json:"qty"`
			Chest    *point `json:"chest"`
			Area     *struct {
				Min *point `json:"min"`
				Max *point `json:"max"`
			} `json:"area"`
		}
		if err := strict(&t); err != nil {
			return err
		}
		if !gatherItems[t.Item] {
			return errors.New("unknown item")
		}
		if err := checkQty(t.Qty); err != nil {
			return err
		}
		if err := t.Chest.check(); err != nil {
			return err
		}
		if t.Area != nil {
			if err := t.Area.Min.check(); err != nil {
				return err
			}
			return t.Area.Max.check()
		}
		return nil
	case "haul":
		var t struct {
			Template string         `json:"template"`
			Items    map[string]int `json:"items"`
			From     []*point       `json:"from"`
			To       *point         `json:"to"`
		}
		if err := strict(&t); err != nil {
			return err
		}
		if len(t.Items) < 1 || len(t.Items) > maxHaulItems {
			return fmt.Errorf("items must list 1 to %d items", maxHaulItems)
		}
		for item, q := range t.Items {
			if !gatherItems[item] {
				return errors.New("unknown item")
			}
			if err := checkQty(q); err != nil {
				return err
			}
		}
		if len(t.From) < 1 || len(t.From) > maxHaulFrom {
			return fmt.Errorf("from must list 1 to %d chests", maxHaulFrom)
		}
		for _, c := range t.From {
			if err := c.check(); err != nil {
				return err
			}
		}
		return t.To.check()
	}
	return errors.New("unknown template")
}
