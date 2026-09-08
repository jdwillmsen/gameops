package main

import (
	"testing"
	"time"
)

const (
	minD = 5 * time.Second
	maxD = 300 * time.Second
)

// A session that lasted must reset the delay, or a bot that has been happily
// connected for a day reconnects as slowly as one in a crash loop.
func TestBackoffResetsAfterAStableSession(t *testing.T) {
	if got := backoff(120*time.Second, stableSession, minD, maxD); got != minD {
		t.Errorf("backoff after a stable session = %v, want %v", got, minD)
	}
}

func TestBackoffDoublesAfterAShortSession(t *testing.T) {
	if got := backoff(10*time.Second, time.Second, minD, maxD); got != 20*time.Second {
		t.Errorf("backoff = %v, want it doubled to 20s", got)
	}
}

// Without a ceiling a bot that cannot connect eventually waits hours, and a
// server that comes back stays empty until someone notices.
func TestBackoffIsCapped(t *testing.T) {
	if got := backoff(maxD, time.Second, minD, maxD); got != maxD {
		t.Errorf("backoff = %v, want it capped at %v", got, maxD)
	}
}

// Jitter exists so a server restart does not bring every bot back in lockstep.
// It must stay within the delay it is spreading, or the cap above means
// nothing.
func TestJitterStaysWithinBounds(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := jitter(10 * time.Second)
		if got < 5*time.Second || got > 10*time.Second {
			t.Fatalf("jitter = %v, want between half the delay and the delay", got)
		}
	}
}

func TestJitterHandlesZero(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
}
