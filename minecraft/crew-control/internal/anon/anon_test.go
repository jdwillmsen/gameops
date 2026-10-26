package anon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
)

func TestCacheReusesAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCache()
	c.SetClock(func() time.Time { return now })
	calls := 0
	do := func(key string) Reply {
		return c.Do(key, func() (Reply, bool) { calls++; return Reply{Status: 200, Body: []byte(key)}, true })
	}
	do("a")
	do("a")
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 within the ttl", calls)
	}
	do("b")
	if calls != 2 {
		t.Fatalf("calls = %d, want a distinct key to compute", calls)
	}
	now = now.Add(ttl + time.Second)
	do("a")
	if calls != 3 {
		t.Fatalf("calls = %d, want an expired entry to recompute", calls)
	}
}

func TestCacheDoesNotKeepFailures(t *testing.T) {
	c := NewCache()
	calls := 0
	for range 2 {
		c.Do("k", func() (Reply, bool) { calls++; return Reply{Status: 503}, false })
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want a failed scan retried", calls)
	}
}

func TestCacheIsBounded(t *testing.T) {
	c := NewCache()
	for i := range maxEntries * 3 {
		c.Do(string(rune('A'+i%26))+string(rune(i)), func() (Reply, bool) { return Reply{Status: 200}, true })
	}
	if len(c.m) > maxEntries {
		t.Fatalf("len = %d, want at most %d", len(c.m), maxEntries)
	}
}

func TestSigned(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if Signed(nil, r) {
		t.Fatal("nil gate must read as anonymous")
	}
	in := GateFunc(func(*http.Request) (auth.Operator, bool) { return auth.Operator{ID: "x"}, true })
	if !Signed(in, r) {
		t.Fatal("operator session must read as signed in")
	}
}
