package chunks

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)

func set(ps ...Pos) Set {
	s := Set{}
	for _, p := range ps {
		s[p] = struct{}{}
	}
	return s
}

var (
	a = Pos{Overworld, 0, 0}
	b = Pos{Overworld, 10, 0}
	c = Pos{Nether, 1, 1}
	d = Pos{End, 5, 5}
)

func openLedger(t *testing.T, path string) *Ledger {
	t.Helper()
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLedger_AChunkThatDisappearsIsReportedLostOnEveryCycle(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "chunks.bin"))

	r := mustObserve(t, l, set(a, b, c), t0)
	if r.TotalLost() != 0 || r.Present[Overworld] != 2 || r.Present[Nether] != 1 {
		t.Fatalf("first census: %+v", r)
	}
	for i := range 2 {
		r = mustObserve(t, l, set(a, c, d), t0.Add(time.Duration(i+1)*15*time.Minute))
		if r.Lost[Overworld] != 1 || r.Missing[Overworld] != 1 || len(r.Sample) != 1 || r.Sample[0] != b {
			t.Fatalf("census %d after b vanished: %+v", i+1, r)
		}
	}
}

// Bedrock regenerates a chunk from the seed as soon as a player comes near
// where it was, so a lost chunk reappears with everything built on it gone.
// Its coming back says nothing about whether the loss was repaired, and the
// places players visit most are the first to come back.
func TestLedger_AChunkThatComesBackIsStillLost(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "chunks.bin"))
	mustObserve(t, l, set(a, b), t0)
	mustObserve(t, l, set(a), t0.Add(time.Minute))

	r := mustObserve(t, l, set(a, b), t0.Add(time.Hour))
	if r.Lost[Overworld] != 1 || r.Missing[Overworld] != 0 {
		t.Errorf("after b came back: lost %v, missing %v", r.Lost, r.Missing)
	}
}

func TestLedger_RemembersAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chunks", "seen.bin")
	l := openLedger(t, path)
	mustObserve(t, l, set(a, b, c), t0)
	mustObserve(t, l, set(a), t0.Add(time.Minute))

	// The loss is known before the restarted service has counted anything,
	// so an alert on it does not lapse while the pod comes back.
	again := openLedger(t, path)
	if r, ok := again.Last(); !ok || r.TotalLost() != 2 || r.Lost[Nether] != 1 {
		t.Errorf("after a restart, before any census: %+v, %v", r, ok)
	}
	if r := mustObserve(t, again, set(a), t0.Add(time.Hour)); r.TotalLost() != 2 {
		t.Errorf("after a restart: %+v", r)
	}
}

// A ledger that could not be written must not quietly drop the chunks it
// was about to record: the next census would find them unremarkable, and a
// restart would forget them.
func TestLedger_AFailedWriteIsRetried(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chunks")
	path := filepath.Join(dir, "seen.bin")
	l := openLedger(t, path)
	mustObserve(t, l, set(a), t0)

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Observe(set(a, b), t0.Add(time.Minute)); err == nil {
		t.Fatal("a census whose record could not be written reported success")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustObserve(t, l, set(a, b), t0.Add(2*time.Minute))

	if r := mustObserve(t, openLedger(t, path), set(a), t0.Add(time.Hour)); r.Lost[Overworld] != 1 {
		t.Errorf("b was never recorded: %+v", r)
	}
}

// Replacing the ledger is a rename, and a rename the directory has not been
// flushed for can be undone by the node stopping. So a count is not
// recorded until the directory holding the new ledger is on the disk, and
// one whose directory could not be flushed is not reported as recorded.
func TestLedger_ACountIsNotRecordedUntilItsDirectoryIsFlushed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "chunks")
	path := filepath.Join(dir, "seen.bin")
	l := openLedger(t, path)
	mustObserve(t, l, set(a), t0)

	var flushed []string
	l.flush = func(d string) error {
		flushed = append(flushed, d)
		// By now the directory names the new ledger, or flushing it would
		// make the old one durable instead.
		if r, ok := openLedger(t, path).Last(); !ok || !r.At.Equal(t0.Add(time.Minute)) {
			t.Errorf("the directory was flushed before the new ledger was in it: %+v", r)
		}
		return nil
	}
	mustObserve(t, l, set(a, b), t0.Add(time.Minute))
	if len(flushed) != 1 || flushed[0] != dir {
		t.Fatalf("flushed %v, want %s once", flushed, dir)
	}

	l.flush = func(string) error { return errors.New("input/output error") }
	if _, err := l.Observe(set(a), t0.Add(2*time.Minute)); err == nil {
		t.Fatal("a count whose ledger may not survive a stop reported success")
	}
	if err := l.Acknowledge(t0.Add(time.Minute)); err == nil {
		t.Fatal("an acknowledgement that may not survive a stop reported success")
	}
}

func TestLedger_AcknowledgingAcceptsTheWorldAsItIsNow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seen.bin")
	l := openLedger(t, path)
	if err := l.Acknowledge(t0); !errors.Is(err, ErrNoCensus) {
		t.Errorf("acknowledge before any census: %v", err)
	}
	mustObserve(t, l, set(a, b, c), t0)
	last := mustObserve(t, l, set(a), t0.Add(time.Minute))

	// An operator acknowledges what they looked at. A count that landed
	// since may hold losses they never saw.
	if err := l.Acknowledge(t0); !errors.Is(err, ErrStale) {
		t.Errorf("acknowledging an older count: %v", err)
	}
	if err := l.Acknowledge(last.At); err != nil {
		t.Fatal(err)
	}
	if r, _ := l.Last(); r.TotalLost() != 0 || r.TotalMissing() != 0 {
		t.Errorf("the last report still shows chunks lost: %+v", r)
	}
	if r := mustObserve(t, openLedger(t, path), set(a), t0.Add(time.Hour)); r.TotalLost() != 0 {
		t.Errorf("after acknowledging and restarting: %+v", r)
	}
	// A chunk regenerated after the acknowledgement is just a chunk.
	if r := mustObserve(t, l, set(a, b), t0.Add(2*time.Hour)); r.TotalLost() != 0 {
		t.Errorf("b regenerated after the acknowledgement: %+v", r)
	}
}

func TestLedger_RefusesARecordItCannotRead(t *testing.T) {
	for name, content := range map[string][]byte{
		"not a ledger": []byte("not a ledger"),
		"truncated":    append(append([]byte{}, magic...), 1, 0, 0, 0, 0, 0, 0, 0),
	} {
		path := filepath.Join(t.TempDir(), "seen.bin")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		// Starting again from empty would take the damaged world as the new
		// baseline and hide exactly what this exists to find.
		if _, err := OpenLedger(path); err == nil {
			t.Errorf("%s: opened as an empty ledger", name)
		}
	}
}

func TestLedger_SampleIsBoundedAndStable(t *testing.T) {
	l := openLedger(t, filepath.Join(t.TempDir(), "seen.bin"))
	all := Set{}
	for x := range int32(500) {
		all[Pos{Overworld, x, 0}] = struct{}{}
	}
	mustObserve(t, l, all, t0)
	r := mustObserve(t, l, set(Pos{Overworld, 0, 0}), t0.Add(time.Minute))
	if r.Lost[Overworld] != 499 || len(r.Sample) != maxSample {
		t.Fatalf("lost %d, sample of %d", r.Lost[Overworld], len(r.Sample))
	}
	if r.Sample[0] != (Pos{Overworld, 1, 0}) {
		t.Errorf("sample starts at %+v, want the lowest position", r.Sample[0])
	}
}

func mustObserve(t *testing.T, l *Ledger, s Set, at time.Time) Report {
	t.Helper()
	r, err := l.Observe(s, at)
	if err != nil {
		t.Fatal(err)
	}
	if !r.At.Equal(at) {
		t.Errorf("report time %v, want %v", r.At, at)
	}
	return r
}
