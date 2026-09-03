package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseEvents(t *testing.T) {
	log := strings.Join([]string{
		`[2026-09-02 04:35:59:427 INFO] Player connected: Steve, xuid: 2535457893448396`,
		`[2026-09-02 04:36:10:001 INFO] Player disconnected: Steve, xuid: 2535457893448396`,
		`[2026-09-02 04:36:15:002 INFO] some unrelated line`,
		`terminate called after throwing an instance of 'std::length_error'`,
	}, "\n")

	events, err := ParseEvents(strings.NewReader(log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(events), events)
	}
	if events[0].Type != EventConnect || events[0].Player != "Steve" {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].Type != EventDisconnect || events[1].Player != "Steve" {
		t.Errorf("event 1 = %+v", events[1])
	}
	if events[2].Type != EventCrash {
		t.Errorf("event 2 = %+v, want crash", events[2])
	}
}

func TestParseEvents_Empty(t *testing.T) {
	events, err := ParseEvents(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestEventLog_IngestAssignsIncreasingIDs(t *testing.T) {
	log := NewEventLog()
	log.Ingest("Player connected: Steve, xuid: 111", time.Now())
	log.Ingest("Player connected: Alex, xuid: 222", time.Now())

	got := log.Since(0)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("IDs = [%d %d], want [1 2]", got[0].ID, got[1].ID)
	}
}

func TestEventLog_IngestSkipsUnrecognizedLines(t *testing.T) {
	log := NewEventLog()
	log.Ingest("just some ordinary log noise", time.Now())

	if got := log.Since(0); len(got) != 0 {
		t.Errorf("got %d events, want 0 for an unrecognized line", len(got))
	}
}

func TestEventLog_SinceFiltersToNewerEvents(t *testing.T) {
	log := NewEventLog()
	log.Ingest("Player connected: Steve, xuid: 111", time.Now())
	log.Ingest("Player connected: Alex, xuid: 222", time.Now())
	log.Ingest("Player connected: Notch, xuid: 333", time.Now())

	got := log.Since(1)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Player != "Alex" || got[1].Player != "Notch" {
		t.Errorf("players = [%s %s], want [Alex Notch]", got[0].Player, got[1].Player)
	}
}

func TestEventLog_SinceLatestReturnsEmpty(t *testing.T) {
	log := NewEventLog()
	log.Ingest("Player connected: Steve, xuid: 111", time.Now())

	if got := log.Since(1); len(got) != 0 {
		t.Errorf("got %d events, want 0 when since == latest ID", len(got))
	}
}

func TestEventLog_CapacityBound(t *testing.T) {
	log := NewEventLog()
	for i := 0; i < eventLogCapacity+10; i++ {
		log.Ingest("Player connected: Steve, xuid: 111", time.Now())
	}

	got := log.Since(0)
	if len(got) != eventLogCapacity {
		t.Errorf("got %d retained events, want %d (bounded)", len(got), eventLogCapacity)
	}
	// The oldest events must have aged out, so the retained window's first
	// ID should reflect that (IDs keep incrementing even as old entries are
	// dropped).
	if got[0].ID != int64(eventLogCapacity+10-eventLogCapacity+1) {
		t.Errorf("oldest retained ID = %d, want the log to have dropped the earliest entries", got[0].ID)
	}
}
