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
		Payload: []byte(`{"reason_code":"x","cause_ref":2,"template":"gather","duration_ms":1500,"idle":false,"anchor":"a"}`)})
	if e.Reason != "x" || e.Cause != 2 || e.Template != "gather" || e.Duration != 1500*time.Millisecond || e.Anchor != "a" || e.Task != task.String() {
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
