package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

func newTestWriter(t *testing.T, f *fakeStore) *Writer {
	t.Helper()
	l := &Lazy{}
	l.Set(f)
	w := NewWriter(l, logging.New("error"))
	w.every, w.max = 5*time.Millisecond, 3
	return w
}

func recorded(t *testing.T, f *fakeStore) []Event {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events
}

func TestWriterBatchesAndFlushes(t *testing.T) {
	f := &fakeStore{}
	w := newTestWriter(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	// Five events at a max of three per batch: whatever the timing, none is
	// lost and none arrives twice.
	for i := 0; i < 5; i++ {
		w.Add(Event{Kind: "died", Severity: SevInfo}, Hint{})
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(recorded(t, f)) < 5 {
		if time.Now().After(deadline) {
			t.Fatal("writer never flushed the events")
		}
		time.Sleep(time.Millisecond)
	}
	if got := recorded(t, f); len(got) != 5 {
		t.Fatalf("recorded %d, want 5", len(got))
	}

	// A quiet stream still lands within one flush interval, not only when a
	// batch fills.
	w.Add(Event{Kind: "stuck", Severity: SevInfo}, Hint{})
	deadline = time.Now().Add(2 * time.Second)
	for len(recorded(t, f)) < 6 {
		if time.Now().After(deadline) {
			t.Fatal("a single event waited for a batch it was never given")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

// A store that answers unavailable loses its batch; the writer keeps going
// rather than stopping, because history is best effort by design.
func TestWriterDropsWhatTheStoreRefuses(t *testing.T) {
	f := &fakeStore{}
	w := newTestWriter(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	f.mu.Lock()
	f.err = errors.New("down")
	f.mu.Unlock()
	w.Add(Event{Kind: "died", Severity: SevInfo}, Hint{})
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()

	w.Add(Event{Kind: "stuck", Severity: SevInfo}, Hint{})
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		n := len(f.events)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded %d, want the 1 event the store took", n)
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	kind := f.events[0].Kind
	f.mu.Unlock()
	if kind != "stuck" {
		t.Fatalf("kept %s, want the event from after the outage", kind)
	}
	cancel()
	<-done
}

// An idle haul's start and finish never reach the store; they come out as
// one roll-up when the unit's next real event closes it.
func TestWriterCollapsesIdlePolls(t *testing.T) {
	f := &fakeStore{}
	w := newTestWriter(t, f)
	w.max = 64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	at := time.Now()
	for i := range 10 {
		task := "t" + string(rune('a'+i))
		w.Add(Event{Unit: "crew-03", At: at, Kind: "task_started", Severity: SevInfo}, Hint{Task: task, Template: "haul"})
		w.Add(Event{Unit: "crew-03", At: at.Add(time.Second), Kind: "task_done", Severity: SevOK}, Hint{Task: task, Template: "haul", Took: 200 * time.Millisecond})
	}
	w.Add(Event{Unit: "crew-03", At: at.Add(30 * time.Second), Kind: "died", Severity: SevCrit}, Hint{})

	deadline := time.Now().Add(2 * time.Second)
	for len(recorded(t, f)) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("recorded %d, want the roll-up and the death", len(recorded(t, f)))
		}
		time.Sleep(time.Millisecond)
	}
	got := recorded(t, f)
	if len(got) != 2 || got[0].Kind != KindIdleRollup || got[1].Kind != "died" {
		t.Fatalf("recorded %+v, want one roll-up then died", got)
	}
	cancel()
	<-done
}

// A shutdown must not eat the roll-up still open or the events still queued.
func TestWriterFlushesOpenRollupOnShutdown(t *testing.T) {
	f := &fakeStore{}
	w := newTestWriter(t, f)
	w.every = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	at := time.Now()
	w.Add(Event{Unit: "crew-03", At: at, Kind: "task_done", Severity: SevOK}, Hint{Task: "a", Template: "haul", Took: time.Millisecond})
	w.Add(Event{Unit: "crew-04", At: at, Kind: "died", Severity: SevCrit}, Hint{})
	cancel()
	<-done
	got := recorded(t, f)
	kinds := map[string]bool{}
	for _, e := range got {
		kinds[e.Kind] = true
	}
	if len(got) != 2 || !kinds[KindIdleRollup] || !kinds["died"] {
		t.Fatalf("recorded %+v, want the roll-up and the death", got)
	}
}

// An unknown severity would fail the whole batch at the table's CHECK.
func TestWriterMendsUnknownSeverity(t *testing.T) {
	f := &fakeStore{}
	w := newTestWriter(t, f)
	w.Add(Event{Kind: "died", Severity: "loud"}, Hint{})
	if q := <-w.ch; q.e.Severity != SevInfo {
		t.Fatalf("severity %q, want info", q.e.Severity)
	}
}
