package alerts

import (
	"encoding/json"
	"net/http"
	"time"
)

const (
	maxStreams     = 64
	keepalive      = 15 * time.Second
	writeTimeout   = 5 * time.Second
	maxBody        = 1 << 10
	defaultMuteFor = 15 * time.Minute
)

// Gate is the part of the auth the write routes need.
type Gate interface {
	Require(h http.Handler) http.Handler
}

// API serves the alert list and stream to everyone and acknowledge and mute
// to signed-in operators.
type API struct {
	e       *Engine
	now     func() time.Time
	streams chan struct{}
}

func NewAPI(e *Engine) *API {
	return &API{e: e, now: time.Now, streams: make(chan struct{}, maxStreams)}
}

func (a *API) MountOperator(mux *http.ServeMux, g Gate) {
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, a.payload())
	})
	mux.HandleFunc("GET /v1/alerts/stream", a.stream)
	mux.Handle("POST /v1/alerts/{id}/ack", g.Require(http.HandlerFunc(a.ack)))
	mux.Handle("POST /v1/alerts/rules/{rule}/mute", g.Require(http.HandlerFunc(a.snooze)))
	mux.Handle("POST /v1/alerts/{id}/mute", g.Require(http.HandlerFunc(a.mute)))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// payload is the list plus the snoozed rules; both are free of coordinates
// and operator ids.
func (a *API) payload() map[string]any {
	return map[string]any{"alerts": a.e.List(), "snoozed": a.e.Snoozed(a.now())}
}

func (a *API) ack(w http.ResponseWriter, r *http.Request) {
	if !a.e.Ack(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_alert"})
		return
	}
	writeJSON(w, http.StatusOK, a.payload())
}

// muteFor reads {"for":"30m"}; "0s" lifts and a missing body means the
// default period. It writes the refusal itself.
func muteFor(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	d := defaultMuteFor
	if r.ContentLength == 0 {
		return d, true
	}
	var body struct {
		For string `json:"for"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_body"})
		return 0, false
	}
	if body.For != "" {
		parsed, err := time.ParseDuration(body.For)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_duration"})
			return 0, false
		}
		d = parsed
	}
	return d, true
}

func (a *API) mute(w http.ResponseWriter, r *http.Request) {
	d, ok := muteFor(w, r)
	if !ok {
		return
	}
	if !a.e.Mute(r.PathValue("id"), d, a.now()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_alert"})
		return
	}
	writeJSON(w, http.StatusOK, a.payload())
}

// snooze mutes one alert type, for the same bounded periods as a mute.
func (a *API) snooze(w http.ResponseWriter, r *http.Request) {
	d, ok := muteFor(w, r)
	if !ok {
		return
	}
	if !a.e.SnoozeRule(r.PathValue("rule"), d, a.now()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_rule"})
		return
	}
	writeJSON(w, http.StatusOK, a.payload())
}

// stream sends the whole list on connect, on every change and on a timer;
// the list is small, so a full snapshot beats a delta protocol.
func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case a.streams <- struct{}{}:
		defer func() { <-a.streams }()
	default:
		http.Error(w, "too many viewers", http.StatusServiceUnavailable)
		return
	}
	sig, cancel := a.e.Subscribe()
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	send := func() bool {
		data, err := json.Marshal(a.payload())
		if err != nil {
			return false
		}
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := w.Write(append(append([]byte("event: alerts\ndata: "), data...), '\n', '\n')); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send() {
		return
	}
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sig:
		case <-tick.C:
		}
		if !send() {
			return
		}
	}
}
