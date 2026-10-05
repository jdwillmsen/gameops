package icons

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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
		Fetch: func(context.Context) (map[string][]byte, error) {
			calls.Add(1)
			select {
			case <-answer:
				return map[string][]byte{"cow": picture(t, 16, 16, red)}, nil
			default:
				return nil, errors.New("503 Service Unavailable")
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
		Fetch: func(context.Context) (map[string][]byte, error) { return nil, errors.New("unreachable") }}
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
	m := &Mobs{Dir: dir, Ref: ref, Logger: quiet(), Fetch: func(context.Context) (map[string][]byte, error) {
		return map[string][]byte{"cow": picture(t, 16, 16, red), "pig": picture(t, 16, 16, blue), "mooshroom": picture(t, 16, 16, red)}, nil
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
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (map[string][]byte, error) {
		t.Error("the source was asked again with the volume intact")
		cancel()
		return nil, errors.New("not expected")
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
			m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (map[string][]byte, error) {
				fetched.Add(1)
				return map[string][]byte{"cow": picture(t, 16, 16, green)}, nil
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
