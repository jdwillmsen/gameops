package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

func TestServerRefusesUncleanUnitPathsWithoutRedirect(t *testing.T) {
	var upstream atomic.Int32
	unit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstream.Add(1)
		fmt.Fprint(w, "unit body")
	}))
	defer unit.Close()
	units, err := roster.Parse(fmt.Sprintf(`[{"id":"crew-01","name":"Agate","url":%q}]`, unit.URL))
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), logging.New("error"))

	for _, p := range []string{
		"/units/crew-01/./camera",
		"/units//camera",
		"/units/crew-01//camera",
		"/units/crew-01/v1/camera/../../v1/tasks",
		"/units/crew-01/v1/camera/../../camera",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d (location %q), want 404", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	if n := upstream.Load(); n != 0 {
		t.Fatalf("upstream saw %d requests, want 0", n)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/units/crew-01/camera", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "unit body" || upstream.Load() != 1 {
		t.Fatalf("clean path: status %d body %q upstream %d", rec.Code, rec.Body, upstream.Load())
	}
}

func TestServerRoutesConsoleAndRemovedList(t *testing.T) {
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://127.0.0.1:1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHandler(units, telemetry.NewPoller(units, &http.Client{}, time.Second), logging.New("error"))
	for path, want := range map[string]int{
		"/":         http.StatusOK,
		"/healthz":  http.StatusOK,
		"/camera":   http.StatusFound,
		"/v1/units": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}
