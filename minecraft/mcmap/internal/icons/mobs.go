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
	// indexFormat is raised when a fetch starts reading something more, so
	// that a volume filled by a version that read less is fetched again
	// and not taken as complete.
	indexFormat = 2

	// How long to leave the source alone after it fails. The first wait is
	// short because the usual failure is a network not ready yet at start;
	// the last is long because the unauthenticated listing is rationed by
	// address, and asking more often only spends the ration.
	defaultRetryMin = time.Minute
	defaultRetryMax = time.Hour
)

// Mobs holds the mob icons for one pinned revision and, fetched with them,
// the marker pictures and the display names. It starts empty, which is
// what the page draws dots and rings for and names by tidied ids, and
// fills once all of it has been read from the volume or, failing that,
// fetched. Nothing waits on it.
type Mobs struct {
	// Dir is where what was fetched is kept, one directory per pin.
	Dir string
	Ref string
	// Fetch downloads everything; Source.Fetch outside tests.
	Fetch  func(context.Context) (Set, error)
	Logger *slog.Logger

	// RetryMin and RetryMax bound the wait between failed fetches. Zero
	// uses the defaults.
	RetryMin, RetryMax time.Duration

	mu      sync.RWMutex
	icons   map[string][]byte
	types   []string
	version string

	pictures       map[string][]byte
	pictureKeys    []string
	pictureVersion string
	names          *Names
}

type index struct {
	Format int    `json:"format"`
	Ref    string `json:"ref"`
	// Icons maps a mob type to the SHA-256 of its file, which is also the
	// file's name: several types share one texture.
	Icons map[string]string `json:"icons"`
	// Pictures maps a marker picture's key to its file in the same way.
	Pictures map[string]string `json:"pictures"`
	Lang     map[string]string `json:"lang"`
	Entities []string          `json:"entities"`
	Missing  []string          `json:"missing"`
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
	if set, err := m.load(); err == nil {
		m.set(set)
		m.Logger.Info("mob icons read from the volume", "ref", m.Ref, "types", len(set.Mobs), "pictures", len(set.Pictures), "names", len(set.Lang), "missing", set.Missing)
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
		set, err := m.Fetch(ctx)
		if err == nil {
			metricFetches.WithLabelValues(resultOK).Inc()
			if err := m.store(set); err != nil {
				// Still served from memory; the next start fetches again.
				m.Logger.Warn("mob icons not kept on the volume", "error", err.Error())
			}
			m.set(set)
			m.Logger.Info("mob icons fetched", "ref", m.Ref, "types", len(set.Mobs), "pictures", len(set.Pictures), "names", len(set.Lang), "missing", set.Missing)
			return
		}
		if ctx.Err() != nil {
			return
		}
		metricFetches.WithLabelValues(resultFailed).Inc()
		m.Logger.Warn("mob icons not fetched; the map draws dots and rings, and names by tidied ids, until they are", "ref", m.Ref, "retry_in", wait.String(), "error", err.Error())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, ceiling)
	}
}

// versionOf changes when the pin or any picture in the set does.
func (m *Mobs) versionOf(set map[string][]byte) (string, []string) {
	keys := slices.Sorted(maps.Keys(set))
	sum := sha256.New()
	sum.Write([]byte(m.Ref))
	for _, key := range keys {
		fmt.Fprintf(sum, "\x00%s\x00", key)
		sum.Write(set[key])
	}
	return hex.EncodeToString(sum.Sum(nil)[:8]), keys
}

func (m *Mobs) set(set Set) {
	version, types := m.versionOf(set.Mobs)
	pictureVersion, pictureKeys := m.versionOf(set.Pictures)
	names := NewNames(set.Lang, set.Entities)
	m.mu.Lock()
	m.icons, m.types, m.version = set.Mobs, types, version
	m.pictures, m.pictureKeys, m.pictureVersion = set.Pictures, pictureKeys, pictureVersion
	m.names = names
	m.mu.Unlock()
	metricMobTypes.Set(float64(len(types)))
	metricPictures.Set(float64(len(pictureKeys)))
	metricNames.Set(float64(names.Translated()))
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

// Picture is the PNG for a marker or structure picture, by its key, if
// there is one.
func (m *Mobs) Picture(key string) ([]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	png, ok := m.pictures[key]
	return png, ok
}

// Pictures is the key of every marker and structure picture there is, and
// a version that changes when the pin or any of them does. Both are empty
// until the set has filled.
func (m *Mobs) Pictures() (version string, keys []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.pictureKeys) == 0 {
		return "", nil
	}
	return m.pictureVersion, m.pictureKeys
}

// Names is the display names. Before the set has filled, and for as long
// as the language file is out of reach, they are tidied ids.
func (m *Mobs) Names() *Names {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.names
}

func (m *Mobs) load() (Set, error) {
	home := m.home()
	raw, err := os.ReadFile(filepath.Join(home, indexFile))
	if err != nil {
		return Set{}, err
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return Set{}, err
	}
	if idx.Format != indexFormat || idx.Ref != m.Ref || len(idx.Icons) == 0 || len(idx.Icons) > maxDefinitions ||
		len(idx.Pictures) > maxPictures || len(idx.Lang) > maxLangNames || len(idx.Entities) > maxDefinitions || len(idx.Missing) > maxPictures+1 {
		return Set{}, errors.New("the icon index is not for this pin")
	}
	files := map[string][]byte{}
	read := func(digest string) ([]byte, error) {
		if !isDigest(digest) {
			return nil, errors.New("the icon index is damaged")
		}
		if body, seen := files[digest]; seen {
			return body, nil
		}
		body, err := os.ReadFile(filepath.Join(home, digest+".png"))
		if err != nil {
			return nil, err
		}
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
			return nil, fmt.Errorf("icon %s on the volume is damaged", digest)
		}
		files[digest] = body
		return body, nil
	}
	out := Set{Mobs: make(map[string][]byte, len(idx.Icons)), Pictures: make(map[string][]byte, len(idx.Pictures)), Entities: idx.Entities, Missing: idx.Missing}
	for kind, digest := range idx.Icons {
		if !mobType.MatchString(kind) {
			return Set{}, errors.New("the icon index is damaged")
		}
		if out.Mobs[kind], err = read(digest); err != nil {
			return Set{}, err
		}
	}
	for key, digest := range idx.Pictures {
		if !pictureKey.MatchString(key) {
			return Set{}, errors.New("the icon index is damaged")
		}
		if out.Pictures[key], err = read(digest); err != nil {
			return Set{}, err
		}
	}
	// The names are held to what the language file was, since they go to
	// a browser as they are and nothing vouches for the index but its
	// place on the volume.
	for key, name := range idx.Lang {
		if clean, ok := cleanLangName(name); !ok || clean != name || !wantedLangKey(key) {
			return Set{}, errors.New("the icon index is damaged")
		}
	}
	out.Lang = idx.Lang
	if slices.ContainsFunc(idx.Entities, func(kind string) bool { return !mobType.MatchString(kind) }) {
		return Set{}, errors.New("the icon index is damaged")
	}
	return out, nil
}

func isDigest(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == sha256.Size
}

// store writes the set beside where it will live and renames it into place,
// so a start never finds half of one, then removes what other pins left.
func (m *Mobs) store(set Set) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(m.Dir, "install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	idx := index{Format: indexFormat, Ref: m.Ref, Icons: map[string]string{}, Pictures: map[string]string{}, Lang: set.Lang, Entities: set.Entities, Missing: set.Missing}
	for _, group := range []struct {
		from map[string][]byte
		to   map[string]string
	}{{set.Mobs, idx.Icons}, {set.Pictures, idx.Pictures}} {
		for key, body := range group.from {
			sum := sha256.Sum256(body)
			digest := hex.EncodeToString(sum[:])
			group.to[key] = digest
			if err := os.WriteFile(filepath.Join(staging, digest+".png"), body, 0o644); err != nil {
				return err
			}
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
