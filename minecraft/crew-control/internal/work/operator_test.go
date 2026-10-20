package work

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
)

// fakeGate follows the contract of auth.Auth: Require answers 403 for a POST
// missing the header, then 401 without a session; X-Test-Op is the session.
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

type opRig struct {
	store *Memory
	api   *API
	srv   *httptest.Server
	mu    sync.Mutex
	seen  []string
}

func (o *opRig) notified() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seen...)
}

func newOpRig(t *testing.T, s Store) *opRig {
	t.Helper()
	units, err := roster.Parse(`[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"},{"id":"crew-02","name":"Basalt","url":"http://crew-02:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	o := &opRig{}
	if m, ok := s.(*Memory); ok {
		o.store = m
	}
	mux := http.NewServeMux()
	o.api = Routes(mux, s, units, token, func(u string) {
		o.mu.Lock()
		o.seen = append(o.seen, u)
		o.mu.Unlock()
	})
	o.api.MountOperator(mux, fakeGate{})
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

func (o *opRig) post(t *testing.T, path, body string, hdr map[string]string) (int, string) {
	t.Helper()
	h := map[string]string{"X-Test-Op": "owner", "X-Crew-Request": "1"}
	for k, v := range hdr {
		if v == "" {
			delete(h, k)
		} else {
			h[k] = v
		}
	}
	return o.call(t, "POST", path, body, h)
}

func (o *opRig) call(t *testing.T, method, path, body string, h map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, o.srv.URL+path, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

const (
	gatherOK = `{"template":"gather_and_deposit","item":"cobblestone","qty":8,"chest":{"x":1,"y":64,"z":2}}`
	haulOK   = `{"template":"haul","items":{"oak_log":4},"from":[{"x":1,"y":64,"z":2}],"to":{"x":3,"y":64,"z":4}}`
	gotoOK   = `{"template":"goto","to":{"x":10,"y":64,"z":-20}}`
)

func TestOperatorRoutesNeedSessionAndHeader(t *testing.T) {
	o := newOpRig(t, NewMemory())
	for _, p := range []string{"/v1/work/crew-01/tasks", "/v1/work/crew-01/stop", "/v1/work/crew-01/goto", "/v1/work/crew-01/items/" + zeroID + "/cancel"} {
		if code, _ := o.post(t, p, gatherOK, map[string]string{"X-Test-Op": "", "X-Crew-Request": "1"}); code != 401 {
			t.Errorf("%s anonymous: %d, want 401", p, code)
		}
		if code, _ := o.post(t, p, gatherOK, map[string]string{"X-Crew-Request": ""}); code != 403 {
			t.Errorf("%s without header: %d, want 403", p, code)
		}
	}
	if code, _ := o.call(t, "GET", "/v1/work/crew-01", "", nil); code != 401 {
		t.Errorf("GET anonymous: %d, want 401", code)
	}
	if got, _ := o.store.Recent(context.Background(), "crew-01", 10); len(got) != 0 {
		t.Fatalf("refused requests queued %d items", len(got))
	}
}

const zeroID = "00000000-0000-0000-0000-000000000000"

func TestQueueTasksRecordsOperatorAndWakes(t *testing.T) {
	o := newOpRig(t, NewMemory())
	woke := o.api.waiter("crew-01")
	for _, body := range []string{gatherOK, haulOK, gotoOK} {
		code, resp := o.post(t, "/v1/work/crew-01/tasks", body, nil)
		if code != 202 {
			t.Fatalf("%s: %d %s", body, code, resp)
		}
		var v struct {
			ID, Kind, State, Operator string
			Body                      json.RawMessage
		}
		if err := json.Unmarshal([]byte(resp), &v); err != nil || v.State != Queued || v.Kind != KindTask || v.Operator != "owner" || string(v.Body) != body {
			t.Fatalf("response %s (%v)", resp, err)
		}
	}
	select {
	case <-woke:
	default:
		t.Fatal("poll was not woken")
	}
	if n := len(o.notified()); n != 3 {
		t.Fatalf("notified %d times, want 3", n)
	}
}

func TestQueueTaskRejectsBadTemplates(t *testing.T) {
	o := newOpRig(t, NewMemory())
	for name, body := range map[string]string{
		"not json":      `nope`,
		"array":         `[]`,
		"unknown":       `{"template":"dig"}`,
		"unknown field": `{"template":"goto","to":{"x":1,"y":64,"z":1},"speed":9}`,
		"x high":        `{"template":"goto","to":{"x":500,"y":64,"z":0}}`,
		"x low":         `{"template":"goto","to":{"x":-501,"y":64,"z":0}}`,
		"z high":        `{"template":"goto","to":{"x":0,"y":64,"z":500}}`,
		"y high":        `{"template":"goto","to":{"x":0,"y":321,"z":0}}`,
		"y low":         `{"template":"goto","to":{"x":0,"y":-65,"z":0}}`,
		"missing coord": `{"template":"goto","to":{"x":0,"y":64}}`,
		"float coord":   `{"template":"goto","to":{"x":0.5,"y":64,"z":0}}`,
		"no to":         `{"template":"goto"}`,
		"item":          `{"template":"gather_and_deposit","item":"diamond","qty":8,"chest":{"x":1,"y":64,"z":2}}`,
		"qty zero":      `{"template":"gather_and_deposit","item":"oak_log","qty":0,"chest":{"x":1,"y":64,"z":2}}`,
		"qty high":      `{"template":"gather_and_deposit","item":"oak_log","qty":257,"chest":{"x":1,"y":64,"z":2}}`,
		"chest outside": `{"template":"gather_and_deposit","item":"oak_log","qty":8,"chest":{"x":900,"y":64,"z":2}}`,
		"no chest":      `{"template":"gather_and_deposit","item":"oak_log","qty":8}`,
		"area outside":  `{"template":"gather_and_deposit","item":"oak_log","qty":8,"chest":{"x":1,"y":64,"z":2},"area":{"min":{"x":0,"y":0,"z":0},"max":{"x":600,"y":1,"z":1}}}`,
		"area half":     `{"template":"gather_and_deposit","item":"oak_log","qty":8,"chest":{"x":1,"y":64,"z":2},"area":{"min":{"x":0,"y":0,"z":0}}}`,
		"haul no from":  `{"template":"haul","items":{"oak_log":4},"from":[],"to":{"x":3,"y":64,"z":4}}`,
		"haul no items": `{"template":"haul","items":{},"from":[{"x":1,"y":64,"z":2}],"to":{"x":3,"y":64,"z":4}}`,
		"haul bad item": `{"template":"haul","items":{"dirt":4},"from":[{"x":1,"y":64,"z":2}],"to":{"x":3,"y":64,"z":4}}`,
		"haul bad qty":  `{"template":"haul","items":{"oak_log":999},"from":[{"x":1,"y":64,"z":2}],"to":{"x":3,"y":64,"z":4}}`,
		"haul from out": `{"template":"haul","items":{"oak_log":4},"from":[{"x":1,"y":999,"z":2}],"to":{"x":3,"y":64,"z":4}}`,
		"haul no to":    `{"template":"haul","items":{"oak_log":4},"from":[{"x":1,"y":64,"z":2}]}`,
		"haul too many": `{"template":"haul","items":{"oak_log":4},"from":[{"x":1,"y":1,"z":1},{"x":2,"y":1,"z":1},{"x":3,"y":1,"z":1},{"x":4,"y":1,"z":1},{"x":5,"y":1,"z":1}],"to":{"x":3,"y":64,"z":4}}`,
		"trailing":      gotoOK + ` {}`,
	} {
		code, resp := o.post(t, "/v1/work/crew-01/tasks", body, nil)
		if code != 400 {
			t.Errorf("%s: %d %s, want 400", name, code, resp)
		}
		if strings.Contains(resp, "500") || strings.Contains(resp, "900") {
			t.Errorf("%s: error echoes input: %s", name, resp)
		}
	}
	if got, _ := o.store.Recent(context.Background(), "crew-01", 10); len(got) != 0 {
		t.Fatalf("rejected bodies queued %d items", len(got))
	}
	if code, _ := o.post(t, "/v1/work/crew-01/tasks", strings.Repeat(" ", 5000)+gotoOK, nil); code != 413 {
		t.Errorf("oversized: %d, want 413", code)
	}
}

func TestUnknownUnitIs404OnEveryRoute(t *testing.T) {
	o := newOpRig(t, NewMemory())
	for _, p := range []string{"/v1/work/crew-09/tasks", "/v1/work/crew-09/stop", "/v1/work/crew-09/goto", "/v1/work/crew-09/items/" + zeroID + "/cancel"} {
		if code, _ := o.post(t, p, gotoOK, nil); code != 404 {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if code, _ := o.call(t, "GET", "/v1/work/crew-09", "", map[string]string{"X-Test-Op": "owner"}); code != 404 {
		t.Errorf("GET: %d, want 404", code)
	}
}

func TestGotoQueuesATemplate(t *testing.T) {
	o := newOpRig(t, NewMemory())
	code, resp := o.post(t, "/v1/work/crew-02/goto", `{"x":10,"y":64,"z":-20}`, nil)
	if code != 202 {
		t.Fatalf("%d %s", code, resp)
	}
	got, _ := o.store.Recent(context.Background(), "crew-02", 5)
	if len(got) != 1 || got[0].Kind != KindTask || got[0].Operator != "owner" || string(got[0].Body) != `{"template":"goto","to":{"x":10,"y":64,"z":-20}}` {
		t.Fatalf("stored %+v", got)
	}
	for name, body := range map[string]string{
		"missing": `{"x":1,"y":64}`, "outside": `{"x":1,"y":64,"z":500}`, "extra": `{"x":1,"y":64,"z":1,"w":1}`, "junk": `x`,
	} {
		if code, _ := o.post(t, "/v1/work/crew-02/goto", body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}

func TestStopCancelsOpenWorkThenQueuesStop(t *testing.T) {
	o := newOpRig(t, NewMemory())
	a := must(t, o.store, "crew-01", KindTask, tpl)
	b := must(t, o.store, "crew-01", KindTask, tpl)
	other := must(t, o.store, "crew-02", KindTask, tpl)
	if _, ok, _, _ := o.store.Next(context.Background(), "crew-01", "", o.store.now()); !ok {
		t.Fatal("setup")
	}
	code, resp := o.post(t, "/v1/work/crew-01/stop", "", nil)
	if code != 202 {
		t.Fatalf("%d %s", code, resp)
	}
	items, _ := o.store.Recent(context.Background(), "crew-01", 10)
	if len(items) != 3 || items[0].Kind != KindCommand || items[0].State != Queued || string(items[0].Body) != `{"name":"stop"}` {
		t.Fatalf("items %+v", items)
	}
	for _, it := range items[1:] {
		if it.State != Cancelled || (it.ID != a.ID && it.ID != b.ID) {
			t.Errorf("open item left %s", it.State)
		}
	}
	if got, _ := o.store.Recent(context.Background(), "crew-02", 1); got[0].ID != other.ID || got[0].State != Queued {
		t.Error("another unit's work was touched")
	}
	if n := len(o.notified()); n != 1 {
		t.Fatalf("notified %d", n)
	}
}

func TestCancelItem(t *testing.T) {
	o := newOpRig(t, NewMemory())
	queued := must(t, o.store, "crew-01", KindTask, tpl)
	code, resp := o.post(t, "/v1/work/crew-01/items/"+queued.ID.String()+"/cancel", "", nil)
	if code != 200 && code != 202 {
		t.Fatalf("%d %s", code, resp)
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(resp), &v)
	if v["state"] != Cancelled || v["note"] != nil {
		t.Fatalf("queued cancel: %s", resp)
	}
	if got, _ := o.store.Recent(context.Background(), "crew-01", 5); len(got) != 1 {
		t.Fatalf("a queued cancel must not queue a stop: %+v", got)
	}

	// A delivered item is the unit's: the only lever is stopping the unit.
	first := must(t, o.store, "crew-01", KindTask, tpl)
	second := must(t, o.store, "crew-01", KindTask, tpl)
	if _, ok, _, _ := o.store.Next(context.Background(), "crew-01", "", o.store.now()); !ok {
		t.Fatal("setup")
	}
	code, resp = o.post(t, "/v1/work/crew-01/items/"+first.ID.String()+"/cancel", "", nil)
	v = nil
	_ = json.Unmarshal([]byte(resp), &v)
	if code != 202 || v["id"] != first.ID.String() || v["state"] != Cancelled || v["note"] != "stopped the unit" {
		t.Fatalf("delivered cancel: %d %s", code, resp)
	}
	items, _ := o.store.Recent(context.Background(), "crew-01", 10)
	if items[0].Kind != KindCommand || items[0].State != Queued {
		t.Fatalf("no stop queued: %+v", items[0])
	}
	for _, it := range items {
		if it.ID == second.ID && it.State != Cancelled {
			t.Error("the coarse stop should also cancel the other open item")
		}
	}

	// Finished work is reported as it is.
	code, resp = o.post(t, "/v1/work/crew-01/items/"+first.ID.String()+"/cancel", "", nil)
	if code != 200 || strings.Contains(resp, "note") {
		t.Fatalf("terminal cancel: %d %s", code, resp)
	}

	if code, _ := o.post(t, "/v1/work/crew-01/items/"+zeroID+"/cancel", "", nil); code != 404 {
		t.Errorf("unknown item: %d", code)
	}
	if code, _ := o.post(t, "/v1/work/crew-01/items/not-a-uuid/cancel", "", nil); code != 404 {
		t.Errorf("bad id: %d", code)
	}
	other := must(t, o.store, "crew-02", KindTask, tpl)
	if code, _ := o.post(t, "/v1/work/crew-01/items/"+other.ID.String()+"/cancel", "", nil); code != 404 {
		t.Errorf("another unit's item: %d", code)
	}
}

func TestHistoryIsOperatorOnlyAndCarriesBodies(t *testing.T) {
	o := newOpRig(t, NewMemory())
	must(t, o.store, "crew-01", KindTask, tpl)
	code, resp := o.call(t, "GET", "/v1/work/crew-01", "", map[string]string{"X-Test-Op": "owner"})
	if code != 200 {
		t.Fatalf("%d %s", code, resp)
	}
	var v struct {
		Unit  string
		Items []struct{ Body json.RawMessage }
	}
	if err := json.Unmarshal([]byte(resp), &v); err != nil || v.Unit != "crew-01" || len(v.Items) != 1 || string(v.Items[0].Body) != string(tpl) {
		t.Fatalf("%s (%v)", resp, err)
	}
}

func TestFeedMatchesHistory(t *testing.T) {
	o := newOpRig(t, NewMemory())
	must(t, o.store, "crew-02", KindTask, tpl)
	data, err := o.api.Feed(context.Background(), "crew-02")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Unit  string
		Items []json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil || v.Unit != "crew-02" || len(v.Items) != 1 {
		t.Fatalf("%s (%v)", data, err)
	}
}

type downStore struct{ Store }

func (downStore) Enqueue(context.Context, string, string, json.RawMessage, string) (Item, error) {
	return Item{}, ErrUnavailable
}
func (downStore) StopUnit(context.Context, string, string, *uuid.UUID) ([]Item, Item, Item, bool, error) {
	return nil, Item{}, Item{}, false, ErrUnavailable
}
func (downStore) Recent(context.Context, string, int) ([]Item, error) {
	return nil, ErrUnavailable
}

func TestStoreDownIs503(t *testing.T) {
	o := newOpRig(t, downStore{})
	for _, p := range []string{"/v1/work/crew-01/tasks", "/v1/work/crew-01/stop", "/v1/work/crew-01/goto", "/v1/work/crew-01/items/" + zeroID + "/cancel"} {
		code, resp := o.post(t, p, gotoOK, nil)
		if p == "/v1/work/crew-01/goto" {
			code, resp = o.post(t, p, `{"x":1,"y":64,"z":1}`, nil)
		}
		if code != 503 || resp != `{"error":"queue unavailable"}`+"\n" {
			t.Errorf("%s: %d %q", p, code, resp)
		}
	}
	if code, resp := o.call(t, "GET", "/v1/work/crew-01", "", map[string]string{"X-Test-Op": "owner"}); code != 503 || !strings.Contains(resp, "queue unavailable") {
		t.Errorf("GET: %d %q", code, resp)
	}
	if _, err := o.api.Feed(context.Background(), "crew-01"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Feed: %v", err)
	}
}

// raceStore lets the unit take the item between the lookup and the cancel.
type raceStore struct{ *Memory }

func (r raceStore) CancelQueued(c context.Context, unit string, id uuid.UUID) (Item, bool, error) {
	if _, ok, _, _ := r.Memory.Next(c, unit, "", time.Now()); !ok {
		panic("race setup: nothing to deliver")
	}
	return r.Memory.CancelQueued(c, unit, id)
}

func TestCancelLosingTheDeliveryRaceStopsTheUnit(t *testing.T) {
	m := NewMemory()
	o := newOpRig(t, raceStore{m})
	it := must(t, m, "crew-01", KindTask, tpl)
	code, resp := o.post(t, "/v1/work/crew-01/items/"+it.ID.String()+"/cancel", "", nil)
	var v map[string]any
	_ = json.Unmarshal([]byte(resp), &v)
	if code != 202 || v["note"] != "stopped the unit" || v["state"] != Cancelled {
		t.Fatalf("%d %s", code, resp)
	}
	items, _ := m.Recent(context.Background(), "crew-01", 5)
	if len(items) != 2 || items[0].Kind != KindCommand || items[1].State != Cancelled {
		t.Fatalf("items %+v", items)
	}
}

type finishBeforeStop struct {
	*Memory
	id uuid.UUID
}

func (s finishBeforeStop) StopUnit(ctx context.Context, unit, operator string, onlyIfOpen *uuid.UUID) ([]Item, Item, Item, bool, error) {
	if _, err := s.Memory.Apply(ctx, unit, s.id, Done, 1, ""); err != nil {
		return nil, Item{}, Item{}, false, err
	}
	return s.Memory.StopUnit(ctx, unit, operator, onlyIfOpen)
}

func TestCancelReportsItemThatFinishedBeforeUnitStop(t *testing.T) {
	m := NewMemory()
	first := must(t, m, "crew-01", KindTask, tpl)
	_, ok, _, err := m.Next(context.Background(), "crew-01", "", time.Now())
	if err != nil || !ok {
		t.Fatal("setup: item was not delivered")
	}
	second := must(t, m, "crew-01", KindTask, tpl)
	o := newOpRig(t, finishBeforeStop{Memory: m, id: first.ID})
	code, resp := o.post(t, "/v1/work/crew-01/items/"+first.ID.String()+"/cancel", "", nil)
	var got map[string]any
	if err := json.Unmarshal([]byte(resp), &got); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || got["state"] != Done || got["note"] != nil {
		t.Fatalf("finished item was mislabeled: %d %s", code, resp)
	}
	items, _ := m.Recent(context.Background(), "crew-01", 5)
	if len(items) != 2 || items[0].ID != second.ID || items[0].State != Queued || items[1].ID != first.ID || items[1].State != Done {
		t.Fatalf("terminal race had stop side effects: %+v", items)
	}
}

type stopDown struct{ *Memory }

func (s stopDown) StopUnit(context.Context, string, string, *uuid.UUID) ([]Item, Item, Item, bool, error) {
	return nil, Item{}, Item{}, false, ErrUnavailable
}

func TestStopAnswers503WhenTheStopCannotBeQueued(t *testing.T) {
	m := NewMemory()
	o := newOpRig(t, stopDown{m})
	first := must(t, m, "crew-01", KindTask, tpl)
	second := must(t, m, "crew-01", KindTask, tpl)
	if code, resp := o.post(t, "/v1/work/crew-01/stop", "", nil); code != 503 || !strings.Contains(resp, "queue unavailable") {
		t.Fatalf("%d %s", code, resp)
	}
	items, _ := m.Recent(context.Background(), "crew-01", 5)
	if len(items) != 2 || items[0].ID != second.ID || items[0].State != Queued || items[1].ID != first.ID || items[1].State != Queued {
		t.Fatalf("failed stop left partial cancellation: %+v", items)
	}
}
