package replay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

// MaxSpan bounds one request's range: a replay window is minutes to hours,
// and a month at once is a way to ask for the whole table.
const MaxSpan = 24 * time.Hour

// Gate is the part of auth.Auth the route needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

type API struct{ store Store }

func NewAPI(s Store) *API { return &API{store: s} }

type wireCursor struct {
	At time.Time `json:"a"`
	ID int64     `json:"i"`
}

func encodeCursor(c Cursor) string {
	b, _ := json.Marshal(wireCursor(c))
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (Cursor, error) {
	var w wireCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &w) != nil || w.At.IsZero() || w.ID == 0 {
		return Cursor{}, errors.New("not a cursor")
	}
	return Cursor(w), nil
}

// wireSample is compact because an hour of twenty moving units is tens of
// thousands of rows: t is Unix milliseconds, coordinates keep centiblocks.
type wireSample struct {
	Unit string  `json:"u"`
	T    int64   `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// MountOperator registers the position history. Where units stood is
// operator-only, so the session check precedes any parsing, and nothing here
// is cacheable or logged.
func (a *API) MountOperator(mux *http.ServeMux, g Gate, knownUnit func(string) bool) {
	mux.HandleFunc("GET /v1/replay/positions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.Operator(r); !ok {
			reply(w, http.StatusUnauthorized, map[string]string{"error": "signed_out"})
			return
		}
		q, status, err := parse(r, knownUnit)
		if err != nil {
			reply(w, status, map[string]string{"error": err.Error()})
			return
		}
		page, err := a.store.Query(r.Context(), q)
		if err != nil {
			switch {
			case r.Context().Err() != nil:
			case errors.Is(err, ErrUnavailable):
				reply(w, http.StatusServiceUnavailable, map[string]string{"error": "position store unavailable"})
			default:
				reply(w, http.StatusInternalServerError, map[string]string{"error": "position store error"})
			}
			return
		}
		out := struct {
			Samples []wireSample `json:"samples"`
			Next    *string      `json:"next,omitempty"`
		}{Samples: make([]wireSample, len(page.Samples))}
		for i, s := range page.Samples {
			out.Samples[i] = wireSample{s.Unit, s.At.UnixMilli(), round2(s.X), round2(s.Y), round2(s.Z)}
		}
		if page.Next != nil {
			next := encodeCursor(*page.Next)
			out.Next = &next
		}
		reply(w, http.StatusOK, out)
	})
}

func parse(r *http.Request, knownUnit func(string) bool) (Query, int, error) {
	get := r.URL.Query().Get
	var q Query
	for name, into := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		t, err := time.Parse(time.RFC3339, get(name))
		if err != nil {
			return q, http.StatusBadRequest, errors.New(name + " is required, as RFC 3339")
		}
		*into = t
	}
	if !q.Until.After(q.Since) {
		return q, http.StatusBadRequest, errors.New("until must be after since")
	}
	if q.Until.Sub(q.Since) > MaxSpan {
		return q, http.StatusBadRequest, errors.New("range is longer than 24h")
	}
	if u := get("unit"); u != "" {
		if !knownUnit(u) {
			return q, http.StatusNotFound, errors.New("unknown unit")
		}
		q.Unit = u
	}
	q.Limit = DefaultLimit
	if s := get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > MaxLimit {
			return q, http.StatusBadRequest, errors.New("bad limit")
		}
		q.Limit = n
	}
	if s := get("cursor"); s != "" {
		c, err := decodeCursor(s)
		if err != nil {
			return q, http.StatusBadRequest, errors.New("bad cursor")
		}
		q.Cursor = c
	}
	return q, 0, nil
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
