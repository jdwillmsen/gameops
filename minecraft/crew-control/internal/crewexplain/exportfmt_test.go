package crewexplain

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/export"
)

func TestParseWindowReadsTheServicesExport(t *testing.T) {
	cause := int64(4)
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	x := export.Window{
		SchemaVersion: export.SchemaVersion, RedactionVersion: export.RedactionVersion,
		WindowID: "w-abc", SHA256: "00", From: at, To: at.Add(time.Minute),
		Lines: []export.Line{
			{ID: 5, TS: at.Add(time.Second), Unit: "unit-a", Kind: "task_failed", State: "warn", ReasonCode: "other", Count: 1, CauseRef: &cause},
			{ID: 4, TS: at, Unit: "unit-a", Kind: "task_started", Template: "gather_and_deposit", State: "info", Count: 1},
		},
	}
	w, err := ParseWindow(mustJSON(t, x))
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Events) != 2 || w.Events[0].ID != 4 || w.Events[1].Cause != 4 || w.Events[1].Reason != "other" || w.WindowID != "w-abc" {
		t.Fatalf("mapped wrongly: %+v", w)
	}
}

func TestParseWindowRefusesUnknownExportFields(t *testing.T) {
	b := []byte(`{"schema_version":1,"redaction_version":1,"window_id":"w","from":"2026-10-26T12:00:00Z","to":"2026-10-26T12:01:00Z","lines":[],"position":[1,2,3]}`)
	if _, err := ParseWindow(b); err == nil {
		t.Fatal("an unknown field in an exported window must be refused")
	}
}
