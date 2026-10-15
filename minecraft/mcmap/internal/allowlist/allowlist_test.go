package allowlist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeLister struct {
	answers []map[string]bool
	err     error
	asked   int
}

func (f *fakeLister) XUIDs(context.Context) (map[string]bool, error) {
	f.asked++
	if f.err != nil {
		return nil, f.err
	}
	return f.answers[min(f.asked-1, len(f.answers)-1)], nil
}

func TestList_AnswersFromTheBridgeAndKeepsItBriefly(t *testing.T) {
	now := time.Now()
	f := &fakeLister{answers: []map[string]bool{{"1": true}, {"2": true}}}
	l := New(f, func() time.Time { return now })
	if on, ok := l.Has(t.Context(), "1"); !on || !ok {
		t.Fatal("a listed player is not on it")
	}
	if on, _ := l.Has(t.Context(), "2"); on || f.asked != 1 {
		t.Errorf("asked %d times; a second player was found early", f.asked)
	}
	now = now.Add(CacheFor)
	if on, _ := l.Has(t.Context(), "2"); !on || f.asked != 2 {
		t.Errorf("after the cache ran out: asked %d", f.asked)
	}
	if on, _ := l.Has(t.Context(), "1"); on {
		t.Error("a removed player is still on it")
	}
}

func TestList_WithTheBridgeDownNobodyIsOnItAndItIsAskedAgainAtOnceNotNever(t *testing.T) {
	now := time.Now()
	f := &fakeLister{answers: []map[string]bool{{"1": true}}}
	l := New(f, func() time.Time { return now })
	l.Has(t.Context(), "1")
	now = now.Add(CacheFor)
	f.err = errors.New("down")
	if on, ok := l.Has(t.Context(), "1"); on || ok {
		t.Errorf("on = %v, answered = %v with the bridge down", on, ok)
	}
	now = now.Add(CacheFor)
	f.err = nil
	if on, ok := l.Has(t.Context(), "1"); !on || !ok {
		t.Error("it did not recover")
	}
}

func TestBridge_ReadsXUIDsAndSkipsEntriesThatHaveNone(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/allowlist" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"name":"A","xuid":"2535400000000001","ignoresPlayerLimit":false},{"name":"B","xuid":""},{"name":"C"},{"name":"D","xuid":"not-a-number"}]`))
	}))
	defer srv.Close()
	got, err := NewBridge(srv.URL+"/", "bridge-token").XUIDs(t.Context())
	if err != nil || len(got) != 1 || !got["2535400000000001"] || auth != "Bearer bridge-token" {
		t.Errorf("got %v, %v, auth %q", got, err, auth)
	}
}

func TestBridge_AnythingButAnOKListIsAnError(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"404":      func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"401":      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) },
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<")) },
		"redirect": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", 302) },
	} {
		srv := httptest.NewServer(h)
		if _, err := NewBridge(srv.URL, "t").XUIDs(t.Context()); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
}
