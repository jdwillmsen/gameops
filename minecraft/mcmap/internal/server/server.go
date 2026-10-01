// Package server is the map's HTTP surface: the page, the tiles, and the
// JSON that tells the page what there is to draw.
package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
)

type Server struct {
	Renderer render.Renderer
	MapsDir  string
	World    string
	Status   *worker.Status
	// Refresh is how often the map is re-rendered, so the page knows when
	// looking again is worth it.
	Refresh time.Duration
	Static  fs.FS

	mu    sync.Mutex
	infos map[string]cachedInfo
}

type cachedInfo struct {
	at   time.Time
	info render.Info
}

// info describes a dimension, re-reading the renderer's description only
// when a new render has replaced it. Every tile request needs it, and a map
// view is dozens of tile requests.
func (s *Server) info(dimension string) (render.Info, bool) {
	dir := filepath.Join(s.MapsDir, dimension)
	at, ok := s.Renderer.RenderedAt(dir)
	if !ok {
		return render.Info{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, hit := s.infos[dimension]; hit && c.at.Equal(at) {
		return c.info, true
	}
	info, err := s.Renderer.Info(dir)
	if err != nil {
		return render.Info{}, false
	}
	if s.infos == nil {
		s.infos = map[string]cachedInfo{}
	}
	s.infos[dimension] = cachedInfo{at, info}
	return info, true
}

// filesOnly hides directories from the static file server, which would
// otherwise answer a directory path with a listing of it.
type filesOnly struct{ fs.FS }

func (f filesOnly) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || (info.IsDir() && name != ".") {
		file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /api/map", s.handleMap)
	mux.HandleFunc("GET /tiles/{dimension}/{zoom}/{x}/{y}", s.handleTile)
	static := http.FileServerFS(filesOnly{s.Static})
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The page changes only with a release; embedded files carry no
		// modification time to revalidate against.
		w.Header().Set("Cache-Control", "max-age=300")
		static.ServeHTTP(w, r)
	}))
	return secured(mux)
}

// secured sets the headers every response carries. The page loads nothing
// from another origin and must not be framed by one.
func secured(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type dimensionJSON struct {
	ID         string     `json:"id"`
	Rendered   bool       `json:"rendered"`
	RenderedAt *time.Time `json:"renderedAt,omitempty"`
	*render.Info
}

type mapJSON struct {
	World          string          `json:"world"`
	RefreshSeconds int             `json:"refreshSeconds"`
	SnapshotAt     *time.Time      `json:"snapshotAt,omitempty"`
	Problem        string          `json:"problem,omitempty"`
	Dimensions     []dimensionJSON `json:"dimensions"`
}

func (s *Server) handleMap(w http.ResponseWriter, _ *http.Request) {
	status := s.Status.Snapshot()
	out := mapJSON{World: s.World, RefreshSeconds: int(s.Refresh.Seconds()), Problem: status.Problem}
	if !status.SnapshotAt.IsZero() {
		out.SnapshotAt = &status.SnapshotAt
	}
	for _, id := range render.Dimensions {
		d := dimensionJSON{ID: id}
		if info, ok := s.info(id); ok {
			d.Rendered, d.Info = true, &info
			if at, ok := status.RenderedAt[id]; ok {
				d.RenderedAt = &at
			}
		}
		out.Dimensions = append(out.Dimensions, d)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

// Bounds on what a tile request may ask for. They are far outside any real
// map; they exist so a request cannot make the server format and stat
// arbitrary numbers.
const (
	maxTileCoordinate = 1 << 20
	minTileZoom       = -12
	maxTileZoom       = 4
)

func (s *Server) handleTile(w http.ResponseWriter, r *http.Request) {
	dimension := r.PathValue("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.NotFound(w, r)
		return
	}
	zoom, errZ := strconv.Atoi(r.PathValue("zoom"))
	x, errX := strconv.Atoi(r.PathValue("x"))
	name, ext, _ := strings.Cut(r.PathValue("y"), ".")
	y, errY := strconv.Atoi(name)
	if errZ != nil || errX != nil || errY != nil ||
		zoom < minTileZoom || zoom > maxTileZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}

	dir := filepath.Join(s.MapsDir, dimension)
	info, ok := s.info(dimension)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ext != info.Format {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	f, err := os.Open(s.Renderer.TilePath(dir, info.Format, zoom, x, y))
	if err != nil {
		// Most of a sparse world's grid has no tile; that is not an error.
		w.Header().Set("Cache-Control", "max-age=60")
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/"+info.Format)
	// Short, because a tile changes whenever someone builds there; the
	// Last-Modified below makes the recheck cost a 304.
	w.Header().Set("Cache-Control", "max-age=60")
	http.ServeContent(w, r, "", stat.ModTime(), f)
}
