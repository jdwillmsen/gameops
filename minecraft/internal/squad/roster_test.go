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

func TestStartComesOnlyFromAnExplicitPostAndLapses(t *testing.T) {
	st, clk := newSquadRig()
	clk.hello(st, "crew-18", "alpha", "vanguard")
	clk.hello(st, "crew-19", "alpha", "striker")

	st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Pos: position{X: 10, Y: 64, Z: 20}})
	st.Status(statusIn{Unit: "crew-19", Squad: "alpha", HP: 20, Pos: position{X: 90, Z: 90}})
	if d, _ := st.Delta("alpha", 0); d.Start != nil {
		t.Fatalf("a report set the start %+v; only an explicit post may", d.Start)
	}
	if !st.SetStart("alpha", position{X: 1, Y: 2, Z: 3}) {
		t.Fatal("SetStart refused a live squad")
	}
	if d, _ := st.Delta("alpha", 0); d.Start == nil || d.Start.X != 1 {
		t.Errorf("start %+v after SetStart, want the posted one", d.Start)
	}
	// A hello must not overwrite a posted start.
	clk.hello(st, "crew-19", "alpha", "striker")
	if d, _ := st.Delta("alpha", 0); d.Start == nil || d.Start.X != 1 {
		t.Errorf("start %+v after a hello, want the posted one kept", d.Start)
	}
	if st.SetStart("bravo", position{}) {
		t.Error("SetStart took a squad that does not exist")
	}
	// Keep the squad alive past the lapse with fresh reports, so only the
	// start expires.
	for i := 0; i < 16; i++ {
		clk.after(time.Minute)
		st.Status(statusIn{Unit: "crew-18", Squad: "alpha", HP: 20, Pos: position{X: 10, Y: 64, Z: 20}})
	}
	if d, _ := st.Delta("alpha", 0); d.Start != nil {
		t.Errorf("a start posted %v ago is still in force: %+v", 16*time.Minute, d.Start)
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
	r.do(t, "POST", "/v1/squads/alpha/start", `{"pos":{"x":4711.5,"y":64,"z":-654.5}}`, opHdr)

	for _, path := range []string{"/v1/squads", "/v1/squads/alpha"} {
		got := readBody(t, r.do(t, "GET", path, "", nil))
		for _, leak := range []string{`"pos"`, `"start"`, `"fresh"`, `-760.25`, `4711.5`, `-654.5`} {
			if strings.Contains(got, leak) {
				t.Errorf("%s leaks %s: %s", path, leak, got)
			}
		}
	}

	s := openSquadStream(t, r)
	got := s.event(t)
	for _, leak := range []string{`"pos"`, `"start"`, `"fresh"`, `-760.25`, `4711.5`, `-654.5`} {
		if strings.Contains(got, leak) {
			t.Errorf("the stream leaks %s: %s", leak, got)
		}
	}

	d := r.ordersSince(t, "alpha", "0")
	if len(d.Members) != 1 || d.Members[0].Pos == nil || d.Start == nil || d.Start.X != 4711.5 {
		t.Errorf("the unit-facing poll lacks the roster or the start: %+v", d)
	}
}

var opHdr = map[string]string{"X-Crew-Request": "1"}

func TestStartRouteIsOperatorGatedAndChecked(t *testing.T) {
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
		{"no operator header", "alpha", body, nil, http.StatusForbidden},
		{"unit token alone is not enough", "alpha", body, map[string]string{"Authorization": "Bearer " + token}, http.StatusForbidden},
		{"unknown squad", "bravo", body, opHdr, http.StatusNotFound},
		{"unknown field", "alpha", `{"pos":{"x":1,"y":2,"z":3},"who":"me"}`, opHdr, http.StatusBadRequest},
		{"empty body", "alpha", `{}`, opHdr, http.StatusBadRequest},
		{"empty pos", "alpha", `{"pos":{}}`, opHdr, http.StatusBadRequest},
		{"partial pos", "alpha", `{"pos":{"x":5}}`, opHdr, http.StatusBadRequest},
		{"off the world", "alpha", `{"pos":{"x":1e9,"y":2,"z":3}}`, opHdr, http.StatusBadRequest},
		{"too big", "alpha", `{"pos":{"x":1,"y":2,"z":3},"pad":"` + strings.Repeat("x", 2<<10) + `"}`, opHdr, http.StatusRequestEntityTooLarge},
		{"a good post", "alpha", body, opHdr, http.StatusNoContent},
	} {
		resp := r.do(t, "POST", "/v1/squads/"+tt.path+"/start", tt.body, tt.hdr)
		if resp.StatusCode != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.want)
		}
	}
	if d := r.ordersSince(t, "alpha", "0"); d.Start == nil || *d.Start != (position{X: 1, Y: 2, Z: 3}) {
		t.Errorf("start %+v after a good post", d.Start)
	}
	r.ops = func() string { return "" }
	if resp := r.do(t, "POST", "/v1/squads/alpha/start", body, opHdr); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed out: %d, want 401", resp.StatusCode)
	}
}

func TestStatusRejectsAPositionOutsideTheWorld(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	for _, pos := range []string{`{"x":1e300,"y":64,"z":0}`, `{"x":0,"y":-1e40,"z":0}`, `{"x":0,"y":64,"z":9e9}`} {
		resp := r.do(t, "POST", "/v1/squads/alpha/status", `{"unit":"crew-18","hp":20,"food":10,"weapon":"bow","pos":`+pos+`}`, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status with %s: %d, want 400", pos, resp.StatusCode)
		}
	}
	if resp := r.do(t, "POST", "/v1/squads/alpha/status", `{"unit":"crew-18","hp":20,"food":10,"weapon":"bow","pos":{"x":1,"y":64,"z":2}}`, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("a good status: %d", resp.StatusCode)
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
