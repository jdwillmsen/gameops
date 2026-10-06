package icons

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestMobsAreEmptyWhileTheSourceFailsAndFillWhenItAnswers(t *testing.T) {
	var calls atomic.Int64
	answer := make(chan struct{})
	m := &Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet(), RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond,
		Fetch: func(context.Context) (Set, error) {
			calls.Add(1)
			select {
			case <-answer:
				return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}}, nil
			default:
				return Set{}, errors.New("503 Service Unavailable")
			}
		}}
	done := make(chan struct{})
	go func() { m.Run(t.Context()); close(done) }()

	waitFor(t, "the fetch to be retried", func() bool { return calls.Load() >= 3 })
	if version, types := m.Listing(); version != "" || len(types) != 0 {
		t.Errorf("a set that was never fetched lists %v at version %q", types, version)
	}
	if _, ok := m.Icon("cow"); ok {
		t.Error("an icon was served before any was fetched")
	}

	close(answer)
	<-done
	if _, ok := m.Icon("cow"); !ok {
		t.Fatal("no icon after the source answered")
	}
	if version, types := m.Listing(); version == "" || len(types) != 1 || types[0] != "cow" {
		t.Errorf("listing is %v at version %q", types, version)
	}
}

func TestMobsGiveUpWhenToldToStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m := &Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet(), RetryMin: time.Hour,
		Fetch: func(context.Context) (Set, error) { return Set{}, errors.New("unreachable") }}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run outlived its context while waiting to retry")
	}
}

func filled(t *testing.T, dir, ref string) *Mobs {
	t.Helper()
	m := &Mobs{Dir: dir, Ref: ref, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{
			Mobs:     map[string][]byte{"cow": picture(t, 16, 16, red), "pig": picture(t, 16, 16, blue), "mooshroom": picture(t, 16, 16, red)},
			Pictures: map[string][]byte{"container/chest": picture(t, 16, 16, yellow), "bed/red": picture(t, 16, 16, red)},
			Lang:     map[string]string{"entity.cow.name": "Cow", "tile.chest.name": "Chest"},
			Entities: []string{"cow", "pig", "villager_v2"},
			Missing:  []string{"bed/blue"},
		}, nil
	}}
	m.Run(t.Context())
	return m
}

func TestMobsAreNotFetchedAgainWhileTheVolumeHoldsThisPin(t *testing.T) {
	dir := t.TempDir()
	first := filled(t, dir, testRef)

	// Cancelled by the fetch that must not happen, so that a failure is a
	// failure and not a wait for the retry after it.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		t.Error("the source was asked again with the volume intact")
		cancel()
		return Set{}, errors.New("not expected")
	}}
	again.Run(ctx)
	if t.Failed() {
		return
	}
	if got := colourOf(t, mustIcon(t, again, "pig")); got != blue {
		t.Errorf("pig read back as %v", got)
	}
	v1, _ := first.Listing()
	v2, types := again.Listing()
	if v1 != v2 || len(types) != 3 {
		t.Errorf("the same icons list as %q then %q, %v", v1, v2, types)
	}
}

func mustIcon(t *testing.T, m *Mobs, kind string) []byte {
	t.Helper()
	raw, ok := m.Icon(kind)
	if !ok {
		t.Fatalf("no icon for %s", kind)
	}
	return raw
}

func TestMobsAreFetchedAgainWhenTheVolumeIsDamagedOrThePinMoves(t *testing.T) {
	damage := map[string]func(t *testing.T, home string){
		"an icon overwritten": func(t *testing.T, home string) {
			files, _ := filepath.Glob(filepath.Join(home, "*.png"))
			if len(files) == 0 {
				t.Fatal("no icons on the volume")
			}
			if err := os.WriteFile(files[0], []byte("not what was stored"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"an icon deleted": func(t *testing.T, home string) {
			files, _ := filepath.Glob(filepath.Join(home, "*.png"))
			if err := os.Remove(files[0]); err != nil {
				t.Fatal(err)
			}
		},
		"the index cut short": func(t *testing.T, home string) {
			if err := os.WriteFile(filepath.Join(home, indexFile), []byte(`{"ref":`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, harm := range damage {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			harm(t, filled(t, dir, testRef).home())
			var fetched atomic.Int64
			m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
				fetched.Add(1)
				return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, green)}}, nil
			}}
			m.Run(t.Context())
			if fetched.Load() != 1 {
				t.Fatalf("the source was asked %d times, want 1", fetched.Load())
			}
			if got := colourOf(t, mustIcon(t, m, "cow")); got != green {
				t.Errorf("cow is %v, not the refetched icon", got)
			}
		})
	}

	t.Run("the pin moved", func(t *testing.T) {
		dir := t.TempDir()
		old := filled(t, dir, testRef)
		oldVersion, _ := old.Listing()
		moved := filled(t, dir, "v10.0.0")
		if v, _ := moved.Listing(); v == oldVersion {
			t.Error("the version did not change with the pin, so browsers would keep the old icons")
		}
		if _, err := os.Stat(old.home()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the old pin's icons are still on the volume: %v", err)
		}
		left, _ := os.ReadDir(dir)
		if len(left) != 1 {
			t.Errorf("%d directories left on the volume, want only the current pin's", len(left))
		}
	})
}

func TestPicturesAndNamesAreEmptyUntilFetchedAndKeptOnTheVolumeAfter(t *testing.T) {
	never := &Mobs{Dir: t.TempDir(), Ref: testRef, Logger: quiet()}
	if version, keys := never.Pictures(); version != "" || len(keys) != 0 {
		t.Errorf("a set that was never fetched lists pictures %v at version %q", keys, version)
	}
	if _, ok := never.Picture("container/chest"); ok {
		t.Error("a picture was served before any was fetched")
	}
	// Names are never absent: unfetched, they are tidied ids.
	if got := never.Names().Entity("villager_v2"); got != "Villager" {
		t.Errorf("before any fetch a villager is %q", got)
	}

	dir := t.TempDir()
	first := filled(t, dir, testRef)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		t.Error("the source was asked again with the volume intact")
		cancel()
		return Set{}, errors.New("not expected")
	}}
	again.Run(ctx)
	if t.Failed() {
		return
	}
	for _, m := range []*Mobs{first, again} {
		version, keys := m.Pictures()
		if version == "" || len(keys) != 2 || keys[0] != "bed/red" || keys[1] != "container/chest" {
			t.Errorf("pictures listed as %v at version %q", keys, version)
		}
		raw, ok := m.Picture("container/chest")
		if !ok || colourOf(t, raw) != yellow {
			t.Errorf("the chest read back wrong (held: %v)", ok)
		}
		if _, ok := m.Picture("bed/blue"); ok {
			t.Error("a picture the fetch reported missing is served")
		}
		names := m.Names()
		if names.Entity("cow") != "Cow" || names.Container("chest") != "Chest" || names.Entity("villager_v2") != "Villager" {
			t.Errorf("names read back as %q, %q, %q", names.Entity("cow"), names.Container("chest"), names.Entity("villager_v2"))
		}
		// A type the samples define is in the table whether or not the
		// language file names it.
		if got := names.Table(nil).Entities; got["villager_v2"] != "Villager" || got["pig"] != "Pig" || got["cow"] != "Cow" {
			t.Errorf("table entities = %v", got)
		}
	}
	v1, _ := first.Pictures()
	v2, _ := again.Pictures()
	moved, _ := filled(t, t.TempDir(), "v10.0.0").Pictures()
	if v1 != v2 || moved == v1 {
		t.Errorf("picture versions: %q, then %q from the volume, and %q at another pin", v1, v2, moved)
	}
}

func TestTheVolumeIsNotTrustedForWhatAnEarlierVersionOrADamagedIndexLeft(t *testing.T) {
	rewrite := func(edit func(idx *index)) func(t *testing.T, home string) {
		return func(t *testing.T, home string) {
			raw, err := os.ReadFile(filepath.Join(home, indexFile))
			if err != nil {
				t.Fatal(err)
			}
			var idx index
			if err := json.Unmarshal(raw, &idx); err != nil {
				t.Fatal(err)
			}
			edit(&idx)
			if raw, err = json.Marshal(idx); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, indexFile), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, harm := range map[string]func(t *testing.T, home string){
		"a name that is markup for a terminal": rewrite(func(idx *index) { idx.Lang["entity.cow.name"] = "Cow\x1b[31m" }),
		"a name too long to be one":            rewrite(func(idx *index) { idx.Lang["entity.cow.name"] = strings.Repeat("x", MaxNameLength+1) }),
		"a key the language file has no such":  rewrite(func(idx *index) { idx.Lang["gui.ok"] = "OK" }),
		"a picture key that is a path":         rewrite(func(idx *index) { idx.Pictures["../../etc/passwd"] = idx.Pictures["bed/red"] }),
		"an entity type that is not one":       rewrite(func(idx *index) { idx.Entities = append(idx.Entities, "<script>") }),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			harm(t, filled(t, dir, testRef).home())
			var fetched atomic.Int64
			m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
				fetched.Add(1)
				return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, green)}, Pictures: map[string][]byte{"container/chest": picture(t, 16, 16, green)}}, nil
			}}
			m.Run(t.Context())
			if fetched.Load() != 1 {
				t.Fatalf("the source was asked %d times, want 1", fetched.Load())
			}
			if raw, ok := m.Picture("container/chest"); !ok || colourOf(t, raw) != green {
				t.Error("the chest is not the refetched picture")
			}
		})
	}
}
