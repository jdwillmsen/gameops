package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func knownAt(t *testing.T, now *time.Time) (*Known, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known.json")
	k, err := LoadKnown(path, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return k, path
}

func TestKnown_ARecordedPlayerIsKnownAndSurvivesARestart(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	k, path := knownAt(t, &now)
	if k.Has("2535400000000001") {
		t.Fatal("a player nobody recorded is known")
	}
	if err := k.Record("2535400000000001"); err != nil {
		t.Fatal(err)
	}
	again, err := LoadKnown(path, func() time.Time { return now })
	if err != nil || !again.Has("2535400000000001") || again.Has("2535400000000002") {
		t.Fatalf("after a restart: err %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "amertag") {
		t.Errorf("the file holds more than an XUID and two times: %s", raw)
	}
}

func TestKnown_OnlyNumbersAreRecorded(t *testing.T) {
	now := time.Now()
	k, path := knownAt(t, &now)
	for _, bad := range []string{"", "console", "12 34", "../x"} {
		if k.Record(bad) == nil {
			t.Errorf("recorded %q", bad)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused record wrote a file")
	}
}

func TestKnown_ExpiresAfterTheDaysItIsKnownFor(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k, _ := knownAt(t, &now)
	_ = k.Record("2535400000000001")
	now = now.Add(KnownFor - time.Minute)
	if !k.Has("2535400000000001") {
		t.Error("forgotten early")
	}
	now = now.Add(2 * time.Minute)
	if k.Has("2535400000000001") {
		t.Error("still known after the period")
	}
	// Seen again, the first sighting starts over rather than reaching back
	// to a time the player was out of the period.
	if err := k.Record("2535400000000001"); err != nil || !k.Has("2535400000000001") {
		t.Fatalf("seen again: %v", err)
	}
	if k.players["2535400000000001"].First != now.UTC() {
		t.Error("an expired entry kept its first-seen")
	}
}

func TestKnown_LastSeenMovesOncePerHourAndKeepsFirst(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k, path := knownAt(t, &now)
	first := now
	_ = k.Record("2535400000000001")
	before, _ := os.ReadFile(path)
	now = now.Add(10 * time.Minute)
	_ = k.Record("2535400000000001")
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("rewritten within the hour")
	}
	now = now.Add(2 * time.Hour)
	_ = k.Record("2535400000000001")
	e := k.players["2535400000000001"]
	if !e.First.Equal(first) || !e.Last.Equal(now) {
		t.Errorf("entry = %+v", e)
	}
}

func TestKnown_ALastSeenInTheFutureIsNotKnown(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k, _ := knownAt(t, &now)
	_ = k.Record("2535400000000001")
	now = now.Add(-48 * time.Hour)
	if k.Has("2535400000000001") {
		t.Error("an entry from the future counts")
	}
}

func TestKnown_AFailedWriteChangesNothing(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	k, err := LoadKnown(filepath.Join(dir, "no", "such", "known.json"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	// The directory cannot be made under a file.
	if err := os.WriteFile(filepath.Join(dir, "no"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if k.Record("2535400000000001") == nil {
		t.Fatal("a write that cannot happen was reported done")
	}
	if k.Has("2535400000000001") {
		t.Error("a failed write left the player known")
	}
}

func TestKnown_ACorruptFileIsAnErrorNotAnEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known.json")
	_ = os.WriteFile(path, []byte("{"), 0o600)
	if _, err := LoadKnown(path, time.Now); err == nil {
		t.Error("a corrupt file loaded")
	}
}
