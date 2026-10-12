package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/mirror"
)

type fakeHistorian struct {
	at    []time.Time
	order *[]string
	err   error
}

func (f *fakeHistorian) Capture(_ context.Context, at time.Time) (history.Result, error) {
	f.at = append(f.at, at)
	if f.order != nil {
		*f.order = append(*f.order, "history")
	}
	return history.Result{Day: at.Format("2006-01-02")}, f.err
}

// The day is kept from tiles that are all of one snapshot, so it waits for
// the last dimension and is given the snapshot's own time.
func TestCycle_OffersTheMapToHistoryOnceEveryDimensionIsDrawn(t *testing.T) {
	var order []string
	s, r := &fakeSyncer{}, &fakeRenderer{order: &order}
	w := newWorker(s, r, "")
	h := &fakeHistorian{order: &order}
	w.History = h

	w.Cycle(context.Background(), noon)

	if got := strings.Join(order, " "); got != "overworld nether end history" {
		t.Fatalf("order = %q", got)
	}
	if len(h.at) != 1 || !h.at[0].Equal(noon) {
		t.Fatalf("history was offered %v, want the snapshot's time", h.at)
	}
}

// A day made of two dimensions from now and one from an hour ago would be
// a picture of no moment the world was ever in.
func TestCycle_AFailedRenderKeepsTheMapOutOfHistory(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{failOn: "nether"}
	w := newWorker(s, r, "")
	h := &fakeHistorian{}
	w.History = h

	w.Cycle(context.Background(), noon)

	if len(h.at) != 0 {
		t.Fatalf("history was offered a map with a dimension that failed to draw")
	}
}

func TestCycle_NothingIsOfferedToHistoryWithoutASnapshot(t *testing.T) {
	s, r := &fakeSyncer{err: mirror.ErrBusy}, &fakeRenderer{}
	w := newWorker(s, r, "")
	h := &fakeHistorian{}
	w.History = h
	w.Cycle(context.Background(), noon)
	if len(h.at) != 0 {
		t.Fatalf("history was offered a map after a refused snapshot")
	}
}

func TestCycle_AFailedHistoryCaptureIsLoggedAndStopsNothing(t *testing.T) {
	s, r := &fakeSyncer{}, &fakeRenderer{}
	w := newWorker(s, r, "")
	var log bytes.Buffer
	w.Logger = slog.New(slog.NewTextHandler(&log, nil))
	w.History = &fakeHistorian{err: errors.New("no space left on device")}
	survey := &fakeSurveyor{}
	w.Structures = survey

	if got := w.Cycle(context.Background(), noon); got != Applied {
		t.Errorf("outcome = %v, want applied", got)
	}
	if snap := w.Status.Snapshot(); snap.Problem != "" {
		t.Errorf("status = %+v: the page has nothing to say about a day not kept", snap)
	}
	if len(survey.dirs) != 1 {
		t.Errorf("the survey behind it ran %d times", len(survey.dirs))
	}
	if !strings.Contains(log.String(), "map history not kept") {
		t.Errorf("the failure was not logged:\n%s", log.String())
	}
}
