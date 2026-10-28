package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
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

	minXZ, maxXZ = -500, 499
	minY, maxY   = -64, 320

	// maxRepairSpan is the widest repair box on any axis.
	maxRepairSpan = 64
	// worldEdge bounds a repair area's x and z. The Colosseum lies beyond the
	// lab square goto uses, and the unit checks the arena's own footprint, so
	// this only keeps absurd values out of the queue.
	worldEdge = 30_000_000
)

const (
	repairRole = "mender"
	buildRole  = "builder"
)

var (
	blueprintName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

	errWrongRole = errors.New("this unit's role does not take this task")
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
	mux.Handle("POST /v1/work/bulk", g.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op, _ := g.Operator(r)
		a.bulk(w, r, op.ID)
	})))
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
	if r.Context().Err() != nil {
		return
	}
	switch {
	case errors.Is(err, ErrUnavailable):
		opError(w, http.StatusServiceUnavailable, storeMessage(err))
	case errors.Is(err, ErrInvalid):
		opError(w, http.StatusBadRequest, storeMessage(err))
	default:
		opError(w, http.StatusInternalServerError, storeMessage(err))
	}
}

// storeMessage words a store failure without detail that could leak.
func storeMessage(err error) string {
	switch {
	case errors.Is(err, ErrUnavailable):
		return "queue unavailable"
	case errors.Is(err, ErrInvalid):
		return "invalid request"
	}
	return "queue error"
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
	if err := a.checkRole(unit, body); err != nil {
		opError(w, http.StatusConflict, err.Error())
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

func (a *API) stop(w http.ResponseWriter, r *http.Request, unit, op string) {
	cancelled, stop, _, stopped, err := a.store.StopUnit(r.Context(), unit, op, nil)
	if err != nil {
		opStoreError(w, r, err)
		return
	}
	if stopped {
		a.changed(unit)
	}
	opJSON(w, http.StatusAccepted, struct {
		View
		Cancelled int `json:"cancelled"`
	}{view(stop), len(cancelled)})
}

func (a *API) cancelItem(w http.ResponseWriter, r *http.Request, unit, op string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		opError(w, http.StatusNotFound, "no such item")
		return
	}
	it, cmd, err := a.store.CancelTask(r.Context(), unit, id, op)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrWrongUnit):
		opError(w, http.StatusNotFound, "no such item")
	case err != nil:
		opStoreError(w, r, err)
	case cmd != nil:
		// The unit still holds the task until it hears the command, so the
		// item is returned as it is: accepted, not yet cancelled.
		a.changed(unit)
		opJSON(w, http.StatusAccepted, view(it))
	default:
		a.changed(unit)
		opJSON(w, http.StatusOK, view(it))
	}
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

// checkRole refuses a role-bound template for a unit whose reported role is
// known and is another one. A unit that has reported no role is let through:
// the unit enforces its own role and fails the task with its reason.
func (a *API) checkRole(unit string, body []byte) error {
	var head struct {
		Template string `json:"template"`
	}
	_ = json.Unmarshal(body, &head)
	want := ""
	switch head.Template {
	case "repair_build":
		want = repairRole
	case "build_blueprint":
		want = buildRole
	}
	if want == "" || a.Role == nil {
		return nil
	}
	if got := a.Role(unit); got != "" && got != want {
		return errWrongRole
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
	case "repair_build":
		var t struct {
			Template string `json:"template"`
			Plan     string `json:"plan"`
			Area     *struct {
				Min *point `json:"min"`
				Max *point `json:"max"`
			} `json:"area"`
			DryRun bool `json:"dry_run"`
		}
		if err := strict(&t); err != nil {
			return err
		}
		if t.Plan != "colosseum" {
			return errors.New("plan must be colosseum")
		}
		if t.Area == nil {
			return errors.New("area with min and max is required")
		}
		return checkRepairArea(t.Area.Min, t.Area.Max)
	case "build_blueprint":
		var t struct {
			Template  string `json:"template"`
			Blueprint string `json:"blueprint"`
			At        *point `json:"at"`
			Rotation  int    `json:"rotation"`
			DryRun    bool   `json:"dry_run"`
		}
		if err := strict(&t); err != nil {
			return err
		}
		if !blueprintName.MatchString(t.Blueprint) {
			return errors.New("unknown blueprint")
		}
		if t.Rotation != 0 && t.Rotation != 90 && t.Rotation != 180 && t.Rotation != 270 {
			return errors.New("rotation must be 0, 90, 180 or 270")
		}
		return t.At.check()
	}
	return errors.New("unknown template")
}

func checkRepairArea(min, max *point) error {
	if min == nil || max == nil || min.X == nil || min.Y == nil || min.Z == nil || max.X == nil || max.Y == nil || max.Z == nil {
		return errors.New("area with min and max is required")
	}
	for _, ax := range [][2]int{{*min.X, *max.X}, {*min.Y, *max.Y}, {*min.Z, *max.Z}} {
		if ax[0] > ax[1] {
			return errors.New("area min must not exceed max")
		}
		if ax[1]-ax[0]+1 > maxRepairSpan {
			return fmt.Errorf("area must span at most %d blocks on each axis", maxRepairSpan)
		}
	}
	for _, ax := range [][2]int{{*min.X, *max.X}, {*min.Z, *max.Z}} {
		if ax[0] < -worldEdge || ax[1] > worldEdge {
			return errors.New("a position is outside the allowed area")
		}
	}
	if *min.Y < minY || *max.Y > maxY {
		return errors.New("a position is outside the allowed area")
	}
	return nil
}
