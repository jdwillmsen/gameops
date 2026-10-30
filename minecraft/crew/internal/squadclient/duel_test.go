package squadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

const (
	duelGoBody   = `{"state":"go","round":"r1","fighters":["Rusty"],"enemies":["Slate"],"eliminated":["Slate"]}`
	duelOverBody = `{"state":"over","round":"r1"}`
)

// duelRoute stubs the duel route: a set body, a set failure status, and a
// count of the reads.
type duelRoute struct {
	mu     sync.Mutex
	body   string
	status int
	reads  int
}

func (d *duelRoute) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	d.mu.Lock()
	body, status := d.body, d.status
	d.reads++
	d.mu.Unlock()
	switch {
	case status != 0:
		w.WriteHeader(status)
	case body == "":
		http.NotFound(w, req)
	default:
		_, _ = w.Write([]byte(body))
	}
}

func (d *duelRoute) set(body string, status int) {
	d.mu.Lock()
	d.body, d.status = body, status
	d.mu.Unlock()
}

func (d *duelRoute) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reads
}

func newDuelRig(t *testing.T) (*rig, *duelRoute, *httptest.Server) {
	t.Helper()
	ri, dr := &rig{}, &duelRoute{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/squads/{id}/hello", ri.recorder("hello"))
	mux.HandleFunc("POST /v1/squads/{id}/status", ri.recorder("status"))
	mux.HandleFunc("GET /v1/squads/{id}/orders", ri.ordersRoute)
	mux.Handle("GET /v1/squads/{id}/duel", dr)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return ri, dr, srv
}

func TestFetchDuelServesTheSquadAndCaches(t *testing.T) {
	_, dr, srv := newDuelRig(t)
	dr.set(duelGoBody, 0)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())

	d, ok := c.FetchDuel(context.Background())
	if !ok || d.State != DuelGo || d.Round != "r1" || len(d.Fighters) != 1 || d.Enemies[0] != "Slate" || d.Eliminated[0] != "Slate" {
		t.Fatalf("the duel read as %+v %v", d, ok)
	}
	for i := 0; i < 3; i++ {
		if _, ok := c.FetchDuel(context.Background()); !ok {
			t.Fatal("a cached ask lost the duel")
		}
	}
	if n := dr.count(); n != 1 {
		t.Errorf("the cached asks cost %d reads, want 1", n)
	}
}

func TestFetchDuelTakesTheMissAsNoFightAndPacesReads(t *testing.T) {
	_, dr, srv := newDuelRig(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	c.duelEvery = time.Hour
	for i := 0; i < 4; i++ {
		if d, ok := c.FetchDuel(context.Background()); ok {
			t.Fatalf("ask %d served %+v, want no duel", i, d)
		}
	}
	if n := dr.count(); n != 1 {
		t.Fatalf("the learned miss cost %d reads, want 1", n)
	}
	dr.set(duelGoBody, 0)
	c.duelEvery = time.Millisecond
	time.Sleep(2 * time.Millisecond)
	if d, ok := c.FetchDuel(context.Background()); !ok || d.State != DuelGo {
		t.Errorf("the arrived duel did not serve: %+v %v", d, ok)
	}
}

// A failed read is no answer, and the last good one does not stand in for
// it: a unit that cannot hear the squad must stop hunting.
func TestFetchDuelFailsClosedWithoutAStaleWindow(t *testing.T) {
	_, dr, srv := newDuelRig(t)
	dr.set(duelGoBody, 0)
	log := newRecLog()
	c := newTestClient(t, srv.URL, "crew-18", log)
	c.duelEvery = 5 * time.Millisecond

	if _, ok := c.FetchDuel(context.Background()); !ok {
		t.Fatal("the healthy ask did not serve")
	}
	dr.set("", http.StatusInternalServerError)
	time.Sleep(8 * time.Millisecond)
	if d, ok := c.FetchDuel(context.Background()); ok {
		t.Errorf("a failed read served %+v", d)
	}
	waitFor(t, func() bool { return log.n("squad_duel_failed") > 0 }, "the fetch failure logged")

	// Inside the interval the failure is not retried.
	before := dr.count()
	if _, ok := c.FetchDuel(context.Background()); ok {
		t.Error("a retry inside the interval served a duel")
	}
	if dr.count() != before {
		t.Error("a failed read was retried inside the interval")
	}

	dr.set("not json", 0)
	time.Sleep(8 * time.Millisecond)
	if _, ok := c.FetchDuel(context.Background()); ok {
		t.Error("an unreadable body served a duel")
	}
}

func TestFetchDuelRefetchesWhenTheStreamMoves(t *testing.T) {
	ri, dr, srv := newDuelRig(t)
	dr.set(duelGoBody, 0)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	c.duelEvery = time.Hour
	runClient(t, c)

	if d, ok := c.FetchDuel(context.Background()); !ok || d.State != DuelGo {
		t.Fatalf("the first ask served %+v %v", d, ok)
	}
	dr.set(duelOverBody, 0)
	ri.setAnswer(&pollAnswer{Cursor: 1, DuelSeq: 1})
	waitFor(t, func() bool { return c.streamDuelSeq() == 1 }, "the poll to carry the new duel revision")
	if d, ok := c.FetchDuel(context.Background()); !ok || d.State != DuelOver {
		t.Errorf("the moved revision served %+v %v, want over", d, ok)
	}
}

// The two ends meet through the real routes: a PUT stores the duel and
// wakes the parked poll, and the unit's next ask reads it with no wait.
func TestDuelEndsMeetThroughTheRealRoutes(t *testing.T) {
	store := squad.NewStore()
	mux := http.NewServeMux()
	squad.Routes(mux, store, namedUnits{"crew-18": "Rusty", "crew-19": "Slate"}, "wt-e2e")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := New(base, "crew-18", "lab-red", "", squadmsg.RoleStriker, "wt-e2e", newRecLog())
	c.sleep = fastSleep
	c.helloEvery = 20 * time.Millisecond
	c.statusEvery = 5 * time.Millisecond
	c.duelEvery = time.Hour
	runClient(t, c)
	// The duel's enemy has to be a member of a lab squad too.
	foe := New(base, "crew-19", "lab-blue", "", squadmsg.RoleStriker, "wt-e2e", newRecLog())
	foe.sleep = fastSleep
	foe.helloEvery = 20 * time.Millisecond
	foe.statusEvery = 5 * time.Millisecond
	runClient(t, foe)
	waitFor(t, func() bool { return store.Has("lab-red") && store.Has("lab-blue") }, "the hellos to build the squads")

	put := func(body string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/squads/lab-red/duel", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer wt-e2e")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("PUT answered %d", resp.StatusCode)
		}
	}
	if _, ok := c.FetchDuel(context.Background()); ok {
		t.Fatal("a squad nobody started a duel on served one")
	}
	put(duelGoBody)
	waitFor(t, func() bool {
		d, ok := c.FetchDuel(context.Background())
		return ok && d.State == DuelGo && d.Enemies[0] == "Slate"
	}, "the duel to reach the unit")
	put(duelOverBody)
	waitFor(t, func() bool {
		d, ok := c.FetchDuel(context.Background())
		return ok && d.State == DuelOver
	}, "the end of the duel to reach the unit")
}

// namedUnits is a roster that knows each unit's in-game name, as the console's
// does; the squad service checks a duel's names against it.
type namedUnits map[string]string

func (u namedUnits) Has(id string) bool { _, ok := u[id]; return ok }

func (u namedUnits) NameOf(id string) (string, bool) { n, ok := u[id]; return n, ok }
