package jobs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/ledger"
)

// fakeGate follows the real check order: header, then session.
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

// busyStore reports a job as held by another pass for the first busy Locks.
type busyStore struct {
	Store
	busy  int
	locks atomic.Int32
}

func (b *busyStore) Lock(ctx context.Context, id uuid.UUID, fn func(*Job) error) (bool, error) {
	if int(b.locks.Add(1)) <= b.busy {
		return false, nil
	}
	return b.Store.Lock(ctx, id, fn)
}

type routeRig struct {
	*rig
	led     *ledger.Memory
	srv     *httptest.Server
	changed atomic.Int32
	up      bool
	view    ledger.Ledger // what the API sees instead of led, when set
}

// downLedger is a ledger whose database cannot be reached.
type downLedger struct{ ledger.Ledger }

func (downLedger) Place(context.Context, string) (ledger.Place, bool, error) {
	return ledger.Place{}, false, ledger.ErrUnavailable
}
func (downLedger) Places(context.Context) ([]ledger.Place, error) {
	return nil, ledger.ErrUnavailable
}
func (downLedger) Stock(context.Context, string) ([]ledger.Stock, error) {
	return nil, ledger.ErrUnavailable
}

func newRouteRig(t *testing.T, store func(Store) Store) *routeRig {
	t.Helper()
	rr := &routeRig{rig: newRig(t, "crew-01", "crew-02", "crew-03"), up: true}
	rr.led = world(t, chestPlace("store", 1), chestPlace("outbox-a", 2), chestPlace("outbox-b", 3),
		areaPlace("quarry-1", 20, "cobblestone"), areaPlace("grove-1", 40, "oak_log"))
	if store != nil {
		rr.jobs = store(rr.jobs)
		rr.s = rr.scheduler()
	}
	api := NewAPI(func() (ledger.Ledger, *Scheduler) {
		if !rr.up {
			return nil, nil
		}
		if rr.view != nil {
			return rr.view, rr.s
		}
		return rr.led, rr.s
	}, func() { rr.changed.Add(1) })
	api.retryEvery = time.Millisecond
	mux := http.NewServeMux()
	api.MountOperator(mux, fakeGate{})
	rr.srv = httptest.NewServer(mux)
	t.Cleanup(rr.srv.Close)
	return rr
}

var signed = map[string]string{"X-Test-Op": "owner", "X-Crew-Request": "1"}

func (rr *routeRig) do(method, path, body string, hdr map[string]string) (int, string) {
	rr.t.Helper()
	req, _ := http.NewRequest(method, rr.srv.URL+path, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rr.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type jobJSON struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Spec     json.RawMessage `json:"spec"`
	State    string          `json:"state"`
	Reason   string          `json:"reason"`
	Operator string          `json:"operator"`
	Deadline time.Time       `json:"deadline"`
	Created  time.Time       `json:"created"`
	Steps    []struct {
		Key   string `json:"key"`
		State string `json:"state"`
	} `json:"steps"`
}

func (rr *routeRig) create(body string) jobJSON {
	rr.t.Helper()
	code, out := rr.do("POST", "/v1/jobs", body, signed)
	if code != 202 {
		rr.t.Fatalf("create %s: %d %s", body, code, out)
	}
	var j jobJSON
	if err := json.Unmarshal([]byte(out), &j); err != nil {
		rr.t.Fatal(err)
	}
	return j
}

func stepKeys(j jobJSON) []string {
	var out []string
	for _, s := range j.Steps {
		out = append(out, s.Key)
	}
	return out
}

const planBody = `{"kind":"plan","template":"parallel-demo"}`
const stockBody = `{"kind":"stock","place":"store","want":{"oak_log":32}}`

func TestJobRoutesNeedASession(t *testing.T) {
	rr := newRouteRig(t, nil)
	id := uuid.NewString()
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/jobs", planBody},
		{"GET", "/v1/jobs", ""},
		{"GET", "/v1/jobs/" + id, ""},
		{"POST", "/v1/jobs/" + id + "/cancel", "{}"},
	} {
		if code, _ := rr.do(c.method, c.path, c.body, nil); code != 401 && code != 403 {
			t.Errorf("%s %s anonymous: %d", c.method, c.path, code)
		}
		if c.method == "GET" {
			if code, _ := rr.do(c.method, c.path, "", nil); code != 401 {
				t.Errorf("%s %s anonymous: %d", c.method, c.path, code)
			}
			continue
		}
		if code, _ := rr.do(c.method, c.path, c.body, map[string]string{"X-Test-Op": "owner"}); code != 403 {
			t.Errorf("%s %s without header: %d", c.method, c.path, code)
		}
		if code, _ := rr.do(c.method, c.path, c.body, map[string]string{"X-Crew-Request": "1"}); code != 401 {
			t.Errorf("%s %s without session: %d", c.method, c.path, code)
		}
	}
	if got := rr.changed.Load(); got != 0 {
		t.Fatalf("rejected requests published %d changes", got)
	}
}

func TestCreatePlanJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	j := rr.create(planBody)
	if j.Kind != KindPlan || j.State != JobRunning || j.Operator != "owner" || len(j.Steps) != 4 {
		t.Fatalf("job %+v", j)
	}
	if rr.changed.Load() != 1 {
		t.Fatalf("changed %d times", rr.changed.Load())
	}
	code, body := rr.do("GET", "/v1/jobs/"+j.ID, "", signed)
	var got jobJSON
	if code != 200 || json.Unmarshal([]byte(body), &got) != nil || got.ID != j.ID {
		t.Fatalf("get: %d %s", code, body)
	}
}

func TestCreatePlanJobRejectsAnythingElse(t *testing.T) {
	rr := newRouteRig(t, nil)
	for _, body := range []string{
		`{"kind":"plan","template":"other"}`,
		`{"kind":"plan"}`,
		`{"kind":"plan","template":"parallel-demo","place":"store"}`,
		`{"kind":"nope"}`,
		`{"kind":"plan","template":"parallel-demo","extra":1}`,
		`{"kind":"plan","template":"parallel-demo"} {}`,
		`nope`,
		``,
	} {
		if code, _ := rr.do("POST", "/v1/jobs", body, signed); code != 400 {
			t.Errorf("%q: %d", body, code)
		}
	}
	if jobs, _ := rr.jobs.List(ctx, 10); len(jobs) != 0 {
		t.Fatalf("stored %d rejected jobs", len(jobs))
	}
}

func TestCreatePlanJobNamesAMissingPlace(t *testing.T) {
	rr := newRouteRig(t, nil)
	if err := rr.led.DeletePlace(ctx, "grove-1"); err != nil {
		t.Fatal(err)
	}
	code, body := rr.do("POST", "/v1/jobs", planBody, signed)
	if code != 422 || strings.TrimSpace(body) != `{"error":"place grove-1 not found"}` {
		t.Fatalf("%d %s", code, body)
	}
	if jobs, _ := rr.jobs.List(ctx, 10); len(jobs) != 0 {
		t.Fatal("stored a job it could not plan")
	}
}

func TestCreateStockJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	j := rr.create(stockBody)
	if j.Kind != KindStock || j.State != JobRunning {
		t.Fatalf("job %+v", j)
	}
	var spec StockSpec
	if err := json.Unmarshal(j.Spec, &spec); err != nil || spec.Place != "store" || spec.Want["oak_log"] != 32 {
		t.Fatalf("spec %s", j.Spec)
	}
	if d := j.Deadline.Sub(j.Created); d < 29*time.Minute || d > 31*time.Minute {
		t.Fatalf("deadline %s after creation", d)
	}
	keys := stepKeys(j)
	if keys[0] != "inspect-target" || keys[len(keys)-1] != "check" {
		t.Fatalf("steps %v", keys)
	}
}

func TestStockJobCountsStockSeenWithinTenMinutes(t *testing.T) {
	rr := newRouteRig(t, nil)
	store, _, _ := rr.led.Place(ctx, "store")
	observe(t, rr.led, store, 5*time.Minute, map[string]int{"oak_log": 32})
	if keys := stepKeys(rr.create(stockBody)); len(keys) != 1 || keys[0] != "check" {
		t.Fatalf("fresh stock should leave only the check: %v", keys)
	}
	rr = newRouteRig(t, nil)
	observe(t, rr.led, store, 11*time.Minute, map[string]int{"oak_log": 32})
	if keys := stepKeys(rr.create(stockBody)); keys[0] != "inspect-target" {
		t.Fatalf("stale stock should be inspected first: %v", keys)
	}
}

func TestCreateStockJobRejections(t *testing.T) {
	rr := newRouteRig(t, nil)
	for body, want := range map[string]struct {
		code int
		msg  string
	}{
		`{"kind":"stock","place":"store","want":{"iron_ingot":4}}`:             {422, `{"error":"no source for iron_ingot"}`},
		`{"kind":"stock","place":"nowhere","want":{"oak_log":4}}`:              {422, `{"error":"place nowhere not found"}`},
		`{"kind":"stock","place":"quarry-1","want":{"oak_log":4}}`:             {422, `{"error":"place quarry-1 is not a chest"}`},
		`{"kind":"stock","place":"store","want":{}}`:                           {400, ""},
		`{"kind":"stock","place":"store","want":{"oak_log":0}}`:                {400, ""},
		`{"kind":"stock","place":"store","want":{"oak_log":9999}}`:             {400, ""},
		`{"kind":"stock","want":{"oak_log":4}}`:                                {400, ""},
		`{"kind":"stock","place":"store","want":{"oak_log":4},"template":"x"}`: {400, ""},
	} {
		code, out := rr.do("POST", "/v1/jobs", body, signed)
		if code != want.code || (want.msg != "" && strings.TrimSpace(out) != want.msg) {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if jobs, _ := rr.jobs.List(ctx, 10); len(jobs) != 0 {
		t.Fatalf("stored %d rejected jobs", len(jobs))
	}
}

func TestCreateCraftJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	j := rr.create(`{"kind":"craft","place":"store","want":{"stick":4}}`)
	if j.Kind != KindCraft || j.State != JobRunning {
		t.Fatalf("job %+v", j)
	}
	var spec CraftSpec
	if err := json.Unmarshal(j.Spec, &spec); err != nil || spec.Place != "store" || spec.Want["stick"] != 4 {
		t.Fatalf("spec %s", j.Spec)
	}
	// Nothing stocked and the grove next door: the plan starts by felling.
	if keys := stepKeys(j); strings.Join(keys, ",") != "gather-oak_log-1,withdraw-store,craft-oak_planks,craft-stick,deposit,check" {
		t.Fatalf("steps %v", keys)
	}
}

func TestCreateCraftJobRejections(t *testing.T) {
	rr := newRouteRig(t, nil)
	for body, want := range map[string]struct {
		code int
		msg  string
	}{
		`{"kind":"craft","place":"store","want":{"diamond":1}}`:              {422, `{"error":"no recipe for diamond"}`},
		`{"kind":"craft","place":"store","want":{"chest":1}}`:                {422, `{"error":"needs a crafting table"}`},
		`{"kind":"craft","place":"nowhere","want":{"stick":4}}`:              {422, `{"error":"place nowhere not found"}`},
		`{"kind":"craft","place":"quarry-1","want":{"stick":4}}`:             {422, `{"error":"place quarry-1 is not a chest"}`},
		`{"kind":"craft","place":"store","want":{"stick":4,"oak_planks":8}}`: {400, ""},
		`{"kind":"craft","place":"store","want":{}}`:                         {400, ""},
		`{"kind":"craft","place":"store","want":{"stick":0}}`:                {400, ""},
		`{"kind":"craft","place":"store","want":{"stick":65}}`:               {400, ""},
		`{"kind":"craft","want":{"stick":4}}`:                                {400, ""},
		`{"kind":"craft","place":"store","want":{"stick":4},"template":"x"}`: {400, ""},
	} {
		code, out := rr.do("POST", "/v1/jobs", body, signed)
		if code != want.code || (want.msg != "" && strings.TrimSpace(out) != want.msg) {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}
	if jobs, _ := rr.jobs.List(ctx, 10); len(jobs) != 0 {
		t.Fatalf("stored %d rejected jobs", len(jobs))
	}
}

func TestListShowsTheNewestFifty(t *testing.T) {
	rr := newRouteRig(t, nil)
	for i := 0; i < 52; i++ {
		rr.create(planBody)
	}
	code, body := rr.do("GET", "/v1/jobs", "", signed)
	var list []jobJSON
	if code != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list) != 50 {
		t.Fatalf("%d, %d jobs", code, len(list))
	}
}

func TestGetUnknownJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		if code, _ := rr.do("GET", "/v1/jobs/"+id, "", signed); code != 404 {
			t.Errorf("get %s: %d", id, code)
		}
		if code, _ := rr.do("POST", "/v1/jobs/"+id+"/cancel", "{}", signed); code != 404 {
			t.Errorf("cancel %s: %d", id, code)
		}
	}
}

func TestCancelJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	j := rr.create(planBody)
	code, body := rr.do("POST", "/v1/jobs/"+j.ID+"/cancel", "{}", signed)
	var got jobJSON
	if code != 200 || json.Unmarshal([]byte(body), &got) != nil || got.State != JobCancelled {
		t.Fatalf("cancel: %d %s", code, body)
	}
	code, again := rr.do("POST", "/v1/jobs/"+j.ID+"/cancel", "{}", map[string]string{"X-Test-Op": "someone-else", "X-Crew-Request": "1"})
	if code != 200 {
		t.Fatalf("cancel twice: %d", code)
	}
	var second jobJSON
	_ = json.Unmarshal([]byte(again), &second)
	if second.State != JobCancelled || second.Reason != got.Reason {
		t.Fatalf("a terminal job changed: %s", again)
	}
}

func TestCancelRetriesWhileATickHoldsTheJob(t *testing.T) {
	var b *busyStore
	rr := newRouteRig(t, func(s Store) Store { b = &busyStore{Store: s, busy: 2}; return b })
	// Creating takes no lock, so the first locks are the cancel's own.
	j := rr.create(planBody)
	code, body := rr.do("POST", "/v1/jobs/"+j.ID+"/cancel", "{}", signed)
	if code != 200 || !strings.Contains(body, `"cancelled"`) {
		t.Fatalf("%d %s", code, body)
	}
	if n := b.locks.Load(); n != 3 {
		t.Fatalf("%d attempts", n)
	}
}

func TestCancelGivesUpAfterFiveBusyAttempts(t *testing.T) {
	var b *busyStore
	rr := newRouteRig(t, func(s Store) Store { b = &busyStore{Store: s, busy: 99}; return b })
	j := rr.create(planBody)
	code, body := rr.do("POST", "/v1/jobs/"+j.ID+"/cancel", "{}", signed)
	if code != 409 || strings.TrimSpace(body) != `{"error":"job busy, try again"}` {
		t.Fatalf("%d %s", code, body)
	}
	if n := b.locks.Load(); n != 5 {
		t.Fatalf("%d attempts", n)
	}
}

func TestJobRoutesAnswer503WithoutAStore(t *testing.T) {
	rr := newRouteRig(t, nil)
	rr.up = false
	id := uuid.NewString()
	for _, c := range []struct{ m, p, b string }{
		{"POST", "/v1/jobs", planBody}, {"GET", "/v1/jobs", ""}, {"GET", "/v1/jobs/" + id, ""}, {"POST", "/v1/jobs/" + id + "/cancel", "{}"},
	} {
		if code, _ := rr.do(c.m, c.p, c.b, signed); code != 503 {
			t.Errorf("%s %s: %d", c.m, c.p, code)
		}
	}
}

func TestFeedListsTheLatestJobs(t *testing.T) {
	rr := newRouteRig(t, nil)
	api := NewAPI(func() (ledger.Ledger, *Scheduler) { return rr.led, rr.s }, nil)
	rr.create(planBody)
	data, err := api.Feed(ctx)
	var got struct{ Jobs []jobJSON }
	if err != nil || json.Unmarshal(data, &got) != nil || len(got.Jobs) != 1 {
		t.Fatalf("%v %s", err, data)
	}
	rr.up = false
	down := NewAPI(func() (ledger.Ledger, *Scheduler) { return nil, nil }, nil)
	if _, err := down.Feed(ctx); err == nil {
		t.Fatal("a feed without a store should fail so the stream skips it")
	}
}

func TestOversizeJobBodyIs413(t *testing.T) {
	rr := newRouteRig(t, nil)
	body := `{"kind":"stock","place":"` + strings.Repeat("x", 70000) + `"}`
	if code, out := rr.do("POST", "/v1/jobs", body, signed); code != 413 {
		t.Fatalf("oversize: %d %s", code, out)
	}
	if code, _ := rr.do("POST", "/v1/jobs", `{"kind":`, signed); code != 400 {
		t.Fatalf("malformed: %d", code)
	}
}

func TestLedgerOutageDuringPlanningIs503AndStoresNoJob(t *testing.T) {
	rr := newRouteRig(t, nil)
	rr.view = downLedger{rr.led}
	if code, out := rr.do("POST", "/v1/jobs", stockBody, signed); code != 503 {
		t.Fatalf("stock: %d %s", code, out)
	}
	if code, out := rr.do("POST", "/v1/jobs", planBody, signed); code != 503 {
		t.Fatalf("plan: %d %s", code, out)
	}
	js, err := rr.jobs.List(context.Background(), 50)
	if err != nil || len(js) != 0 {
		t.Fatalf("stored %d jobs, err %v", len(js), err)
	}
	if rr.changed.Load() != 0 {
		t.Fatal("a failed create announced a change")
	}
}
