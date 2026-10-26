package crewexplain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/export"
)

// parseExported reads the service's own export answer. It is decoded against
// the exporter's types with unknown fields refused, so a field added there is
// an error here until this reader has been taught what it means.
func parseExported(b []byte) (Window, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var x export.Window
	if err := dec.Decode(&x); err != nil {
		return Window{}, fmt.Errorf("window: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Window{}, errors.New("window: trailing data after the JSON object")
	}
	w := Window{
		SchemaVersion: x.SchemaVersion, RedactionVersion: x.RedactionVersion,
		WindowID: x.WindowID, From: x.From, To: x.To,
		Events: make([]Event, 0, len(x.Lines)),
	}
	for _, l := range x.Lines {
		e := Event{ID: l.ID, Unit: l.Unit, At: l.TS, Kind: l.Kind, Reason: l.ReasonCode, Template: l.Template}
		if l.CauseRef != nil {
			e.Cause = *l.CauseRef
		}
		w.Events = append(w.Events, e)
	}
	if err := w.Validate(); err != nil {
		return Window{}, err
	}
	return w, nil
}

// isExported tells the exporter's answer from the native file by its "lines"
// key, which the native format never has.
func isExported(b []byte) bool {
	var probe struct {
		Lines json.RawMessage `json:"lines"`
	}
	return json.Unmarshal(b, &probe) == nil && probe.Lines != nil
}
