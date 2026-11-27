package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/internal/presenceapi"
)

func newServer(t *testing.T, def presenceapi.State) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newStub(def, slog.New(slog.NewTextHandler(io.Discard, nil))).routes())
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, base, etag string) (presenceapi.Presence, string, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/actors/idle-1/presence", nil)
	req.Header.Set("Authorization", "Bearer anything")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var p presenceapi.Presence
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
	}
	return p, resp.Header.Get("ETag"), resp.StatusCode
}

func setState(t *testing.T, base, state string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, base+"/stub/actors/idle-1?state="+state, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestUnsetActorTakesTheDefault(t *testing.T) {
	srv := newServer(t, presenceapi.StateParked)
	p, _, code := fetch(t, srv.URL, "")
	if code != http.StatusOK || p.Effective != presenceapi.StateParked || p.ActorID != "idle-1" {
		t.Fatalf("got %d %+v, want 200 parked idle-1", code, p)
	}
}

func TestParkChangesTheETag(t *testing.T) {
	srv := newServer(t, presenceapi.StatePresent)
	_, tag, _ := fetch(t, srv.URL, "")
	if _, _, code := fetch(t, srv.URL, tag); code != http.StatusNotModified {
		t.Fatalf("unchanged poll = %d, want 304", code)
	}
	if code := setState(t, srv.URL, "parked"); code != http.StatusOK {
		t.Fatalf("park = %d", code)
	}
	p, next, code := fetch(t, srv.URL, tag)
	if code != http.StatusOK || p.Effective != presenceapi.StateParked || next == tag {
		t.Fatalf("after park: %d %+v etag %s->%s", code, p, tag, next)
	}
	// Setting the same state again is not a change a unit should hear about.
	setState(t, srv.URL, "parked")
	if _, _, code := fetch(t, srv.URL, next); code != http.StatusNotModified {
		t.Fatalf("repeat park poll = %d, want 304", code)
	}
}

func TestBadStateIsRefused(t *testing.T) {
	srv := newServer(t, presenceapi.StatePresent)
	if code := setState(t, srv.URL, "asleep"); code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", code)
	}
}

func TestStatusReportIsShown(t *testing.T) {
	srv := newServer(t, presenceapi.StatePresent)
	body := `{"connected":true,"observed_state":"present","last_seen":"2026-11-27T00:00:00Z","process_version":"1.2.3"}`
	resp, err := http.Post(srv.URL+"/v1/actors/idle-1/status", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("report = %d, want 204", resp.StatusCode)
	}
	resp, err = http.Get(srv.URL + "/stub/actors/idle-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v view
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.Last == nil || !v.Last.Connected || v.Last.ProcessVersion != "1.2.3" || v.LastAt == nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestDefaultFlagIsChecked(t *testing.T) {
	if code := run([]string{"-default", "maybe"}, io.Discard); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// A restarted stub must not answer 304 to a tag from before the restart,
// whatever state it now starts in.
func TestETagChangesAcrossRestarts(t *testing.T) {
	first := newServer(t, presenceapi.StatePresent)
	_, tag, _ := fetch(t, first.URL, "")
	second := newServer(t, presenceapi.StateParked)
	p, _, code := fetch(t, second.URL, tag)
	if code != http.StatusOK || p.Effective != presenceapi.StateParked {
		t.Fatalf("after restart: %d %+v, want 200 parked", code, p)
	}
}

func TestPollIsShown(t *testing.T) {
	srv := newServer(t, presenceapi.StatePresent)
	fetch(t, srv.URL, "")
	resp, err := http.Get(srv.URL + "/stub/actors/idle-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v view
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v.PolledAt == nil {
		t.Fatalf("view = %+v, want polled_at", v)
	}
}

func TestMalformedActorIDIsRefused(t *testing.T) {
	srv := newServer(t, presenceapi.StatePresent)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/stub/actors/Bad%0AId?state=parked", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

func TestLogSafeDropsLineBreaks(t *testing.T) {
	if got := logSafe("a\nb\r\nc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestObservedNamesOnlyContractStates(t *testing.T) {
	if got := observed("parked\nfake"); got != "invalid" {
		t.Fatalf("got %q", got)
	}
	if got := observed(presenceapi.StateParked); got != "parked" {
		t.Fatalf("got %q", got)
	}
}
