package operators

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const steve, alex = "2535412345678901", "2535412345678902"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeBridge struct {
	mu    sync.Mutex
	perms map[string]string
	err   error
	asked int
}

func (f *fakeBridge) Permissions(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.perms, f.err
}

func (f *fakeBridge) set(perms map[string]string, err error) {
	f.mu.Lock()
	f.perms, f.err = perms, err
	f.mu.Unlock()
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func open(t *testing.T, b *fakeBridge, c *clock) (*List, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operators.json")
	l, err := Open(path, b, c.now, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return l, path
}

func TestList_OnlyTheLiteralOperatorIsAnAdmin(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	for level, want := range map[string]bool{
		"operator": true, "member": false, "visitor": false, "": false,
		"Operator": false, "OPERATOR": false, " operator": false, "operator ": false, "admin": false,
	} {
		b := &fakeBridge{perms: map[string]string{steve: level}}
		l, _ := open(t, b, c)
		if got, src := l.IsOperator(t.Context(), steve); got != want || src != Live {
			t.Errorf("level %q: admin %v from %s, want %v from live", level, got, src, want)
		}
	}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, _ := open(t, b, c)
	if got, _ := l.IsOperator(t.Context(), alex); got {
		t.Error("an XUID absent from the list is an admin")
	}
}

func TestList_AsksTheBridgeAtMostOnceInTenSeconds(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, _ := open(t, b, c)
	for range 5 {
		l.IsOperator(t.Context(), steve)
	}
	if b.asked != 1 {
		t.Fatalf("asked %d times within the cache, want 1", b.asked)
	}
	b.set(map[string]string{steve: "member"}, nil)
	c.add(CacheFor)
	if got, _ := l.IsOperator(t.Context(), steve); got {
		t.Error("a demotion is not seen once the cache has run out")
	}
}

func TestList_FallsBackToLastKnownGoodForADayFromConfirmation(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())

	for name, failure := range map[string]error{"unreachable": errors.New("dial tcp: refused"), "file deleted": ErrNoFile} {
		b.set(nil, failure)
		c.add(CacheFor)
		if got, src := l.IsOperator(t.Context(), steve); !got || src != LastKnownGood {
			t.Errorf("%s within the window: admin %v from %s", name, got, src)
		}
	}
	// A list unchanged for days is still confirmed within the hour while
	// lookups succeed, so the window runs from the last success.
	b.set(map[string]string{steve: "operator"}, nil)
	for range 30 {
		c.add(ConfirmEvery)
		l.Refresh(t.Context())
	}
	b.set(nil, errors.New("bridge down"))
	c.add(23 * time.Hour)
	if got, src := l.IsOperator(t.Context(), steve); !got || src != LastKnownGood {
		t.Errorf("an unchanged list confirmed an hour before the outage, 23 h in: admin %v from %s", got, src)
	}
	c.add(2 * time.Hour)
	if got, src := l.IsOperator(t.Context(), steve); got || src != None {
		t.Errorf("past the window: admin %v from %s, want nobody", got, src)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "member") || strings.Contains(string(raw), "gamertag") || !strings.Contains(string(raw), steve) {
		t.Errorf("the file holds more than the operators' XUIDs and the time: %s", raw)
	}
}

func TestList_RewritesTheSetOnlyWhenItChangesAndTheTimeHourly(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())
	first, _ := os.Stat(path)

	c.add(10 * time.Minute)
	l.Refresh(t.Context())
	if again, _ := os.Stat(path); !again.ModTime().Equal(first.ModTime()) {
		t.Error("an unchanged set was rewritten within the hour")
	}
	c.add(ConfirmEvery)
	l.Refresh(t.Context())
	if read(t, path).ConfirmedAt.Before(c.now().Add(-time.Minute)) {
		t.Error("confirmedAt was not moved on after an hour of successful lookups")
	}
	b.set(map[string]string{steve: "operator", alex: "operator"}, nil)
	l.Refresh(t.Context())
	if got := read(t, path).XUIDs; len(got) != 2 {
		t.Errorf("a changed set was not written: %v", got)
	}
}

func TestList_StartupLookupSetsTheGauge(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator", alex: "member"}}
	l, path := open(t, b, c)
	l.Refresh(t.Context())
	if got := testutil.ToFloat64(metricOperators); got != 1 {
		t.Errorf("gauge after a live lookup = %v, want 1", got)
	}

	// The map restarts with the bridge down: the gauge comes from the file.
	b.set(nil, errors.New("down"))
	again, err := Open(path, b, c.now, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if src := again.Refresh(t.Context()); src != LastKnownGood || testutil.ToFloat64(metricOperators) != 1 {
		t.Errorf("restart with the bridge down: source %s, gauge %v", src, testutil.ToFloat64(metricOperators))
	}
	c.add(Window + time.Minute)
	if src := again.Refresh(t.Context()); src != None || testutil.ToFloat64(metricOperators) != 0 {
		t.Errorf("past the window: source %s, gauge %v", src, testutil.ToFloat64(metricOperators))
	}
}

// Declared on a vector nobody else has touched, so that a result left out
// of declareLookups shows as a missing series.
func TestList_EveryLookupResultIsDeclared(t *testing.T) {
	v := newLookups(promauto.With(nil))
	declareLookups(v)
	if n := testutil.CollectAndCount(v); n != 3 {
		t.Errorf("%d lookup series declared, want live, last_known_good and none", n)
	}
}

// Many requests arriving once the cache has run out ask the bridge once.
func TestList_ConcurrentRequestsShareOneLookup(t *testing.T) {
	c := &clock{t: time.Unix(1_790_000_000, 0)}
	b := &fakeBridge{perms: map[string]string{steve: "operator"}}
	l, _ := open(t, b, c)
	l.Refresh(t.Context())
	c.add(CacheFor)
	before := b.asked
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); l.IsOperator(t.Context(), steve) }()
	}
	wg.Wait()
	if got := b.asked - before; got != 1 {
		t.Errorf("20 requests after the cache ran out asked the bridge %d times, want 1", got)
	}
}

func read(t *testing.T, path string) file {
	t.Helper()
	f, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
