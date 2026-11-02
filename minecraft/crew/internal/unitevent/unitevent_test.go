package unitevent

import (
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/sessionid"
)

func TestEmitFillsWhatTheProducerLeftBlank(t *testing.T) {
	var got []Event
	SetSink(func(e Event) { got = append(got, e) })
	t.Cleanup(func() { SetSink(nil); SetVersion("") })
	SetVersion("1.80.0")
	sid := sessionid.Begin()

	Emit(Event{Kind: "kit_failed", ReasonCode: "kit_no_path"})
	Emit(Event{Kind: "session_error", SessionID: "abcdef012345", At: time.Unix(5, 0)})

	if len(got) != 2 {
		t.Fatalf("got %d events", len(got))
	}
	if e := got[0]; e.SessionID != sid || e.CrewVersion != "1.80.0" || e.At.IsZero() {
		t.Errorf("defaults not filled: %+v", e)
	}
	if e := got[1]; e.SessionID != "abcdef012345" || !e.At.Equal(time.Unix(5, 0)) {
		t.Errorf("the producer's own values were replaced: %+v", e)
	}
}

func TestEmitWithNoListenerIsANoop(t *testing.T) {
	SetSink(nil)
	Emit(Event{Kind: "kit_failed"})
}
