package squad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const testGo = `{"state":"go","round":"r1","fighters":["Rusty"],"enemies":["Slate","Tinder"],"eliminated":["Tinder"]}`

// duelRig is a lab-red squad of Rusty facing a lab-blue squad of Slate and
// Tinder, the way a director's two squads stand.
func duelRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	r.hello(t, "lab-blue", "crew-19", "striker")
	r.hello(t, "lab-blue", "crew-20", "ranger")
	return r
}

func TestDuelRoundTripOnALabSquad(t *testing.T) {
	r := duelRig(t)

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
	r := duelRig(t)
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
	r := duelRig(t)
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

func namedStore() (*Store, *fakeClock) {
	st, cl := newSquadRig()
	st.nameOf = testUnits{"crew-18": true, "crew-19": true, "crew-20": true}.NameOf
	return st, cl
}

const (
	redUnit  = "crew-18"
	blueUnit = "crew-19"
)

var testDuel = duelWire{State: duelGo, Fighters: []string{"Rusty"}, Enemies: []string{"Slate"}}

func mustSetDuel(t *testing.T, st *Store, id string, d duelWire) bool {
	t.Helper()
	changed, err := st.SetDuel(id, d)
	if err != nil {
		t.Fatalf("SetDuel(%s): %v", id, err)
	}
	return changed
}

// A duel nobody renews lapses within the TTL: a director that died, or whose
// withdrawal failed, leaves the units hunting for under a minute.
func TestDuelLapsesWithoutAHeartbeat(t *testing.T) {
	st, cl := namedStore()
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	mustSetDuel(t, st, "lab-red", testDuel)
	if duelTTL > time.Minute {
		t.Fatalf("duelTTL = %s; a dead director must lapse within a minute", duelTTL)
	}
	// The members renew their claims as the units would, so only the
	// duel's own age is in question.
	for waited := time.Duration(0); waited < duelTTL-time.Second; waited += 5 * time.Second {
		cl.after(5 * time.Second)
		cl.hello(st, redUnit, "lab-red", "striker")
		cl.hello(st, blueUnit, "lab-blue", "striker")
	}
	if _, ok := st.Duel("lab-red"); !ok {
		t.Fatal("the duel lapsed early")
	}
	cl.after(2 * time.Second)
	if _, ok := st.Duel("lab-red"); ok {
		t.Error("the duel outlived its TTL with nobody renewing it")
	}
}

func TestDuelHeartbeatKeepsItAliveWithoutBumpingTheRevision(t *testing.T) {
	st, cl := namedStore()
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	if !mustSetDuel(t, st, "lab-red", testDuel) {
		t.Fatal("a new duel was not reported as changed")
	}
	seq := st.squads["lab-red"].duelSeq
	for beat := 0; beat < 20; beat++ {
		cl.after(10 * time.Second)
		cl.hello(st, redUnit, "lab-red", "striker")
		cl.hello(st, blueUnit, "lab-blue", "striker")
		if mustSetDuel(t, st, "lab-red", testDuel) {
			t.Fatalf("beat %d: an unchanged duel reported a change", beat)
		}
	}
	if _, ok := st.Duel("lab-red"); !ok {
		t.Fatal("a duel renewed every 10s lapsed")
	}
	if got := st.squads["lab-red"].duelSeq; got != seq {
		t.Errorf("heartbeats moved duelSeq %d -> %d; every unit would refetch each beat", seq, got)
	}
	moved := testDuel
	moved.Eliminated = []string{"Slate"}
	if !mustSetDuel(t, st, "lab-red", moved) || st.squads["lab-red"].duelSeq != seq+1 {
		t.Error("a changed duel did not bump the revision")
	}
}

// A beat that arrives after the duel lapsed is a new duel: units that already
// read "no fight" must be woken to refetch.
func TestDuelBeatAfterALapseIsAFreshDuel(t *testing.T) {
	st, cl := namedStore()
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	mustSetDuel(t, st, "lab-red", testDuel)
	cl.after(duelTTL + time.Second)
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	if !mustSetDuel(t, st, "lab-red", testDuel) {
		t.Error("a duel renewed after its lapse did not bump the revision")
	}
}

// Over the wire: a director that stops beating leaves the squad serving 404
// within the TTL, which the unit reads as no fight.
func TestDuelServedUntilTheTTLWithoutBeats(t *testing.T) {
	r := duelRig(t)
	clock := &fakeClock{t: time.Now()}
	r.store.now = clock.Now
	put := func() {
		t.Helper()
		if resp := r.do(t, "PUT", "/v1/squads/lab-red/duel", `{"state":"go","fighters":["Rusty"],"enemies":["Slate"]}`, nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("put: %d", resp.StatusCode)
		}
	}
	get := func() int { return r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil).StatusCode }
	put()
	for i := 0; i < 6; i++ {
		clock.after(10 * time.Second)
		r.hello(t, "lab-red", "crew-18", "vanguard")
		r.hello(t, "lab-blue", "crew-19", "striker")
		put()
		if got := get(); got != http.StatusOK {
			t.Fatalf("beat %d: the live duel answers %d", i, got)
		}
	}
	// The director dies here: no more beats, the members keep helloing.
	clock.after(duelTTL - time.Second)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	if got := get(); got != http.StatusOK {
		t.Fatalf("just inside the TTL the duel answers %d", got)
	}
	clock.after(2 * time.Second)
	if got := get(); got != http.StatusNotFound {
		t.Errorf("past the TTL with no beat the duel answers %d, want 404", got)
	}
}

func TestDuelDiesWithTheSquad(t *testing.T) {
	st, cl := namedStore()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	if _, err := st.SetDuel("alpha", duelWire{State: duelIdle}); err == nil {
		t.Error("a production squad took a duel at the store")
	}
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	mustSetDuel(t, st, "lab-red", testDuel)
	st.SetDuel("lab-red", duelWire{State: duelIdle})
	cl.after(memberTTL + time.Second)
	st.Get("lab-red")
	if _, ok := st.Duel("lab-red"); ok {
		t.Error("the duel outlived the squad's last claim")
	}
	if _, err := st.SetDuel("lab-/../fwb", duelWire{State: duelIdle}); err == nil {
		t.Error("a path-shaped id took a duel")
	}
}

// Anyone holding the shared unit token must not be able to point a unit at a
// human: both sides of a duel have to be lab squad members the roster names.
func TestDuelNamesMustBeLabSquadMembers(t *testing.T) {
	r := duelRig(t)
	put := func(body string) int {
		return r.do(t, "PUT", "/v1/squads/lab-red/duel", body, nil).StatusCode
	}
	tests := []struct {
		name string
		body string
		want int
	}{
		{"crew names across two lab squads", testGo, http.StatusNoContent},
		{"a human among the enemies", `{"state":"go","fighters":["Rusty"],"enemies":["Steve"]}`, http.StatusForbidden},
		{"a human beside a crew enemy", `{"state":"go","fighters":["Rusty"],"enemies":["Slate","Steve"]}`, http.StatusForbidden},
		{"a human among the fighters", `{"state":"go","fighters":["Steve"],"enemies":["Slate"]}`, http.StatusForbidden},
		{"a fighter from another squad", `{"state":"go","fighters":["Slate"],"enemies":["Tinder"]}`, http.StatusForbidden},
		{"a case-shifted name", `{"state":"go","fighters":["Rusty"],"enemies":["slate"]}`, http.StatusForbidden},
		{"a unit id is no name", `{"state":"go","fighters":["Rusty"],"enemies":["crew-19"]}`, http.StatusForbidden},
		{"armed names a human too", `{"state":"armed","fighters":["Rusty"],"enemies":["Steve"]}`, http.StatusForbidden},
		{"idle names nobody", `{"state":"idle"}`, http.StatusNoContent},
		{"over names nobody", `{"state":"over","round":"r1"}`, http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := put(tt.body); got != tt.want {
				t.Errorf("status %d, want %d", got, tt.want)
			}
		})
	}
	// The refused PUTs left the accepted one standing, not a human target.
	if body := readBody(t, r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil)); strings.Contains(body, "Steve") {
		t.Errorf("a human reached the served duel: %s", body)
	}
}

func TestDuelEnemiesOutsideLabSquadsAreRefused(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "lab-red", "crew-18", "vanguard")
	r.hello(t, "alpha", "crew-19", "striker") // a production squad's unit
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel", `{"state":"go","fighters":["Rusty"],"enemies":["Slate"]}`, nil).StatusCode; got != http.StatusForbidden {
		t.Errorf("a production unit as the enemy: %d, want 403", got)
	}
}

func TestDuelNamedMemberWhoLapsedIsNoLongerAnEnemy(t *testing.T) {
	st, cl := namedStore()
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	mustSetDuel(t, st, "lab-red", testDuel)
	cl.after(memberTTL + time.Second)
	cl.hello(st, redUnit, "lab-red", "striker")
	if _, err := st.SetDuel("lab-red", testDuel); err == nil {
		t.Error("a duel named an enemy whose claim had lapsed")
	}
}

// A roster that cannot name its units makes a naming duel impossible rather
// than unchecked.
func TestDuelWithoutARosterNamerNamesNobody(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, redUnit, "lab-red", "striker")
	cl.hello(st, blueUnit, "lab-blue", "striker")
	if _, err := st.SetDuel("lab-red", testDuel); err == nil {
		t.Error("a duel naming units was stored with no way to check the names")
	}
	if _, err := st.SetDuel("lab-red", duelWire{State: duelIdle}); err != nil {
		t.Errorf("withdrawing a duel needs no names: %v", err)
	}
}

type plainUnits map[string]bool

func (u plainUnits) Has(id string) bool { return u[id] }

func TestDuelOverRoutesWithAPlainRosterRefusesNames(t *testing.T) {
	mux := http.NewServeMux()
	st := NewStore()
	Routes(mux, st, plainUnits{"crew-18": true}, token)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := &rig{store: st, srv: srv, ops: func() string { return "owner" }}
	r.hello(t, "lab-red", "crew-18", "vanguard")
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel", testGo, nil).StatusCode; got != http.StatusForbidden {
		t.Errorf("a roster that cannot name units took a duel: %d, want 403", got)
	}
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel", `{"state":"idle"}`, nil).StatusCode; got != http.StatusNoContent {
		t.Errorf("idle: %d", got)
	}
}

// The unit reads the crew roster from the server's own membership, so what a
// director wrote can never widen it.
func TestDuelAnswerCarriesTheCrewNamesTheServerVouchesFor(t *testing.T) {
	r := duelRig(t)
	r.hello(t, "alpha", "crew-20", "ranger") // moves Tinder out of the lab
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel", testGo, nil).StatusCode; got != http.StatusForbidden {
		t.Fatalf("an enemy that left the lab: %d, want 403", got)
	}
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel", `{"state":"go","fighters":["Rusty"],"enemies":["Slate"]}`, nil).StatusCode; got != http.StatusNoContent {
		t.Fatalf("put: %d", got)
	}
	var ans struct {
		Crew []string `json:"crew"`
	}
	if err := json.Unmarshal([]byte(readBody(t, r.do(t, "GET", "/v1/squads/lab-red/duel", "", nil))), &ans); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Rusty", "Slate"}; !slices.Equal(ans.Crew, want) {
		t.Errorf("crew = %v, want %v: lab squad members only, not the production unit", ans.Crew, want)
	}
	if got := r.do(t, "PUT", "/v1/squads/lab-red/duel",
		`{"state":"go","fighters":["Rusty"],"enemies":["Slate"],"crew":["Steve"]}`, nil).StatusCode; got != http.StatusBadRequest {
		t.Errorf("a director writing the crew list: %d, want 400", got)
	}
}
