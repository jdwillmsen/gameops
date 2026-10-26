package explain

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
	a.now = func() time.Time { return t0.Add(time.Hour) }
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
	day := "/v1/units/u/explain?from=2026-10-23T00:00:00Z&to=2026-10-23T12:00:00Z"
	anonMux, _ := serve(t, events.NewMemory(), false)
	if c := code(anonMux, day); c != 400 {
		t.Fatalf("anonymous 12h window: %d, want 400", c)
	}
	if c := code(anonMux, "/v1/units/u/explain"); c != 200 {
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
	code(mux, "/v1/units/u/explain")
	first := st.queries
	for range 5 {
		code(mux, "/v1/units/u/explain")
	}
	if st.queries != first {
		t.Fatalf("queries = %d after repeats, want %d", st.queries, first)
	}
	code(mux, "/v1/units/other/explain")
	if st.queries == first {
		t.Fatal("a different unit must read the store")
	}
}

func TestOperatorsBypassTheCache(t *testing.T) {
	st := &countingStore{Store: events.NewMemory()}
	mux, _ := serve(t, st, true)
	code(mux, "/v1/units/u/explain")
	n := st.queries
	code(mux, "/v1/units/u/explain")
	if st.queries == n {
		t.Fatal("a signed-in request must read fresh")
	}
}

func TestPageCapIsReported(t *testing.T) {
	st := &countingStore{Store: events.NewMemory(), endless: true}
	mux, _ := serve(t, st, false)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/units/u/explain", nil))
	if st.queries != maxPages {
		t.Fatalf("queries = %d, want the %d-page cap", st.queries, maxPages)
	}
	if !strings.Contains(rec.Body.String(), `"truncated":true`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAnonymousVariationsThatMeanTheSameQueryShareOneScan(t *testing.T) {
	st := &countingStore{Store: events.NewMemory()}
	mux, _ := serve(t, st, false)
	code(mux, "/v1/units/u/explain?to=2026-10-23T06:30:05Z&from=2026-10-23T06:00:10Z")
	n := st.queries
	for _, p := range []string{
		"?from=2026-10-23T06:00:40Z&to=2026-10-23T06:30:55Z",
		"?to=2026-10-23T06:30:00Z&from=2026-10-23T06:00:00Z&junk=1",
		"?junk=2&from=2026-10-23T06:00:59Z&to=2026-10-23T06:30:59Z",
	} {
		code(mux, "/v1/units/u/explain"+p)
	}
	if st.queries != n {
		t.Fatalf("queries = %d, want %d", st.queries, n)
	}
}
