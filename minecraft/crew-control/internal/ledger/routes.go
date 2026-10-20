package ledger

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

const maxPlaceBody = 4 << 10

// Gate is the part of auth.Auth the operator routes need.
type Gate interface {
	Require(h http.Handler) http.Handler
	Operator(r *http.Request) (auth.Operator, bool)
}

// API serves places to signed-in operators. The ledger exists only once the
// database is up, so get is asked per request and nil means 503.
//
// An auto-created chest's id spells out its coordinates, so ids are never
// logged here and the routes stay behind sign-in.
type API struct {
	get func() Ledger
}

func NewAPI(get func() Ledger) *API { return &API{get: get} }

type stockView struct {
	Item   string    `json:"item"`
	Count  int       `json:"count"`
	SeenAt time.Time `json:"seen_at"`
	Unit   string    `json:"unit"`
}

type placeView struct {
	Place
	// Seeded places come from configuration and cannot be changed here.
	Seeded bool        `json:"seeded"`
	Stock  []stockView `json:"stock"`
}

// MountOperator registers the place routes. Without sign-in configured they
// still exist and answer 401.
func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	mux.Handle("POST /v1/places", g.Require(http.HandlerFunc(a.put)))
	mux.Handle("POST /v1/places/{id}/delete", g.Require(http.HandlerFunc(a.remove)))
	mux.HandleFunc("GET /v1/places", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			writeError(w, http.StatusUnauthorized, "signed_out")
			return
		}
		a.list(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
	case errors.Is(err, ErrSeeded):
		writeError(w, http.StatusConflict, "place comes from configuration")
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "another chest is there")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "no such place")
	case errors.Is(err, ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "places unavailable")
	default:
		writeError(w, http.StatusInternalServerError, "place error")
	}
}

func (a *API) ledger(w http.ResponseWriter) (Ledger, bool) {
	l := a.get()
	if l == nil {
		writeError(w, http.StatusServiceUnavailable, "places unavailable")
		return nil, false
	}
	return l, true
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	l, ok := a.ledger(w)
	if !ok {
		return
	}
	ps, err := l.Places(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	out := make([]placeView, len(ps))
	for i, p := range ps {
		rows, err := l.Stock(r.Context(), p.ID)
		if err != nil {
			fail(w, r, err)
			return
		}
		out[i] = placeView{Place: p, Seeded: p.Seeded, Stock: make([]stockView, len(rows))}
		for j, s := range rows {
			out[i].Stock[j] = stockView{s.Item, s.Count, s.SeenAt, s.Unit}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) put(w http.ResponseWriter, r *http.Request) {
	l, ok := a.ledger(w)
	if !ok {
		return
	}
	var p Place
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlaceBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "place too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad place")
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad place")
		return
	}
	if err := Validate(p); err != nil {
		fail(w, r, err)
		return
	}
	if err := l.PutPlace(r.Context(), p); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	l, ok := a.ledger(w)
	if !ok {
		return
	}
	if err := l.DeletePlace(r.Context(), r.PathValue("id")); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
