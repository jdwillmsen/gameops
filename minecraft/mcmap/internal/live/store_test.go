package live

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func mobs(n int, prefix string) []Entity {
	out := make([]Entity, n)
	for i := range out {
		out[i] = Entity{ID: fmt.Sprintf("%s%d", prefix, i), Type: "zombie", X: float64(i)}
	}
	return out
}

func part(gen int64, kind Kind, dimension string, part, parts int, items []Entity) Record {
	return Record{Gen: gen, Kind: kind, Dimension: dimension, Part: part, Parts: parts, Items: items}
}

func ids(es []Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestStoreAppliesOnlyCompleteGenerations(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	if got := s.Apply(part(1, Mobs, "overworld", 0, 3, mobs(2, "a")), t0); got != resultBuffered {
		t.Fatalf("first part = %s", got)
	}
	// Parts need not arrive in order.
	if got := s.Apply(part(1, Mobs, "overworld", 2, 3, mobs(2, "c")), t0); got != resultBuffered {
		t.Fatalf("third part = %s", got)
	}
	if f := s.Snapshot("overworld", t0); !f.Stale || len(f.Mobs) != 0 {
		t.Fatalf("two of three parts are drawn: %+v", f)
	}
	if got := s.Apply(part(1, Mobs, "overworld", 1, 3, mobs(2, "b")), t0); got != resultApplied {
		t.Fatalf("last part = %s", got)
	}
	f := s.Snapshot("overworld", t0)
	if got, want := fmt.Sprint(ids(f.Mobs)), "[a0 a1 b0 b1 c0 c1]"; got != want || f.Stale || !f.At.Equal(t0) {
		t.Errorf("frame = %v stale=%v at=%v, want %s in part order", got, f.Stale, f.At, want)
	}
	// One kind and one dimension say nothing about the others.
	if f := s.Snapshot("nether", t0); !f.Stale {
		t.Errorf("nether = %+v", f)
	}
	if len(f.Players) != 0 {
		t.Errorf("players = %+v", f.Players)
	}
}

// Part 1 of 3 arrives and the rest are lost. What was on screen stays there
// until something whole replaces it or it ages out; half a sample never is.
func TestStoreKeepsLastCompleteGeneration(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	s.Apply(part(1, Mobs, "overworld", 0, 1, mobs(3, "old")), t0)

	s.Apply(part(2, Mobs, "overworld", 0, 3, mobs(2, "new")), t0.Add(time.Second))
	if got := fmt.Sprint(ids(s.Snapshot("overworld", t0.Add(time.Second)).Mobs)); got != "[old0 old1 old2]" {
		t.Fatalf("with one part of three in: %s", got)
	}
	// The next sample starts before the last one finished.
	s.Apply(part(3, Mobs, "overworld", 0, 3, mobs(2, "newer")), t0.Add(2*time.Second))
	s.Apply(part(3, Mobs, "overworld", 1, 3, mobs(2, "newer")), t0.Add(2*time.Second))
	f := s.Snapshot("overworld", t0.Add(2*time.Second))
	if got := fmt.Sprint(ids(f.Mobs)); got != "[old0 old1 old2]" || !f.At.Equal(t0) {
		t.Fatalf("with two parts of three in: %s at %v", got, f.At)
	}
	// Nothing whole ever arrives, so the old list goes at its TTL and the
	// parts that did arrive are still not drawn.
	if f := s.Snapshot("overworld", t0.Add(10*time.Second)); !f.Stale || len(f.Mobs) != 0 {
		t.Errorf("at the TTL: %+v", f)
	}
	// A part of an abandoned list cannot complete a later one.
	if got := s.Apply(part(2, Mobs, "overworld", 2, 3, mobs(2, "late")), t0.Add(3*time.Second)); got == resultApplied {
		t.Errorf("a part of generation 2 completed generation 3")
	}
}

func TestStoreExpiresAtTheTTL(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	s.Apply(part(1, Players, "end", 0, 1, mobs(1, "p")), t0)
	s.Apply(part(1, Mobs, "end", 0, 1, mobs(4, "m")), t0.Add(3*time.Second))

	if f := s.Snapshot("end", t0.Add(9999*time.Millisecond)); len(f.Players) != 1 || len(f.Mobs) != 4 || f.Stale {
		t.Fatalf("just inside the TTL: %+v", f)
	}
	// Each list ages on its own: the players are gone, the mobs are not.
	f := s.Snapshot("end", t0.Add(10*time.Second))
	if len(f.Players) != 0 || len(f.Mobs) != 4 || f.Stale || !f.At.Equal(t0.Add(3*time.Second)) {
		t.Fatalf("at the players' TTL: %+v", f)
	}
	if f := s.Snapshot("end", t0.Add(13*time.Second)); !f.Stale || len(f.Mobs) != 0 || !f.At.IsZero() {
		t.Errorf("at the mobs' TTL: %+v", f)
	}
}

// Going stale is a change the page has to be told about, and nothing
// arrives to prompt it.
func TestStoreReportsAListGoingStaleAsAChange(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	if got := s.Changed(t0); len(got) != 0 {
		t.Fatalf("an empty store changed: %v", got)
	}
	s.Apply(part(1, Mobs, "nether", 0, 1, mobs(2, "m")), t0)
	got := s.Changed(t0)
	if f, ok := got["nether"]; len(got) != 1 || !ok || len(f.Mobs) != 2 {
		t.Fatalf("after a list: %v", got)
	}
	if got := s.Changed(t0.Add(5 * time.Second)); len(got) != 0 {
		t.Fatalf("changed with nothing new: %v", got)
	}
	got = s.Changed(t0.Add(10 * time.Second))
	if f, ok := got["nether"]; len(got) != 1 || !ok || !f.Stale {
		t.Fatalf("at the TTL: %v", got)
	}
	if got := s.Changed(t0.Add(11 * time.Second)); len(got) != 0 {
		t.Errorf("stale reported twice: %v", got)
	}
}

// The server restarted, and the pack with it, counting from zero again.
func TestStoreResetsOnAGenerationGoingBackwards(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	s.Apply(part(900, Mobs, "overworld", 0, 1, mobs(1, "before")), t0)
	s.Apply(part(901, Mobs, "overworld", 0, 2, mobs(1, "half")), t0)

	// The same generation number as the half-built list, from the new
	// process: without a reset its first part would complete the old one.
	if got := s.Apply(part(0, Tick, "", 0, 0, nil), t0.Add(time.Second)); got != resultHeartbeat {
		t.Fatalf("heartbeat = %s", got)
	}
	if got := s.Apply(part(901, Mobs, "overworld", 1, 2, mobs(1, "stray")), t0.Add(time.Second)); got == resultApplied {
		t.Fatal("a list from before the restart was completed after it")
	}
	s = &Store{TTL: 10 * time.Second}
	s.Apply(part(900, Mobs, "overworld", 0, 1, mobs(1, "before")), t0)
	s.Apply(part(901, Mobs, "overworld", 0, 2, mobs(1, "half")), t0)
	if got := s.Apply(part(3, Mobs, "overworld", 0, 1, mobs(2, "after")), t0.Add(time.Second)); got != resultApplied {
		t.Fatalf("first list after the restart = %s", got)
	}
	if got := fmt.Sprint(ids(s.Snapshot("overworld", t0.Add(time.Second)).Mobs)); got != "[after0 after1]" {
		t.Errorf("after the restart: %s", got)
	}
	// A generation the old process also used is not a repeat of it.
	s.Apply(part(900, Mobs, "overworld", 0, 1, mobs(1, "again")), t0.Add(2*time.Second))
	if got := fmt.Sprint(ids(s.Snapshot("overworld", t0.Add(2*time.Second)).Mobs)); got != "[again0]" {
		t.Errorf("generation 900 of the new process: %s", got)
	}
}

func TestStoreDropsARepeatedRecord(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	s.Apply(part(5, Mobs, "overworld", 0, 1, mobs(1, "a")), t0)
	if got := s.Apply(part(5, Mobs, "overworld", 0, 1, mobs(1, "b")), t0); got != resultDropped {
		t.Errorf("a list already applied = %s", got)
	}
	s.Apply(part(6, Mobs, "overworld", 0, 2, mobs(1, "a")), t0)
	if got := s.Apply(part(6, Mobs, "overworld", 0, 2, mobs(1, "a")), t0); got != resultDropped {
		t.Errorf("a part already held = %s", got)
	}
	if got := s.Apply(part(6, Mobs, "overworld", 1, 3, mobs(1, "a")), t0); got != resultDropped {
		t.Errorf("a part disagreeing about how many there are = %s", got)
	}
}

func TestStoreCapsAListAndCountsWhatItCut(t *testing.T) {
	s := &Store{TTL: 10 * time.Second, Max: 5}
	a, b := part(1, Mobs, "overworld", 0, 2, mobs(4, "a")), part(1, Mobs, "overworld", 1, 2, mobs(4, "b"))
	a.More, b.More = 600, 600
	s.Apply(a, t0)
	s.Apply(b, t0)
	f := s.Snapshot("overworld", t0)
	// 600 the pack left out, and 3 of the 8 it sent.
	if len(f.Mobs) != 5 || f.More != 603 {
		t.Errorf("%d mobs and %d more, want 5 and 603", len(f.Mobs), f.More)
	}
}

func TestStoreIsSafeConcurrently(t *testing.T) {
	s := &Store{TTL: 10 * time.Second}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for gen := range int64(200) {
				for p := range 3 {
					s.Apply(part(gen, Mobs, []string{"overworld", "nether", "end"}[w%3], p, 3, mobs(5, "m")), t0)
				}
				s.Apply(part(gen, Tick, "", 0, 0, nil), t0)
			}
		})
		wg.Go(func() {
			for range 200 {
				s.Snapshot("overworld", t0)
				s.Changed(t0)
			}
		})
	}
	wg.Wait()
}
