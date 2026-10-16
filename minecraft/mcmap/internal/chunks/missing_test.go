package chunks

import (
	"path/filepath"
	"testing"
	"time"
)

// What the map marks as lost is the chunks absent now, not every chunk that
// was ever lost: one that came back is ground again.
func TestLedger_MissingIsWhatIsAbsentNowAndIsACopy(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "chunks.bin"))
	if len(l.Missing()) != 0 {
		t.Error("an empty ledger has chunks missing")
	}
	mustObserve(t, l, set(a, b, c), t0)
	mustObserve(t, l, set(a), t0.Add(time.Minute))
	got := l.Missing()
	if len(got) != 2 {
		t.Fatalf("missing %v, want b and c", got)
	}
	if _, ok := got[b]; !ok {
		t.Errorf("b is not missing: %v", got)
	}
	delete(got, b)
	if len(l.Missing()) != 2 {
		t.Error("the ledger's own set was handed out")
	}
	mustObserve(t, l, set(a, b), t0.Add(time.Hour))
	if m := l.Missing(); len(m) != 1 {
		t.Errorf("after b came back: %v", m)
	} else if _, ok := m[c]; !ok {
		t.Errorf("c is not missing: %v", m)
	}
	if r, _ := l.Last(); r.Lost[Overworld] != 1 {
		t.Errorf("a chunk that came back is no longer lost: %+v", r.Lost)
	}
}

func TestLedger_BoundsAreTheBoxOfEveryChunkSeenPerDimension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chunks.bin")
	l := openLedger(t, path)
	if _, ok := l.Bounds(Overworld); ok {
		t.Error("an empty ledger has a box")
	}
	mustObserve(t, l, set(Pos{Overworld, -3, 7}, Pos{Overworld, 10, -2}, Pos{Overworld, 4, 4}, Pos{Nether, 1, 1}), t0)
	got, ok := l.Bounds(Overworld)
	if !ok || got != (Box{MinX: -3, MinZ: -2, MaxX: 10, MaxZ: 7}) {
		t.Errorf("overworld %+v, %v", got, ok)
	}
	if n, _ := l.Bounds(Nether); n != (Box{1, 1, 1, 1}) {
		t.Errorf("nether %+v", n)
	}
	if _, ok := l.Bounds(End); ok {
		t.Error("the end has a box and no chunk")
	}
	// A chunk that is gone is still where the world has been; and a ledger
	// read back from the volume has the same box.
	mustObserve(t, l, set(Pos{Overworld, 4, 4}), t0.Add(time.Minute))
	again := openLedger(t, path)
	if b, _ := again.Bounds(Overworld); b != got {
		t.Errorf("after a restart %+v, want %+v", b, got)
	}
}
