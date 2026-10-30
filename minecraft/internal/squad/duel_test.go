package squad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testGo = `{"state":"go","round":"r1","fighters":["Rusty"],"enemies":["Slate","Tinder"],"eliminated":["Tinder"]}`

func TestDuelRoundTripOnALabSquad(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")

	if resp := r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("before any PUT: %d, want 404", resp.StatusCode)
	}
	if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", testGo, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	body := readBody(t, r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil))
	for _, want := range []string{`"state":"go"`, `"round":"r1"`, `"fighters":["Rusty"]`,
		`"enemies":["Slate","Tinder"]`, `"eliminated":["Tinder"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("the duel is missing %s: %s", want, body)
		}
	}
	if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", `{"state":"over","round":"r1"}`, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("over: %d", resp.StatusCode)
	}
	if body := readBody(t, r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil)); !strings.Contains(body, `"state":"over"`) || strings.Contains(body, "enemies") {
		t.Errorf("the squad still serves the old duel: %s", body)
	}
}

func TestDuelRoutesAreGuardedLikeTheProfileRoutes(t *testing.T) {
	if resp := newRig(t, false).do(t, "PUT", "/v1/squads/lab-red/duel", testGo, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a write without a token: %d, want the route not to exist", resp.StatusCode)
	}
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	tests := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no bearer", map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong bearer", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"through the ingress", map[string]string{"X-Forwarded-For": "203.0.113.7"}, http.StatusNotFound},
		{"the shared token", map[string]string{}, http.StatusNoContent},
	}
	for _, tt := range tests {
		if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", testGo, tt.hdr); resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
	if resp := r.do(t, "GET", "/v1/squads/lab-red/duel", "", map[string]string{"Authorization": ""}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a read without a bearer: %d, want 401", resp.StatusCode)
	}
}

func TestDuelPUTNeedsALabSquad(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	if resp := r.do(t, "PUT", "/v1/squads/alpha/duel", testGo, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a production squad took a duel: %d, want 403", resp.StatusCode)
	}
	if resp := r.do(t, "GET", "/v1/squads/alpha/duel", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the refused squad serves a duel: %d, want 404", resp.StatusCode)
	}
	if resp := r.do(t, "PUT", "/v1/squads/lab-nope/duel", testGo, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a squad that does not exist: %d, want 404", resp.StatusCode)
	}
}

func TestDuelPUTChecksItsShape(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	names := func(n int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(`"u%d"`, i)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		name string
		body string
		want int
	}{
		{"an unknown state", `{"state":"fight"}`, http.StatusBadRequest},
		{"no state", `{}`, http.StatusBadRequest},
		{"unknown fields", `{"state":"idle","mood":"bold"}`, http.StatusBadRequest},
		{"not even JSON", `bold`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
		{"trailing data", testGo + ` {}`, http.StatusBadRequest},
		{"go with nobody to hunt", `{"state":"go","fighters":["Rusty"]}`, http.StatusBadRequest},
		{"armed with no fighters", `{"state":"armed","enemies":["Slate"]}`, http.StatusBadRequest},
		{"a unit on both sides", `{"state":"go","fighters":["Rusty"],"enemies":["Rusty"]}`, http.StatusBadRequest},
		{"eliminated from nowhere", `{"state":"go","fighters":["Rusty"],"enemies":["Slate"],"eliminated":["Ghost"]}`, http.StatusBadRequest},
		{"a duplicate name", `{"state":"go","fighters":["Rusty"],"enemies":["Slate","Slate"]}`, http.StatusBadRequest},
		{"an empty name", `{"state":"go","fighters":["Rusty"],"enemies":[""]}`, http.StatusBadRequest},
		{"a quote in a name", `{"state":"go","fighters":["Rusty"],"enemies":["Sl\"ate"]}`, http.StatusBadRequest},
		{"a newline in a name", `{"state":"go","fighters":["Rusty"],"enemies":["Sl\nate"]}`, http.StatusBadRequest},
		{"a long name", `{"state":"go","fighters":["Rusty"],"enemies":["` + strings.Repeat("a", 33) + `"]}`, http.StatusBadRequest},
		{"too many names", `{"state":"go","fighters":["Rusty"],"enemies":[` + names(17) + `]}`, http.StatusBadRequest},
		{"a long round id", `{"state":"idle","round":"` + strings.Repeat("r", 33) + `"}`, http.StatusBadRequest},
		{"a body past the cap", `{"state":"idle","round":"` + strings.Repeat("r", maxDuelLen) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", tt.body, nil); resp.StatusCode != tt.want {
				t.Errorf("status %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
	if resp := r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a refused PUT still left a duel: %d", resp.StatusCode)
	}
}

func TestDuelChangeWakesThePoll(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	r.do(t, "POST", "/v1/squads/lab-red/order", `{"kind":"hold"}`, map[string]string{"X-Crew-Request": "1"})
	fresh := r.ordersSince(t, "lab-red", "0")
	if fresh.DuelSeq != 0 {
		t.Fatalf("a squad with no duel answers duelSeq %d, want 0", fresh.DuelSeq)
	}
	type answer struct {
		status int
		d      Delta
	}
	poll := make(chan answer, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/lab-red/orders?wait=10s&since="+fmt.Sprint(fresh.Cursor)+"&rev="+fmt.Sprint(fresh.Rev), "", nil)
		var d Delta
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
		}
		poll <- answer{resp.StatusCode, d}
	}()
	time.Sleep(200 * time.Millisecond)
	if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", testGo, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put: %d", resp.StatusCode)
	}
	select {
	case got := <-poll:
		if got.status != http.StatusOK || got.d.DuelSeq != 1 {
			t.Errorf("the woken poll answered %d with duelSeq %d, want 200 and 1", got.status, got.d.DuelSeq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stored duel did not wake the wait")
	}
}

// A duel the director never withdrew must not stay in force: it lapses,
// and the squad then serves none, which the unit reads as no fight.
func TestDuelLapsesAndDiesWithTheSquad(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	if st.SetDuel("alpha", duelWire{State: duelIdle}) {
		t.Error("a production squad took a duel at the store")
	}
	cl.hello(st, "crew-19", "lab-red", "striker")
	if !st.SetDuel("lab-red", duelWire{State: duelGo, Fighters: []string{"a"}, Enemies: []string{"b"}}) {
		t.Fatal("a lab squad refused a duel")
	}
	// The squad's claim is renewed as the unit would, so only the duel's
	// own age is in question.
	for waited := time.Duration(0); waited < duelTTL-time.Second; waited += 30 * time.Second {
		cl.after(30 * time.Second)
		cl.hello(st, "crew-19", "lab-red", "striker")
	}
	if _, ok := st.Duel("lab-red"); !ok {
		t.Fatal("the duel lapsed early")
	}
	cl.after(31 * time.Second)
	cl.hello(st, "crew-19", "lab-red", "striker")
	if _, ok := st.Duel("lab-red"); ok {
		t.Error("the duel outlived its TTL")
	}
	st.SetDuel("lab-red", duelWire{State: duelIdle})
	cl.after(memberTTL + time.Second)
	st.Get("lab-red")
	if _, ok := st.Duel("lab-red"); ok {
		t.Error("the duel outlived the squad's last claim")
	}
	if st.SetDuel("lab-/../fwb", duelWire{State: duelIdle}) {
		t.Error("a path-shaped id took a duel")
	}
}
