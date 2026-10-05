package live

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Layer is the live picture and the browsers watching it.
type Layer struct {
	Store *Store
	Hub   *Hub
	// Logger records what the heartbeat says of the pack. Nil discards it.
	Logger *slog.Logger

	flush sync.Mutex

	mu        sync.Mutex
	heartbeat time.Time
	interval  float64
}

// New builds a Layer that draws nothing older than ttl and at most max
// entities of each kind per dimension.
func New(ttl time.Duration, max int, logger *slog.Logger) *Layer {
	return &Layer{Store: &Store{TTL: ttl, Max: max}, Hub: &Hub{}, Logger: logger}
}

// RawRecord is a record as the bridge hands it over.
type RawRecord struct {
	// ID orders records within one run of the bridge and restarts with it.
	ID int64
	// At is when the bridge received the line.
	At time.Time
	// Data is the record's JSON. The bridge has already removed the
	// sentinel; a whole console line is read too.
	Data string
}

// Ingest takes one record from the bridge, read at now.
func (l *Layer) Ingest(raw RawRecord, now time.Time) {
	rec, stripped, err := Parse(raw.Data)
	if stripped {
		metricFrames.WithLabelValues(resultNulStripped).Inc()
	}
	if err != nil {
		metricFrames.WithLabelValues(resultUnparseable).Inc()
		return
	}
	at := raw.At
	// The bridge's clock is trusted for age, but not to be ahead of this
	// one: a record from the future would outlive its welcome by the skew.
	if at.IsZero() || at.After(now) {
		at = now
	}

	if rec.Kind == Tick {
		l.mu.Lock()
		l.beat(rec, at)
		l.mu.Unlock()
	}

	metricFrames.WithLabelValues(l.Store.Apply(rec, at)).Inc()
	metricLastFrame.Set(float64(at.UnixMilli()) / 1000)
	metricIngestLag.Observe(now.Sub(at).Seconds())
	if rec.SentMillis > 0 {
		metricLogLag.Observe(max(0, at.Sub(time.UnixMilli(rec.SentMillis)).Seconds()))
	}
}

// beat publishes what the pack says of itself. A changed interval is the
// pack slowing down, or recovering, to protect the server's tick rate, and
// is the one thing here worth a log line.
func (l *Layer) beat(rec Record, at time.Time) {
	if !l.heartbeat.IsZero() {
		metricFrameInterval.Observe(at.Sub(l.heartbeat).Seconds())
	}
	l.heartbeat = at
	metricPackScan.Set(rec.ScanMillis / 1000)
	metricPackInterval.Set(rec.IntervalMillis / 1000)
	if rec.IntervalMillis != l.interval && l.interval != 0 && l.Logger != nil {
		l.Logger.Warn("live pack changed its sampling interval", "was_ms", l.interval, "now_ms", rec.IntervalMillis, "scan_ms", rec.ScanMillis)
	}
	l.interval = rec.IntervalMillis
}

// Flush sends a frame to the browsers watching each dimension that has
// changed since the last call, including one that has just gone stale.
func (l *Layer) Flush(now time.Time) {
	// Records arriving and lists going stale are noticed on different
	// goroutines. Without this, one could read the store first and publish
	// last, leaving browsers on the older frame until the next sample.
	l.flush.Lock()
	defer l.flush.Unlock()
	for dimension, f := range l.Store.Changed(now) {
		l.Hub.Publish(dimension, l.message(f, now))
	}
}

// Current is the frame a browser gets on connecting, so it does not wait
// for the next sample to draw anything.
func (l *Layer) Current(dimension string, now time.Time) Message {
	return l.message(l.Store.Snapshot(dimension, now), now)
}

// Subscribe starts delivering a dimension's frames; false means the hub is
// full.
func (l *Layer) Subscribe(dimension string) (*Subscription, bool) {
	return l.Hub.Subscribe(dimension)
}

type frameJSON struct {
	At *time.Time `json:"at,omitempty"`
	// ServerNow lets the page correct its own clock before it works out how
	// old a frame is.
	ServerNow   time.Time `json:"serverNow"`
	Players     []Entity  `json:"players"`
	Mobs        []Entity  `json:"mobs"`
	More        int       `json:"more"`
	MorePlayers int       `json:"morePlayers,omitempty"`
	Stale       bool      `json:"stale"`
	// TTLSeconds is how long the page may go on drawing this frame if no
	// other arrives.
	TTLSeconds float64 `json:"ttlSeconds"`
}

func (l *Layer) message(f Frame, now time.Time) Message {
	out := frameJSON{ServerNow: now.UTC().Truncate(time.Millisecond), Players: f.Players, Mobs: f.Mobs,
		More: f.More, MorePlayers: f.MorePlayers, Stale: f.Stale, TTLSeconds: l.Store.TTL.Seconds()}
	if !f.At.IsZero() {
		at := f.At.UTC().Truncate(time.Millisecond)
		out.At = &at
	}
	// A list with nothing in it is still a list to the page.
	if out.Players == nil {
		out.Players = []Entity{}
	}
	if out.Mobs == nil {
		out.Mobs = []Entity{}
	}
	data, _ := json.Marshal(out)
	return Message{Data: data, Ready: time.Now()}
}
