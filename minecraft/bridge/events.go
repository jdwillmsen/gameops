package main

import (
	"bufio"
	"io"
	"regexp"
	"sync"
	"time"
)

// EventType categorizes a parsed server log line.
type EventType string

const (
	EventConnect      EventType = "connect"
	EventDisconnect   EventType = "disconnect"
	EventContentError EventType = "content_error"
	EventCrash        EventType = "crash"
)

// Event is one typed occurrence parsed from server stdout.
type Event struct {
	// ID is a monotonically increasing sequence number, unique within one
	// process lifetime. Callers page with GET /events?since=<ID> rather than
	// by timestamp, since two events in the same broadcast can share a
	// timestamp.
	ID     int64     `json:"id"`
	Type   EventType `json:"type"`
	Time   time.Time `json:"time"`
	Player string    `json:"player,omitempty"`
	Raw    string    `json:"raw"`
}

// The connect/disconnect patterns match the ones the platform's Grafana
// dashboards already rely on, which are the only confirmed-stable signatures
// for these events in Bedrock's console output.
var (
	connectRe    = regexp.MustCompile(`Player connected: (?P<player>[^,]+),`)
	disconnectRe = regexp.MustCompile(`Player disconnected: (?P<player>[^,]+),`)

	// contentErrorRe matches lines emitted when
	// content-log-console-output-enabled=true surfaces pack/content errors.
	// This is a best-effort pattern based on the documented log level tag;
	// it has not been validated against a real content-log error and should
	// be refined once one is observed in Stage 2 hardening.
	contentErrorRe = regexp.MustCompile(`^\[.*\]\s*\[ERROR\]`)

	// crashRe is deliberately narrow: it has not been validated against the
	// open upstream join crash (a std::length_error abort) because that crash
	// was not reproduced during this scaffold. Treat crash detection as a
	// placeholder to be tightened against a real crash log.
	crashRe = regexp.MustCompile(`(?i)(unhandled exception|fatal error|terminate called)`)
)

// ParseEvents scans r line by line and returns every recognized event. It is
// tolerant of unrecognized lines (they are simply skipped, not errors).
func ParseEvents(r io.Reader) ([]Event, error) {
	var events []Event
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if e, ok := parseLine(line); ok {
			events = append(events, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

// eventLogCapacity bounds how many recognized events EventLog retains. Once
// full, the oldest events are dropped - a caller polling GET /events?since=
// on any reasonable interval never approaches this, but a bound keeps a
// stalled bridge (no one polling) from growing memory unbounded.
const eventLogCapacity = 2000

// EventLog is a bounded, ID-ordered buffer of parsed events, fed directly
// from the console websocket's own stdout/stderr/logHistory broadcasts (see
// console.go) rather than a separate log-tailing mechanism - the websocket
// connection the bridge already holds for commands is also the event
// source, since mc-server-runner pushes every console log line over it.
type EventLog struct {
	mu     sync.Mutex
	nextID int64
	events []Event
}

// NewEventLog builds an empty EventLog.
func NewEventLog() *EventLog {
	return &EventLog{}
}

// Ingest parses a single raw console line and, if it matches a recognized
// pattern, appends it to the log with a freshly assigned ID. Unrecognized
// lines are silently dropped, matching ParseEvents' tolerance.
func (l *EventLog) Ingest(raw string, receivedAt time.Time) {
	e, ok := parseLine(raw)
	if !ok {
		return
	}
	e.Time = receivedAt

	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	e.ID = l.nextID
	l.events = append(l.events, e)
	if len(l.events) > eventLogCapacity {
		l.events = l.events[len(l.events)-eventLogCapacity:]
	}
}

// Since returns every retained event with ID > sinceID, oldest first. A
// sinceID older than the log's retained window simply returns everything
// still retained - callers cannot distinguish "nothing new" from "some
// events aged out"; that's an accepted limit of a bounded in-memory log.
func (l *EventLog) Since(sinceID int64) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]Event, 0, len(l.events))
	for _, e := range l.events {
		if e.ID > sinceID {
			out = append(out, e)
		}
	}
	return out
}

func parseLine(line string) (Event, bool) {
	if m := connectRe.FindStringSubmatch(line); m != nil {
		return Event{Type: EventConnect, Player: m[1], Raw: line}, true
	}
	if m := disconnectRe.FindStringSubmatch(line); m != nil {
		return Event{Type: EventDisconnect, Player: m[1], Raw: line}, true
	}
	if crashRe.MatchString(line) {
		return Event{Type: EventCrash, Raw: line}, true
	}
	if contentErrorRe.MatchString(line) {
		return Event{Type: EventContentError, Raw: line}, true
	}
	return Event{}, false
}
