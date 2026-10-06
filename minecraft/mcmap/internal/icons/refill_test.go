package icons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/color"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heard keeps what was logged, so a test can say how often.
type heard struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *heard) Enabled(context.Context, slog.Level) bool { return true }
func (h *heard) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *heard) WithGroup(string) slog.Handler            { return h }
func (h *heard) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *heard) count(level slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if r.Level == level {
			n++
		}
	}
	return n
}

func neverFetch(t *testing.T) func(context.Context) (Set, error) {
	return func(context.Context) (Set, error) {
		t.Error("everything was fetched again for the sake of what was missing")
		return Set{}, errors.New("not expected")
	}
}

// One answer that a file is absent is not the last word: a source can give
// a wrong one. A set kept with something missing is asked for that, and
// only that, when the service next starts.
func TestWhatASetIsMissingIsAskedForAgainOnStartAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	first := filled(t, dir, testRef)
	before, _ := first.Pictures()

	var asked [][]string
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t),
		Fill: func(_ context.Context, missing []string) (Set, error) {
			asked = append(asked, slices.Clone(missing))
			return Set{Pictures: map[string][]byte{"bed/blue": picture(t, 16, 16, blue)}, Lang: map[string]string{"entity.pig.name": "Pig From The File"}}, nil
		}}
	m.Run(t.Context())
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"bed/blue"}) {
		t.Fatalf("asked for %v, want the one missing picture once", asked)
	}
	if raw, ok := m.Picture("bed/blue"); !ok || colourOf(t, raw) != blue {
		t.Error("the picture that was missing is not served after it was fetched")
	}
	if raw, ok := m.Picture("container/chest"); !ok || colourOf(t, raw) != yellow {
		t.Error("a picture already held was lost in fetching the one that was missing")
	}
	if _, ok := m.Icon("pig"); !ok {
		t.Error("a mob icon was lost in fetching the picture that was missing")
	}
	if after, keys := m.Pictures(); after == before || len(keys) != 3 {
		t.Errorf("pictures are %v at version %q, which was %q before: a browser would not ask for the new one", keys, after, before)
	}
	if got := m.Names().Entity("pig"); got != "Pig From The File" {
		t.Errorf("names fetched late are not served: pig is %q", got)
	}

	// It was kept: the next start has nothing to ask for.
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t),
		Fill: func(_ context.Context, missing []string) (Set, error) {
			t.Errorf("asked again for %v after it was fetched and kept", missing)
			return Set{}, nil
		}}
	again.Run(t.Context())
	if _, ok := again.Picture("bed/blue"); !ok {
		t.Error("the picture fetched late was not kept on the volume")
	}
}

// A file the pin really does not hold is asked for again every so often,
// for good, and that must cost nothing but the request: what is held goes
// on being served, and nothing is logged for an answer that has not
// changed.
func TestAGapThatStaysAGapIsAskedAgainSlowlyAndWithoutALogLineEachTime(t *testing.T) {
	dir := t.TempDir()
	filled(t, dir, testRef)
	var calls atomic.Int64
	log := &heard{}
	ctx, cancel := context.WithCancel(t.Context())
	m := &Mobs{Dir: dir, Ref: testRef, Logger: slog.New(log), Fetch: neverFetch(t), RefillEvery: time.Millisecond, RetryMin: time.Hour,
		Fill: func(_ context.Context, missing []string) (Set, error) {
			calls.Add(1)
			return Set{Missing: missing}, nil
		}}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	waitFor(t, "the gap to be asked for several times", func() bool { return calls.Load() >= 6 })
	if _, ok := m.Picture("container/chest"); !ok {
		t.Error("what is held stopped being served while a gap was asked for")
	}
	if _, ok := m.Picture("bed/blue"); ok {
		t.Error("a picture the source does not hold is served")
	}
	cancel()
	<-done
	// The one line is the start's own, which names what is missing.
	if got := len(log.records); got != 1 {
		t.Errorf("%d log lines for %d askings of a gap that never changed, want the start's one", got, calls.Load())
	}

	// Slowly: a day by default, so one start does not ask twice.
	calls.Store(0)
	ctx, cancel = context.WithCancel(t.Context())
	slow := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t), RetryMin: time.Millisecond, Fill: m.Fill}
	done = make(chan struct{})
	go func() { slow.Run(ctx); close(done) }()
	waitFor(t, "the gap to be asked for on start", func() bool { return calls.Load() >= 1 })
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if n := calls.Load(); n != 1 {
		t.Errorf("a gap the source answered for was asked for %d times in a moment, want once and then a long wait", n)
	}
}

// Not being able to ask is not an answer. It is tried again soon, with a
// growing wait, and said once.
func TestWhatCouldNotBeAskedForIsTriedAgainSoonAndLoggedOnce(t *testing.T) {
	dir := t.TempDir()
	filled(t, dir, testRef)
	var calls atomic.Int64
	log := &heard{}
	m := &Mobs{Dir: dir, Ref: testRef, Logger: slog.New(log), Fetch: neverFetch(t),
		// A day between askings of a settled gap: only the short retry
		// can get this test to its end.
		RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond,
		Fill: func(_ context.Context, missing []string) (Set, error) {
			switch n := calls.Add(1); {
			case n <= 3:
				return Set{Missing: missing, Unreached: missing}, nil
			case n == 4:
				return Set{}, errors.New("dial tcp: i/o timeout")
			}
			return Set{Pictures: map[string][]byte{"bed/blue": picture(t, 16, 16, blue)}}, nil
		}}
	done := make(chan struct{})
	go func() { m.Run(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("after %d askings the missing picture was still being waited for", calls.Load())
	}
	if _, ok := m.Picture("bed/blue"); !ok {
		t.Error("the picture is not served after the source came back")
	}
	if got := log.count(slog.LevelWarn); got != 1 {
		t.Errorf("%d warnings for %d failed askings in a row, want one", got, calls.Load()-1)
	}
}

func TestASetFetchedWholeIsNotAskedForAnythingMore(t *testing.T) {
	m := &Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet(),
		Fetch: func(context.Context) (Set, error) {
			return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}}, nil
		},
		Fill: func(_ context.Context, missing []string) (Set, error) {
			t.Errorf("asked for %v with nothing missing", missing)
			return Set{}, nil
		}}
	m.Run(t.Context())
}

// recorded is the paths a stand-in source was asked for.
func recorded(s *samples) *[]string {
	var asked []string
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		asked = append(asked, r.URL.Path)
		s.mu.Unlock()
		inner.ServeHTTP(w, r)
	})
	return &asked
}

func TestFillAsksTheSourceOnlyForWhatIsMissing(t *testing.T) {
	s := newSamples(t)
	asked := recorded(s)
	got, err := s.source().Fill(t.Context(), []string{"container/chest", "shulker/red", langPath})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/raw/" + testRef + "/resource_pack/textures/blocks/chest_front.png", "/raw/" + testRef + "/resource_pack/textures/entity/shulker/shulker_red.png", "/raw/" + testRef + "/" + langPath}
	slices.Sort(*asked)
	slices.Sort(want)
	// No listing, which is the rationed request, and no definitions.
	if !slices.Equal(*asked, want) {
		t.Errorf("asked for %v\nwant      %v", *asked, want)
	}
	if len(got.Pictures) != 2 || got.Lang["entity.cow.name"] != "Synthetic Cow" || len(got.Missing) != 0 || len(got.Mobs) != 0 {
		t.Errorf("got %d pictures, %d names, missing %v", len(got.Pictures), len(got.Lang), got.Missing)
	}
	if colourOf(t, got.Pictures["container/chest"]) != yellow {
		t.Error("the chest is not the chest's texture")
	}

	// A bed's path is in the atlas and nowhere else.
	*asked = nil
	got, err = s.source().Fill(t.Context(), []string{"bed/red"})
	if err != nil || len(*asked) != 2 || !strings.HasSuffix((*asked)[0], "/item_texture.json") || !strings.HasSuffix((*asked)[1], "/textures/items/bed_red.png") {
		t.Errorf("for a bed, asked for %v (%v)", *asked, err)
	}
	if raw, ok := got.Pictures["bed/red"]; !ok || colourOf(t, raw) != bedColour(14) {
		t.Error("the red bed is not the texture the atlas lists fourteenth")
	}
}

func TestFillTellsWhatTheSourceDoesNotHoldFromWhatItCouldNotBeAskedFor(t *testing.T) {
	s := newSamples(t)
	s.mu.Lock()
	delete(s.files, "resource_pack/textures/blocks/chest_front.png")
	s.files["resource_pack/textures/blocks/barrel_side.png"] = []byte("<html>not a picture</html>")
	s.mu.Unlock()
	asked := recorded(s)
	got, err := s.source().Fill(t.Context(), []string{"container/chest", "container/barrel", "marker/waypoint", "no/such_picture", "../../etc/passwd"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"../../etc/passwd", "container/barrel", "container/chest", "no/such_picture"}; !slices.Equal(got.Missing, want) || len(got.Unreached) != 0 {
		t.Errorf("missing %v, unreached %v; want %v and none", got.Missing, got.Unreached, want)
	}
	if _, ok := got.Pictures["marker/waypoint"]; !ok || len(got.Pictures) != 1 {
		t.Errorf("pictures = %v, want the waypoint alone", keys(got.Pictures))
	}
	for _, path := range *asked {
		if strings.Contains(path, "passwd") || strings.Contains(path, "such_picture") {
			t.Errorf("asked the source for %s", path)
		}
	}

	down := newSamples(t)
	var requests atomic.Int64
	down.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	missing := append(pictureKeys(), langPath)
	slices.Sort(missing)
	got, err = down.source().Fill(t.Context(), missing)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Missing, missing) || !slices.Equal(got.Unreached, missing) || len(got.Pictures) != 0 {
		t.Errorf("with the source refusing: %d missing, %d unreached of %d, %d pictures", len(got.Missing), len(got.Unreached), len(missing), len(got.Pictures))
	}
	// Found out of reach once, not once for each of forty-three files.
	if n := requests.Load(); n > fetchWorkers {
		t.Errorf("%d requests to a source that refused the first", n)
	}
}

// earlierVolume writes a pin's mob icons the way the version before marker
// pictures and names did: an index of a pin and its icons, and no more.
func earlierVolume(t *testing.T, dir, ref string) {
	t.Helper()
	m := &Mobs{Dir: dir, Ref: ref}
	home := m.home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	icons := map[string]string{}
	for kind, body := range map[string][]byte{"cow": picture(t, 16, 16, red), "pig": picture(t, 16, 16, blue)} {
		sum := sha256.Sum256(body)
		icons[kind] = hex.EncodeToString(sum[:])
		if err := os.WriteFile(filepath.Join(home, icons[kind]+".png"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(map[string]any{"ref": ref, "icons": icons})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, indexFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Rolling this version out must never take away what the last one was
// serving. A volume it filled holds mob icons and nothing else; with the
// source out of reach, or its listing rationed out, those are still drawn,
// the page has no pictures and names by tidied ids, and when the source
// answers everything else arrives without the icons being fetched again.
func TestAnEarlierVersionsMobIconsAreServedUntilTheRestCanBeFetched(t *testing.T) {
	dir := t.TempDir()
	earlierVolume(t, dir, testRef)
	s := newSamples(t)
	var up atomic.Bool
	var listings, definitions atomic.Int64
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/list/") {
			listings.Add(1)
		}
		if strings.Contains(r.URL.Path, "/resource_pack/entity/") {
			definitions.Add(1)
		}
		if !up.Load() {
			http.Error(w, "API rate limit exceeded", http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	})
	source := s.source()
	var fills atomic.Int64
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: source.Fetch, RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond,
		Fill: func(ctx context.Context, missing []string) (Set, error) {
			fills.Add(1)
			return source.Fill(ctx, missing)
		}}
	done := make(chan struct{})
	go func() { m.Run(t.Context()); close(done) }()

	waitFor(t, "the source to be asked more than once", func() bool { return fills.Load() >= 3 })
	for kind, want := range map[string]color.NRGBA{"cow": red, "pig": blue} {
		if raw, ok := m.Icon(kind); !ok || colourOf(t, raw) != want {
			t.Errorf("with the source out of reach the %s icon the volume held is not served", kind)
		}
	}
	if version, types := m.Listing(); version == "" || len(types) != 2 {
		t.Errorf("mob icons listed as %v at version %q", types, version)
	}
	if version, keys := m.Pictures(); version != "" || len(keys) != 0 {
		t.Errorf("pictures listed with none fetched: %v at %q", keys, version)
	}
	if got := m.Names().Entity("villager_v2"); got != "Villager" {
		t.Errorf("with no names fetched a villager is %q, want the tidied id", got)
	}
	if got := m.Names().Table(nil).Entities["cow"]; got != "Cow" {
		t.Errorf("a type the volume has an icon for is named %q in the table", got)
	}

	up.Store(true)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rest was never fetched after the source came back")
	}
	if _, keys := m.Pictures(); !slices.Equal(keys, everyPicture()) {
		t.Errorf("pictures after the source came back = %v", keys)
	}
	if got := m.Names().Entity("villager_v2"); got != "Synthetic Villager" {
		t.Errorf("after the source came back a villager is %q", got)
	}
	if raw, ok := m.Icon("pig"); !ok || colourOf(t, raw) != blue {
		t.Error("the pig icon the volume held was lost when the rest arrived")
	}
	// The icons were whole already: neither the rationed listing nor a
	// single definition was asked for, down or up.
	if listings.Load() != 0 || definitions.Load() != 0 {
		t.Errorf("%d listing and %d definition requests to add pictures and names to icons already held", listings.Load(), definitions.Load())
	}

	// And it is on the volume as this version writes it: nothing to ask.
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t), Fill: func(_ context.Context, missing []string) (Set, error) {
		t.Errorf("asked again for %v", missing)
		return Set{}, nil
	}}
	again.Run(t.Context())
	if _, keys := again.Pictures(); len(keys) != len(everyPicture()) {
		t.Errorf("%d pictures read back from the volume", len(keys))
	}
	if _, types := again.Listing(); len(types) != 2 {
		t.Errorf("mob icons read back as %v", types)
	}
}

// On a first start the same holds within one fetch: the icons it read are
// served and kept though the pictures and the names could not be asked
// for, and those follow on the short retry, not a day later.
func TestMobIconsJustFetchedAreServedThoughTheRestCouldNotBeAskedFor(t *testing.T) {
	s := newSamples(t)
	var up atomic.Bool
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mine := strings.Contains(r.URL.Path, "/textures/blocks/") || strings.Contains(r.URL.Path, "/textures/entity/") || strings.Contains(r.URL.Path, "/texts/") ||
			strings.Contains(r.URL.Path, "/items/bed_") || strings.Contains(r.URL.Path, "/items/compass") || strings.Contains(r.URL.Path, "/items/netherbrick")
		if mine && !up.Load() {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		inner.ServeHTTP(w, r)
	})
	source := s.source()
	dir := t.TempDir()
	var fills atomic.Int64
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: source.Fetch, RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond,
		Fill: func(ctx context.Context, missing []string) (Set, error) {
			fills.Add(1)
			return source.Fill(ctx, missing)
		}}
	done := make(chan struct{})
	go func() { m.Run(t.Context()); close(done) }()
	waitFor(t, "the rest to be asked for again", func() bool { return fills.Load() >= 2 })
	if _, types := m.Listing(); len(types) != 3 {
		t.Fatalf("mob icons = %v: the fetch that read them was thrown away for what it could not reach", types)
	}
	// Kept, too: a restart now would not need the listing again.
	if kept, _, err := (&Mobs{Dir: dir, Ref: testRef}).load(); err != nil || len(kept.Mobs) != 3 || len(kept.Missing) == 0 {
		t.Errorf("on the volume: %d mob icons, missing %v, %v", len(kept.Mobs), kept.Missing, err)
	}
	up.Store(true)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rest was never fetched after the source came back")
	}
	if _, keys := m.Pictures(); !slices.Equal(keys, everyPicture()) {
		t.Errorf("pictures after the source came back = %v", keys)
	}
}
