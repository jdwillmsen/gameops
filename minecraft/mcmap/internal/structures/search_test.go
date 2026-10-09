package structures

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// searcher is a surveyor that keeps what it finds in dir, with answer
// standing in for the search. calls counts the searches it started.
func searcher(t *testing.T, dir string, log *bytes.Buffer, answer func() (uint32, int, error)) (*Surveyor, *atomic.Int32) {
	s := surveyor(t, log)
	s.SeedFile = filepath.Join(dir, "structure-seed.json")
	calls := &atomic.Int32{}
	s.search = func(context.Context, []Evidence, int) (uint32, int, error) {
		calls.Add(1)
		return answer()
	}
	return s, calls
}

func (s *Surveyor) settled(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		s.mu.Lock()
		busy := s.searching
		s.mu.Unlock()
		if !busy {
			return
		}
	}
	t.Fatal("the search did not finish")
}

func wrongWorld(t *testing.T) string {
	w := evidence(t)
	w.fortresses()
	wrong := levelSeed + 1
	w.seed = &wrong
	return w.write()
}

func TestTake_WorksOutTheSeedTheWorldsRecordsAnswerTo(t *testing.T) {
	world, dir, log := wrongWorld(t), t.TempDir(), &bytes.Buffer{}
	s, calls := searcher(t, dir, log, func() (uint32, int, error) { return testSeed, 3, nil })

	got, err := s.Take(context.Background(), world, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedRefuted {
		t.Fatalf("first survey: %+v, want level.dat's seed refuted", got.Check)
	}
	s.settled(t)
	if got, err = s.Take(context.Background(), world, surveyedAt); err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified || len(got.Layers[chunks.Nether].Predicted) == 0 {
		t.Errorf("after the search: %+v with %d predictions, want verified", got.Check, len(got.Layers[chunks.Nether].Predicted))
	}
	if calls.Load() != 1 {
		t.Errorf("searched %d times, want once", calls.Load())
	}
	if info, err := os.Stat(s.SeedFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the seed file: %v, %v, want it kept for its owner alone", info, err)
	}
	// A seed is never logged, in any base a search could print it in.
	if strings.Contains(log.String(), strconv.Itoa(testSeed)) || strings.Contains(log.String(), strconv.FormatInt(testSeed, 16)) {
		t.Errorf("the seed is in the log:\n%s", log)
	}

	// A restart reads what was kept instead of searching again.
	again, calls := searcher(t, dir, nil, func() (uint32, int, error) { return 0, 0, ErrNoSeed })
	if got, err = again.Take(context.Background(), world, surveyedAt); err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified || calls.Load() != 0 {
		t.Errorf("after a restart: %+v and %d searches, want verified and none", got.Check, calls.Load())
	}
}

func TestTake_DoesNotSearchTheSameRecordsTwice(t *testing.T) {
	world, dir := wrongWorld(t), t.TempDir()
	s, calls := searcher(t, dir, nil, func() (uint32, int, error) { return 0, 1, ErrNoSeed })
	for range 3 {
		if _, err := s.Take(context.Background(), world, surveyedAt); err != nil {
			t.Fatal(err)
		}
		s.settled(t)
	}
	if calls.Load() != 1 {
		t.Errorf("searched %d times, want once for one set of records", calls.Load())
	}
	again, calls := searcher(t, dir, nil, func() (uint32, int, error) { return 0, 1, ErrNoSeed })
	if _, err := again.Take(context.Background(), world, surveyedAt); err != nil {
		t.Fatal(err)
	}
	again.settled(t)
	if calls.Load() != 0 {
		t.Errorf("searched %d times after a restart, want the failure remembered", calls.Load())
	}
}

// A search's best seed can explain too little to be believed. It is not
// kept in the way of level.dat's seed, and its records are not searched
// again.
func TestTake_DropsAFoundSeedItsOwnRecordsRefute(t *testing.T) {
	world, dir := wrongWorld(t), t.TempDir()
	s, calls := searcher(t, dir, nil, func() (uint32, int, error) { return testSeed + 2, 3, nil })
	for range 3 {
		if _, err := s.Take(context.Background(), world, surveyedAt); err != nil {
			t.Fatal(err)
		}
		s.settled(t)
	}
	if _, found := s.worked(); found || calls.Load() != 1 {
		t.Errorf("found seed kept: %v, searches: %d, want it dropped after one search", found, calls.Load())
	}
	again, calls := searcher(t, dir, nil, func() (uint32, int, error) { return testSeed, 3, nil })
	if _, err := again.Take(context.Background(), world, surveyedAt); err != nil {
		t.Fatal(err)
	}
	again.settled(t)
	if _, found := again.worked(); found || calls.Load() != 0 {
		t.Errorf("after a restart: found %v, searches %d, want neither", found, calls.Load())
	}
}

func TestTake_SearchesAgainAfterOneCutShort(t *testing.T) {
	world := wrongWorld(t)
	s, calls := searcher(t, t.TempDir(), nil, func() (uint32, int, error) { return 0, 0, context.DeadlineExceeded })
	for range 2 {
		if _, err := s.Take(context.Background(), world, surveyedAt); err != nil {
			t.Fatal(err)
		}
		s.settled(t)
	}
	if calls.Load() != 2 {
		t.Errorf("searched %d times, want one for each survey while none finishes", calls.Load())
	}
}

func TestTake_LeavesAnOperatorsSeedAlone(t *testing.T) {
	s, calls := searcher(t, t.TempDir(), nil, func() (uint32, int, error) { return testSeed, 3, nil })
	wrong := uint32(testSeed + 1)
	s.StructureSeed = &wrong
	got, err := s.Take(context.Background(), wrongWorld(t), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	s.settled(t)
	if got.Check.State != SeedRefuted || calls.Load() != 0 {
		t.Errorf("check %+v and %d searches, want the operator's seed refuted and left", got.Check, calls.Load())
	}
}
