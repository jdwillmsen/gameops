package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// failing is a primary that is down for every call.
type failing struct{ err error }

func (f failing) Append(context.Context, []Event) error { return f.err }
func (f failing) Query(context.Context, Query) (Page, error) {
	return Page{}, f.err
}
func (f failing) Prune(context.Context, time.Time, int, bool) (PruneReport, error) {
	return PruneReport{}, f.err
}

func newFallback() *Fallback { return NewFallback(logging.New("info")) }

func TestFallbackServesFromMemoryBeforeTheDatabaseIsReached(t *testing.T) {
	f := newFallback()
	if !f.Degraded() {
		t.Fatal("a store with no database must say it is degraded")
	}
	if err := f.Append(ctx, batch(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(mustQuery(t, f, Query{})); n != 6 {
		t.Fatalf("memory holds %d, want 6", n)
	}
	if f.Diverted() != 6 {
		t.Fatalf("diverted = %d, want 6", f.Diverted())
	}
}

func TestFallbackWritesHeldEventsBackWhenTheDatabaseReturns(t *testing.T) {
	f := newFallback()
	down := errors.New("connection refused")
	f.SetPrimary(failing{down})
	if err := f.Append(ctx, batch(time.Now())[:3]); err != nil {
		t.Fatal(err)
	}
	if !f.Degraded() || f.Diverted() != 3 {
		t.Fatalf("degraded=%v diverted=%d, want true and 3", f.Degraded(), f.Diverted())
	}

	up := NewMemory()
	f.SetPrimary(up)
	if err := f.Append(ctx, batch(time.Now())[3:]); err != nil {
		t.Fatal(err)
	}
	if f.Degraded() {
		t.Fatal("still degraded after a write went through")
	}
	if f.Replayed() != 3 {
		t.Fatalf("replayed = %d, want 3", f.Replayed())
	}
	if n := len(mustQuery(t, up, Query{})); n != 6 {
		t.Fatalf("database holds %d, want the 3 replayed and the 3 live", n)
	}
	if n := len(f.mem.drain()); n != 0 {
		t.Fatalf("memory still holds %d after replay", n)
	}
}

func TestFallbackKeepsHeldEventsWhileReplayFails(t *testing.T) {
	f := newFallback()
	f.SetPrimary(failing{errors.New("down")})
	for range 3 {
		if err := f.Append(ctx, batch(time.Now())[:2]); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(mustQuery(t, f, Query{})); n != 6 {
		t.Fatalf("memory holds %d across three failed batches, want 6", n)
	}
}

func TestFallbackDropsABatchTheDatabaseRejects(t *testing.T) {
	f := newFallback()
	f.SetPrimary(failing{errors.New("down")})
	if err := f.Append(ctx, batch(time.Now())[:2]); err != nil {
		t.Fatal(err)
	}
	f.SetPrimary(failing{&pgconn.PgError{Code: "23514"}})
	if err := f.Append(ctx, batch(time.Now())[2:3]); err != nil {
		t.Fatal(err)
	}
	// The poison batch must not stay in front of everything after it.
	if f.Lost() != 2 {
		t.Fatalf("lost = %d, want the 2 held events", f.Lost())
	}
}

func TestFallbackMemoryIsBounded(t *testing.T) {
	f := newFallback()
	big := make([]Event, fallbackCap+25)
	for i := range big {
		big[i] = Event{Unit: "crew-01", At: time.Unix(int64(i), 0), Kind: "online", Severity: SevOK}
	}
	if err := f.Append(ctx, big); err != nil {
		t.Fatal(err)
	}
	if f.Lost() != 25 {
		t.Fatalf("lost = %d, want 25", f.Lost())
	}
	oldest := mustQuery(t, f, Query{Asc: true, Limit: 1})[0]
	if !oldest.At.Equal(time.Unix(25, 0)) {
		t.Fatalf("oldest kept = %v, want the 26th event", oldest.At)
	}
}

func TestFallbackQueryFallsBackButPassesInvalidThrough(t *testing.T) {
	f := newFallback()
	f.SetPrimary(failing{errors.New("down")})
	if err := f.Append(ctx, batch(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(mustQuery(t, f, Query{})); n != 6 {
		t.Fatalf("fallback query returned %d, want 6", n)
	}
	f.SetPrimary(failing{ErrInvalid})
	if _, err := f.Query(ctx, Query{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an invalid query must not be answered from memory: %v", err)
	}
}

func TestLazyConnectKeepsTheFallbackAndSetsThePrimary(t *testing.T) {
	l := &Lazy{}
	f := newFallback()
	l.Set(f)
	up := NewMemory()
	l.Connect(up)
	if err := l.Append(ctx, batch(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(mustQuery(t, up, Query{})); n != 6 || f.Degraded() {
		t.Fatalf("primary holds %d, degraded=%v", n, f.Degraded())
	}
	bare := &Lazy{}
	bare.Connect(up)
	if _, err := bare.Query(ctx, Query{}); err != nil {
		t.Fatalf("connect on an empty lazy: %v", err)
	}
}

// Against a real database: one that cannot be reached, then one that can.
func TestFallbackAgainstRealPostgres(t *testing.T) {
	real := pgStore(t)

	dead, err := pgxpool.New(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dead.Close)

	f := newFallback()
	f.SetPrimary(NewPostgres(dead))
	if err := f.Append(ctx, batch(time.Now())); err != nil {
		t.Fatalf("an unreachable database must not fail the append: %v", err)
	}
	if !f.Degraded() || f.Diverted() != 6 {
		t.Fatalf("degraded=%v diverted=%d", f.Degraded(), f.Diverted())
	}
	if n := len(mustQuery(t, f, Query{})); n != 6 {
		t.Fatalf("the console sees %d events during the outage, want 6", n)
	}

	f.SetPrimary(real)
	if err := f.Append(ctx, []Event{{Unit: "crew-09", At: time.Now(), Kind: "online", Severity: SevOK}}); err != nil {
		t.Fatal(err)
	}
	if f.Degraded() || f.Replayed() != 6 {
		t.Fatalf("degraded=%v replayed=%d, want recovered with 6 replayed", f.Degraded(), f.Replayed())
	}
	if n := len(mustQuery(t, real, Query{})); n != 7 {
		t.Fatalf("database holds %d, want 7", n)
	}
}
