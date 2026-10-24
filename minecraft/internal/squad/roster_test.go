package squad

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func memberOf(t *testing.T, d Delta, unit string) RosterMember {
	t.Helper()
	for _, m := range d.Members {
		if m.Unit == unit {
			return m
		}
	}
	t.Fatalf("no member %s in %+v", unit, d.Members)
	return RosterMember{}
}

func TestPollCarriesEachMembersRolePositionAndLiveness(t *testing.T) {
	st, clk := newSquadRig()
	clk.hello(st, "crew-19", "alpha", "striker")
	clk.hello(st, "crew-18", "alpha", "vanguard")
	clk.hello(st, "crew-20", "alpha", "ranger")
	st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Food: 10, Pos: position{X: 4, Y: 64, Z: 5}})
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 0, Food: 10, Pos: position{X: 7, Y: 64, Z: 8}})

	d, ok := st.Delta("alpha", 0)
	if !ok {
		t.Fatal("no answer for the squad")
	}
	if len(d.Members) != 3 || d.Members[0].Unit != "crew-18" || d.Members[1].Unit != "crew-19" || d.Members[2].Unit != "crew-20" {
		t.Fatalf("members %+v, want the chain of command", d.Members)
	}
	van := memberOf(t, d, "crew-18")
	if van.Role != "vanguard" || !van.Alive || !van.Fresh || van.Pos == nil || *van.Pos != (position{X: 4, Y: 64, Z: 5}) {
		t.Errorf("vanguard %+v", van)
	}
	if dead := memberOf(t, d, "crew-19"); dead.Alive || dead.Pos == nil || dead.Pos.X != 7 {
		t.Errorf("a dead striker keeps its last place and is not alive: %+v", dead)
	}
	if mute := memberOf(t, d, "crew-20"); !mute.Alive || mute.Pos != nil {
		t.Errorf("a ranger with no report has no place and counts alive: %+v", mute)
	}
}

func TestMemberTurnsStaleAfterTheLeaderGrace(t *testing.T) {
	st, clk := newSquadRig()
	clk.hello(st, "crew-18", "alpha", "vanguard")
	clk.hello(st, "crew-19", "alpha", "striker")
	clk.status(st, "crew-19", "alpha", 20)
	clk.after(leaderGrace + time.Second)
	clk.status(st, "crew-18", "alpha", 20)

	d, _ := st.Delta("alpha", 0)
	if memberOf(t, d, "crew-19").Fresh {
		t.Error("a striker silent past the grace is still fresh")
	}
	if !memberOf(t, d, "crew-18").Fresh {
		t.Error("a vanguard that just spoke is not fresh")
	}
}

func TestStartIsTheLeadersFirstPlaceAndStays(t *testing.T) {
	st, clk := newSquadRig()
	clk.hello(st, "crew-18", "alpha", "vanguard")
	clk.hello(st, "crew-19", "alpha", "striker")

	if d, _ := st.Delta("alpha", 0); d.Start != nil {
		t.Fatalf("a squad that has not reported has a start %+v", d.Start)
	}
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 20, Pos: position{X: 90, Z: 90}})
	if d, _ := st.Delta("alpha", 0); d.Start != nil {
		t.Fatalf("a follower's report set the start %+v", d.Start)
	}
	st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Pos: position{X: 10, Y: 64, Z: 20}})
	st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Pos: position{X: 50, Y: 64, Z: 60}})

	d, _ := st.Delta("alpha", 0)
	if d.Start == nil || *d.Start != (position{X: 10, Y: 64, Z: 20}) {
		t.Errorf("start %+v, want where the leader first reported", d.Start)
	}
	if !st.SetStart("alpha", position{X: 1, Y: 2, Z: 3}) {
		t.Fatal("SetStart refused a live squad")
	}
	if d, _ := st.Delta("alpha", 0); d.Start == nil || d.Start.X != 1 {
		t.Errorf("start %+v after SetStart, want the director's", d.Start)
	}
	if st.SetStart("bravo", position{}) {
		t.Error("SetStart took a squad that does not exist")
	}
}

func TestRosterChangesMoveTheRev(t *testing.T) {
	st, clk := newSquadRig()
	clk.hello(st, "crew-18", "alpha", "vanguard")
	clk.hello(st, "crew-19", "alpha", "striker")
	clk.status(st, "crew-18", "alpha", 20)
	clk.status(st, "crew-19", "alpha", 20)
	rev := func() int64 { d, _ := st.Delta("alpha", 0); return d.Rev }

	base := rev()
	if rev() != base {
		t.Fatal("a read with nothing new moved the rev")
	}
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 20, Food: 10, Weapon: "diamond_sword", Pos: position{X: 1.2, Y: 2, Z: 3}})
	if rev() != base {
		t.Error("a follower shifting within its block moved the rev")
	}
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 20, Pos: position{X: 6, Y: 2, Z: 3}})
	moved := rev()
	if moved == base {
		t.Error("a follower walking to another block did not move the rev")
	}
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 0, Pos: position{X: 6, Y: 2, Z: 3}})
	died := rev()
	if died == moved {
		t.Error("a follower dying did not move the rev")
	}
	clk.after(leaderGrace + time.Second)
	st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Pos: position{X: 1, Y: 2, Z: 3}})
	if rev() == died {
		t.Error("a member going silent did not move the rev")
	}
	before := rev()
	st.SetStart("alpha", position{X: 99})
	if rev() == before {
		t.Error("a new start point did not move the rev")
	}
}

func TestPublicReadsCarryNeitherRosterPositionsNorStart(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":12.5,"food":7,"weapon":"bow","pos":{"x":1.5,"y":64,"z":-760.25}}`, nil)
	r.do(t, "POST", "/v1/squads/alpha/start", `{"pos":{"x":321,"y":64,"z":-654.5}}`, nil)

	for _, path := range []string{"/v1/squads", "/v1/squads/alpha"} {
		got := readBody(t, r.do(t, "GET", path, "", nil))
		for _, leak := range []string{`"pos"`, `"start"`, `"fresh"`, `-760.25`, `321`, `-654.5`} {
			if strings.Contains(got, leak) {
				t.Errorf("%s leaks %s: %s", path, leak, got)
			}
		}
	}

	s := openSquadStream(t, r)
	got := s.event(t)
	for _, leak := range []string{`"pos"`, `"start"`, `"fresh"`, `-760.25`, `321`, `-654.5`} {
		if strings.Contains(got, leak) {
			t.Errorf("the stream leaks %s: %s", leak, got)
		}
	}

	d := r.ordersSince(t, "alpha", "0")
	if len(d.Members) != 1 || d.Members[0].Pos == nil || d.Start == nil || d.Start.X != 321 {
		t.Errorf("the unit-facing poll lacks the roster or the start: %+v", d)
	}
}

func TestStartRouteIsGuardedAndChecked(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	body := `{"pos":{"x":1,"y":2,"z":3}}`

	for _, tt := range []struct {
		name string
		path string
		body string
		hdr  map[string]string
		want int
	}{
		{"no bearer", "alpha", body, map[string]string{"Authorization": ""}, http.StatusUnauthorized},
		{"wrong bearer", "alpha", body, map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"through the ingress", "alpha", body, map[string]string{"X-Forwarded-For": "203.0.113.7"}, http.StatusNotFound},
		{"unknown squad", "bravo", body, nil, http.StatusNotFound},
		{"unknown field", "alpha", `{"pos":{"x":1,"y":2,"z":3},"who":"me"}`, nil, http.StatusBadRequest},
		{"off the world", "alpha", `{"pos":{"x":1e9,"y":2,"z":3}}`, nil, http.StatusBadRequest},
		{"too big", "alpha", `{"pos":{"x":1,"y":2,"z":3},"pad":"` + strings.Repeat("x", 2<<10) + `"}`, nil, http.StatusRequestEntityTooLarge},
		{"the shared token", "alpha", body, nil, http.StatusNoContent},
	} {
		resp := r.do(t, "POST", "/v1/squads/"+tt.path+"/start", tt.body, tt.hdr)
		if resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
	if resp := r.do(t, "POST", "/v1/squads/alpha/start", body, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	if d := r.ordersSince(t, "alpha", "0"); d.Start == nil || *d.Start != (position{X: 1, Y: 2, Z: 3}) {
		t.Errorf("start %+v after a good post", d.Start)
	}

	open := newRig(t, false)
	if resp := open.do(t, "POST", "/v1/squads/alpha/start", body, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("start without a unit token: %d, want 404", resp.StatusCode)
	}
}

// settableClock lets a test move the store's time while handlers read it.
type settableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *settableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *settableClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// A member going silent makes no request, so nothing would wake a waiting
// poll but its own re-read.
func TestSilenceReachesAWaitingPoll(t *testing.T) {
	r := newRig(t, true)
	clk := &settableClock{t: time.Unix(1000, 0)}
	r.store.now = clk.now
	r.api.reread = 20 * time.Millisecond
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.hello(t, "alpha", "crew-19", "striker")
	for _, u := range []string{"crew-18", "crew-19"} {
		r.do(t, "POST", "/v1/squads/alpha/status",
			`{"unit":"`+u+`","hp":20,"food":20,"weapon":"sword","pos":{"x":1,"y":64,"z":2}}`, nil)
	}
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"hold"}`, map[string]string{"X-Crew-Request": "1"})
	first := r.ordersSince(t, "alpha", "0")

	answer := make(chan Delta, 1)
	go func() {
		resp := r.do(t, "GET", "/v1/squads/alpha/orders?wait=10s&since=1&rev="+strconv.FormatInt(first.Rev, 10), "", nil)
		var d Delta
		if resp.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(readBody(t, resp)), &d); err != nil {
				panic(err)
			}
		}
		answer <- d
	}()
	time.Sleep(100 * time.Millisecond)
	clk.advance(leaderGrace + time.Second)

	select {
	case d := <-answer:
		if len(d.Members) != 2 || memberOf(t, d, "crew-19").Fresh {
			t.Errorf("the wake did not show crew-19 silent: %+v", d.Members)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a member going silent never reached the waiting poll")
	}
}
