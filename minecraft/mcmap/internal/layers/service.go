package layers

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

var (
	metricRenderSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mcmap_layers_render_duration_seconds",
		Help:    "How long a cave or level tile took to draw, from the sub-chunks to the picture, by dimension. Tiles served from the cache are not in it.",
		Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"dimension"})
	metricPhaseSeconds = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mcmap_layers_render_phase_seconds_total",
		Help: "Where the time of drawn tiles went: reading and decoding the sub-chunks, painting, and encoding the picture.",
	}, []string{"phase"})
	metricCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_layers_cache_hits_total",
		Help: "Tiles of the cave and level views served without drawing them.",
	})
	metricCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_layers_cache_misses_total",
		Help: "Tiles of the cave and level views that had to be drawn.",
	})
	metricCacheBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_cache_bytes",
		Help: "Bytes of pictures the tile cache holds.",
	})
	metricCacheEntries = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_cache_entries",
		Help: "Tiles the tile cache holds, those with nothing in them included.",
	})
	metricRendersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_renders_active",
		Help: "Tiles being drawn now; the most there can be is the configured limit.",
	})
	metricRendersWaiting = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mcmap_layers_renders_waiting",
		Help: "Tiles asked for and waiting for one of the places to draw in.",
	})
	metricRenderFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_layers_render_failures_total",
		Help: "Tiles that could not be drawn.",
	})
	metricUnreadable = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mcmap_layers_unreadable_chunks_total",
		Help: "Chunks drawn with a sub-chunk that would not read, which is drawn as a gap.",
	})
)

// ErrNotReady is a tile asked for before the world has been opened.
var ErrNotReady = errors.New("the world has not been opened yet")

// Key is everything a tile's picture depends on.
type Key struct {
	Version string
	Spec    Spec
	Zoom    int
	X, Y    int
}

// cached is one cached tile. A tile with nothing in it is kept too, with no
// data: a sparse world's grid is mostly such tiles, and drawing one costs
// as much as reading the chunks of a full one.
type cached struct {
	key  Key
	data []byte
	ok   bool
	size int
}

// entryOverhead is what an entry costs besides its picture, so that a cache
// of thousands of empty tiles is not counted as free.
const entryOverhead = 256

// Cache holds drawn tiles up to a number of bytes and lets go of the least
// recently asked for first.
type Cache struct {
	mu    sync.Mutex
	limit int
	used  int
	order *list.List
	items map[Key]*list.Element
}

// NewCache makes a cache that holds up to limit bytes.
func NewCache(limit int) *Cache {
	return &Cache{limit: limit, order: list.New(), items: map[Key]*list.Element{}}
}

func (c *Cache) publish() {
	metricCacheBytes.Set(float64(c.used))
	metricCacheEntries.Set(float64(len(c.items)))
}

// Get is the picture held for key, and whether there is anything held.
func (c *Cache) Get(key Key) (data []byte, ok, held bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, held := c.items[key]
	if !held {
		return nil, false, false
	}
	c.order.MoveToFront(el)
	e := el.Value.(*cached)
	return e.data, e.ok, true
}

// Put keeps a picture, or the fact that there is none, and lets go of the
// least recently used until what is kept is within the limit. One larger
// than the limit is not kept.
func (c *Cache) Put(key Key, data []byte, ok bool) {
	size := len(data) + entryOverhead
	c.mu.Lock()
	defer c.mu.Unlock()
	if size > c.limit {
		return
	}
	if el, held := c.items[key]; held {
		c.used -= el.Value.(*cached).size
		c.order.Remove(el)
		delete(c.items, key)
	}
	c.items[key] = c.order.PushFront(&cached{key: key, data: data, ok: ok, size: size})
	c.used += size
	for c.used > c.limit {
		last := c.order.Back()
		e := last.Value.(*cached)
		c.used -= e.size
		delete(c.items, e.key)
		c.order.Remove(last)
	}
	c.publish()
}

// Drop lets go of every tile that is not of version, which is every tile
// once a newer reading of the world is in use.
func (c *Cache) Drop(version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		if e := el.Value.(*cached); e.key.Version != version {
			c.used -= e.size
			delete(c.items, e.key)
			c.order.Remove(el)
		}
		el = next
	}
	c.publish()
}

// Len and Bytes say what is held.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *Cache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// Config is what a Service is made from.
type Config struct {
	// WorkDir is where views of the world are made, on the same filesystem
	// as the mirror so that they are links and not copies.
	WorkDir string
	// CacheBytes is how much the tile cache may hold, and MaxRenders how
	// many tiles may be drawn at once: the cost of a tile falls on a CPU
	// the game server shares.
	CacheBytes int
	MaxRenders int
	// Biomes is where the biomes the tint is read from are, as of each tile.
	Biomes func() Biomes
	Logger *slog.Logger
}

// view is the world as of one snapshot, opened for reading.
type view struct {
	renderer *Renderer
	version  string
	// dir is the directory of the two the view was opened in.
	dir   int
	close func()
}

// Service draws tiles of cuts through the world on request, keeps the
// pictures in a cache bounded in bytes, draws a tile asked for by several
// at once only once, and never draws more than a few at a time.
type Service struct {
	cfg      Config
	cache    *Cache
	registry *Registry
	slots    chan struct{}

	// mu guards cur, which a render holds for reading for as long as it
	// uses the database, and a refresh holds for writing only to swap.
	mu  sync.RWMutex
	cur *view
	// seq makes each refresh a version of its own.
	seq int

	flightMu sync.Mutex
	flights  map[Key]*flight

	// onDraw, for tests, is called once a tile has a place to be drawn in.
	onDraw func()
	// onRender, for tests, is called with the world held for reading, just
	// before a tile is drawn from it.
	onRender func()
}

type flight struct {
	done chan struct{}
	data []byte
	ok   bool
	err  error
}

// NewService makes a service with nothing open: Refresh opens the world.
func NewService(cfg Config) *Service {
	if cfg.MaxRenders < 1 {
		cfg.MaxRenders = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		cfg:      cfg,
		cache:    NewCache(cfg.CacheBytes),
		registry: NewRegistry(),
		slots:    make(chan struct{}, cfg.MaxRenders),
		flights:  map[Key]*flight{},
	}
}

// Refresh opens the database at dbDir as the world of version, which is what
// tiles are drawn from and cached under from now on. The view before it is
// closed once the tiles being drawn from it are done, and what the cache
// held of it is let go of. Two directories are used in turn, because
// opening a view clears what is left in its directory.
func (s *Service) Refresh(dbDir, version string) error {
	// The directory the view in use is not in, so that a refresh that
	// failed leaves the next one where it was.
	s.mu.RLock()
	dirIndex := 0
	if s.cur != nil {
		dirIndex = 1 - s.cur.dir
	}
	s.mu.RUnlock()
	dir := filepath.Join(s.cfg.WorkDir, fmt.Sprintf("view-%d", dirIndex))
	db, done, err := chunks.OpenView(dbDir, dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	// Two refreshes in one second are two worlds, as far as a tile kept of
	// the first is concerned.
	s.seq++
	unique := fmt.Sprintf("%s.%d", version, s.seq)
	next := &view{
		renderer: &Renderer{Reader: Reader{DB: db, Registry: s.registry}, Biomes: s.cfg.Biomes},
		version:  unique,
		dir:      dirIndex,
		close:    done,
	}
	old := s.cur
	s.cur = next
	s.mu.Unlock()
	s.cache.Drop(unique)
	if old != nil {
		old.close()
	}
	return nil
}

// Version names the world tiles are being drawn from, or is empty before
// the first Refresh.
func (s *Service) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur == nil {
		return ""
	}
	return s.cur.version
}

// Close lets go of the world.
func (s *Service) Close() {
	s.mu.Lock()
	old := s.cur
	s.cur = nil
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
}

// Cache is the tile cache, for what is held to be looked at.
func (s *Service) Cache() *Cache { return s.cache }

// Result is a tile as it was got.
type Result struct {
	Data []byte
	// Drawn is false for a tile with nothing in the band.
	Drawn bool
	// Cached is true when no drawing was done for it.
	Cached bool
	Timings
}

// Tile is the picture of a tile of the world as of the current version,
// from the cache where it is there. A tile being drawn for another request
// is waited for and not drawn again.
func (s *Service) Tile(ctx context.Context, spec Spec, zoom, tx, ty int) (Result, error) {
	if err := spec.Valid(); err != nil {
		return Result{}, err
	}
	if zoom < MinZoom || zoom > MaxZoom {
		return Result{}, errors.New("no tiles at that zoom")
	}
	version := s.Version()
	if version == "" {
		return Result{}, ErrNotReady
	}
	key := Key{Version: version, Spec: spec, Zoom: zoom, X: tx, Y: ty}
	if data, ok, held := s.cache.Get(key); held {
		metricCacheHits.Inc()
		return Result{Data: data, Drawn: ok, Cached: true}, nil
	}

	for {
		s.flightMu.Lock()
		f, running := s.flights[key]
		if !running {
			f = &flight{done: make(chan struct{})}
			s.flights[key] = f
			s.flightMu.Unlock()
			// However the drawing ends, those waiting for it are let go.
			defer func() {
				s.flightMu.Lock()
				delete(s.flights, key)
				s.flightMu.Unlock()
				close(f.done)
			}()
			res, err := s.drawSafely(ctx, key)
			f.data, f.ok, f.err = res.Data, res.Drawn, err
			return res, err
		}
		s.flightMu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		// The one that was drawing may have been given up on by the viewer
		// who asked it; that is not this request's failure, so it asks again.
		if f.err != nil && (errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded)) && ctx.Err() == nil {
			continue
		}
		metricCacheHits.Inc()
		return Result{Data: f.data, Drawn: f.ok, Cached: true}, f.err
	}
}

// draw waits for a place to draw in and draws, and keeps the picture.
func (s *Service) draw(ctx context.Context, key Key) (Result, error) {
	metricRendersWaiting.Inc()
	select {
	case s.slots <- struct{}{}:
		metricRendersWaiting.Dec()
	case <-ctx.Done():
		metricRendersWaiting.Dec()
		return Result{}, ctx.Err()
	}
	metricRendersActive.Inc()
	defer func() {
		<-s.slots
		metricRendersActive.Dec()
	}()
	metricCacheMisses.Inc()
	if s.onDraw != nil {
		s.onDraw()
	}

	return s.render(ctx, key)
}

// drawSafely is draw with a panic in it made an error: a world that
// the reader did not expect is one tile's failure and not the service's.
func (s *Service) drawSafely(ctx context.Context, key Key) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			metricRenderFailures.Inc()
			s.cfg.Logger.Error("tile render panicked", "panic", p, "stack", string(debug.Stack()))
			res, err = Result{}, fmt.Errorf("render panicked: %v", p)
		}
	}()
	return s.draw(ctx, key)
}

// render draws from the world as it is now, and keeps the picture whether or
// not the viewer who asked is still there: it is drawn, and the next one
// need not. The world is held for reading all the while, so a refresh waits
// for the tile and drops what the cache kept of the old world after it, and
// nothing of an old version is kept past the drop.
func (s *Service) render(ctx context.Context, key Key) (Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur := s.cur
	if cur == nil {
		return Result{}, ErrNotReady
	}
	// A tile asked for under a version that a refresh has since replaced
	// is drawn from the newer world and is kept as the newer one's.
	key.Version = cur.version
	if s.onRender != nil {
		s.onRender()
	}
	began := time.Now()
	data, tm, ok, err := cur.renderer.Tile(key.Spec, key.Zoom, key.X, key.Y)
	if err != nil {
		metricRenderFailures.Inc()
		return Result{}, err
	}
	metricRenderSeconds.WithLabelValues(key.Spec.Dim.Name()).Observe(time.Since(began).Seconds())
	metricPhaseSeconds.WithLabelValues("read").Add(tm.Read.Seconds())
	metricPhaseSeconds.WithLabelValues("paint").Add(tm.Paint.Seconds())
	metricPhaseSeconds.WithLabelValues("encode").Add(tm.Encode.Seconds())
	metricUnreadable.Add(float64(tm.Unreadable))
	s.cache.Put(key, data, ok)
	return Result{Data: data, Drawn: ok, Timings: tm}, nil
}
