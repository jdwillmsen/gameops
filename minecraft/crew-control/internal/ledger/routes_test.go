package ledger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

// fakeGate follows the order of the real check: header, then session.
type fakeGate struct{}

func (fakeGate) Operator(r *http.Request) (auth.Operator, bool) {
	if id := r.Header.Get("X-Test-Op"); id != "" {
		return auth.Operator{ID: id, Name: id}, true
	}
	return auth.Operator{}, false
}

func (g fakeGate) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Crew-Request") != "1" {
			http.Error(w, "bad_request", http.StatusForbidden)
			return
		}
		if _, ok := g.Operator(r); !ok {
			http.Error(w, "signed_out", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type routeRig struct {
	led *Memory
	srv *httptest.Server
}

func newRouteRig(t *testing.T, up bool) *routeRig {
	t.Helper()
	rg := &routeRig{led: NewMemory()}
	mux := http.NewServeMux()
	NewAPI(func() Ledger {
		if !up {
			return nil
		}
		return rg.led
	}).MountOperator(mux, fakeGate{})
	rg.srv = httptest.NewServer(mux)
	t.Cleanup(rg.srv.Close)
	return rg
}

func (rg *routeRig) do(t *testing.T, method, path, body string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, rg.srv.URL+path, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

var signed = map[string]string{"X-Test-Op": "owner", "X-Crew-Request": "1"}

const store = `{"id":"store","name":"Store","kind":"chest","box":{"min":{"x":1,"y":64,"z":1},"max":{"x":1,"y":64,"z":1}}}`

func TestPlaceRoutesNeedASession(t *testing.T) {
	rg := newRouteRig(t, true)
	cases := []struct{ method, path string }{
		{"GET", "/v1/places"},
		{"POST", "/v1/places"},
		{"POST", "/v1/places/store/delete"},
	}
	for _, c := range cases {
		if code, _ := rg.do(t, c.method, c.path, store, nil); code != 401 && code != 403 {
			t.Errorf("%s %s anonymous: %d", c.method, c.path, code)
		}
		if c.method == "POST" {
			if code, _ := rg.do(t, c.method, c.path, store, map[string]string{"X-Test-Op": "owner"}); code != 403 {
				t.Errorf("%s %s without header: %d", c.method, c.path, code)
			}
			if code, _ := rg.do(t, c.method, c.path, store, map[string]string{"X-Crew-Request": "1"}); code != 401 {
				t.Errorf("%s %s without session: %d", c.method, c.path, code)
			}
		} else if code, _ := rg.do(t, c.method, c.path, "", nil); code != 401 {
			t.Errorf("GET anonymous: %d", code)
		}
	}
}

func TestPlaceRoutesRoundTrip(t *testing.T) {
	rg := newRouteRig(t, true)
	if code, body := rg.do(t, "POST", "/v1/places", store, signed); code != 200 {
		t.Fatalf("create: %d %s", code, body)
	}
	at := time.Now()
	if err := rg.led.Observe(ctx, "crew-01", at, Pos{1, 64, 1}, map[string]int{"oak_log": 7}); err != nil {
		t.Fatal(err)
	}
	code, body := rg.do(t, "GET", "/v1/places", "", signed)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	var got []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Stock []struct {
			Item  string `json:"item"`
			Count int    `json:"count"`
		} `json:"stock"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "store" || len(got[0].Stock) != 1 || got[0].Stock[0].Item != "oak_log" || got[0].Stock[0].Count != 7 {
		t.Fatalf("list body %s", body)
	}
	renamed := strings.Replace(store, `"Store"`, `"Depot"`, 1)
	if code, _ := rg.do(t, "POST", "/v1/places", renamed, signed); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if p, _, _ := rg.led.Place(ctx, "store"); p.Name != "Depot" {
		t.Fatalf("not updated: %+v", p)
	}
	if code, _ := rg.do(t, "POST", "/v1/places/store/delete", "{}", signed); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if _, ok, _ := rg.led.Place(ctx, "store"); ok {
		t.Fatal("still there")
	}
	if code, _ := rg.do(t, "POST", "/v1/places/store/delete", "{}", signed); code != 404 {
		t.Fatalf("delete twice: %d", code)
	}
}

func TestPlaceRouteErrors(t *testing.T) {
	rg := newRouteRig(t, true)
	seeded := Place{ID: "home", Name: "Home", Kind: KindChest, Box: Box{Pos{9, 64, 9}, Pos{9, 64, 9}}}
	if err := rg.led.Seed(ctx, []Place{seeded}); err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(store, `"store"`, `"home"`, 1)
	if code, body := rg.do(t, "POST", "/v1/places", other, signed); code != 409 {
		t.Errorf("seeded update: %d %s", code, body)
	}
	if code, _ := rg.do(t, "POST", "/v1/places/home/delete", "{}", signed); code != 409 {
		t.Errorf("seeded delete: %d", code)
	}
	atHome := strings.NewReplacer(`"x":1`, `"x":9`, `"z":1`, `"z":9`).Replace(store)
	if code, body := rg.do(t, "POST", "/v1/places", atHome, signed); code != 409 || !strings.Contains(body, "another chest is there") {
		t.Errorf("position conflict: %d %s", code, body)
	}
	for name, body := range map[string]string{
		"bad id":    strings.Replace(store, `"store"`, `"Bad Id"`, 1),
		"bad kind":  strings.Replace(store, `"chest"`, `"vault"`, 1),
		"unknown":   strings.Replace(store, `"name"`, `"seeded":true,"name"`, 1),
		"not json":  `nope`,
		"trailing":  store + store,
		"too large": `{"name":"` + strings.Repeat("x", 9000) + `"}`,
	} {
		if code, _ := rg.do(t, "POST", "/v1/places", body, signed); code != 400 && code != 413 {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestPlaceRoutesAnswer503WithoutALedger(t *testing.T) {
	rg := newRouteRig(t, false)
	for _, c := range []struct{ m, p, b string }{{"GET", "/v1/places", ""}, {"POST", "/v1/places", store}, {"POST", "/v1/places/x/delete", "{}"}} {
		if code, _ := rg.do(t, c.m, c.p, c.b, signed); code != 503 {
			t.Errorf("%s %s: %d", c.m, c.p, code)
		}
	}
}
