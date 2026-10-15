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
