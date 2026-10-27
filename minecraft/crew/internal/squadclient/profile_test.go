package squadclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

// Two whole profiles as the trainer would send them: a champion with one
// family override, and its successor.
const (
	profileA = `{"version":1,"name":"champ-4","default":{"focusRange":12,"healBelow":14,"healSafe":3,"pressRange":1.6,"regroupWithin":3,"kiteBackoff":5,"shotEvery":30,"arrowNear":3,"lineWidth":1.2,"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12},"overrides":{"creeper":{"kiteBackoff":9}}}`
	profileB = `{"version":1,"name":"champ-5","parent":"champ-4","default":{"focusRange":13,"healBelow":14,"healSafe":3,"pressRange":1.6,"regroupWithin":3,"kiteBackoff":6,"shotEvery":30,"arrowNear":3,"lineWidth":1.2,"orbitMin":1.5,"critWindow":12,"meleeShieldTicks":12}}`
)

// profileRoute is the stub of the profile route: one set body, or a set
// failure status, and a count of the reads.
func (r *rig) profileRoute(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	body, status := r.profileJSON, r.profileStatus
	r.profileFetches++
	r.mu.Unlock()
	switch {
	case status != 0:
		w.WriteHeader(status)
	case body == nil:
		http.NotFound(w, req)
	default:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func (r *rig) setProfile(body []byte) {
	r.mu.Lock()
	r.profileJSON = body
	r.mu.Unlock()
}

func (r *rig) setProfileStatus(status int) {
	r.mu.Lock()
	r.profileStatus = status
	r.mu.Unlock()
}

func (r *rig) profileReads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.profileFetches
}

func TestFetchProfileServesTheSquadAndCaches(t *testing.T) {
	ri, srv := newRigServer(t)
	ri.setProfile([]byte(profileA))
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())

	pr, ok := c.FetchProfile(context.Background())
	if !ok {
		t.Fatal("a served profile did not reach the unit")
	}
	if pr.Version != tactics.Version || pr.Name != "champ-4" {
		t.Errorf("profile = %q v%d, want champ-4 v%d", pr.Name, pr.Version, tactics.Version)
	}
	if got := pr.For(tactics.FamilyCreeper).KiteBackoff; got != 9 {
		t.Errorf("creeper kiteBackoff = %v, want the override's 9", got)
	}
	if got := pr.For(tactics.FamilyZombie).KiteBackoff; got != 5 {
		t.Errorf("zombie kiteBackoff = %v, want the default's 5", got)
	}

	// One fetch serves every ask inside the interval.
	if _, ok := c.FetchProfile(context.Background()); !ok {
		t.Error("the second ask lost the profile")
	}
	if n := ri.profileReads(); n != 1 {
		t.Errorf("the cached asks cost %d reads, want 1", n)
	}
}

// A 404 is an answer: the squad serves no profile and the unit fights on
// the shipped defaults. The learned miss is cached like a hit, so an
// engine asking every tick costs one read per interval.
func TestFetchProfileTakesTheMissAsDefaultsAndPacesItsReads(t *testing.T) {
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	c.profileEvery = time.Hour

	for i := 0; i < 4; i++ {
		if pr, ok := c.FetchProfile(context.Background()); ok {
			t.Fatalf("ask %d served %+v, want the defaults", i, pr.Name)
		}
	}
	if n := ri.profileReads(); n != 1 {
		t.Fatalf("the learned miss cost %d reads, want 1", n)
	}

	// The miss does not freeze: a profile that arrives is read on the
	// next interval, without the caller doing anything but asking.
	ri.setProfile([]byte(profileA))
	c.profileEvery = time.Millisecond
	time.Sleep(2 * time.Millisecond)
	pr, ok := c.FetchProfile(context.Background())
	if !ok || pr.Name != "champ-4" {
		t.Errorf("the arrived profile did not serve: %+v %v", pr, ok)
	}
}

// The poll carries the profile revision, so a profile the trainer stores
// is refetched at once — the interval does not get to delay it.
func TestFetchProfileRefetchesWhenTheStreamMoves(t *testing.T) {
	ri, srv := newRigServer(t)
	ri.setProfile([]byte(profileA))
	c := newTestClient(t, srv.URL, "crew-18", newRecLog())
	c.profileEvery = time.Hour
	runClient(t, c)

	if pr, ok := c.FetchProfile(context.Background()); !ok || pr.Name != "champ-4" {
		t.Fatalf("the first ask served %+v %v", pr, ok)
	}
	ri.setProfile([]byte(profileB))
	ri.setAnswer(&pollAnswer{Cursor: 1, ProfileSeq: 1})
	waitFor(t, func() bool {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.profileSeq == 1
	}, "the poll to carry the new profile revision")

	pr, ok := c.FetchProfile(context.Background())
	if !ok || pr.Name != "champ-5" {
		t.Errorf("the moved revision served %+v %v, want champ-5", pr, ok)
	}
	if n := ri.profileReads(); n != 2 {
		t.Errorf("the refetch cost %d reads, want 2", n)
	}
}

// While the server flaps, the last good profile serves inside its stale
// window and no longer: a long outage must not freeze a unit's tuning
// for ever, and a short one must not flip it mid-fight.
func TestFetchProfileServesTheLastGoodOneWhileTheServerFlaps(t *testing.T) {
	ri, srv := newRigServer(t)
	ri.setProfile([]byte(profileA))
	log := newRecLog()
	c := newTestClient(t, srv.URL, "crew-18", log)
	c.profileEvery = 5 * time.Millisecond
	c.profileStale = 40 * time.Millisecond

	if _, ok := c.FetchProfile(context.Background()); !ok {
		t.Fatal("the healthy ask did not serve")
	}
	ri.setProfileStatus(http.StatusInternalServerError)
	pr, ok := c.FetchProfile(context.Background())
	if !ok || pr.Name != "champ-4" {
		t.Errorf("inside the stale window the unit got %+v %v, want the last good profile", pr, ok)
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.FetchProfile(context.Background()); ok {
		t.Error("a profile outlived its stale window while the server was down")
	}
	waitFor(t, func() bool { return log.n("squad_profile_failed") > 0 }, "the fetch failure logged")
}

// The load is validated by the contract: the unit does not trust the
// server to have checked, because the server cannot even name the
// fields. A version the engine cannot read is an error; a value outside
// its bounds is clamped where the bounds live.
func TestFetchProfileValidatesWhatItLoads(t *testing.T) {
	t.Run("a version the engine cannot read", func(t *testing.T) {
		ri, srv := newRigServer(t)
		ri.setProfile([]byte(`{"version":99,"name":"future","default":{}}`))
		log := newRecLog()
		c := newTestClient(t, srv.URL, "crew-18", log)
		c.profileEvery = time.Millisecond
		if pr, ok := c.FetchProfile(context.Background()); ok {
			t.Errorf("a version-99 profile served %+v", pr.Name)
		}
		waitFor(t, func() bool { return log.n("squad_profile_failed") > 0 }, "the unreadable profile logged")
	})
	t.Run("a value outside its bounds is clamped on load", func(t *testing.T) {
		ri, srv := newRigServer(t)
		ri.setProfile([]byte(`{"version":1,"name":"wide","default":{"healSafe":500}}`))
		c := newTestClient(t, srv.URL, "crew-18", newRecLog())
		pr, ok := c.FetchProfile(context.Background())
		if !ok || pr.Default.HealSafe != 8 {
			t.Errorf("healSafe = %v %v, want the clamped 8", pr.Default.HealSafe, ok)
		}
	})
}

// e2eUnits is the roster the real routes check hellos against.
type e2eUnits map[string]bool

func (u e2eUnits) Has(id string) bool { return u[id] }

// The two ends meet through the real routes crew-control serves: a
// profile PUT stores what the contract wrote, and the unit's FetchProfile
// reads it back as a tactics.Profile — the server's mirrored shape
// against the contract's own JSON. A replaced profile reaches the unit
// without it waiting an interval: the PUT wakes the parked poll, the
// answer carries the new revision, and the next ask refetches.
func TestProfileEndsMeetThroughTheRealRoutes(t *testing.T) {
	store := squad.NewStore()
	mux := http.NewServeMux()
	squad.Routes(mux, store, e2eUnits{"crew-18": true}, "wt-e2e")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := New(base, "crew-18", "lab-alpha", "", squadmsg.RoleStriker, "wt-e2e", newRecLog())
	c.sleep = fastSleep
	c.helloEvery = 20 * time.Millisecond
	c.statusEvery = 5 * time.Millisecond
	runClient(t, c)

	waitFor(t, func() bool { return store.Has("lab-alpha") }, "the hello to build the squad")
	put := func(t *testing.T, body string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, srv.URL+"/v1/squads/lab-alpha/profile", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer wt-e2e")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := put(t, profileA); got != http.StatusNoContent {
		t.Fatalf("the first PUT answered %d", got)
	}
	pr, ok := c.FetchProfile(context.Background())
	if !ok || pr.Name != "champ-4" {
		t.Fatalf("through the real routes the unit got %+v %v", pr, ok)
	}
	if got := pr.For(tactics.FamilyCreeper).KiteBackoff; got != 9 {
		t.Errorf("the family override did not survive the server: kiteBackoff %v", got)
	}

	if got := put(t, profileB); got != http.StatusNoContent {
		t.Fatalf("the replacing PUT answered %d", got)
	}
	waitFor(t, func() bool {
		pr, ok := c.FetchProfile(context.Background())
		return ok && pr.Name == "champ-5"
	}, "the replaced profile to reach the unit")
}
