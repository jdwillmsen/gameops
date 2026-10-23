package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

// fakeGate stands in for the session check: signedIn decides both of its
// methods, the way a real session does.
type fakeGate struct{ signedIn bool }

func (g fakeGate) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.signedIn {
			opError(w, http.StatusUnauthorized, "signed_out")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (g fakeGate) Operator(*http.Request) (auth.Operator, bool) {
	return auth.Operator{ID: "owner"}, g.signedIn
}

// api builds the routes over a memory store, ready for requests.
func api(t *testing.T) (*Lazy, *http.ServeMux) {
	t.Helper()
	l := &Lazy{}
	l.Set(NewMemory())
	mux := http.NewServeMux()
	NewAPI(l).MountOperator(mux, fakeGate{signedIn: true})
	return l, mux
}

// seed stores events straight into the store: what the writer would have put
// there, so the routes are tested on what history reads.
func seed(t *testing.T, s Store, ev ...Event) {
	t.Helper()
	if err := s.Append(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
}

func post(mux *http.ServeMux, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHistoryIsSignedIn(t *testing.T) {
	mux := http.NewServeMux()
	NewAPI(&Lazy{}).MountOperator(mux, fakeGate{signedIn: false})
	if rec := get(mux, "/v1/events"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: status %d, want 401", rec.Code)
	}
	// Nothing behind the routes yet either: the store answers 503 until a
	// backend is connected, like the work routes.
	l2, mux2 := api(t)
	l2.mu.Lock()
	l2.s = nil
	l2.mu.Unlock()
	if rec := get(mux2, "/v1/events"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store: status %d, want 503", rec.Code)
	}
}

func TestHistoryFiltersSortsAndPages(t *testing.T) {
	l, mux := api(t)
	at := func(sec int) time.Time { return time.Date(2026, 10, 22, 12, 0, sec, 0, time.UTC) }
	seed(t, l,
		Event{Unit: "crew-01", At: at(0), Kind: "died", Severity: SevCrit},
		Event{Unit: "crew-01", At: at(1), Kind: "stuck", Severity: SevInfo},
		Event{Unit: "crew-01", At: at(2), Kind: "task_done", Severity: SevOK},
		Event{Unit: "crew-02", At: at(3), Kind: "died", Severity: SevCrit})

	var page struct {
		Events []view  `json:"events"`
		Next   *string `json:"next"`
	}
	rec := get(mux, "/v1/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("all: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 || page.Events[0].Kind != "died" || page.Events[0].Unit != "crew-02" {
		t.Fatalf("newest first: %+v", page.Events)
	}

	rec = get(mux, "/v1/events?unit=crew-01&limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered: %d", rec.Code)
	}
	page.Next = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Next == nil {
		t.Fatalf("first page: %d events, next %v", len(page.Events), page.Next)
	}
	// The cursor walks the same filter, and meets the excluded rows never.
	seen := append([]view{}, page.Events...)
	for page.Next != nil {
		rec := get(mux, "/v1/events?unit=crew-01&limit=2&cursor="+*page.Next)
		if rec.Code != http.StatusOK {
			t.Fatalf("next page: %d %s", rec.Code, rec.Body.String())
		}
		page.Next = nil
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page.Events...)
	}
	if len(seen) != 3 || seen[0].At.Before(seen[len(seen)-1].At) {
		t.Fatalf("walk: %+v", seen)
	}

	rec = get(mux, "/v1/events?sort=asc")
	if rec.Code != http.StatusOK {
		t.Fatalf("asc: %d", rec.Code)
	}
	page.Next = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.Events[0].At.Equal(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("sort asc: %+v", page.Events)
	}

	for bad := range map[string]bool{
		"/v1/events?severity=loud": true, "/v1/events?since=nope": true,
		"/v1/events?until=nope": true, "/v1/events?limit=0": true,
		"/v1/events?limit=999": true, "/v1/events?sort=sideways": true,
		"/v1/events?cursor=pants": true,
	} {
		if rec := get(mux, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", bad, rec.Code)
		}
	}
}

func TestPruneEndpoint(t *testing.T) {
	l, mux := api(t)
	seed(t, l, Event{Unit: "crew-01", At: time.Now().Add(-MinRetention - time.Hour), Kind: "died", Severity: SevCrit})

	// Signed-out prune is refused by the session wrapper, not the route.
	gated := http.NewServeMux()
	NewAPI(l).MountOperator(gated, fakeGate{signedIn: false})
	if rec := post(gated, "/v1/events/prune", "", `{"dry_run":true}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out prune: %d", rec.Code)
	}

	rec := post(mux, "/v1/events/prune", "", `{"dry_run":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rec.Code, rec.Body.String())
	}
	var rep struct {
		Pruned int    `json:"pruned"`
		Total  int    `json:"total"`
		Would  []view `json:"would"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Pruned != 0 || rep.Total != 1 || len(rep.Would) != 1 || rep.Would[0].Kind != "died" {
		t.Fatalf("dry run report: %+v", rep)
	}
	if n := len(mustQuery(t, l, Query{})); n != 1 {
		t.Fatalf("dry run pruned anyway: %d rows", n)
	}

	if rec := post(mux, "/v1/events/prune", "", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("prune: %d %s", rec.Code, rec.Body.String())
	}
	if n := len(mustQuery(t, l, Query{})); n != 0 {
		t.Fatalf("prune left %d rows", n)
	}
	if rec := post(mux, "/v1/events/prune", "", `{"dry":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d, want 400", rec.Code)
	}
}
