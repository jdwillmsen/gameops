package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/history"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// kept is days really kept, from a map these tests draw on: what changed is
// worked out from two days, and a made-up answer would test only itself.
type kept struct {
	t     *testing.T
	maps  string
	store *history.Store
}

func keeping(t *testing.T) (*Server, *kept) {
	t.Helper()
	s, _ := fixture(t)
	k := &kept{t: t, maps: t.TempDir()}
	for _, d := range render.Dimensions {
		dir := filepath.Join(k.maps, d)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "unmined.map.properties.js"), []byte(properties), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, err := history.Open(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store.MapsDir, store.Source = k.maps, &render.Unmined{}
	k.store = store
	s.History = store
	return s, k
}

func (k *kept) path(x, y int) string {
	return (&render.Unmined{}).TilePath(filepath.Join(k.maps, "overworld"), "webp", 0, x, y)
}

func (k *kept) draw(x, y int, content string) {
	k.t.Helper()
	if err := os.MkdirAll(filepath.Dir(k.path(x, y)), 0o755); err != nil {
		k.t.Fatal(err)
	}
	if err := os.WriteFile(k.path(x, y), []byte(content), 0o644); err != nil {
		k.t.Fatal(err)
	}
}

func (k *kept) erase(x, y int) {
	k.t.Helper()
	if err := os.Remove(k.path(x, y)); err != nil {
		k.t.Fatal(err)
	}
}

func (k *kept) keep(day int) {
	k.t.Helper()
	if _, err := k.store.Capture(context.Background(), time.Date(2026, 10, day, 0, 20, 0, 0, time.UTC)); err != nil {
		k.t.Fatal(err)
	}
}

type changesAnswer struct {
	Day    *string
	Before *string
	Zoom   *int
	Tiles  [][2]int
	Of     *int
	More   *int
}

func changesOf(t *testing.T, rec *httptest.ResponseRecorder) changesAnswer {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	var out changesAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Day == nil || out.Before == nil || out.Zoom == nil || out.Tiles == nil || out.Of == nil || out.More == nil {
		t.Fatalf("the answer leaves a field out or null: %s", rec.Body)
	}
	return out
}

const (
	keptForGood = "private, max-age=31536000, immutable"
	notKept     = "no-store"
)

func TestHistoryChanges_NameTheTilesThatDifferFromTheDayBefore(t *testing.T) {
	s, k := keeping(t)
	k.draw(3, -4, "a hut")
	k.draw(4, -4, "a field")
	k.draw(3, -3, "a pond")
	k.keep(12)
	k.draw(3, -4, "a castle")
	k.erase(3, -3)
	k.draw(3, -5, "a road")
	k.keep(13)

	rec := get(t, s, "/api/history/changes/2026-10-13?dimension=overworld")
	got := changesOf(t, rec)
	if *got.Day != "2026-10-13" || *got.Before != "2026-10-12" || *got.Zoom != 0 || *got.More != 0 || *got.Of != 4 {
		t.Errorf("answer = %s", rec.Body)
	}
	// The road added, the hut redrawn and the pond gone; not the field.
	if want := [][2]int{{3, -5}, {3, -4}, {3, -3}}; !reflect.DeepEqual(got.Tiles, want) {
		t.Errorf("tiles = %v, want %v", got.Tiles, want)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != keptForGood {
		t.Errorf("cache control = %q; no day can come between the 12th and the 13th", cache)
	}
}

// Nothing to compare the first day with is an answer and not a refusal, and
// is one any earlier day put in would change.
func TestHistoryChanges_TheFirstDayHeldHasAnEmptyBefore(t *testing.T) {
	s, k := keeping(t)
	k.draw(3, -4, "a hut")
	k.keep(12)

	rec := get(t, s, "/api/history/changes/2026-10-12?dimension=overworld")
	got := changesOf(t, rec)
	if *got.Before != "" || len(got.Tiles) != 0 || *got.More != 0 {
		t.Errorf("answer = %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"tiles":[]`) {
		t.Errorf("no tiles are not said as an empty list: %s", rec.Body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != notKept {
		t.Errorf("cache control = %q, want %q", cache, notKept)
	}
}

// A browser that kept an answer across a gap would go on outlining what
// changed since a day that is no longer the day before.
func TestHistoryChanges_AreKeptForGoodOnlyWhereNoDayCanComeBetween(t *testing.T) {
	s, k := keeping(t)
	k.draw(3, -4, "a hut")
	k.keep(12)
	k.draw(3, -4, "a castle")
	k.keep(15)

	rec := get(t, s, "/api/history/changes/2026-10-15?dimension=overworld")
	if got := changesOf(t, rec); *got.Before != "2026-10-12" || len(got.Tiles) != 1 {
		t.Fatalf("answer = %s", rec.Body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != notKept {
		t.Errorf("the newest day, across a gap: cache control = %q, want %q", cache, notKept)
	}

	// The 14th is put in afterwards, as a backfill does, the castle built.
	k.keep(14)
	rec = get(t, s, "/api/history/changes/2026-10-15?dimension=overworld")
	if got := changesOf(t, rec); *got.Before != "2026-10-14" || len(got.Tiles) != 0 {
		t.Errorf("after the 14th was put in = %s", rec.Body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != keptForGood {
		t.Errorf("the newest day, with the day before it held: cache control = %q, want %q", cache, keptForGood)
	}
	rec = get(t, s, "/api/history/changes/2026-10-14?dimension=overworld")
	if got := changesOf(t, rec); *got.Before != "2026-10-12" || len(got.Tiles) != 1 {
		t.Errorf("the day put in = %s", rec.Body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != notKept {
		t.Errorf("a day that is not the newest, across a gap: cache control = %q, want %q", cache, notKept)
	}
}

// A day as the store writes it down, for a day of more tiles than a test can
// wait to have kept: a capture flushes every one.
func writeDay(t *testing.T, dir, day string, tiles map[string]string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, "days", day+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	err = json.NewEncoder(gz).Encode(map[string]any{
		"version": 1, "day": day, "takenAt": day + "T00:20:00Z",
		"dimensions": map[string]any{"overworld": map[string]any{"tiles": tiles}},
	})
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestHistoryChanges_StopAtALimitAndSayHowManyWereLeftOut(t *testing.T) {
	s, _ := fixture(t)
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := history.Open(dir, logger); err != nil {
		t.Fatal(err)
	}
	const drawn = 4100
	first := map[string]string{"0/0/9": "aa"}
	second := map[string]string{"0/0/9": "aa"}
	for x := 1; x <= drawn; x++ {
		second[fmt.Sprintf("0/%d/9", x)] = fmt.Sprintf("%04x", x)
	}
	writeDay(t, dir, "2026-10-12", first)
	writeDay(t, dir, "2026-10-13", second)
	store, err := history.Open(dir, logger)
	if err != nil {
		t.Fatal(err)
	}
	s.History = store

	rec := get(t, s, "/api/history/changes/2026-10-13?dimension=overworld")
	got := changesOf(t, rec)
	if len(got.Tiles) >= drawn || len(got.Tiles)+*got.More != drawn || *got.Of != drawn+1 {
		t.Errorf("%d tiles and %d more of %d, want %d between them of %d", len(got.Tiles), *got.More, *got.Of, drawn, drawn+1)
	}
}

func TestHistoryChanges_RefuseWhatIsNotADimensionOfADayHeld(t *testing.T) {
	s, k := keeping(t)
	k.draw(3, -4, "a hut")
	k.keep(12)
	for path, want := range map[string]int{
		"/api/history/changes/2026-10-11?dimension=overworld":         http.StatusNotFound,
		"/api/history/changes/..%2Fdays%2F2026-10-12?dimension=end":   http.StatusNotFound,
		"/api/history/changes/2026-10-12.json.gz?dimension=overworld": http.StatusNotFound,
		"/api/history/changes/2026-10-12/overworld":                   http.StatusNotFound,
		"/api/history/changes/":                                       http.StatusNotFound,
		"/api/history/changes/2026-10-12?dimension=moon":              http.StatusBadRequest,
		"/api/history/changes/2026-10-12?dimension=..%2Foverworld":    http.StatusBadRequest,
		"/api/history/changes/2026-10-12":                             http.StatusBadRequest,
	} {
		rec := get(t, s, path)
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
		if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
			t.Errorf("%s: a refusal may be kept for good: %q", path, got)
		}
	}
}

func TestHistoryChanges_NeedASession(t *testing.T) {
	s, k := keeping(t)
	k.draw(3, -4, "a hut")
	k.keep(12)
	login := withLogin(t)
	s.Sessions, s.Codes, s.InternalToken = login.Sessions, login.Codes, login.InternalToken
	if rec := do(s.Handler(), "GET", "/api/history/changes/2026-10-12?dimension=overworld", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session = %d, want 401", rec.Code)
	}
}

func TestHistoryChanges_WithoutHistoryThereIsNothingToAskFor(t *testing.T) {
	s, _ := fixture(t)
	if code := get(t, s, "/api/history/changes/2026-10-12?dimension=overworld").Code; code != http.StatusNotFound {
		t.Errorf("without history = %d", code)
	}
}
