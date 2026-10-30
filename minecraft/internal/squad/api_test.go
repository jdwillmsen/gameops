package squad

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const token = "s3cret"

// testUnits is the roster the routes check hellos against: the design's
// three warrior slots, no more.
type testUnits map[string]bool

func (u testUnits) Has(id string) bool { return u[id] }

// NameOf gives the three warrior slots the gamertags the duel tests name.
func (u testUnits) NameOf(id string) (string, bool) {
	n, ok := map[string]string{"crew-18": "Rusty", "crew-19": "Slate", "crew-20": "Tinder"}[id]
	return n, ok && u[id]
}

var units = testUnits{"crew-18": true, "crew-19": true, "crew-20": true}

type rig struct {
	api   *API
	store *Store
	srv   *httptest.Server
	ops   func() string // the operator a gated call presents, "" for nobody
}

func newRig(t *testing.T, tokenSet bool) *rig {
	t.Helper()
	r := &rig{store: NewStore(), ops: func() string { return "owner" }}
	mux := http.NewServeMux()
	if tokenSet {
		r.api = Routes(mux, r.store, units, token)
	} else {
		r.api = Routes(mux, r.store, units, "")
	}
	mux.Handle("POST /v1/squads/{id}/order", gateProbe(r))
	mux.Handle("POST /v1/squads/{id}/start", startGateProbe(r))
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	return r
}

// startGateProbe is the same gate in front of the start route.
func startGateProbe(r *rig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Crew-Request") != "1" {
			http.Error(w, `{"error":"bad_request"}`, http.StatusForbidden)
			return
		}
		if r.ops() == "" {
			http.Error(w, `{"error":"signed_out"}`, http.StatusUnauthorized)
			return
		}
		r.api.start(w, req)
	})
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

// openWaiters counts the channels still parked on the squad's wakeup
// registry.
func (r *rig) openWaiters(squad string) int {
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	return len(r.api.waits[squad])
}

// waitRigged issues one long request under a fresh cancellable context and
// leaves it waiting; the returned cancel is the handle the test hangs the
// wait up with.
func (r *rig) waitingRequest(t *testing.T, path string) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	// Long enough for the handler to reach its wait.
	time.Sleep(100 * time.Millisecond)
	return cancel
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

// The public reads carry the console-safe shape only: the same redaction
// the stream serves, so an anonymous viewer learns no member position and
// no operator id. The unit-facing poll keeps both behind its token.
func TestPublicReadsAreConsoleSafe(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":12.5,"food":7,"weapon":"bow","pos":{"x":1.5,"y":64,"z":-760.25}}`, nil)
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"focus","target":"captain"}`,
		map[string]string{"X-Crew-Request": "1"})

	body := readBody(t, r.do(t, "GET", "/v1/squads/alpha", "", nil))
	for _, want := range []string{`"id":"alpha"`, `"leader":"crew-18"`,
		`"hp":12.5`, `"weapon":"bow"`, `"kind":"focus"`, `"target":"captain"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the single read is missing %s: %s", want, body)
		}
	}
	for _, path := range []string{"/v1/squads", "/v1/squads/alpha"} {
		got := readBody(t, r.do(t, "GET", path, "", nil))
		for _, leak := range []string{`"pos"`, `"by"`, `"owner"`, `-760.25`} {
			if strings.Contains(got, leak) {
				t.Errorf("%s leaks %s: %s", path, leak, got)
			}
		}
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
		{"a battalion one past the line", `{"unit":"crew-19","role":"striker","battalion":"` + strings.Repeat("a", 33) + `"}`, http.StatusBadRequest},
		{"a battalion past 32 printable characters", `{"unit":"crew-19","role":"striker","battalion":"ab` + "\n" + `cd"}`, http.StatusBadRequest},
		{"a battalion_name one past the line", `{"unit":"crew-19","role":"striker","battalion_name":"` + strings.Repeat("a", 33) + `"}`, http.StatusBadRequest},
		{"a battalion_name with a newline", `{"unit":"crew-19","role":"striker","battalion_name":"Sec\nond"}`, http.StatusBadRequest},
		{"a readable battalion_name with a space", `{"unit":"crew-19","role":"striker","battalion_name":"Second Wing"}`, http.StatusNoContent},
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
	for _, want := range []string{`"hp":12.5`, `"food":18`, `"alive":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("the view %s is missing %s", got, want)
		}
	}
	// The public route is console-safe: a member's position is not on it.
	if strings.Contains(got, `"pos"`) || strings.Contains(got, `-760.25`) {
		t.Errorf("the view %s leaks the position", got)
	}
	tests := []struct {
		name, body string
		want       int
	}{
		{"unknown unit", `{"unit":"crew-21","hp":1,"food":1,"pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"impossible food", `{"unit":"crew-18","hp":1,"food":21,"pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"unknown fields", `{"unit":"crew-18","hp":1,"food":1,"pos":{"x":0,"y":0,"z":0},"xp":9}`, http.StatusBadRequest},
		{"a weapon one past the line", `{"unit":"crew-18","hp":1,"food":1,"weapon":"` + strings.Repeat("w", 33) + `","pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"a weapon with a newline", `{"unit":"crew-18","hp":1,"food":1,"weapon":"one\ntwo","pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
		{"a weapon with a control byte", `{"unit":"crew-18","hp":1,"food":1,"weapon":"sword` + "\x01" + `tail","pos":{"x":0,"y":0,"z":0}}`, http.StatusBadRequest},
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

// A leader change or a moved leader must reach a unit whose cursor is
// already current, not only the next order or call-out: the waiting poll
// answers at once the moment what "who leads and where it stands" says
// changed.
func TestLeaderChangeWakesThePoll(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.hello(t, "alpha", "crew-19", "striker")
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":20,"food":20,"weapon":"sword","pos":{"x":1,"y":64,"z":2}}`, nil)

	// Prime the cursor so the polls below have nothing new to read and
	// would otherwise only ride their wait out.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)
	fresh := r.ordersSince(t, "alpha", "0")
	if fresh.Leader == nil || fresh.Leader.Unit != "crew-18" || fresh.Leader.Pos == nil {
		t.Fatalf("the primed answer's leader is %+v, want crew-18 placed", fresh.Leader)
	}
	rev := fmt.Sprintf("%d", fresh.Rev)

	waitPoll := func(t *testing.T, body chan Delta) Delta {
		t.Helper()
		select {
		case d := <-body:
			return d
		case <-time.After(3 * time.Second):
			t.Fatal("the waited poll never answered")
			return Delta{}
		}
	}

	// The leader moves: a poll older than the move answers at once.
	moved := make(chan Delta, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1&rev="+rev, "", nil)
		var d Delta
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
		}
		moved <- d
	}()
	time.Sleep(200 * time.Millisecond)
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":20,"food":20,"weapon":"sword","pos":{"x":9,"y":65,"z":3}}`, nil)
	d := waitPoll(t, moved)
	if d.Leader == nil || d.Leader.Pos == nil || d.Leader.Pos.X != 9 {
		t.Errorf("a moved leader did not wake the poll: %+v", d.Leader)
	}
	rev = fmt.Sprintf("%d", d.Rev)

	// The leader falls: the next in the chain must reach the waiting unit.
	promoted := make(chan Delta, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1&rev="+rev, "", nil)
		var d Delta
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
		}
		promoted <- d
	}()
	time.Sleep(200 * time.Millisecond)
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":0,"food":0,"weapon":"sword","pos":{"x":9,"y":65,"z":3}}`, nil)
	d = waitPoll(t, promoted)
	if d.Leader == nil || d.Leader.Unit != "crew-19" || len(d.Orders) != 0 {
		t.Errorf("a leader change did not wake the poll: leader %+v orders %v", d.Leader, d.Orders)
	}
}

// gatedStore delays one Delta read past the handler's answer point, so a
// broadcast can land in the gap between reading the state and waiting.
type gatedStore struct {
	*Store
	mu   sync.Mutex
	slow bool
}

func (s *gatedStore) Delta(id string, since int64) (Delta, bool) {
	s.mu.Lock()
	slow := s.slow
	s.slow = false
	s.mu.Unlock()
	d, ok := s.Store.Delta(id, since)
	if slow {
		// The read point is fixed; hold the handler out of its waiting
		// step long enough for the broadcast below to find nobody there.
		time.Sleep(50 * time.Millisecond)
	}
	return d, ok
}

// A broadcast that lands while a poll is between reading the state and
// registering its waiter must not be lost: the waiter is there before
// anything is read, so the read-after-wake finds the new message.
func TestBroadcastCannotFallPastADeltaRead(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)
	r.api.store = &gatedStore{Store: r.store}

	for i := 0; i < 5; i++ {
		since := i + 1
		g := r.api.store.(*gatedStore)
		g.mu.Lock()
		g.slow = true
		g.mu.Unlock()

		type answer struct {
			status int
			kind   string
		}
		poll := make(chan answer, 1)
		go func() {
			resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since="+strconv.Itoa(since), "", nil)
			kind := ""
			if resp.StatusCode == http.StatusOK {
				var d Delta
				if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
					panic(err)
				}
				if d.Order != nil {
					kind = d.Order.Kind
				}
			}
			poll <- answer{resp.StatusCode, kind}
		}()
		// Inside the delayed read by now; the order lands while the
		// handler is still short of its waiter.
		time.Sleep(10 * time.Millisecond)
		r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"regroup"}`, op)
		select {
		case got := <-poll:
			if got.status != http.StatusOK || got.kind != "regroup" {
				t.Errorf("iteration %d: the woken poll answered %d %q", i, got.status, got.kind)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("iteration %d: a broadcast between the read and the wait lost the wake", i)
		}
	}
}

// A poll that finished by itself — timed out or cancelled — must leave no
// channel behind to be closed by the next broadcast of a poll that is long
// gone.
func TestFinishedPollsLeaveNoWaiters(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	// One order to prime the cursor, so a poll since=1 is genuinely fully
	// read and waits its bound or its caller out.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)

	// Timed out: the 204 path takes its channel with it.
	if resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=1s&since=1", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the fully read poll answered %d", resp.StatusCode)
	}
	if n := r.openWaiters("alpha"); n != 0 {
		t.Errorf("a timed-out poll left %d waiters", n)
	}

	// Cancelled by the caller: the context path takes its channel too.
	cancel := r.waitingRequest(t, "/v1/squads/alpha/orders?wait=10s&since=1")
	cancel()
	time.Sleep(50 * time.Millisecond)
	if n := r.openWaiters("alpha"); n != 0 {
		t.Errorf("a cancelled poll left %d waiters", n)
	}

	// A woken poll leaves nothing either: its channel is gone with it.
	type got struct {
		status int
		kind   string
	}
	poll := make(chan got, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1", "", nil)
		kind := ""
		if resp.StatusCode == http.StatusOK {
			var d Delta
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
			if d.Order != nil {
				kind = d.Order.Kind
			}
		}
		poll <- got{resp.StatusCode, kind}
	}()
	time.Sleep(100 * time.Millisecond)
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"regroup"}`, op)
	select {
	case g := <-poll:
		if g.status != http.StatusOK || g.kind != "regroup" {
			t.Errorf("the woken poll answered %d %q", g.status, g.kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the woken poll never answered")
	}
	if n := r.openWaiters("alpha"); n != 0 {
		t.Errorf("an answered poll left %d waiters", n)
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

	// A cursor ahead of the squad is a unit that read a log this server no
	// longer has: crew-control's restart winds seq back to 0 under an old
	// cursor, so the server answers at once with everything from the
	// start instead of letting the unit sit on a cursor nothing will ever
	// pass.
	if resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=1s&since=9", "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("a poll past the log answered %d, want a resync 200", resp.StatusCode)
	}

	// Prime the cursor with one order, so the waiting poll is genuinely out
	// of date, and reading it again costs nothing.
	op := map[string]string{"X-Crew-Request": "1"}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, op)
	if d := r.ordersSince(t, "alpha", "0"); d.Cursor != 1 || d.Order.Kind != "hold" {
		t.Fatalf("the priming read is cursor %d order %+v, want seq 1 / hold", d.Cursor, d.Order)
	}
	// The same hold rides a resync read: the restarted-log unit gets the
	// standing order again from seq 0.
	if d := r.ordersSince(t, "alpha", "9"); d.Cursor != 1 ||
		d.Order == nil || d.Order.Seq != 1 || len(d.Orders) != 1 {
		t.Errorf("a past-the-log poll after order seq 1 delivered %+v, want the full log", d)
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
