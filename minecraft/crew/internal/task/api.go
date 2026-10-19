package task

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MaxBodyBytes is far above a real template (about 100 bytes) and far below
// anything worth buffering.
const MaxBodyBytes = 4096

// Routes registers the task endpoints. With an empty token it registers
// nothing, so the endpoints do not exist rather than being open.
func Routes(mux *http.ServeMux, r *Runner, token string) {
	if token == "" {
		return
	}
	want := sha256.Sum256([]byte(token))
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			got, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
			// Hashing first makes the comparison length-independent.
			sum := sha256.Sum256([]byte(got))
			if !ok || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, req)
		}
	}

	mux.HandleFunc("POST /v1/tasks", guard(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxBodyBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		tpl, err := ParseTemplate(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t, err := r.Submit(tpl)
		switch {
		case errors.Is(err, ErrQueueFull):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		case err != nil:
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			writeJSON(w, http.StatusAccepted, t)
		}
	}))
	mux.HandleFunc("GET /v1/tasks", guard(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, r.List())
	}))
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", guard(func(w http.ResponseWriter, req *http.Request) {
		t, err := r.Cancel(req.PathValue("id"))
		switch {
		case errors.Is(err, ErrNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, ErrFinished):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			writeJSON(w, http.StatusOK, t)
		}
	}))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
