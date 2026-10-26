package alerts

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

type memStore struct {
	mu    sync.Mutex
	saved []Alert
	err   error
	saves int
}

func (m *memStore) Load(context.Context) ([]Alert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alert(nil), m.saved...), m.err
}

func (m *memStore) Save(_ context.Context, a []Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.saved, m.saves = append([]Alert(nil), a...), m.saves+1
	return nil
}

func (m *memStore) snapshot() ([]Alert, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alert(nil), m.saved...), m.saves
}

func died(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(Defaults(), t0)
	e.Poll(t0, []telemetry.Status{st("a", true, f32(20), t0)}, states("a", "dead"))
	return e
}

func TestRestoreKeepsAcknowledgementAndMuteAcrossARestart(t *testing.T) {
	before := died(t)
	before.Ack("unit_died.a")
	before.Mute("unit_died.a", 30*time.Minute, t0)
	saved := before.List()

	after := NewEngine(Defaults(), t0.Add(time.Minute))
	after.Restore(saved, t0.Add(time.Minute))
	a := find(after, "unit_died.a")
	if a == nil || a.State != StateFiring || !a.Acked || a.MutedUntil == nil {
		t.Fatalf("restored alert: %+v", a)
	}
	// The first poll still sees the unit dead: the alert must not be
	// re-announced as new, which is what losing its Since would do.
	after.Poll(t0.Add(2*time.Minute), []telemetry.Status{st("a", true, f32(20), t0)}, states("a", "dead"))
	b := find(after, "unit_died.a")
	if !b.Acked || !b.Since.Equal(a.Since) {
		t.Fatalf("after the first poll: %+v", b)
	}
}

func TestRestoreDoesNotOverwriteWhatThePollAlreadySaw(t *testing.T) {
	e := died(t)
	stale := []Alert{{ID: "unit_died.a", Rule: RuleDied, Unit: "a", Severity: SevCrit, State: StateResolved,
		Since: t0.Add(-time.Hour), ResolvedAt: &t0}}
	e.Restore(stale, t0.Add(time.Second))
	if a := find(e, "unit_died.a"); a.State != StateFiring || !a.Since.Equal(t0) {
		t.Fatalf("a newer reading was overwritten: %+v", a)
	}
}

func TestRestoredStuckAlertSurvivesTheFirstPoll(t *testing.T) {
	cfg := Defaults()
	e := NewEngine(cfg, t0)
	since := t0.Add(-10 * time.Minute)
	e.Restore([]Alert{{ID: "unit_stuck.a", Rule: RuleStuck, Unit: "a", Severity: SevWarn, State: StateFiring, Since: since}}, t0)
	e.Poll(t0, []telemetry.Status{st("a", true, f32(20), t0)}, states("a", "stuck"))
	if a := find(e, "unit_stuck.a"); a == nil || a.State != StateFiring || !a.Since.Equal(since) {
		t.Fatalf("stuck alert after restart: %+v", a)
	}
}

func TestRestoreDropsResolvedAlertsPastTheirKeepTime(t *testing.T) {
	e := NewEngine(Defaults(), t0)
	old := t0.Add(-time.Hour)
	e.Restore([]Alert{{ID: "unit_died.a", Rule: RuleDied, Unit: "a", Severity: SevCrit, State: StateResolved, Since: old, ResolvedAt: &old}}, t0)
	if n := len(e.List()); n != 0 {
		t.Fatalf("%d stale resolved alerts restored", n)
	}
}

func TestPersistRestoresThenSavesChanges(t *testing.T) {
	st0 := &memStore{saved: died(t).List()}
	e := NewEngine(Defaults(), t0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Persist(ctx, e, st0, logging.New("error")); close(done) }()

	waitFor(t, func() bool { return find(e, "unit_died.a") != nil })
	if !e.Ack("unit_died.a") {
		t.Fatal("restored alert cannot be acknowledged")
	}
	waitFor(t, func() bool {
		saved, _ := st0.snapshot()
		return len(saved) == 1 && saved[0].Acked
	})
	cancel()
	<-done
}

func TestPersistKeepsRunningWhileTheStoreIsDown(t *testing.T) {
	down := &memStore{err: errors.New("connection refused")}
	e := died(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Persist(ctx, e, down, logging.New("error")); close(done) }()
	e.Ack("unit_died.a")
	time.Sleep(persistDelay + 200*time.Millisecond)
	if a := find(e, "unit_died.a"); !a.Acked {
		t.Fatal("alerts stopped working with the store down")
	}
	down.mu.Lock()
	down.err = nil
	down.mu.Unlock()
	e.Mute("unit_died.a", time.Minute, t0)
	waitFor(t, func() bool {
		saved, _ := down.snapshot()
		return len(saved) == 1 && saved[0].Acked && saved[0].MutedUntil != nil
	})
	cancel()
	<-done
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestPostgresStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	c := context.Background()
	pool, err := pgxpool.New(c, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sql, err := os.ReadFile("../../migrations/V13__minecraft_crew_alerts.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`CREATE SCHEMA IF NOT EXISTS minecraft`, string(sql), `TRUNCATE ` + alertTable} {
		if _, err := pool.Exec(c, stmt); err != nil {
			t.Fatal(err)
		}
	}
	s := NewPostgres(pool)

	e := died(t)
	e.Poll(t0, []telemetry.Status{st("b", true, f32(1), t0)}, states("b", "idle"))
	e.Ack("unit_died.a")
	e.Mute("unit_died.a", 30*time.Minute, t0)
	if err := s.Save(c, e.List()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(c)
	if err != nil {
		t.Fatal(err)
	}
	var firing int
	for _, a := range got {
		if a.ID == "unit_died.a" {
			if !a.Acked || a.MutedUntil == nil || !a.Since.Equal(t0) || a.State != StateFiring {
				t.Errorf("round trip: %+v", a)
			}
		}
		if a.State == StateFiring {
			firing++
		}
	}
	if len(got) != len(e.List()) || firing == 0 {
		t.Fatalf("loaded %d, engine holds %d", len(got), len(e.List()))
	}

	// An alert that left the list leaves the table; an empty list empties it.
	if err := s.Save(c, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load(c); err != nil || len(got) != 0 {
		t.Fatalf("after an empty save: %d rows, %v", len(got), err)
	}
	// A restart reads back the same state through the engine.
	if err := s.Save(c, e.List()); err != nil {
		t.Fatal(err)
	}
	saved, _ := s.Load(c)
	again := NewEngine(Defaults(), t0)
	again.Restore(saved, t0)
	if a := find(again, "unit_died.a"); a == nil || !a.Acked {
		t.Fatalf("restored through postgres: %+v", a)
	}
}
