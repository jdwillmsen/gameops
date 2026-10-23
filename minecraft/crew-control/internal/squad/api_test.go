package squad

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

const token = "s3cret"

type rig struct {
	api   *API
	store *Store
	srv   *httptest.Server
	ops   func() string // the operator a gated call presents, "" for nobody
}

func newRig(t *testing.T, tokenSet bool) *rig {
	t.Helper()
	units, err := roster.Parse(`[
	  {"id":"crew-18","name":"Cassia","url":"http://crew-18:8080"},
	  {"id":"crew-19","name":"Liron","url":"http://crew-19:8080"},
	  {"id":"crew-20","name":"Ferula","url":"http://crew-20:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{store: NewStore(), ops: func() string { return "owner" }}
	mux := http.NewServeMux()
	if tokenSet {
		r.api = Routes(mux, r.store, units, token)
	} else {
		r.api = Routes(mux, r.store, units, "")
	}
	mux.Handle("POST /v1/squads/{id}/order", gateProbe(r))
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

// gateProbe mimics auth.Auth's Require closely enough to test the handler
// behind it: an operator POST needs the custom header and a session.
func gateProbe(r *rig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Crew-Request") != "1" {
			http.Error(w, `{"error":"bad_request"}`, http.StatusForbidden)
			return
		}
		if r.ops() == "" {
			http.Error(w, `{"error":"signed_out"}`, http.StatusUnauthorized)
			return
		}
		r.api.order(w, req, r.ops())
	})
}

func (r *rig) do(t *testing.T, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *rig) hello(t *testing.T, squad, unit, role string) {
	t.Helper()
	body := strings.Replace(`{"unit":"$u","role":"$r"}`, "$u", unit, 1)
	body = strings.Replace(body, "$r", role, 1)
	resp := r.do(t, "POST", "/v1/squads/"+squad+"/hello", body, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hello %s/%s: status %d", squad, unit, resp.StatusCode)
	}
}

// The reads are public: the console's anonymous viewers see the squad.
func TestPublicReadsAnswerWithoutAToken(t *testing.T) {
	r := newRig(t, false)
	if resp := r.do(t, "GET", "/v1/squads", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	if resp := r.do(t, "GET", "/v1/squads/alpha", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown squad: %d", resp.StatusCode)
	}
	// The unit routes are not there to refuse, as the work routes are.
	if resp := r.do(t, "POST", "/v1/squads/alpha/hello", `{"unit":"crew-18","role":"vanguard"}`, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("hello without a token: %d, want 404", resp.StatusCode)
	}
}

func TestGuardShape(t *testing.T) {
	r := newRig(t, true)
	tests := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no bearer at all", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong bearer", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"through the ingress", map[string]string{"X-Forwarded-For": "203.0.113.7"}, http.StatusNotFound},
		{"the shared token", map[string]string{}, http.StatusNoContent},
	}
	for _, tt := range tests {
		resp := r.do(t, "POST", "/v1/squads/alpha/hello", `{"unit":"crew-18","role":"vanguard"}`, tt.hdr)
		if resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
}

func TestHelloChecksItsBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"join", `{"unit":"crew-18","role":"vanguard"}`, http.StatusNoContent},
		{"join with a name and a parent", `{"unit":"crew-19","role":"striker","squad_name":"Warriors","battalion":"bravo","battalion_name":"Second"}`, http.StatusNoContent},
		{"unknown unit", `{"unit":"crew-99","role":"striker"}`, http.StatusBadRequest},
		{"a role not in the table", `{"unit":"crew-19","role":"goblin"}`, http.StatusBadRequest},
		{"missing role", `{"unit":"crew-19"}`, http.StatusBadRequest},
		{"unknown fields", `{"unit":"crew-19","role":"striker","team":1}`, http.StatusBadRequest},
		{"not even JSON", `zero`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, true)
			resp := r.do(t, "POST", "/v1/squads/alpha/hello", tt.body, nil)
			if resp.StatusCode != tt.want {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestHellosShowInTheViews(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.do(t, "POST", "/v1/squads/alpha/hello",
		`{"unit":"crew-19","role":"striker","squad_name":"Warriors","battalion":"bravo","battalion_name":"Second"}`, nil)

	got := readBody(t, r.do(t, "GET", "/v1/squads", "", nil))
	for _, want := range []string{`"id":"alpha"`, `"name":"Warriors"`, `"leader":"crew-18"`,
		`"role":"striker"`, `"battalions":[{"id":"bravo"`, `"name":"Second"`, `"squads":["alpha"]`} {
		if !strings.Contains(got, want) {
			t.Errorf("the list %s is missing %s", got, want)
		}
	}
	if got := readBody(t, r.do(t, "GET", "/v1/squads/alpha", "", nil)); !strings.Contains(got, `"members":[`) {
		t.Errorf("the view %s has no members", got)
	}
}

func TestStatusUpdateAndValidation(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	resp := r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":12.5,"food":18,"weapon":"diamond_sword","pos":{"x":1.5,"y":64,"z":-760.25}}`, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got := readBody(t, r.do(t, "GET", "/v1/squads/alpha", "", nil))
	for _, want := range []string{`"hp":12.5`, `"food":18`, `"alive":true`, `-760.25`} {
		if !strings.Contains(got, want) {
			t.Errorf("the view %s is missing %s", got, want)
		}
	}
	tests := []struct {
		name, body string
		want       int
	}{
		{"unknown unit", `{"unit":"crew-21","hp":1,"food":1,"pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"impossible food", `{"unit":"crew-18","hp":1,"food":21,"pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"unknown fields", `{"unit":"crew-18","hp":1,"food":1,"pos":{"x":0,"y":0,"z":0},"xp":9}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		resp := r.do(t, "POST", "/v1/squads/alpha/status", tt.body, nil)
		if resp.StatusCode != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
}

// A dead heart reads as not alive in the view, before any chain decision:
// the console and the store must agree on the words.
func TestDeadStatusShowsNotAlive(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":0,"food":0,"pos":{"x":0,"y":0,"z":0}}`, nil)
	if got := readBody(t, r.do(t, "GET", "/v1/squads/alpha", "", nil)); strings.Contains(got, `"alive":true`) {
		t.Errorf("a reported-dead member reads alive: %s", got)
	}
}

func TestCalloutNeedsMembership(t *testing.T) {
	r := newRig(t, true)
	callout := `{"unit":"crew-18","kind":"low_health"}`
	if resp := r.do(t, "POST", "/v1/squads/alpha/callout", callout, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("callout with no squad: %d", resp.StatusCode)
	}
	r.hello(t, "alpha", "crew-19", "striker")
	if resp := r.do(t, "POST", "/v1/squads/alpha/callout", callout, nil); resp.StatusCode != http.StatusConflict {
		t.Errorf("callout from an outsider: %d", resp.StatusCode)
	}
	r.hello(t, "alpha", "crew-18", "vanguard")
	if resp := r.do(t, "POST", "/v1/squads/alpha/callout", callout, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("callout from a member: %d", resp.StatusCode)
	}
	d := r.ordersSince(t, "alpha", "0")
	if len(d.Callouts) != 1 || d.Callouts[0].Kind != "low_health" || d.Callouts[0].From != "crew-18" {
		t.Errorf("the callout did not reach the poll: %v", d.Callouts)
	}
	if resp := r.do(t, "POST", "/v1/squads/alpha/callout", `{"unit":"crew-18","kind":"dinner"}`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a callout kind not in the table: %d", resp.StatusCode)
	}
}

// ordersSince is the call a unit makes, as the client does it.
func (r *rig) ordersSince(t *testing.T, squad, since string) Delta {
	t.Helper()
	resp := r.do(t, "GET", "/v1/squads/"+squad+"/orders?wait=1s&since="+since, "", nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("orders: %d", resp.StatusCode)
	}
	var d Delta
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
			t.Fatalf("orders body: %s", err)
		}
	}
	return d
}

func TestOrderRouteChecksItsShape(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")

	back := r.ops
	r.ops = func() string { return "" }
	if resp := r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, map[string]string{"X-Crew-Request": "1"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an order without a session: %d", resp.StatusCode)
	}
	r.ops = back
	if resp := r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("an order without the operator header: %d", resp.StatusCode)
	}
	resp := r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold","target":"captain"}`, map[string]string{"X-Crew-Request": "1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("order: %d", resp.StatusCode)
	}
	if d := r.ordersSince(t, "alpha", "0"); d.Order == nil || d.Order.Kind != "hold" || d.Order.By != "owner" {
		t.Errorf("the order did not land with its operator: %+v", d.Order)
	}
}

func TestOrderRefusesUnknownSquadsAndBadKinds(t *testing.T) {
	r := newRig(t, true)
	op := map[string]string{"X-Crew-Request": "1"}
	if resp := r.do(t, "POST", "/v1/squads/nope/order", `{"kind":"hold"}`, op); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an order at an empty console: %d", resp.StatusCode)
	}
	r.hello(t, "alpha", "crew-18", "vanguard")
	tests := []struct{ name, body string }{
		{"a kind not in the table", `{"kind":"attack"}`},
		{"missing kind", `{}`},
		{"a target that is too long", `{"kind":"focus","target":"` + strings.Repeat("x", 40) + `"}`},
		{"unknown fields", `{"kind":"hold","loud":true}`},
	}
	for _, tt := range tests {
		if resp := r.do(t, "POST", "/v1/squads/alpha/order", tt.body, op); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", tt.name, resp.StatusCode)
		}
	}
}

// A poll with a fresh cursor answers at once; a fully read one rides out
// its wait; and one waiting pays off the moment an order lands.
func TestOrdersLongPoll(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")

	d := r.ordersSince(t, "alpha", "0")
	if d.Cursor != 0 {
		t.Fatalf("the first read found cursor %d, want a fresh squad's seq 0", d.Cursor)
	}
	if d.Leader == nil || d.Leader.Unit != "crew-18" {
		t.Errorf("the joining unit's first read says the leader is %+v", d.Leader)
	}

	// A cursor ahead of the squad changes nothing, so the poll rides out
	// its wait.
	start := time.Now()
	if resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=1s&since=1", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("a fully read poll answered %d", resp.StatusCode)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("the 204 came back after %v, ahead of its wait", d)
	}

	// Prime the cursor with one order, so the waiting poll is genuinely out
	// of date, and reading it again costs nothing.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)
	if d := r.ordersSince(t, "alpha", "0"); d.Cursor != 1 || d.Order.Kind != "hold" {
		t.Fatalf("the priming read is cursor %d order %+v, want seq 1 / hold", d.Cursor, d.Order)
	}

	type answer struct {
		status int
		order  string
	}
	poll := make(chan answer, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1", "", nil)
		want := ""
		if resp.StatusCode == http.StatusOK {
			var d Delta
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
			if d.Order != nil {
				want = d.Order.Kind
			}
		} else {
			want = resp.Status
		}
		poll <- answer{resp.StatusCode, want}
	}()
	time.Sleep(200 * time.Millisecond)
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"regroup"}`, op)
	select {
	case got := <-poll:
		if got.status != http.StatusOK || got.order != "regroup" {
			t.Errorf("the woken poll answered %d %q", got.status, got.order)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a landing order did not wake the wait")
	}
}

// A call-out wakes a waiting poll the same way an order does.
func TestCalloutWakesThePoll(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.hello(t, "alpha", "crew-19", "striker")

	// Prime the cursor, since a since=0 read answers at once whatever has
	// landed so far.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)
	if d := r.ordersSince(t, "alpha", "0"); d.Cursor != 1 {
		t.Fatalf("the priming read is cursor %d, want seq 1", d.Cursor)
	}

	type got struct {
		status int
		from   string
	}
	ch := make(chan got, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1", "", nil)
		var from string
		if resp.StatusCode == http.StatusOK {
			var d Delta
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
			if len(d.Callouts) > 0 {
				from = d.Callouts[0].From
			}
		}
		ch <- got{resp.StatusCode, from}
	}()
	time.Sleep(200 * time.Millisecond)
	r.do(t, "POST", "/v1/squads/alpha/callout", `{"unit":"crew-19","kind":"target_down"}`, nil)
	select {
	case g := <-ch:
		if g.status != http.StatusOK || g.from != "crew-19" {
			t.Errorf("the woken poll answered %d with callout from %q", g.status, g.from)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a landing callout did not wake the wait")
	}
}

func TestOrdersPollValidatesSince(t *testing.T) {
	r := newRig(t, true)
	for _, since := range []string{"-1", "one", "1.5"} {
		if resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=1s&since="+since, "", nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("since=%q: status %d", since, resp.StatusCode)
		}
	}
	if resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=1s", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("no such squad: %d", resp.StatusCode)
	}
}
