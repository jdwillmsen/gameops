// Package server is the map's HTTP surface: the page, the tiles, and the
// JSON that tells the page what there is to draw.
package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
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

	// Sessions and Codes are the login. With Sessions nil there is none and
	// the map is open to whoever can reach it, which is only acceptable
	// while nothing publishes it.
	Sessions *auth.Sessions
	Codes    *auth.Codes
	// InternalToken is what the agent presents to report who typed a code.
	InternalToken string
	// Chunks is the world's chunk census, which the agent reads to warn
	// players while chunks are missing. Nil leaves those routes out.
	Chunks ChunkCensus
	// Generations is the retained copies of the world. Nil leaves them out
	// of the world report.
	Generations Generations
	// Log records logins issued and revoked. Nil discards them.
	Log *slog.Logger
	// Live is where players and mobs are right now. Nil leaves the live
	// stream out altogether.
	Live *live.Layer
	// LiveKeepalive is how long a live stream may go without writing
	// anything. It has to stay well under the shortest idle timeout between
	// here and the browser, or a quiet stream is cut.
	LiveKeepalive time.Duration
	// Markers is what the world holds that is worth a mark: beds, containers
	// and named mobs. Nil leaves them out.
	Markers MarkerStore
	// Waypoints reads the logged-in player's own waypoints. Nil leaves them
	// out, as does having no login: without one there is nobody to be.
	Waypoints WaypointSource
	// MobIcons and Heads are the pictures the live layer draws in place of
	// dots. With both nil the page is not offered any.
	MobIcons MobIcons
	Heads    PlayerHeads
	// Structures is the structures the world recorded and the ones its
	// seed predicts. Nil leaves the route out.
	Structures StructureSource
	// Biomes is the biomes the world stored for its chunks. Nil leaves the
	// overlay and its routes out; search then finds no biomes.
	Biomes BiomeSource
	// Trails is where players have recently been. Nil leaves the route out.
	Trails TrailSource

	mu    sync.Mutex
	infos map[string]cachedInfo
	// search holds what a search would otherwise decode again every time.
	search searchCache
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

// Handler is everything a browser may reach. Where the world is, is behind
// the login; the page itself is not, since it has to load to show the login
// and holds nothing about the world.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.Handle("GET /api/map", s.gated(s.handleMap))
	mux.Handle("GET /tiles/{dimension}/{zoom}/{x}/{y}", s.gated(s.handleTile))
	if s.Live != nil {
		mux.Handle("GET /api/live", s.gated(s.handleLive))
	}
	if s.Markers != nil {
		mux.Handle("GET /api/markers", s.gated(s.handleMarkers))
	}
	if s.Waypoints != nil && s.Sessions != nil {
		mux.Handle("GET /api/waypoints", s.gated(s.handleWaypoints))
	}
	if s.MobIcons != nil || s.Heads != nil {
		mux.Handle("GET /api/icons", s.gated(s.handleIcons))
	}
	if s.MobIcons != nil {
		mux.Handle("GET /api/icons/mob/{type}", s.gated(s.handleMobIcon))
	}
	if s.Heads != nil {
		mux.Handle("GET /api/icons/head", s.gated(s.handleHead))
	}
	if s.Structures != nil {
		mux.Handle("GET /api/structures", s.gated(s.handleStructures))
	}
	if s.Biomes != nil {
		mux.Handle("GET /api/biomes", s.gated(s.handleBiomes))
		mux.Handle("GET /api/biomes/at", s.gated(s.handleBiomeAt))
		mux.Handle("GET /api/biomes/nearest", s.gated(s.handleBiomeNearest))
		mux.Handle("GET /api/biomes/region", s.gated(s.handleBiomeRegion))
		mux.Handle("GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}", s.gated(s.handleBiomeTile))
	}
	if s.Trails != nil {
		mux.Handle("GET /api/trails", s.gated(s.handleTrails))
	}
	mux.Handle("GET /api/search", s.gated(s.handleSearch))
	if s.Sessions != nil {
		mux.Handle("GET /api/me", s.gated(s.handleMe))
		mux.HandleFunc("POST /auth/start", s.handleStart)
		mux.HandleFunc("GET /auth/status", s.handleStatus)
		mux.HandleFunc("POST /auth/logout", s.handleLogout)
	}
	static := http.FileServerFS(filesOnly{s.Static})
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The page changes only with a release; embedded files carry no
		// modification time to revalidate against.
		w.Header().Set("Cache-Control", "max-age=300")
		static.ServeHTTP(w, r)
	}))
	// Refuses a state-changing request that another site started in the
	// visitor's browser, which would otherwise be able to log them out or
	// spend login codes in their name. Sites elsewhere under the same parent
	// domain count as other sites.
	return secured(http.NewCrossOriginProtection().Handler(mux))
}

// secured sets the headers every response carries. The page loads nothing
// from another origin and must not be framed by one.
func secured(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		// Ignored over plain HTTP, so harmless on a local port-forward.
		h.Set("Strict-Transport-Security", "max-age=31536000")
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
	World          string     `json:"world"`
	RefreshSeconds int        `json:"refreshSeconds"`
	SnapshotAt     *time.Time `json:"snapshotAt,omitempty"`
	Problem        string     `json:"problem,omitempty"`
	// Live tells the page whether there is a live stream to open. It is
	// here, behind the session, and not in the public config.
	Live       bool            `json:"live"`
	Dimensions []dimensionJSON `json:"dimensions"`
}

func (s *Server) handleMap(w http.ResponseWriter, _ *http.Request) {
	status := s.Status.Snapshot()
	out := mapJSON{World: s.World, RefreshSeconds: int(s.Refresh.Seconds()), Problem: status.Problem, Live: s.Live != nil}
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
		w.Header().Set("Cache-Control", "private, max-age=60")
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
	// Last-Modified below makes the recheck cost a 304. Private, because a
	// tile is only for the logged-in browser that asked for it.
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeContent(w, r, "", stat.ModTime(), f)
}
