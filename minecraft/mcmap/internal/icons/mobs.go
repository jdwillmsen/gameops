package icons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const (
	indexFile = "index.json"

	// How long to leave the source alone after it fails. The first wait is
	// short because the usual failure is a network not ready yet at start;
	// the last is long because the unauthenticated listing is rationed by
	// address, and asking more often only spends the ration.
	defaultRetryMin = time.Minute
	defaultRetryMax = time.Hour
)

// Mobs holds the mob icons for one pinned revision. It starts empty, which
// is what the page draws dots for, and fills once the icons have been read
// from the volume or, failing that, fetched. Nothing waits on it.
type Mobs struct {
	// Dir is where fetched icons are kept, one directory per pin.
	Dir string
	Ref string
	// Fetch downloads every icon; Source.Fetch outside tests.
	Fetch  func(context.Context) (map[string][]byte, error)
	Logger *slog.Logger

	// RetryMin and RetryMax bound the wait between failed fetches. Zero
	// uses the defaults.
	RetryMin, RetryMax time.Duration

	mu      sync.RWMutex
	icons   map[string][]byte
	types   []string
	version string
}

type index struct {
	Ref string `json:"ref"`
	// Icons maps a mob type to the SHA-256 of its file, which is also the
	// file's name: several types share one texture.
	Icons map[string]string `json:"icons"`
}

// home is the directory for this pin. The pin is hashed because it is a
// setting and may hold characters a path should not.
func (m *Mobs) home() string {
	sum := sha256.Sum256([]byte(m.Ref))
	return filepath.Join(m.Dir, hex.EncodeToString(sum[:8]))
}

// Run fills the set and returns when it is full or ctx ends. What is on the
// volume for this pin is used if every file of it is intact, so a restart
// asks the source for nothing; otherwise the source is asked until it
// answers, with a growing wait in between.
func (m *Mobs) Run(ctx context.Context) {
	if icons, err := m.load(); err == nil {
		m.set(icons)
		m.Logger.Info("mob icons read from the volume", "ref", m.Ref, "types", len(icons))
		return
	}
	wait, ceiling := m.RetryMin, m.RetryMax
	if wait <= 0 {
		wait = defaultRetryMin
	}
	if ceiling <= 0 {
		ceiling = defaultRetryMax
	}
	for {
		icons, err := m.Fetch(ctx)
		if err == nil {
			metricFetches.WithLabelValues(resultOK).Inc()
			if err := m.store(icons); err != nil {
				// Still served from memory; the next start fetches again.
				m.Logger.Warn("mob icons not kept on the volume", "error", err.Error())
			}
			m.set(icons)
			m.Logger.Info("mob icons fetched", "ref", m.Ref, "types", len(icons))
			return
		}
		if ctx.Err() != nil {
			return
		}
		metricFetches.WithLabelValues(resultFailed).Inc()
		m.Logger.Warn("mob icons not fetched; the map draws dots until they are", "ref", m.Ref, "retry_in", wait.String(), "error", err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, ceiling)
	}
}

func (m *Mobs) set(icons map[string][]byte) {
	types := slices.Sorted(maps.Keys(icons))
	sum := sha256.New()
	sum.Write([]byte(m.Ref))
	for _, kind := range types {
		fmt.Fprintf(sum, "\x00%s\x00", kind)
		sum.Write(icons[kind])
	}
	m.mu.Lock()
	m.icons, m.types, m.version = icons, types, hex.EncodeToString(sum.Sum(nil)[:8])
	m.mu.Unlock()
	metricMobTypes.Set(float64(len(types)))
}

// Icon is the PNG for a mob type, if there is one.
func (m *Mobs) Icon(kind string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	png, ok := m.icons[kind]
	return png, ok
}

// Listing is every type that has an icon, and a version that changes when
// the pin or any icon does. Both are empty until the set has filled.
func (m *Mobs) Listing() (version string, types []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version, m.types
}

func (m *Mobs) load() (map[string][]byte, error) {
	home := m.home()
	raw, err := os.ReadFile(filepath.Join(home, indexFile))
	if err != nil {
		return nil, err
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, err
	}
	if idx.Ref != m.Ref || len(idx.Icons) == 0 || len(idx.Icons) > maxDefinitions {
		return nil, errors.New("the icon index is not for this pin")
	}
	files := map[string][]byte{}
	out := make(map[string][]byte, len(idx.Icons))
	for kind, digest := range idx.Icons {
		if !mobType.MatchString(kind) || !isDigest(digest) {
			return nil, errors.New("the icon index is damaged")
		}
		body, seen := files[digest]
		if !seen {
			if body, err = os.ReadFile(filepath.Join(home, digest+".png")); err != nil {
				return nil, err
			}
			if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
				return nil, fmt.Errorf("icon %s on the volume is damaged", digest)
			}
			files[digest] = body
		}
		out[kind] = body
	}
	return out, nil
}

func isDigest(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == sha256.Size
}

// store writes the set beside where it will live and renames it into place,
// so a start never finds half of one, then removes what other pins left.
func (m *Mobs) store(icons map[string][]byte) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(m.Dir, "install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	idx := index{Ref: m.Ref, Icons: map[string]string{}}
	for kind, body := range icons {
		sum := sha256.Sum256(body)
		digest := hex.EncodeToString(sum[:])
		idx.Icons[kind] = digest
		if err := os.WriteFile(filepath.Join(staging, digest+".png"), body, 0o644); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, indexFile), raw, 0o644); err != nil {
		return err
	}
	home := m.home()
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	if err := os.Rename(staging, home); err != nil {
		return err
	}
	others, _ := os.ReadDir(m.Dir)
	for _, other := range others {
		if other.Name() != filepath.Base(home) {
			_ = os.RemoveAll(filepath.Join(m.Dir, other.Name()))
		}
	}
	return nil
}
