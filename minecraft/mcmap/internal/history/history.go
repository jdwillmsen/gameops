// Package history keeps the map as it was on each day, so a place can be
// looked at over time.
//
// What is kept is the rendered tiles and not the world. A tile is an image:
// showing it again needs nothing of the game's version, the save format or
// the renderer that drew it. And a day of tiles costs only the tiles that
// changed, where a day of the world costs nearly a whole world, since
// compaction rewrites a third or more of its bytes every quarter of an hour.
package history

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

const (
	// blobsDir holds each distinct tile once, named by the SHA-256 of its
	// bytes, so a tile that did not change between two days is one file.
	blobsDir = "blobs"
	// daysDir holds one manifest per day. A day exists once its manifest
	// does, and a manifest is written after every blob it names.
	daysDir = "days"

	manifestSuffix  = ".json.gz"
	manifestVersion = 1
	dayLayout       = "2006-01-02"
	tempPrefix      = ".tmp-"

	// A tile is a few hundred kilobytes at the very most. Anything near
	// this is not a tile, and is refused before it is read whole.
	maxTileBytes = 32 << 20
	// Decoded manifests held for tile requests: enough for a comparison of
	// two days and a neighbour of each.
	cachedManifests = 4
)

// Source is what a capture needs from the renderer whose tiles it keeps.
type Source interface {
	Info(out string) (render.Info, error)
	Tiles(out, format string, yield func(render.Tile, string) error) error
}

// Stamp is what drew a day's tiles. Two days with different stamps may
// differ everywhere without anything in the world having changed.
type Stamp struct {
	SHA256     string `json:"sha256"`
	NetherTopY int    `json:"netherTopY"`
}

type manifest struct {
	Version    int                  `json:"version"`
	Day        string               `json:"day"`
	TakenAt    time.Time            `json:"takenAt"`
	Renderer   Stamp                `json:"renderer"`
	Dimensions map[string]dimension `json:"dimensions"`
}

type dimension struct {
	Info render.Info `json:"info"`
	// Changed is how many of Tiles differ from the day before, or were not
	// in it. It is worked out once, at capture, while both days are at hand.
	Changed int `json:"changed"`
	// Tiles is the blob at each address, "zoom/x/y".
	Tiles map[string]string `json:"tiles"`
}

// Day is one captured day, without its tiles.
type Day struct {
	Day      string    `json:"day"`
	TakenAt  time.Time `json:"takenAt"`
	Renderer Stamp     `json:"renderer"`
	// RendererChanged is true on a day drawn differently from the one
	// before it, where a difference between the two is not all the world's.
	RendererChanged bool           `json:"rendererChanged,omitempty"`
	Dimensions      []DayDimension `json:"dimensions"`
}

// DayDimension is a dimension's extent on one day.
type DayDimension struct {
	ID string `json:"id"`
	render.Info
	Tiles   int `json:"tiles"`
	Changed int `json:"changed"`
}

// Result is what a capture did.
type Result struct {
	Day string
	// Skipped means the day was already held and nothing was read.
	Skipped bool
	Tiles   int
	Changed int
	// NewBytes is what the day added to the store.
	NewBytes int64
}

// Store is the days kept under one directory.
type Store struct {
	// MapsDir is where the renderer's output is, one directory a dimension.
	MapsDir  string
	Source   Source
	Renderer Stamp

	dir    string
	logger *slog.Logger

	// capturing keeps two captures from building the same day.
	capturing sync.Mutex

	mu    sync.Mutex
	days  map[string]Day
	bytes int64
	cache map[string]*manifest
	// used is the cached days, least recently asked for first.
	used []string
}

// Open reads what is held under dir, creating it if there is nothing. A
// manifest that cannot be read, or is of a version this code does not know,
// is left out and logged: the other days are still good, and guessing at
// one would serve tiles from the wrong places.
func Open(dir string, logger *slog.Logger) (*Store, error) {
	s := &Store{dir: dir, logger: logger, days: map[string]Day{}, cache: map[string]*manifest{}}
	for _, sub := range []string{blobsDir, daysDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
		// What a killed capture left half-written; nothing else writes here.
		leftovers, _ := filepath.Glob(filepath.Join(dir, sub, tempPrefix+"*"))
		for _, p := range leftovers {
			_ = os.Remove(p)
		}
	}
	err := filepath.WalkDir(filepath.Join(dir, blobsDir), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		s.bytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, daysDir))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), manifestSuffix)
		if !ok || !isDay(day) {
			continue
		}
		m, err := readManifest(filepath.Join(dir, daysDir), day)
		if err != nil {
			logger.Error("a day of map history cannot be read and is left out", "day", day, "error", err)
			continue
		}
		s.days[day] = summary(m)
	}
	s.publish()
	return s, nil
}

func isDay(s string) bool {
	t, err := time.Parse(dayLayout, s)
	return err == nil && t.Format(dayLayout) == s
}

func isHash(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == sha256.Size && strings.ToLower(s) == s
}

func tileKey(t render.Tile) string {
	return fmt.Sprintf("%d/%d/%d", t.Zoom, t.X, t.Y)
}

func summary(m *manifest) Day {
	d := Day{Day: m.Day, TakenAt: m.TakenAt, Renderer: m.Renderer}
	for _, id := range render.Dimensions {
		if dim, ok := m.Dimensions[id]; ok {
			d.Dimensions = append(d.Dimensions, DayDimension{ID: id, Info: dim.Info, Tiles: len(dim.Tiles), Changed: dim.Changed})
		}
	}
	return d
}

func readManifest(dir, day string) (*manifest, error) {
	f, err := os.Open(filepath.Join(dir, day+manifestSuffix))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.NewDecoder(gz).Decode(&m); err != nil {
		return nil, err
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("manifest version %d, and this reads version %d", m.Version, manifestVersion)
	}
	if m.Day != day {
		return nil, fmt.Errorf("manifest is of %q", m.Day)
	}
	return &m, nil
}

// writeManifest is what makes a day exist, so the file is flushed before it
// is given its name and the name before this returns.
func writeManifest(dir string, m manifest) error {
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	gz := gzip.NewWriter(tmp)
	err = json.NewEncoder(gz).Encode(m)
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, m.Day+manifestSuffix)); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Days is every day held, oldest first.
func (s *Store) Days() []Day {
	s.mu.Lock()
	defer s.mu.Unlock()
	days := make([]Day, 0, len(s.days))
	for _, d := range s.days {
		days = append(days, d)
	}
	slices.SortFunc(days, func(a, b Day) int { return strings.Compare(a.Day, b.Day) })
	for i := 1; i < len(days); i++ {
		days[i].RendererChanged = days[i].Renderer != days[i-1].Renderer
	}
	return days
}

// Bytes is the size of every tile held, each counted once.
func (s *Store) Bytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

func (s *Store) publish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	metricDays.Set(float64(len(s.days)))
	metricBytes.Set(float64(s.bytes))
}

// manifestOf returns a held day's manifest. s.mu is held by the caller.
func (s *Store) manifestOf(day string) (*manifest, bool) {
	if _, held := s.days[day]; !held {
		return nil, false
	}
	if m, hit := s.cache[day]; hit {
		s.used = append(slices.DeleteFunc(s.used, func(d string) bool { return d == day }), day)
		return m, true
	}
	m, err := readManifest(filepath.Join(s.dir, daysDir), day)
	if err != nil {
		s.logger.Error("a day of map history can no longer be read", "day", day, "error", err)
		return nil, false
	}
	if len(s.used) >= cachedManifests {
		delete(s.cache, s.used[0])
		s.used = s.used[1:]
	}
	s.cache[day] = m
	s.used = append(s.used, day)
	return m, true
}

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.dir, blobsDir, hash[:2], hash)
}

// Tile is the file holding a tile as it was on a day, and its format. The
// day is looked up among the days held and never made into a path, so no
// spelling of it reaches outside the store.
func (s *Store) Tile(day, dimension string, tile render.Tile) (path, format string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.manifestOf(day)
	if !ok {
		return "", "", false
	}
	dim, ok := m.Dimensions[dimension]
	if !ok {
		return "", "", false
	}
	hash, ok := dim.Tiles[tileKey(tile)]
	if !ok || !isHash(hash) {
		return "", "", false
	}
	return s.blobPath(hash), dim.Info.Format, true
}

// before is the newest day held that is earlier than day, if there is one.
func (s *Store) before(day string) (*manifest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	newest := ""
	for d := range s.days {
		if d < day && d > newest {
			newest = d
		}
	}
	return s.manifestOf(newest)
}

// Capture keeps the map as it stands as the day at falls on, in UTC. A day
// already held is left as it is: the first capture of a day is the day.
//
// The caller must not be rendering. A day is every tile hashed, the ones
// not yet held copied in and flushed, and then the manifest; a capture that
// stops anywhere before the manifest has added no day, and the tiles it
// did copy are found already held by the next one.
func (s *Store) Capture(ctx context.Context, at time.Time) (Result, error) {
	s.capturing.Lock()
	defer s.capturing.Unlock()

	day := at.UTC().Format(dayLayout)
	s.mu.Lock()
	_, held := s.days[day]
	s.mu.Unlock()
	if held {
		return Result{Day: day, Skipped: true}, nil
	}

	started := time.Now()
	result, err := s.capture(ctx, day, at)
	s.publish()
	if err != nil {
		metricCaptures.WithLabelValues("failed").Inc()
		return Result{}, fmt.Errorf("keeping the map of %s: %w", day, err)
	}
	metricCaptures.WithLabelValues("captured").Inc()
	metricCapturedAt.Set(float64(at.Unix()))
	metricCaptureSeconds.Set(time.Since(started).Seconds())
	metricCaptureTiles.Set(float64(result.Tiles))
	metricCaptureChanged.Set(float64(result.Changed))
	metricCaptureBytes.Set(float64(result.NewBytes))
	return result, nil
}

func (s *Store) capture(ctx context.Context, day string, at time.Time) (Result, error) {
	previous, _ := s.before(day)
	m := manifest{Version: manifestVersion, Day: day, TakenAt: at.UTC(), Renderer: s.Renderer, Dimensions: map[string]dimension{}}
	result := Result{Day: day}
	for _, id := range render.Dimensions {
		out := filepath.Join(s.MapsDir, id)
		info, err := s.Source.Info(out)
		if err != nil {
			return result, fmt.Errorf("%s is not rendered: %w", id, err)
		}
		dim := dimension{Info: info, Tiles: map[string]string{}}
		var was map[string]string
		if previous != nil {
			was = previous.Dimensions[id].Tiles
		}
		err = s.Source.Tiles(out, info.Format, func(tile render.Tile, path string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			hash, added, err := s.keep(path)
			if err != nil {
				return err
			}
			key := tileKey(tile)
			dim.Tiles[key] = hash
			if was[key] != hash {
				dim.Changed++
			}
			result.NewBytes += added
			return nil
		})
		if err != nil {
			return result, err
		}
		result.Tiles += len(dim.Tiles)
		result.Changed += dim.Changed
		m.Dimensions[id] = dim
	}
	if err := writeManifest(filepath.Join(s.dir, daysDir), m); err != nil {
		return result, err
	}
	s.mu.Lock()
	s.days[day] = summary(&m)
	s.mu.Unlock()
	return result, nil
}

// keep makes sure the store holds the tile at path, and returns the name it
// is held under and how many bytes that added.
func (s *Store) keep(path string) (hash string, added int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTileBytes+1))
	f.Close()
	if err != nil {
		return "", 0, err
	}
	if len(data) > maxTileBytes {
		return "", 0, fmt.Errorf("%s is over %d bytes, which no tile is", filepath.Base(path), maxTileBytes)
	}
	sum := sha256.Sum256(data)
	hash = hex.EncodeToString(sum[:])
	blob := s.blobPath(hash)
	if info, err := os.Stat(blob); err == nil && info.Size() == int64(len(data)) {
		return hash, 0, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", 0, err
	}

	// Flushed before it has its name: a manifest must never name a blob
	// that a power cut could still take.
	tmp, err := os.CreateTemp(filepath.Join(s.dir, blobsDir), tempPrefix+"*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), blob); err != nil {
		return "", 0, err
	}
	s.mu.Lock()
	s.bytes += int64(len(data))
	s.mu.Unlock()
	return hash, int64(len(data)), nil
}
