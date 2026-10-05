package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricScriptRecords = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mc_console_bridge_script_records_total",
		Help: "Live console lines carrying the map pack's record sentinel, by what became of them: stored as they came (ok), stored after a leading NUL was removed (nul_stripped), or dropped for being over the size cap (oversize) or not a JSON object (unparseable).",
	}, []string{"result"})
	metricScriptLastRecord = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mc_console_bridge_script_last_record_timestamp_seconds",
		Help: "When the bridge received the latest script record it stored.",
	})
)

const (
	// scriptLogCapacity is sized to the pack's worst case, not its usual one:
	// with every dimension at its mob cap one sample is about 50 records, so
	// this holds roughly five samples, enough for the reader to miss a poll
	// without missing a sample.
	scriptLogCapacity = 256

	// scriptMaxRecordBytes bounds one stored payload, and with the capacity
	// the ring's memory. The pack keeps its records under 3,500 bytes because
	// the server corrupts the line after one longer than 4 KB, so anything
	// this long is a pack fault and its JSON is likely cut short anyway.
	scriptMaxRecordBytes = 4096

	// scriptMaxWaiters bounds the requests parked in Wait. Each is a held
	// connection and goroutine on a sidecar whose HTTP server has no
	// connection cap of its own. The map is one; the rest is room for its
	// restart overlap and for a person with curl.
	scriptMaxWaiters = 4
)

// scriptRecordRe matches one record from the map's script pack: the server's
// log prefix, the tag it puts on everything a script prints, then the pack's
// sentinel, which is also its format version. It is anchored at the start of
// the line so text further along, which a script or a player's name can
// control, cannot pass for a record. Requiring the sentinel is what keeps
// another pack's output, and a later format of this one, out of the ring.
var scriptRecordRe = regexp.MustCompile(`^\[[^\]]*\] \[Scripting\] MCMAP1 (.*)$`)

// scriptPayload returns what follows the sentinel on a record line. The
// server starts the line after an over-long one with a NUL byte; that line is
// intact past the NUL, so it is skipped rather than costing a second record.
func scriptPayload(raw string) (payload string, nulStripped, ok bool) {
	line := strings.TrimLeft(raw, "\x00")
	m := scriptRecordRe.FindStringSubmatch(line)
	if m == nil {
		return "", false, false
	}
	return strings.TrimSpace(m[1]), len(line) != len(raw), true
}

// isScriptRecord reports whether a console line carries the record sentinel,
// whatever the state of its payload.
func isScriptRecord(raw string) bool {
	_, _, ok := scriptPayload(raw)
	return ok
}

// ScriptRecord is one record line from the map's script pack.
type ScriptRecord struct {
	// ID increases by one per stored record and is unique within one process
	// lifetime. It restarts at 1 with the bridge.
	ID int64 `json:"id"`
	// At is when the bridge received the line, not when the server printed
	// it: the server's own stamp is not parsed.
	At time.Time `json:"at"`
	// Data is the JSON object after the sentinel, passed through unread.
	Data json.RawMessage `json:"data"`
}

// ScriptLog is a small ring of the latest script records with a way to wait
// for the next one.
//
// It is deliberately not part of EventLog, though both are fed from the same
// console lines. The event ring is the agent's roster feed: it holds joins
// and leaves for as long as possible so a slow poller misses none. Records
// arrive every second whether or not anyone is online, and through that ring
// they would push every join out of it within the hour and ride along on
// every roster poll. Here an old record is worthless as soon as a newer
// sample exists, so the ring is short and losing its tail is normal.
type ScriptLog struct {
	mu      sync.Mutex
	nextID  int64
	records []ScriptRecord
	// changed is closed and replaced on every append, so any number of
	// waiters wake on one record without the appender tracking them.
	changed chan struct{}
	closed  bool

	waiters chan struct{}
}

// NewScriptLog builds an empty ScriptLog.
func NewScriptLog() *ScriptLog {
	return &ScriptLog{
		changed: make(chan struct{}),
		waiters: make(chan struct{}, scriptMaxWaiters),
	}
}

// Ingest stores raw if it is a record line and reports whether it was one,
// stored or not: a record the ring refuses is still not a line for anything
// else to interpret.
func (l *ScriptLog) Ingest(raw string, receivedAt time.Time) bool {
	payload, nulStripped, ok := scriptPayload(raw)
	if !ok {
		return false
	}
	switch {
	case len(payload) > scriptMaxRecordBytes:
		metricScriptRecords.WithLabelValues("oversize").Inc()
		return true
	case !strings.HasPrefix(payload, "{") || !json.Valid([]byte(payload)):
		// Checked here because Data is written into the response as it is:
		// one malformed record would make the whole response undecodable.
		metricScriptRecords.WithLabelValues("unparseable").Inc()
		return true
	}

	l.mu.Lock()
	l.nextID++
	if len(l.records) == scriptLogCapacity {
		l.records = append(l.records[:0], l.records[1:]...)
	}
	l.records = append(l.records, ScriptRecord{ID: l.nextID, At: receivedAt, Data: json.RawMessage(payload)})
	l.wakeLocked()
	l.mu.Unlock()

	result := "ok"
	if nulStripped {
		result = "nul_stripped"
	}
	metricScriptRecords.WithLabelValues(result).Inc()
	metricScriptLastRecord.Set(float64(receivedAt.UnixMilli()) / 1000)
	return true
}

func (l *ScriptLog) wakeLocked() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// Since returns every retained record with ID > sinceID, oldest first.
func (l *ScriptLog) Since(sinceID int64) []ScriptRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	records, _ := l.sinceLocked(sinceID)
	return records
}

// sinceLocked also reports a gap: records the caller's cursor says it has not
// seen and that are no longer here to send. No cursor (zero) is never a gap.
//
// A cursor ahead of every id handed out can only have come from an earlier
// process, since ids restart with the bridge. It is answered as a gap from
// the start of the ring, so a restart costs the caller one flagged response
// and not a wait for ids to climb back to where it left off.
func (l *ScriptLog) sinceLocked(sinceID int64) (records []ScriptRecord, gap bool) {
	if sinceID > l.nextID {
		gap, sinceID = true, 0
	} else if sinceID > 0 && len(l.records) > 0 && l.records[0].ID > sinceID+1 {
		gap = true
	}
	records = make([]ScriptRecord, 0, len(l.records))
	for _, r := range l.records {
		if r.ID > sinceID {
			records = append(records, r)
		}
	}
	return records, gap
}

var errScriptWaitersFull = errors.New("too many callers already waiting for script records")

// Wait returns the records after sinceID at once if there are any. Otherwise
// it parks until one arrives, wait elapses, ctx ends or the log is closed,
// and then returns what there is, which may be nothing. Only a call that has
// to park counts against scriptMaxWaiters; past it Wait returns
// errScriptWaitersFull without waiting.
func (l *ScriptLog) Wait(ctx context.Context, sinceID int64, wait time.Duration) ([]ScriptRecord, bool, error) {
	records, gap, changed, closed := l.poll(sinceID)
	if len(records) > 0 || closed || wait <= 0 {
		return records, gap, nil
	}

	select {
	case l.waiters <- struct{}{}:
		defer func() { <-l.waiters }()
	default:
		return nil, false, errScriptWaitersFull
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-changed:
		case <-timer.C:
			return records, gap, nil
		case <-ctx.Done():
			return records, gap, nil
		}
		records, gap, changed, closed = l.poll(sinceID)
		if len(records) > 0 || closed {
			return records, gap, nil
		}
	}
}

// poll reads the records together with the channel that announces the next
// change, under one lock, so a record appended between a reader's look and
// its wait still wakes it.
func (l *ScriptLog) poll(sinceID int64) (records []ScriptRecord, gap bool, changed <-chan struct{}, closed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	records, gap = l.sinceLocked(sinceID)
	return records, gap, l.changed, l.closed
}

// Close releases every parked waiter and makes later calls return without
// parking. A graceful HTTP shutdown waits for handlers but does not cancel
// them, so without this each waiter would hold the shutdown for its full
// wait.
func (l *ScriptLog) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.wakeLocked()
}
