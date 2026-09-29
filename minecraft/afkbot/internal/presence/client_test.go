package presence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

// seen is what a test handler observed. Handlers run on the server's
// goroutines, so they hand it over on a channel rather than through shared
// variables the race detector would flag.
type seen struct {
	method, path, auth, ifNoneMatch string
	hasIfNoneMatch                  bool
	body                            []byte
}

func record(r *http.Request, out chan<- seen) {
	body, _ := io.ReadAll(r.Body)
	_, has := r.Header["If-None-Match"]
	out <- seen{
		method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"),
		ifNoneMatch: r.Header.Get("If-None-Match"), hasIfNoneMatch: has, body: body,
	}
}

func TestFetchSendsTokenAndETag(t *testing.T) {
	reqs := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r, reqs)
		w.Header().Set("ETag", `"3-parked"`)
		_ = json.NewEncoder(w).Encode(presenceapi.Presence{ActorID: "afk-bot-1", Effective: presenceapi.StateParked, Default: presenceapi.StatePresent})
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), `"2-present"`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	req := <-reqs
	if req.auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", req.auth, "Bearer tok")
	}
	if req.ifNoneMatch != `"2-present"` {
		t.Errorf("If-None-Match = %q, want the ETag passed in", req.ifNoneMatch)
	}
	if req.path != "/v1/actors/afk-bot-1/presence" {
		t.Errorf("path = %q", req.path)
	}
	if got.NotModified || got.Presence.Effective != presenceapi.StateParked || got.ETag != `"3-parked"` {
		t.Errorf("Fetched = %+v, want parked with the new ETag", got)
	}
}

// The first poll has nothing to compare against; an empty If-None-Match
// header would be a malformed conditional request.
func TestFetchOmitsIfNoneMatchWithoutAnETag(t *testing.T) {
	reqs := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r, reqs)
		_ = json.NewEncoder(w).Encode(presenceapi.Presence{Effective: presenceapi.StatePresent})
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), ""); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if (<-reqs).hasIfNoneMatch {
		t.Error("If-None-Match sent on a request with no ETag")
	}
}

func TestFetchNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), `"1-parked"`)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !got.NotModified || got.ETag != `"1-parked"` {
		t.Errorf("Fetched = %+v, want NotModified keeping the ETag", got)
	}
}

// GET presence does not check token binding to an actor, only the
// presence:read scope — so a 403 here is worded about the missing scope, not
// about being bound to a different actor (amendment B2).
func TestFetchErrorCarriesStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(presenceapi.Error{Code: presenceapi.CodeForbidden, Message: "token missing presence:read scope"})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), "")
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.StatusCode != http.StatusForbidden || he.Body.Code != presenceapi.CodeForbidden {
		t.Errorf("HTTPError = %+v", he)
	}
}

// A state outside the contract must not be read as either state: the
// reconciler keeps its last answer instead.
func TestFetchRejectsUnknownState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"actor_id":"afk-bot-1","effective":"hibernating","default":"present"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), "")
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err = %v, want ErrInvalidResponse", err)
	}
}

func TestReportPostsStatus(t *testing.T) {
	reqs := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r, reqs)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	want := presenceapi.Status{Connected: true, ObservedState: presenceapi.StatePresent, LastSeen: time.Date(2026, 9, 23, 6, 0, 0, 0, time.UTC), ProcessVersion: "1.2.3"}
	if err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Report(context.Background(), want); err != nil {
		t.Fatalf("Report: %v", err)
	}
	req := <-reqs
	if req.method != http.MethodPost || req.auth != "Bearer tok" || req.path != "/v1/actors/afk-bot-1/status" {
		t.Errorf("request = %s %s auth %q", req.method, req.path, req.auth)
	}
	var got presenceapi.Status
	if err := json.Unmarshal(req.body, &got); err != nil {
		t.Fatalf("decode posted body: %v", err)
	}
	if !got.LastSeen.Equal(want.LastSeen) || got.Connected != want.Connected || got.ObservedState != want.ObservedState || got.ProcessVersion != want.ProcessVersion {
		t.Errorf("posted %+v, want %+v", got, want)
	}
}

func TestReportErrorCarriesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Report(context.Background(), presenceapi.Status{ObservedState: presenceapi.StateParked})
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *HTTPError 401", err)
	}
}
