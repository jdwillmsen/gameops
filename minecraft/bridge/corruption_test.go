package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The exact lines Bedrock 1.26.52 printed when the world lost table files on
// 2026-10-02, and when it found corruption at run time in August.
var corruptionLines = []string{
	"[2026-10-02 01:55:02:629 WARN] LevelDB worlds/FWB/db status NOT OK(Corruption: 25 missing files; e.g.: worlds/FWB/db/3606723.ldb). Trying repair.",
	"[2026-08-19 04:14:58:120 ERROR] Level corruption detected, disconnecting clients and shutting down server",
}

// resetCorruption clears the gauges between tests; the metrics are
// process-wide. The counter cannot go back, so tests compare its change.
func resetCorruption() {
	metricCorruptionDetected.Set(0)
	metricCorruptionSeenAt.Set(0)
	corruptionCounted.Lock()
	clear(corruptionCounted.lines)
	corruptionCounted.latest = time.Time{}
	corruptionCounted.Unlock()
}

func TestCorruption_ALineFromTheServerRaisesTheSignalAndItStaysRaised(t *testing.T) {
	resetCorruption()
	linesBefore := testutil.ToFloat64(metricCorruptionLines)
	log := NewEventLog()
	at := time.Date(2026, 10, 2, 1, 55, 2, 0, time.UTC)

	log.Ingest("[2026-10-02 01:55:02:419 INFO] Opening level 'worlds/FWB/db'", at)
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 0 {
		t.Fatalf("detected before any corruption line = %v", got)
	}

	for i, line := range corruptionLines {
		log.Ingest(line, at.Add(time.Duration(i)*time.Second))
	}
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 1 {
		t.Errorf("detected = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metricCorruptionLines) - linesBefore; got != 2 {
		t.Errorf("lines = %v, want 2", got)
	}
	// The line's own time, which is when the server found it.
	if got := testutil.ToFloat64(metricCorruptionSeenAt); got != float64(time.Date(2026, 10, 2, 1, 55, 2, 0, time.UTC).Unix()) {
		t.Errorf("last seen = %v", time.Unix(int64(got), 0).UTC())
	}

	// A clean start afterwards does not mean the world was repaired.
	log.Ingest("[2026-10-02 01:55:40:129 INFO] Server started.", at.Add(time.Minute))
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 1 {
		t.Errorf("detected after a normal line = %v, want it to stay 1", got)
	}
}

// The repair line is printed once, at world open, which is usually just
// before the bridge's console connection comes up: the replayed history is
// often the only place it is seen.
func TestCorruption_SeenInReplayedHistory(t *testing.T) {
	resetCorruption()
	NewEventLog().IngestBackfill(corruptionLines[0], time.Now())
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 1 {
		t.Errorf("detected = %v", got)
	}
}

func TestCorruption_OrdinaryLinesDoNotRaiseIt(t *testing.T) {
	resetCorruption()
	log := NewEventLog()
	for _, line := range []string{
		"[2026-10-02 01:55:02:419 INFO] Opening level 'worlds/FWB/db'",
		"[2026-10-02 01:55:40:129 ERROR] Your current connection type is not set to NetherNet.",
		"[2026-10-02 01:56:00:000 INFO] Player connected: Steve, xuid: 1",
		"<Steve> is the world corrupted?",
		"[2026-10-02 01:56:00:000 INFO] [Chat] Steve: Corruption: 3 missing files",
	} {
		log.Ingest(line, time.Now())
	}
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 0 {
		t.Errorf("detected = %v", got)
	}
}

// mc-server-runner replays its history on every connect, so one line from
// the server arrives once live and again after every redial. It is one
// report, and an alert on the counter or the time must not see it again.
func TestCorruption_ReplayedHistoryIsNotCountedAgain(t *testing.T) {
	resetCorruption()
	before := testutil.ToFloat64(metricCorruptionLines)
	log := NewEventLog()
	log.Ingest(corruptionLines[0], time.Now())
	for range 3 {
		log.IngestBackfill(corruptionLines[0], time.Now())
	}
	if got := testutil.ToFloat64(metricCorruptionLines) - before; got != 1 {
		t.Errorf("one line counted %v times", got)
	}
	if got := testutil.ToFloat64(metricCorruptionSeenAt); got != float64(time.Date(2026, 10, 2, 1, 55, 2, 0, time.UTC).Unix()) {
		t.Errorf("last seen moved to %v", time.Unix(int64(got), 0).UTC())
	}
}

// Any status LevelDB reports as not OK is followed by the same repair, not
// only the missing-files case seen so far.
func TestCorruption_AnyNotOKStatusCounts(t *testing.T) {
	resetCorruption()
	NewEventLog().Ingest("[2026-10-04 03:00:00:000 WARN] LevelDB worlds/FWB/db status NOT OK(IO error: worlds/FWB/db/000123.ldb: Input/output error). Trying repair.", time.Now())
	if got := testutil.ToFloat64(metricCorruptionDetected); got != 1 {
		t.Errorf("detected = %v", got)
	}
}
