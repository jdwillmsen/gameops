package explorer

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

type gate struct{ op string }

func (g gate) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.op == "" {
			fail(w, http.StatusUnauthorized, "signed_out")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (g gate) Operator(*http.Request) (auth.Operator, bool) {
	return auth.Operator{ID: g.op}, g.op != ""
}

func setup(t *testing.T, op string) (*API, *events.Memory, *http.ServeMux) {
	t.Helper()
	mem := events.NewMemory()
	a := NewAPI(StoreSource{mem}, NewMemoryViews(), gate{op})
	a.now = func() time.Time { return t0.Add(time.Hour) }
	mux := http.NewServeMux()
	a.Mount(mux)
	return a, mem, mux
}

func put(t *testing.T, mem *events.Memory, es ...events.Event) {
	t.Helper()
	for i := range es {
		es[i].ID = 0
	}
	if err := mem.Append(context.Background(), es); err != nil {
		t.Fatal(err)
	}
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestEverythingNeedsSignIn(t *testing.T) {
	_, mem, mux := setup(t, "")
	put(t, mem, sample()...)
	for _, c := range [][2]string{
		{"GET", "/v1/explorer/events"}, {"GET", "/v1/explorer/tail"}, {"GET", "/v1/explorer/views"},
		{"POST", "/v1/explorer/views"}, {"POST", "/v1/explorer/views/delete"},
	} {
		rec := do(mux, c[0], c[1], `{}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%v: %d", c, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "crew-0") {
			t.Errorf("%v leaked events", c)
		}
	}
}

func TestQueryRoute(t *testing.T) {
	_, mem, mux := setup(t, "owner")
	es := sample()
	es[2].Pos = &events.Pos{X: 1, Y: 2, Z: 3}
	put(t, mem, es...)
	rec := do(mux, "GET", "/v1/explorer/events?severity=crit,warn&sort=unit&limit=1", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var out struct {
		Events []view
		Total  int
		Next   string
		Head   int64
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 2 || len(out.Events) != 1 || out.Next == "" || out.Head != 4 || out.Events[0].Unit != "crew-02" {
		t.Fatalf("%+v", out)
	}
	cursor := out.Next
	out.Next = ""
	rec = do(mux, "GET", "/v1/explorer/events?severity=crit,warn&sort=unit&limit=1&cursor="+cursor, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Events[0].Pos == nil || out.Events[0].Unit != "crew-01" || out.Next != "" {
		t.Fatalf("%+v", out)
	}
	if rec := do(mux, "GET", "/v1/explorer/events?severity=loud", ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	if rec := do(mux, "GET", "/v1/explorer/events?cursor=zzz", ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
}

func TestPagedSourceMatchesSnapshot(t *testing.T) {
	mem := events.NewMemory()
	put(t, mem, bigSet(450)...)
	// Hiding the snapshot fast path forces the paging fallback.
	got, err := StoreSource{pageOnly{mem}}.Load(context.Background(), Window{})
	if err != nil || len(got.Events) != 450 || got.Truncated {
		t.Fatalf("%d %v %v", len(got.Events), got.Truncated, err)
	}
	after, _ := StoreSource{pageOnly{mem}}.Load(context.Background(), Window{AfterID: 440})
	if len(after.Events) != 10 {
		t.Fatalf("after: %d", len(after.Events))
	}
}

type pageOnly struct{ events.Store }

func TestSavedViewsArePerOperatorAndValidated(t *testing.T) {
	a, _, mux := setup(t, "owner")
	rec := do(mux, "POST", "/v1/explorer/views", `{"name":" No pickup ","query":"?q=no+pickup&severity=warn&sort=unit"}`)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var v View
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Name != "No pickup" || v.Query != "q=no+pickup&severity=warn&sort=unit" {
		t.Fatalf("%+v", v)
	}
	// Same name replaces rather than duplicating.
	do(mux, "POST", "/v1/explorer/views", `{"name":"No pickup","query":"kind=died"}`)
	if l, _ := a.views.List("owner"); len(l) != 1 || l[0].Query != "kind=died" {
		t.Fatalf("%+v", l)
	}
	for _, body := range []string{`{"name":"","query":""}`, `{"name":"x","query":"severity=loud"}`, `{"name":"x","query":"%zz"}`, `{"name":"x","query":"","extra":1}`, `not json`, `{"name":"` + strings.Repeat("a", 61) + `","query":""}`} {
		if rec := do(mux, "POST", "/v1/explorer/views", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	if l, _ := a.views.List("someone-else"); len(l) != 0 {
		t.Fatal("views leaked across operators")
	}
	if err := a.views.Delete("someone-else", v.ID); err != ErrViewNotFound {
		t.Fatalf("other operator deleted a view: %v", err)
	}
	rec = do(mux, "GET", "/v1/explorer/views", "")
	if !strings.Contains(rec.Body.String(), v.ID) {
		t.Fatal(rec.Body)
	}
	if rec := do(mux, "POST", "/v1/explorer/views/delete", `{"id":"`+v.ID+`"}`); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if rec := do(mux, "POST", "/v1/explorer/views/delete", `{"id":"`+v.ID+`"}`); rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestViewLimit(t *testing.T) {
	m := NewMemoryViews()
	for i := range maxViews {
		if _, err := m.Save("o", string(rune('a'+i%26))+strings.Repeat("x", i/26), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Save("o", "one too many", ""); err != ErrViewLimit {
		t.Fatal(err)
	}
}

func TestTailPushesOnlyNewMatchingEvents(t *testing.T) {
	a, mem, mux := setup(t, "owner")
	a.every = 10 * time.Millisecond
	put(t, mem, sample()...)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/explorer/tail?severity=crit&after=4", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	// Written after the tail connected: one that matches, one that does not.
	start := time.Now()
	put(t, mem, ev(0, "crew-03", "online", "ok", 5*time.Second, `{}`), ev(0, "crew-03", "died", "crit", 6*time.Second, `{}`))
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var out struct {
			Events []view
			Head   int64
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Events) != 1 || out.Events[0].Kind != "died" || out.Head != 6 {
			t.Fatalf("%+v", out)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("took %v", d)
		}
		return
	}
	t.Fatal("stream ended without an event")
}

func TestTailCapAndBadAfter(t *testing.T) {
	a, _, mux := setup(t, "owner")
	if rec := do(mux, "GET", "/v1/explorer/tail?after=-1", ""); rec.Code != 400 {
		t.Fatal(rec.Code)
	}
	a.tails.Store(maxTails)
	if rec := do(mux, "GET", "/v1/explorer/tail", ""); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	if n := a.tails.Load(); n != maxTails {
		t.Fatalf("rejected tail leaked a slot: %d", n)
	}
}

func TestUnavailableStoreIs503(t *testing.T) {
	l := &events.Lazy{}
	a := NewAPI(StoreSource{l}, NewMemoryViews(), gate{"owner"})
	mux := http.NewServeMux()
	a.Mount(mux)
	if rec := do(mux, "GET", "/v1/explorer/events", ""); rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}
