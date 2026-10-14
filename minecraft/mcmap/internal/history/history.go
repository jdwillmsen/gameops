// Package history keeps the map as it was on each day, so a place can be
// looked at over time.
//
// What is kept is the rendered tiles and not the world. A tile is an image:
// showing it again needs nothing of the game's version, the save format or
// the renderer that drew it. And a day of tiles costs only the tiles that
// changed, where a day of the world costs nearly a whole world, since
// compaction rewrites a third or more of its bytes every quarter of an hour.
//
// With a day's tiles go the markers the map served over them, where there
// were any: a few hundred kilobytes that say what stood on that terrain.
package history

import (
	"cmp"
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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

const (
	// blobsDir holds each distinct tile once, named by the SHA-256 of its
	// bytes, so a tile that did not change between two days is one file. A
	// dimension's markers are held the same way, beside the tiles.
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

	// The zoom whose tiles are compared to say where a day differs from the
	// one before: the finest, where a tile is 256 blocks square.
	changesZoom = 0
	// The most tiles named as changed in one answer. A day drawn by another
	// renderer differs everywhere, and a list of everywhere outlines nothing.
	maxChanges = 4096
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
	// Markers is the blob holding the dimension's markers as the map
	// served them that day, or empty where none were kept. A reader from
	// before there were any passes over it, so the version is the same.
	Markers string `json:"markers,omitempty"`
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
	// Markers is whether the day kept the dimension's markers.
	Markers bool `json:"markers,omitempty"`
}

// Changes is where a day's terrain differs from the day held before it.
type Changes struct {
	Day string `json:"day"`
	// Before is the day compared with: the newest held that is earlier, or
	// empty for the first day held, which has nothing to differ from.
	Before string `json:"before"`
	Zoom   int    `json:"zoom"`
	// Tiles is the address, x then y, of each tile at that zoom whose bytes
	// are not the earlier day's: drawn differently, added, or gone since.
	Tiles [][2]int `json:"tiles"`
	// Of is how many addresses the two days hold between them at that zoom.
	Of int `json:"of"`
	// More is how many changed tiles were left out at the limit.
	More int `json:"more"`
	// Settled is true where no day can come between the two: the earlier
	// one is the calendar day before, so the answer stands for as long as
	// both are held.
	Settled bool `json:"-"`
}

// Result is what a capture did.
type Result struct {
	Day string
	// Skipped means the day was already held and nothing was read.
	Skipped bool
	Tiles   int
	Changed int
	// Markers is how many dimensions' markers were kept with the day.
	Markers int
	// NewBytes is what the day added to the store.
	NewBytes int64
}

// Store is the days kept under one directory.
type Store struct {
	// MapsDir is where the renderer's output is, one directory a dimension.
	MapsDir  string
	Source   Source
	Renderer Stamp
	// Markers, if set, is asked at each capture for a dimension's markers
	// as the map serves them, and says false while it has none to keep.
	Markers func(dimension string) (body []byte, ok bool)

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
			d.Dimensions = append(d.Dimensions, DayDimension{ID: id, Info: dim.Info, Tiles: len(dim.Tiles), Changed: dim.Changed, Markers: dim.Markers != ""})
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

// Bytes is the size of every tile and every set of markers held, each
// counted once.
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
	var newest Day
	for _, d := range s.days {
		if d.Day > newest.Day {
			newest = d
		}
	}
	if newest.Day != "" {
		metricCapturedAt.Set(float64(newest.TakenAt.Unix()))
	}
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

// MarkersOn is the file holding a dimension's markers as they were on a
// day, where the day kept them. As for a tile, the day is looked up among
// the days held and never made into a path.
func (s *Store) MarkersOn(day, dimension string) (path string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.manifestOf(day)
	if !ok {
		return "", false
	}
	hash := m.Dimensions[dimension].Markers
	if !isHash(hash) {
		return "", false
	}
	return s.blobPath(hash), true
}

// earlier is the newest day held that is before day, or empty if there is
// none. s.mu is held by the caller.
func (s *Store) earlier(day string) string {
	newest := ""
	for d := range s.days {
		if d < day && d > newest {
			newest = d
		}
	}
	return newest
}

// before is the newest day held that is earlier than day, if there is one.
func (s *Store) before(day string) (*manifest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifestOf(s.earlier(day))
}

// finest is a tile's x and y, for an address at the zoom that is compared.
func finest(key string) (at [2]int, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != strconv.Itoa(changesZoom) {
		return at, false
	}
	x, errX := strconv.Atoi(parts[1])
	y, errY := strconv.Atoi(parts[2])
	return [2]int{x, y}, errX == nil && errY == nil
}

// Changes is where a dimension's terrain on a day differs from the day held
// before it, worked out from the two manifests: no tile is read. The day
// before is whichever is held now, and not the one there was when the day
// was kept, so a day put in between them later is what the answer is then
// against. A day that did not hold the dimension has no answer; an earlier
// day that did not is one every tile differs from, as a capture counts it.
func (s *Store) Changes(day, dimension string) (Changes, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.manifestOf(day)
	if !ok {
		return Changes{}, false
	}
	dim, ok := m.Dimensions[dimension]
	if !ok {
		return Changes{}, false
	}
	out := Changes{Day: day, Zoom: changesZoom, Tiles: [][2]int{}}
	out.Before = s.earlier(day)
	if out.Before == "" {
		return out, true
	}
	previous, ok := s.manifestOf(out.Before)
	if !ok {
		return Changes{}, false
	}
	if at, err := time.Parse(dayLayout, day); err == nil {
		out.Settled = at.AddDate(0, 0, -1).Format(dayLayout) == out.Before
	}
	was := previous.Dimensions[dimension].Tiles
	for key, hash := range dim.Tiles {
		at, ok := finest(key)
		if !ok {
			continue
		}
		out.Of++
		if was[key] != hash {
			out.Tiles = append(out.Tiles, at)
		}
	}
	for key := range was {
		if _, still := dim.Tiles[key]; still {
			continue
		}
		if at, ok := finest(key); ok {
			out.Of++
			out.Tiles = append(out.Tiles, at)
		}
	}
	// In one order whatever order the maps gave them in, so the same days
	// give the same bytes and the same tiles are left out at the limit.
	slices.SortFunc(out.Tiles, func(a, b [2]int) int {
		return cmp.Or(cmp.Compare(a[1], b[1]), cmp.Compare(a[0], b[0]))
	})
	if len(out.Tiles) > maxChanges {
		out.More = len(out.Tiles) - maxChanges
		out.Tiles = out.Tiles[:maxChanges]
	}
	return out, true
}

// Capture keeps the map as it stands as the day at falls on, in UTC. A day
// already held is left as it is: the first capture of a day is the day.
//
// The caller must not be rendering. A day is every tile hashed, the ones
// not yet held copied in and flushed, and then the manifest; a capture that
// stops anywhere before the manifest has added no day, and the tiles it
// did copy are found already held by the next one.
//
// The markers are kept with the day where there are any, and a day is
// never lost over them: with none to keep, or none that could be written,
// it is kept without.
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
		if hash, added, ok := s.keepMarkers(day, id); ok {
			dim.Markers = hash
			result.Markers++
			result.NewBytes += added
		}
		m.Dimensions[id] = dim
	}
	if err := s.syncBlobs(m); err != nil {
		return result, err
	}
	if err := writeManifest(filepath.Join(s.dir, daysDir), m); err != nil {
		return result, err
	}
	s.mu.Lock()
	s.days[day] = summary(&m)
	s.mu.Unlock()
	return result, nil
}

// syncBlobs flushes the name of every blob m names, and not only of the ones
// this capture copied in: a blob found already held may be what a capture
// that stopped part-way left, named and never flushed.
func (s *Store) syncBlobs(m manifest) error {
	blobs := filepath.Join(s.dir, blobsDir)
	shards := map[string]bool{}
	for _, dim := range m.Dimensions {
		for _, hash := range dim.Tiles {
			shards[hash[:2]] = true
		}
		if dim.Markers != "" {
			shards[dim.Markers[:2]] = true
		}
	}
	for shard := range shards {
		if err := syncDir(filepath.Join(blobs, shard)); err != nil {
			return err
		}
	}
	return syncDir(blobs)
}

// keepMarkers holds a dimension's markers as they stand, if there are any.
func (s *Store) keepMarkers(day, dimension string) (hash string, added int64, ok bool) {
	if s.Markers == nil {
		return "", 0, false
	}
	body, ok := s.Markers(dimension)
	if !ok {
		return "", 0, false
	}
	hash, added, err := s.hold(body)
	if err != nil {
		s.logger.Error("a day of map history is kept without its markers", "day", day, "dimension", dimension, "error", err)
		return "", 0, false
	}
	return hash, added, true
}

// AddMarkers gives a day already held the markers it was kept without:
// each dimension in bodies that the day holds and has no markers for. The
// blobs are written and flushed first and the manifest then replaced as a
// new one is written, so a stop anywhere leaves the day as it was or as it
// is to be. Nothing else of the day changes, and a day that lacks none of
// them is not written again.
func (s *Store) AddMarkers(day string, bodies map[string][]byte) (added int, err error) {
	s.capturing.Lock()
	defer s.capturing.Unlock()

	s.mu.Lock()
	held, ok := s.manifestOf(day)
	s.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("%s is not a day held", day)
	}
	defer s.publish()
	// A copy: the one held is being read by whoever is asking for tiles.
	m := *held
	m.Dimensions = maps.Clone(held.Dimensions)
	for id, dim := range held.Dimensions {
		body, given := bodies[id]
		if !given || dim.Markers != "" {
			continue
		}
		hash, _, err := s.hold(body)
		if err != nil {
			return 0, fmt.Errorf("keeping the markers of %s: %w", day, err)
		}
		dim.Markers = hash
		m.Dimensions[id] = dim
		added++
	}
	if added == 0 {
		return 0, nil
	}
	if err := s.syncBlobs(m); err != nil {
		return 0, fmt.Errorf("keeping the markers of %s: %w", day, err)
	}
	if err := writeManifest(filepath.Join(s.dir, daysDir), m); err != nil {
		return 0, fmt.Errorf("keeping the markers of %s: %w", day, err)
	}
	s.mu.Lock()
	s.days[day] = summary(&m)
	if _, cached := s.cache[day]; cached {
		s.cache[day] = &m
	}
	s.mu.Unlock()
	return added, nil
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
	return s.hold(data)
}

// hold makes sure the store holds data, and returns the name it is held
// under and how many bytes that added.
func (s *Store) hold(data []byte) (hash string, added int64, err error) {
	sum := sha256.Sum256(data)
	hash = hex.EncodeToString(sum[:])
	blob := s.blobPath(hash)
	if info, err := os.Stat(blob); err == nil && info.Size() == int64(len(data)) {
		return hash, 0, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", 0, err
	}

	// Flushed before it has its name, and the name before the manifest: a
	// manifest must never name a blob that a power cut could still take.
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
