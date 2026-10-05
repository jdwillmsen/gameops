package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testScriptPayload = `{"gen":417,"tick":8340,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,"items":[]}`

func scriptLine(payload string) string {
	return "[2026-10-05 12:00:00:123 INFO] [Scripting] MCMAP1 " + payload
}

func numberedScriptLine(n int) string {
	return scriptLine(fmt.Sprintf(`{"gen":%d}`, n))
}

func scriptResult(result string) float64 {
	return testutil.ToFloat64(metricScriptRecords.WithLabelValues(result))
}

// waitForScriptWaiters blocks until exactly n callers are parked in Wait, so
// a test acts on a waiter that is really waiting rather than one about to.
func waitForScriptWaiters(t *testing.T, l *ScriptLog, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(l.waiters) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d waiters parked, want %d", len(l.waiters), n)
		}
		time.Sleep(time.Millisecond)
	}
}

type scriptWaitResult struct {
	records []ScriptRecord
	gap     bool
	err     error
}

func waitInBackground(ctx context.Context, l *ScriptLog, since int64, wait time.Duration) <-chan scriptWaitResult {
	done := make(chan scriptWaitResult, 1)
	go func() {
		records, gap, err := l.Wait(ctx, since, wait)
		done <- scriptWaitResult{records, gap, err}
	}()
	return done
}

func TestScriptLogRecognisesOnlyOurSentinel(t *testing.T) {
	l := NewScriptLog()
	now := time.Now()

	foreign := []string{
		"[2026-10-05 12:00:00:123 INFO] [Scripting] another pack says hello",
		"[2026-10-05 12:00:00:123 INFO] [Scripting] MCMAP2 " + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] [Scripting] MCMAP1" + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] [Scripting] mcmap1 " + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] MCMAP1 " + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] [Scripting] relayed: [Scripting] MCMAP1 " + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] Player connected: [Scripting] MCMAP1 " + testScriptPayload + ", xuid: 111",
		"[2026-10-05 12:00:00:123 INFO] [Scripting] echo: " + scriptLine(testScriptPayload),
		"[2026-10-05 12:00:00:123 INFO] Player connected: [x] [Scripting] MCMAP1 " + testScriptPayload + ", xuid: 111",
		"[Scripting] MCMAP1 " + testScriptPayload,
		"MCMAP1 " + testScriptPayload,
		"[2026-10-05 12:00:00:123 INFO] Player connected: Steve, xuid: 111",
	}
	for _, line := range foreign {
		if l.Ingest(line, now) {
			t.Errorf("Ingest claimed a line that is not a record: %q", line)
		}
	}
	if got := l.Since(0); len(got) != 0 {
		t.Fatalf("foreign lines stored %d records, want 0: %+v", len(got), got)
	}

	if !l.Ingest(scriptLine(testScriptPayload), now) {
		t.Fatal("Ingest did not claim a record line")
	}
	got := l.Since(0)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if string(got[0].Data) != testScriptPayload {
		t.Errorf("data = %s, want the payload after the sentinel", got[0].Data)
	}
	if !got[0].At.Equal(now) {
		t.Errorf("at = %v, want the receive time %v", got[0].At, now)
	}
	if got[0].ID != 1 {
		t.Errorf("id = %d, want 1 for the first record", got[0].ID)
	}
}

func TestScriptLogToleratesALeadingNul(t *testing.T) {
	l := NewScriptLog()
	okBefore, nulBefore := scriptResult("ok"), scriptResult("nul_stripped")

	l.Ingest("\x00"+scriptLine(testScriptPayload), time.Now())

	got := l.Since(0)
	if len(got) != 1 {
		t.Fatalf("got %d records, want the NUL-prefixed line stored", len(got))
	}
	if string(got[0].Data) != testScriptPayload {
		t.Errorf("data = %q, want the payload without the NUL", got[0].Data)
	}
	if d := scriptResult("nul_stripped") - nulBefore; d != 1 {
		t.Errorf("nul_stripped grew by %v, want 1", d)
	}
	if d := scriptResult("ok") - okBefore; d != 0 {
		t.Errorf("ok grew by %v, want 0: one line has one result", d)
	}
}

func TestScriptLogDropsWhatItCannotServe(t *testing.T) {
	l := NewScriptLog()
	oversizeBefore, unparseableBefore := scriptResult("oversize"), scriptResult("unparseable")

	oversize := `{"n":"` + strings.Repeat("x", scriptMaxRecordBytes) + `"}`
	unparseable := []string{
		`{"gen":417,"items":[`,
		`"a string, not an object"`,
		`[1,2,3]`,
		``,
	}

	if !l.Ingest(scriptLine(oversize), time.Now()) {
		t.Error("an oversize record line was not claimed as ours")
	}
	for _, payload := range unparseable {
		if !l.Ingest(scriptLine(payload), time.Now()) {
			t.Errorf("an unparseable record line was not claimed as ours: %q", payload)
		}
	}

	if got := l.Since(0); len(got) != 0 {
		t.Fatalf("stored %d records, want 0: %+v", len(got), got)
	}
	if d := scriptResult("oversize") - oversizeBefore; d != 1 {
		t.Errorf("oversize grew by %v, want 1", d)
	}
	if d := scriptResult("unparseable") - unparseableBefore; d != float64(len(unparseable)) {
		t.Errorf("unparseable grew by %v, want %d", d, len(unparseable))
	}
}

func TestScriptLogRecordsTheLastReceiveTime(t *testing.T) {
	l := NewScriptLog()
	at := time.Date(2026, 10, 5, 12, 0, 0, 500_000_000, time.UTC)

	l.Ingest(scriptLine(testScriptPayload), at)

	if got, want := testutil.ToFloat64(metricScriptLastRecord), float64(at.UnixMilli())/1000; got != want {
		t.Errorf("last record timestamp = %v, want %v", got, want)
	}
}

func TestScriptLogRingDropsOldest(t *testing.T) {
	l := NewScriptLog()
	const extra = 10
	for i := 1; i <= scriptLogCapacity+extra; i++ {
		l.Ingest(numberedScriptLine(i), time.Now())
	}

	got := l.Since(0)
	if len(got) != scriptLogCapacity {
		t.Fatalf("retained %d records, want %d", len(got), scriptLogCapacity)
	}
	if got[0].ID != extra+1 {
		t.Errorf("oldest retained id = %d, want %d", got[0].ID, extra+1)
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID != got[i-1].ID+1 {
			t.Fatalf("ids not consecutive at %d: %d then %d", i, got[i-1].ID, got[i].ID)
		}
	}
	if last := got[len(got)-1]; string(last.Data) != fmt.Sprintf(`{"gen":%d}`, scriptLogCapacity+extra) {
		t.Errorf("newest record = %s, want the last one ingested", last.Data)
	}
}

func TestScriptLogSinceReturnsOnlyNewer(t *testing.T) {
	l := NewScriptLog()
	for i := 1; i <= 3; i++ {
		l.Ingest(numberedScriptLine(i), time.Now())
	}

	records, gap, err := l.Wait(context.Background(), 1, 0)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if gap {
		t.Error("gap = true with nothing lost")
	}
	if len(records) != 2 || records[0].ID != 2 || records[1].ID != 3 {
		t.Errorf("records = %+v, want ids 2 and 3", records)
	}
}

func TestScriptLogFlagsAGap(t *testing.T) {
	l := NewScriptLog()
	for i := 1; i <= scriptLogCapacity+10; i++ {
		l.Ingest(numberedScriptLine(i), time.Now())
	}
	oldest := l.Since(0)[0].ID

	for _, tc := range []struct {
		name    string
		since   int64
		wantGap bool
		wantLen int
	}{
		{"no cursor", 0, false, scriptLogCapacity},
		{"cursor behind the ring", oldest - 2, true, scriptLogCapacity},
		{"cursor at the edge of the ring", oldest - 1, false, scriptLogCapacity},
		{"cursor inside the ring", oldest, false, scriptLogCapacity - 1},
		{"cursor from an earlier process", oldest + 100_000, true, scriptLogCapacity},
	} {
		records, gap, err := l.Wait(context.Background(), tc.since, 0)
		if err != nil {
			t.Fatalf("%s: Wait: %v", tc.name, err)
		}
		if gap != tc.wantGap {
			t.Errorf("%s: gap = %v, want %v", tc.name, gap, tc.wantGap)
		}
		if len(records) != tc.wantLen {
			t.Errorf("%s: got %d records, want %d", tc.name, len(records), tc.wantLen)
		}
	}
}

// A caller holding a cursor from before a bridge restart must not be left
// waiting for an id the new process will take hours to reach.
func TestScriptLogWakesACursorFromAnEarlierProcess(t *testing.T) {
	l := NewScriptLog()
	done := waitInBackground(context.Background(), l, 5000, 5*time.Second)
	waitForScriptWaiters(t, l, 1)

	l.Ingest(scriptLine(testScriptPayload), time.Now())

	select {
	case got := <-done:
		if len(got.records) != 1 || !got.gap {
			t.Errorf("records = %d, gap = %v; want the new record flagged as a gap", len(got.records), got.gap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cursor ahead of the ring never saw the new record")
	}
}

func TestScriptLogWakesAWaiter(t *testing.T) {
	l := NewScriptLog()
	l.Ingest(numberedScriptLine(1), time.Now())

	done := waitInBackground(context.Background(), l, 1, 5*time.Second)
	waitForScriptWaiters(t, l, 1)
	select {
	case got := <-done:
		t.Fatalf("Wait returned with nothing new: %+v", got)
	default:
	}

	l.Ingest(numberedScriptLine(2), time.Now())

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Wait: %v", got.err)
		}
		if len(got.records) != 1 || got.records[0].ID != 2 {
			t.Errorf("records = %+v, want only id 2", got.records)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter was not woken by a new record")
	}
}

func TestScriptLogWaitReturnsEmptyWhenTheWaitElapses(t *testing.T) {
	l := NewScriptLog()

	start := time.Now()
	records, gap, err := l.Wait(context.Background(), 0, 40*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(records) != 0 || gap {
		t.Errorf("records = %+v, gap = %v; want nothing", records, gap)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("returned after %v, want the full wait", elapsed)
	}
	if len(l.waiters) != 0 {
		t.Errorf("%d waiter slots still held after the wait ended", len(l.waiters))
	}
}

func TestScriptLogWaitEndsWithItsContext(t *testing.T) {
	l := NewScriptLog()
	ctx, cancel := context.WithCancel(context.Background())
	done := waitInBackground(ctx, l, 0, 30*time.Second)
	waitForScriptWaiters(t, l, 1)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait outlived its context")
	}
	waitForScriptWaiters(t, l, 0)
}

func TestScriptLogRefusesWaitersPastTheCap(t *testing.T) {
	l := NewScriptLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for range scriptMaxWaiters {
		waitInBackground(ctx, l, 0, 30*time.Second)
	}
	waitForScriptWaiters(t, l, scriptMaxWaiters)

	if _, _, err := l.Wait(ctx, 0, 30*time.Second); !errors.Is(err, errScriptWaitersFull) {
		t.Fatalf("Wait past the cap = %v, want errScriptWaitersFull", err)
	}
	// A call that would not block holds no slot, so the cap must not refuse it.
	if _, _, err := l.Wait(ctx, 0, 0); err != nil {
		t.Errorf("a non-blocking read past the cap = %v, want it served", err)
	}

	cancel()
	waitForScriptWaiters(t, l, 0)
	if _, _, err := l.Wait(context.Background(), 0, time.Millisecond); err != nil {
		t.Errorf("Wait after the waiters left = %v, want a free slot", err)
	}
}

func TestScriptLogCloseReleasesWaiters(t *testing.T) {
	l := NewScriptLog()
	done := waitInBackground(context.Background(), l, 0, 30*time.Second)
	waitForScriptWaiters(t, l, 1)

	l.Close()

	select {
	case got := <-done:
		if got.err != nil {
			t.Errorf("Wait after Close = %v, want a plain empty answer", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close left a waiter parked")
	}

	start := time.Now()
	if _, _, err := l.Wait(context.Background(), 0, 30*time.Second); err != nil {
		t.Errorf("Wait on a closed log = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait on a closed log took %v, want an immediate return", elapsed)
	}
}

func TestScriptLogIsSafeConcurrently(t *testing.T) {
	l := NewScriptLog()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readers := make(chan struct{})
	for range scriptMaxWaiters {
		go func() {
			defer func() { readers <- struct{}{} }()
			var since int64
			for ctx.Err() == nil {
				records, _, _ := l.Wait(ctx, since, 10*time.Millisecond)
				for _, r := range records {
					if r.ID <= since {
						t.Errorf("id %d served again after %d", r.ID, since)
					}
					since = r.ID
				}
			}
		}()
	}
	for i := range 2000 {
		l.Ingest(numberedScriptLine(i), time.Now())
	}
	cancel()
	for range scriptMaxWaiters {
		<-readers
	}
}
