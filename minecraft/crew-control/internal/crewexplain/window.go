// Package crewexplain backs the offline crew-explain tool: it reads an
// exported event window, asks a local model to draft an incident summary, and
// refuses to show anything the window does not support. Nothing here runs
// inside the service.
package crewexplain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// Versions the reader understands. A file with any other value was written
// by a redaction or layout this tool has not been checked against.
const (
	SchemaVersion    = 1
	RedactionVersion = 1
)

const (
	maxFileBytes = 4 << 20
	maxEvents    = 2000
)

// Event is the allow-listed shape of one exported event. It carries no
// position; the decoder rejects any field outside this list.
type Event struct {
	ID         int64     `json:"id"`
	Unit       string    `json:"unit"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Reason     string    `json:"reason_code,omitempty"`
	Cause      int64     `json:"cause_ref,omitempty"`
	Template   string    `json:"template,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Idle       bool      `json:"idle,omitempty"`
	Anchor     string    `json:"anchor,omitempty"`
	Task       string    `json:"task,omitempty"`
}

// Window is one exported span of events.
type Window struct {
	SchemaVersion    int       `json:"schema_version"`
	RedactionVersion int       `json:"redaction_version"`
	WindowID         string    `json:"window_id"`
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	Events           []Event   `json:"events"`
}

var (
	// Values are drawn from closed vocabularies in the service, so a value
	// outside this charset is not something the exporter wrote.
	vocab  = regexp.MustCompile(`^[a-z0-9_.:-]{1,48}$`)
	opaque = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	name   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,48}$`)
)

// ReadWindow loads and validates a window file.
func ReadWindow(path string) (Window, error) {
	f, err := os.Open(path)
	if err != nil {
		return Window{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Window{}, err
	}
	if len(b) > maxFileBytes {
		return Window{}, fmt.Errorf("window file is larger than %d bytes", maxFileBytes)
	}
	return ParseWindow(b)
}

// ParseWindow decodes and validates a window. Unknown versions and unknown
// fields are errors, not warnings: a field the allow-list does not name could
// be a position or a name.
func ParseWindow(b []byte) (Window, error) {
	if isExported(b) {
		return parseExported(b)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var w Window
	if err := dec.Decode(&w); err != nil {
		return Window{}, fmt.Errorf("window: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Window{}, errors.New("window: trailing data after the JSON object")
	}
	if err := w.Validate(); err != nil {
		return Window{}, err
	}
	return w, nil
}

// Validate checks versions and value shapes, then orders the events.
func (w *Window) Validate() error {
	if w.SchemaVersion != SchemaVersion {
		return fmt.Errorf("window: schema_version %d is not supported (want %d)", w.SchemaVersion, SchemaVersion)
	}
	if w.RedactionVersion != RedactionVersion {
		return fmt.Errorf("window: redaction_version %d is not supported (want %d)", w.RedactionVersion, RedactionVersion)
	}
	if !name.MatchString(w.WindowID) {
		return errors.New("window: window_id is missing or has characters outside [A-Za-z0-9_.-]")
	}
	if w.From.IsZero() || w.To.Before(w.From) {
		return errors.New("window: from and to must be set and ordered")
	}
	if len(w.Events) > maxEvents {
		return fmt.Errorf("window: %d events exceed the limit of %d", len(w.Events), maxEvents)
	}
	seen := make(map[int64]bool, len(w.Events))
	for i, e := range w.Events {
		switch {
		case e.ID <= 0:
			return fmt.Errorf("window: event %d has no positive id", i)
		case seen[e.ID]:
			return fmt.Errorf("window: duplicate event id %d", e.ID)
		case !name.MatchString(e.Unit):
			return fmt.Errorf("window: event %d: unit has characters outside the allowed set", e.ID)
		case e.At.IsZero():
			return fmt.Errorf("window: event %d has no time", e.ID)
		case !vocab.MatchString(e.Kind):
			return fmt.Errorf("window: event %d: kind is outside the allowed vocabulary shape", e.ID)
		case e.Reason != "" && !vocab.MatchString(e.Reason):
			return fmt.Errorf("window: event %d: reason_code is outside the allowed vocabulary shape", e.ID)
		case e.Template != "" && !vocab.MatchString(e.Template):
			return fmt.Errorf("window: event %d: template is outside the allowed vocabulary shape", e.ID)
		case e.Anchor != "" && !opaque.MatchString(e.Anchor):
			return fmt.Errorf("window: event %d: anchor has characters outside the allowed set", e.ID)
		case e.Task != "" && !opaque.MatchString(e.Task):
			return fmt.Errorf("window: event %d: task has characters outside the allowed set", e.ID)
		case e.DurationMS < 0 || e.Cause < 0:
			return fmt.Errorf("window: event %d has a negative number", e.ID)
		}
		seen[e.ID] = true
	}
	sort.SliceStable(w.Events, func(i, j int) bool {
		if !w.Events[i].At.Equal(w.Events[j].At) {
			return w.Events[i].At.Before(w.Events[j].At)
		}
		return w.Events[i].ID < w.Events[j].ID
	})
	return nil
}

// IDs is the set of event ids in the window.
func (w Window) IDs() map[int64]bool {
	m := make(map[int64]bool, len(w.Events))
	for _, e := range w.Events {
		m[e.ID] = true
	}
	return m
}

func (e Event) unitlog() unitlog.Event {
	return unitlog.Event{
		ID: e.ID, Unit: e.Unit, At: e.At, Kind: e.Kind, Reason: e.Reason, Cause: e.Cause,
		Template: e.Template, Task: e.Task, Duration: time.Duration(e.DurationMS) * time.Millisecond,
		Idle: e.Idle, Anchor: e.Anchor,
	}
}
