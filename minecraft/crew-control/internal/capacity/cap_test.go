package capacity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/anon"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// countingStore counts reads and, when endless, always claims another page.
type countingStore struct {
	events.Store
	queries int
	endless bool
}

func (c *countingStore) Query(ctx context.Context, q events.Query) (events.Page, error) {
	c.queries++
	if c.endless {
		return events.Page{Next: &events.Cursor{At: q.Since, ID: int64(c.queries)}}, nil
	}
	return c.Store.Query(ctx, q)
}

func serve(t *testing.T, s events.Store, signed bool) (*http.ServeMux, *API) {
	t.Helper()
	a := NewAPI(s)
	a.now = func() time.Time { return time.Now() }
	mux := http.NewServeMux()
	a.Mount(mux, anon.GateFunc(func(*http.Request) (auth.Operator, bool) { return auth.Operator{ID: "op"}, signed }))
	return mux, a
}

func code(mux *http.ServeMux, path string) int {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code
}

func TestAnonymousWindowIsCappedButOperatorsKeepTheDay(t *testing.T) {
	day := "/v1/capacity?window=12h"
	anonMux, _ := serve(t, events.NewMemory(), false)
	if c := code(anonMux, day); c != 400 {
		t.Fatalf("anonymous 12h window: %d, want 400", c)
	}
	if c := code(anonMux, "/v1/capacity"); c != 200 {
		t.Fatalf("anonymous default window: %d", c)
	}
	opMux, _ := serve(t, events.NewMemory(), true)
	if c := code(opMux, day); c != 200 {
		t.Fatalf("operator 12h window: %d, want 200", c)
	}
}

func TestAnonymousRepeatsAreServedFromCache(t *testing.T) {
	st := &countingStore{Store: events.NewMemory()}
	mux, _ := serve(t, st, false)
	code(mux, "/v1/capacity")
	first := st.queries
	for range 5 {
		code(mux, "/v1/capacity")
	}
	if st.queries != first {
		t.Fatalf("queries = %d after repeats, want %d", st.queries, first)
	}
	code(mux, "/v1/capacity?window=30m")
	if st.queries == first {
		t.Fatal("a different window must read the store")
	}
}

func TestOperatorsBypassTheCache(t *testing.T) {
	st := &countingStore{Store: events.NewMemory()}
	mux, _ := serve(t, st, true)
	code(mux, "/v1/capacity")
	n := st.queries
	code(mux, "/v1/capacity")
	if st.queries == n {
		t.Fatal("a signed-in request must read fresh")
	}
}

func TestPageCapIsReported(t *testing.T) {
	st := &countingStore{Store: events.NewMemory(), endless: true}
	mux, _ := serve(t, st, false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/capacity", nil))
	if st.queries != 2*maxPages {
		t.Fatalf("queries = %d, want the %d-page cap for each of two kinds", st.queries, maxPages)
	}
	if !strings.Contains(rec.Body.String(), `"truncated":true`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAnonymousWindowsInTheSameMinuteShareOneScan(t *testing.T) {
	st := &countingStore{Store: events.NewMemory()}
	mux, _ := serve(t, st, false)
	code(mux, "/v1/capacity?window=61s")
	n := st.queries
	code(mux, "/v1/capacity?window=62s&junk=1")
	code(mux, "/v1/capacity?junk=2&window=119s")
	if st.queries != n {
		t.Fatalf("queries = %d, want %d", st.queries, n)
	}
}
