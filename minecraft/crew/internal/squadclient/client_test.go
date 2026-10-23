package squadclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
)

// The unit side is the mirror of crew-control's squad routes: the
// server's own tests lock its shapes, and these speak them from the
// client's side. The packages cannot import each other's internals, so
// the rig replays the routes and the client must match them byte for
// byte.
const workToken = "wt-squad-test"

// fastSleep keeps the loops at speed without a real wait: only a closed
// context still stops them, as in production.
func fastSleep(ctx context.Context, _ time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

// recLog counts events, so tests can assert a drop or a recovery was seen.
type recLog struct {
	mu     sync.Mutex
	events map[string]int
}

func newRecLog() *recLog { return &recLog{events: map[string]int{}} }

func (l *recLog) Info(event string, _ map[string]any) {
	l.mu.Lock()
	l.events[event]++
	l.mu.Unlock()
}

func (l *recLog) Error(event string, _ map[string]any) { l.Info(event, nil) }

func (l *recLog) n(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.events[event]
}

// rig is a stub of crew-control's squad routes. The orders route serves
// one crafted answer once, then goes quiet.
type rig struct {
	mu sync.Mutex

	hellos   []helloBody
	statuses []statusBody
	callouts []calloutBody

	answer *pollAnswer

	// failOrders, when set, makes the orders route a server error, so
	// listen runs its backoff path.
	failOrders   bool
	failCallouts int

	// The last poll's read point, as the client sent it.
	lastCursor int64
	lastRev    int64
}

func newRigServer(t *testing.T) (*rig, *httptest.Server) {
	t.Helper()
	ri := &rig{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/squads/{id}/hello", ri.recorder("hello"))
	mux.HandleFunc("POST /v1/squads/{id}/status", ri.recorder("status"))
	mux.HandleFunc("POST /v1/squads/{id}/callout", ri.recorder("callout"))
	mux.HandleFunc("GET /v1/squads/{id}/orders", ri.ordersRoute)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return ri, srv
}

func (r *rig) recorder(leaf string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch leaf {
		case "hello":
			var h helloBody
			_ = json.NewDecoder(req.Body).Decode(&h)
			r.hellos = append(r.hellos, h)
		case "status":
			var s statusBody
			_ = json.NewDecoder(req.Body).Decode(&s)
			r.statuses = append(r.statuses, s)
		case "callout":
			if r.failCallouts > 0 {
				r.failCallouts--
				http.Error(w, "flapping", http.StatusInternalServerError)
				return
			}
			var k calloutBody
			_ = json.NewDecoder(req.Body).Decode(&k)
			r.callouts = append(r.callouts, k)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (r *rig) setFailOrders(v bool) {
	r.mu.Lock()
	r.failOrders = v
	r.mu.Unlock()
}

func (r *rig) setAnswer(a *pollAnswer) {
	r.mu.Lock()
	r.answer = a
	r.mu.Unlock()
}

// lastQuery returns the cursor and revision the most recent poll carried.
func (r *rig) lastQuery() (int64, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastCursor, r.lastRev
}

func (r *rig) ordersRoute(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	fail := r.failOrders
	a := r.answer
	r.answer = nil
	cursor, _ := strconv.ParseInt(req.URL.Query().Get("since"), 10, 64)
	rev, _ := strconv.ParseInt(req.URL.Query().Get("rev"), 10, 64)
	r.lastCursor, r.lastRev = cursor, rev
	r.mu.Unlock()
	if fail {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	if a == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

func (r *rig) callsPosted() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.callouts)
}

func newTestClient(t *testing.T, srvURL, unit string, log Logger) *Client {
	t.Helper()
	base, err := url.Parse(srvURL)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	c := New(base, unit, "warriors", "Anvil", squadmsg.RoleStriker, workToken, log)
	c.sleep = fastSleep
	c.helloEvery = 20 * time.Millisecond
	c.statusEvery = 5 * time.Millisecond
	return c
}

// waitFor runs want until it holds or the test budget runs out.
func waitFor(t *testing.T, want func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if want() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s never happened", what)
}

// setled runs want until it holds, then keeps checking that it still
// holds, so a one-off cannot fake stability.
func settled(t *testing.T, want func() bool, what string) {
	t.Helper()
	waitFor(t, want, what)
	deadline := time.Now().Add(30 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
		if !want() {
			want()
		}
	}
}

func waitReturned(t *testing.T, done chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// runClient starts Run and returns its finished signal, so a test can
// confirm Run exits with a cancel.
func runClient(t *testing.T, c *Client) chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	t.Cleanup(cancel)
	return done
}

func TestRunJoinsAndReports(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	c.SetSource(func() squadmsg.Status {
		return squadmsg.Status{Unit: "crew-18", HP: 12.5, Food: 10,
			Weapon: "iron_sword", Pos: squadmsg.Pos{X: 1, Y: 64, Z: 2}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	t.Cleanup(cancel)
	waitFor(t, func() bool {
		ri.mu.Lock()
		defer ri.mu.Unlock()
		return len(ri.hellos) > 0 && len(ri.statuses) > 0
	}, "hello and status")

	ri.mu.Lock()
	h := ri.hellos[0]
	s := ri.statuses[len(ri.statuses)-1]
	ri.mu.Unlock()
	if h.Unit != "crew-18" || h.Role != "striker" || h.SquadName != "Anvil" {
		t.Fatalf("hello = %+v", h)
	}
	if s.Unit != "crew-18" || s.HP != 12.5 || s.Food != 10 ||
		s.Weapon != "iron_sword" || s.Pos != (squadmsg.Pos{X: 1, Y: 64, Z: 2}) {
		t.Fatalf("status = %+v", s)
	}

	cancel()
	waitReturned(t, done, "Run to exit cleanly")
}

func TestRunAppliesLeaderAndStandingOrder(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	pos := squadmsg.Pos{X: 3, Y: 70, Z: -4}
	ri.setAnswer(&pollAnswer{
		Cursor: 7,
		Leader: &wireLeader{Unit: "crew-07", Pos: &pos},
		Order: &squadmsg.Order{Kind: squadmsg.OrderFocus, Target: "zombie-2",
			By: "arn", Seq: 2},
	})
	runClient(t, c)
	waitFor(t, func() bool { return c.Orders().Kind == squadmsg.OrderFocus }, "standing order")
	waitFor(t, func() bool {
		_, _, ok := c.Leader()
		return ok
	}, "leader")

	id, got := checkLeader(t, c)
	if id != "crew-07" || got != (mgl32.Vec3{3, 70, -4}) {
		t.Fatalf("leader = %q %v", id, got)
	}
	o := c.Orders()
	if o.Target != "zombie-2" || o.By != "arn" || o.Seq != 2 {
		t.Fatalf("order = %+v", o)
	}

	// A leader named without a position is not one to walk toward.
	ri.setAnswer(&pollAnswer{Cursor: 8, Leader: &wireLeader{Unit: "crew-07"}})
	waitFor(t, func() bool {
		_, _, ok := c.Leader()
		return !ok
	}, "positionless leader to unseat the readable one")
}

func checkLeader(t *testing.T, c *Client) (string, mgl32.Vec3) {
	t.Helper()
	id, pos, ok := c.Leader()
	if !ok {
		t.Fatalf("leader not known")
	}
	return id, pos
}

func TestRunDrainsCallouts(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	runClient(t, c)

	// The from line is the client's own unit; the kind goes through as
	// the engine named it.
	c.Callout(squadmsg.Callout{Kind: squadmsg.CalloutLowHealth})
	waitFor(t, func() bool { return ri.callsPosted() == 1 }, "callout")
	ri.mu.Lock()
	got := ri.callouts[0]
	ri.mu.Unlock()
	if got.Unit != "crew-18" || got.Kind != "low_health" {
		t.Fatalf("callout = %+v", got)
	}

	// An old call-out is stale news: it is never posted, and a quiet
	// while later the count is still the same.
	old := time.Now().Add(-maxCalloutAge - time.Second)
	before := ri.callsPosted()
	c.Callout(squadmsg.Callout{Kind: squadmsg.CalloutTargetDown, At: old})
	waitFor(t, func() bool { return ri.callsPosted() == before }, "stale callout to stay unposted")
	settled(t, func() bool { return ri.callsPosted() == before }, "stale callout to stay unposted")
}

func TestCalloutRetriesWhileServerFlaps(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	ri.mu.Lock()
	ri.failCallouts = 2
	ri.mu.Unlock()
	runClient(t, c)

	c.Callout(squadmsg.Callout{Kind: squadmsg.CalloutNeedHeal})
	waitFor(t, func() bool { return ri.callsPosted() == 1 }, "callout to survive two failures")
}

func TestListenRecoversAfterServerFailure(t *testing.T) {
	pos3 := squadmsg.Pos{X: 5, Y: 66, Z: 7}
	ri, srv := newRigServer(t)
	log := newRecLog()
	c := newTestClient(t, srv.URL, "crew-18", log)
	ri.setFailOrders(true)
	runClient(t, c)
	// The long poll fails; listen backs off and says so, instead of
	// winding down or hammering.
	waitFor(t, func() bool { return log.n("squad_poll_down") > 0 }, "poll_down logged")

	ri.setAnswer(&pollAnswer{Cursor: 2, Leader: &wireLeader{Unit: "crew-07",
		Pos: &pos3}})
	ri.setFailOrders(false)
	waitFor(t, func() bool {
		id, _, ok := c.Leader()
		return ok && id == "crew-07"
	}, "listen to recover and read the leader")
}

// The client echoes the leader-line revision it last saw, and an answer
// with a newer revision — the leader moved, with no message to carry it —
// is read at once instead of waiting for some future order.
func TestPollEchoesRevAndReadsLeaderMoves(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	pos1 := squadmsg.Pos{X: 3, Y: 70, Z: -4}
	ri.setAnswer(&pollAnswer{Cursor: 7, Rev: 3, Leader: &wireLeader{Unit: "crew-07", Pos: &pos1}})
	runClient(t, c)
	waitFor(t, func() bool {
		_, _, ok := c.Leader()
		return ok
	}, "first answer applied")
	// The finished read point rides along on the next poll.
	waitFor(t, func() bool {
		cursor, rev := ri.lastQuery()
		return cursor == 7 && rev == 3
	}, "cursor and revision echoed")

	// The leader moves and nothing else changes: a newer revision alone
	// must reach the unit.
	pos2 := squadmsg.Pos{X: 9, Y: 70, Z: -4}
	ri.setAnswer(&pollAnswer{Rev: 4, Leader: &wireLeader{Unit: "crew-07", Pos: &pos2}})
	waitFor(t, func() bool {
		_, pos, ok := c.Leader()
		return ok && pos == (mgl32.Vec3{9, 70, -4})
	}, "the leader's move to reach the client")
	_, rev := ri.lastQuery()
	if rev != 4 {
		t.Errorf("after the move the client echoes rev %d, want 4", rev)
	}
}

// After a restart of crew-control the squad's seq starts over at 0 while
// the unit still holds a higher cursor: the server answers past-the-log
// polls with the full log, and the client must step its cursor back down,
// or it polls past everything for ever and hears no order again.
func TestCursorResetsWhenTheLogRestarts(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	ri.setAnswer(&pollAnswer{Cursor: 5,
		Order: &squadmsg.Order{Kind: squadmsg.OrderHold, Seq: 5, By: "owner"}})
	runClient(t, c)
	waitFor(t, func() bool { return c.Orders().Kind == squadmsg.OrderHold }, "the pre-restart order")
	waitFor(t, func() bool {
		cursor, _ := ri.lastQuery()
		return cursor == 5
	}, "the unit to poll past the restarted log")

	// The restart: a lower seq with the new standing order and a delta
	// behind it.
	ri.setAnswer(&pollAnswer{Cursor: 2,
		Order:  &squadmsg.Order{Kind: squadmsg.OrderFocus, Seq: 2, By: "owner"},
		Orders: []squadmsg.Order{{Kind: squadmsg.OrderRegroup, Seq: 1, By: "owner"}}})
	waitFor(t, func() bool { return c.Orders().Kind == squadmsg.OrderFocus }, "the restarted log's order")
	waitFor(t, func() bool {
		cursor, _ := ri.lastQuery()
		return cursor == 2
	}, "the cursor to step back down")
}
