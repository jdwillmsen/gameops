package chunks

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func gauge(t *testing.T, name, dim string) float64 {
	t.Helper()
	switch name {
	case "lost":
		return testutil.ToFloat64(metricLost.WithLabelValues(dim))
	case "missing":
		return testutil.ToFloat64(metricMissing.WithLabelValues(dim))
	}
	return testutil.ToFloat64(metricPresent.WithLabelValues(dim))
}

func TestCensus_TakeReportsAndExportsWhatIsLost(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := OpenLedger(filepath.Join(dir, "seen.bin"))
	c := &Census{WorkDir: dir, Ledger: ledger}

	full := writeWorld(t, chunkKey(0, 0, 0, 0x2c), chunkKey(0, 1, 0, 0x2c), chunkKey(1, 0, 0, 0x2c))
	if _, err := c.Take(context.Background(), full, t0); err != nil {
		t.Fatal(err)
	}
	damaged := writeWorld(t, chunkKey(0, 0, 0, 0x2c))
	r, err := c.Take(context.Background(), damaged, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if r.Lost[Overworld] != 1 || r.Lost[Nether] != 1 {
		t.Fatalf("report %+v", r)
	}
	for dim, want := range map[string]float64{"overworld": 1, "nether": 1, "end": 0} {
		if got := gauge(t, "lost", dim); got != want {
			t.Errorf("lost{%s} = %v, want %v", dim, got, want)
		}
		if got := gauge(t, "missing", dim); got != want {
			t.Errorf("missing{%s} = %v, want %v", dim, got, want)
		}
	}
	if got := gauge(t, "present", "overworld"); got != 1 {
		t.Errorf("chunks{overworld} = %v", got)
	}
	if got := testutil.ToFloat64(metricCensusAt); got != float64(t0.Add(time.Minute).Unix()) {
		t.Errorf("census timestamp = %v", got)
	}

	// Acknowledging clears the alert's input at once rather than at the
	// next cycle, so the operator sees it take.
	if err := c.Acknowledge(r.At); err != nil {
		t.Fatal(err)
	}
	if got := gauge(t, "lost", "nether"); got != 0 {
		t.Errorf("lost{nether} after acknowledging = %v", got)
	}
}

// A census that cannot read the world must not look like a world with
// nothing lost: the gauges keep what was last known.
func TestCensus_AFailedScanLeavesTheLastKnownLossInPlace(t *testing.T) {
	dir := t.TempDir()
	ledger, _ := OpenLedger(filepath.Join(dir, "seen.bin"))
	c := &Census{WorkDir: dir, Ledger: ledger}
	c.Take(context.Background(), writeWorld(t, chunkKey(2, 0, 0, 0x2c), chunkKey(2, 1, 0, 0x2c)), t0)
	c.Take(context.Background(), writeWorld(t, chunkKey(2, 0, 0, 0x2c)), t0.Add(time.Minute))

	before := testutil.ToFloat64(metricCensusFailures)
	_, err := c.Take(context.Background(), filepath.Join(dir, "gone"), t0.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v", err)
	}
	if got := gauge(t, "lost", "end"); got != 1 {
		t.Errorf("lost{end} after a failed scan = %v, want the last known 1", got)
	}
	if testutil.ToFloat64(metricCensusFailures) != before+1 {
		t.Error("the failure was not counted")
	}
}

// After a restart the loss is exported before any count, so an alert on it
// does not resolve while the pod comes back or waits out a quiet window.
func TestCensus_PublishExportsWhatTheLedgerRemembers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seen.bin")
	first, _ := OpenLedger(path)
	first.Observe(set(a, b, d), t0)
	first.Observe(set(a), t0.Add(time.Minute))
	metricLost.Reset()

	ledger, err := OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	(&Census{WorkDir: dir, Ledger: ledger}).Publish()
	if got := gauge(t, "lost", "end"); got != 1 {
		t.Errorf("lost{end} after a restart = %v", got)
	}
}

func TestCensus_AcknowledgeErrors(t *testing.T) {
	ledger, _ := OpenLedger(filepath.Join(t.TempDir(), "seen.bin"))
	c := &Census{Ledger: ledger}
	if err := c.Acknowledge(t0); !errors.Is(err, ErrNoCensus) {
		t.Errorf("before a count: %v", err)
	}
}
