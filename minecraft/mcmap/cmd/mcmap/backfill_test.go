package main

import (
	"log/slog"
	"strings"
	"testing"
)

// The job writes into the history for good and unpacks whole worlds where
// it is told to, so a place it was not told in full is a refusal and not a
// guess. All of what is wrong is said at once: it is run by hand.
func TestBackfillRefusesToRunWithoutKnowingWhere(t *testing.T) {
	env := map[string]string{
		"HISTORY_DIR":             "history",
		"BACKFILL_WORK":           "/work",
		"LEVEL_NAME":              "../FWB",
		"RENDER_CHUNK_PROCESSORS": "0",
	}
	err := runBackfill(func(k string) string { return env[k] }, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"HISTORY_DIR", "BACKFILL_ARCHIVES", "LEVEL_NAME", "RENDER_CHUNK_PROCESSORS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "BACKFILL_WORK") {
		t.Errorf("the error names a setting that was right: %v", err)
	}
}
