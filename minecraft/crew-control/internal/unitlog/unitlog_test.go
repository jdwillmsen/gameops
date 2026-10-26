package unitlog

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

func TestFromReadsPayloadAndIgnoresGarbage(t *testing.T) {
	task := uuid.New()
	e := From(events.Event{ID: 4, Unit: "u", Kind: KindTaskDone, TaskID: &task,
		Payload: []byte(`{"reason_code":"no_path","cause_ref":2,"template":"gather","duration_ms":1500,"idle":false,"anchor":"a"}`)})
	if e.Reason != "no_path" || e.Cause != 2 || e.Template != "gather" || e.Duration != 1500*time.Millisecond || e.Anchor != "a" || e.Task != task.String() {
		t.Fatalf("event = %+v", e)
	}
	if !e.Useful() {
		t.Fatal("a done task is useful")
	}
	g := From(events.Event{Kind: KindTaskDone, Payload: []byte(`not json`)})
	if g.Reason != "" || g.Duration != 0 {
		t.Fatalf("garbage payload = %+v", g)
	}
	if (Event{Kind: KindTaskDone, Idle: true}).Useful() {
		t.Fatal("idle roll-up is not useful")
	}
}

func TestFreeTextNeverSurvivesIntoReasonOrTemplate(t *testing.T) {
	for _, c := range []struct{ payload, reason, template string }{
		{`{"reason_code":"no path at 12,64,-300","template":"haul"}`, "other", "haul"},
		{`{"reason_code":"Steve died","template":"Steve"}`, "other", "other"},
		{`{"reason_code":"session_lost","template":"gather at 1 2 3"}`, "session_lost", "other"},
		{`{"template":"a_token_ghp_0123456789abcdefghijklmnopqrstuvwxyz0123"}`, "", "other"},
		{`{}`, "", ""},
	} {
		e := From(events.Event{Kind: KindTaskFailed, Payload: []byte(c.payload)})
		if e.Reason != c.reason || e.Template != c.template {
			t.Errorf("%s: reason %q template %q, want %q %q", c.payload, e.Reason, e.Template, c.reason, c.template)
		}
	}
}
