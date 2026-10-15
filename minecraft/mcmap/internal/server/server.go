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
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/schedule"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/structures"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/worker"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
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
	// NetherTop is the height the nether's own map is cut at, and
	// NetherLevels the extra cuts rendered beside it. With none, the page
	// is told of no levels at all.
	NetherTop    int
	NetherLevels []render.NetherLevel

	// Sessions and Codes are the login. With Sessions nil there is none and
	// the map is open to whoever can reach it, which is only acceptable
	// while nothing publishes it.
	Sessions *auth.Sessions
	Codes    *auth.Codes
	// InternalToken is what the agent presents to report who typed a code.
	InternalToken string
	// Service judges the secret an automated check exchanges for a session
	// of its own. Nil, there is no such exchange.
	Service *auth.Exchange
	// Admin is whether the game's operators are the map's admins, and
	// Operators the list that says who they are. Either off, there are none.
	Admin     bool
	Operators OperatorList
	// Passkeys and Ceremonies are the passkey login. Nil, there is none.
	Passkeys   *auth.Passkeys
	Ceremonies *auth.Ceremonies
	// XboxClient, XboxFlow and Known are the sign-in with an Xbox account,
	// which also needs Sessions.XboxOn. Any nil, there is none. Allowlist,
	// if set, also lets in the players the game server's allowlist names.
	XboxClient XboxProvider
	XboxFlow   *auth.XboxFlow
	Known      KnownPlayers
	Allowlist  Allowlist
	// Quiet is the map's quiet windows, for the status to say whether it is
	// in one.
	Quiet schedule.Quiet
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
	// Art is the pictures markers and structures are drawn with and the
	// display names of what the map shows. Nil leaves both out, and the
	// page keeps its rings, letters and ids.
	Art MarkerArt
	// Structures is the structures the world recorded and the ones its
	// seed predicts. Nil leaves the route out.
	Structures StructureSource
	// Withheld is the kinds of structure the owner keeps off the map
	// altogether. One is in no list, count, catalog, search or answer
	// about a structure, whatever a request says: it is this server's
	// rule, where a kind that is only off until asked for is the page's
	// courtesy.
	Withheld []structures.Kind
	// Biomes is the biomes the world stored for its chunks. Nil leaves the
	// overlay and its routes out; search then finds no biomes.
	Biomes BiomeSource
	// Trails is where players have recently been. Nil leaves the route out.
	Trails TrailSource
	// Players is the player records of the last snapshot. Nil leaves the
	// route out.
	Players PlayerSource
	// History is the map as it was on each day kept. Nil leaves its routes
	// out.
	History HistorySource

	// limitedLog spaces out the log lines of exchanges refused unjudged.
	limitedLog sparse
	// refusedLog budgets the log lines of passkey logins refused unjudged.
	refusedLog logBudget
	// xboxRefusedLog budgets the log lines of Xbox sign-ins turned away.
	xboxRefusedLog logBudget
	// adminMetrics, set only by tests, is where the admin series are declared.
	adminMetrics *adminMetricSet

	// assetsOnce and assetPrints are the fingerprints of the page's files.
	assetsOnce  sync.Once
	assetPrints map[string]string

	mu    sync.Mutex
	infos map[string]cachedInfo
	// search holds what a search would otherwise decode again every time.
	search searchCache
	// listed is the answers about a survey's structures as they were last
	// written out, which is the same bytes until the next survey.
	listed structureCache
	// surfaced is answers written out again with the surface under each
	// marker.
	surfaced surfaceCache
	// players is the UniqueID the world knows each session's player by,
	// by XUID, and playersWorld the world those ids are of. clock, for
	// tests, is what the memory's age is told by.
	players      map[string]knownPlayer
	playersWorld worldMark
	clock        func() time.Time
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

// gate is who a route is open to. Every route has one, said where the route
// is registered, and route is the only place a handler is given to a mux:
// service_test.go reads every file of this package and fails on a route
// registered any other way, or behind a gate it was not told of.
type gate int

const (
	// gatePublic needs nothing: the page, the login, liveness.
	gatePublic gate = iota + 1
	// gatePlayer is a player's session and nobody else's. It is the gate
	// for anything that is one player's own and for anything that asks
	// for, approves or carries out a change, now or later: a service
	// session is refused here, so it can be given no part in either.
	gatePlayer
	// gateViewer is what every logged-in player is shown alike, which a
	// service session may read as well. Only ever a GET.
	gateViewer
	// gateAgent is the agent, by the internal token.
	gateAgent
	// gateExchange is the checks' exchange, which judges the secret it is
	// presented with itself.
	gateExchange
	// gateAdmin is a player's session whose XUID the game's operator list
	// holds, read when the request arrives. A service session has no XUID
	// and is refused before the list is read.
	gateAdmin
)

func (s *Server) route(mux *http.ServeMux, behind gate, pattern string, h http.HandlerFunc) {
	var handler http.Handler = h
	switch behind {
	case gatePublic, gateExchange:
	case gatePlayer:
		if s.Sessions != nil {
			handler = s.Sessions.Require(h)
		}
	case gateViewer:
		if s.Sessions != nil {
			handler = s.Sessions.RequireViewer(h)
		}
	case gateAgent:
		handler = s.agentOnly(h)
	case gateAdmin:
		// Fails closed: with no login or admin off, nobody is let through.
		if s.adminOn() {
			handler = s.Sessions.Require(s.requireAdmin(pattern, h))
			s.metricSet().declareRoute(pattern)
		} else {
			handler = http.HandlerFunc(refuseAdmin)
		}
	default:
		panic("route " + pattern + " was given no gate")
	}
	mux.Handle(pattern, handler)
}

var pageWords = strings.NewReplacer("{{owner}}", web.Owner, "{{copyright}}", web.Copyright, "{{contact}}", web.Contact)

// handlePage serves a page about the map. These hold nothing of the world
// and are open to anyone, since a visitor has to be able to read what the
// map does with them before they log in.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	body, err := fs.ReadFile(s.Static, web.Pages[r.URL.Path])
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=300")
	_, _ = pageWords.WriteString(w, string(body))
}

func healthy(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// filesOnly hides directories from the static file server, which would
// otherwise answer a directory path with a listing of it, and the page files
// that handlePage serves, which hold placeholders it fills in.
type filesOnly struct{ fs.FS }

func (f filesOnly) Open(name string) (fs.File, error) {
	for _, page := range web.Pages {
		if name == page {
			return nil, fs.ErrNotExist
		}
	}
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
//
// Every route is registered through route, which is given the gate it is
// behind: see gate. A route that is one player's own, or that will ever
// change anything, is gatePlayer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.adminOn() {
		s.metricSet().declare()
	}
	s.route(mux, gatePublic, "GET /healthz", healthy)
	s.route(mux, gatePublic, "GET /api/config", s.handleConfig)
	s.route(mux, gateViewer, "GET /api/map", s.handleMap)
	s.route(mux, gateViewer, "GET /api/diagnostics", s.handleDiagnostics)
	if s.adminOn() {
		s.route(mux, gateAdmin, "GET /api/admin/status", s.handleAdminStatus)
	}
	s.route(mux, gateViewer, "GET /tiles/{dimension}/{zoom}/{x}/{y}", s.handleTile)
	if s.Live != nil {
		s.route(mux, gateViewer, "GET /api/live", s.handleLive)
	}
	if s.Markers != nil {
		s.route(mux, gateViewer, "GET /api/markers", s.handleMarkers)
	}
	if s.Waypoints != nil && s.Sessions != nil {
		s.route(mux, gatePlayer, "GET /api/waypoints", s.handleWaypoints)
	}
	if s.MobIcons != nil || s.Heads != nil || s.Art != nil {
		s.route(mux, gateViewer, "GET /api/icons", s.handleIcons)
	}
	if s.Art != nil {
		s.route(mux, gateViewer, "GET /api/icons/picture/{group}/{name}", s.handlePicture)
		s.route(mux, gateViewer, "GET /api/names", s.handleNames)
	}
	if s.MobIcons != nil {
		s.route(mux, gateViewer, "GET /api/icons/mob/{type}", s.handleMobIcon)
	}
	if s.Heads != nil {
		s.route(mux, gateViewer, "GET /api/icons/head", s.handleHead)
	}
	if s.Structures != nil {
		s.route(mux, gateViewer, "GET /api/structures", s.handleStructures)
		s.route(mux, gateViewer, "GET /api/structures/detail", s.handleStructureDetail)
	}
	if s.Biomes != nil {
		s.route(mux, gateViewer, "GET /api/biomes", s.handleBiomes)
		s.route(mux, gateViewer, "GET /api/biomes/at", s.handleBiomeAt)
		s.route(mux, gateViewer, "GET /api/biomes/nearest", s.handleBiomeNearest)
		s.route(mux, gateViewer, "GET /api/biomes/region", s.handleBiomeRegion)
		s.route(mux, gateViewer, "GET /api/biomes/tiles/{dimension}/{zoom}/{x}/{y}", s.handleBiomeTile)
		s.route(mux, gateViewer, "GET /api/topography", s.handleTopography)
		s.route(mux, gateViewer, "GET /api/topography/tiles/{dimension}/{zoom}/{x}/{y}", s.handleTopographyTile)
	}
	if s.Trails != nil {
		s.route(mux, gateViewer, "GET /api/trails", s.handleTrails)
	}
	if s.Players != nil {
		s.route(mux, gateViewer, "GET /api/players/{id}", s.handlePlayer)
	}
	if s.History != nil {
		s.route(mux, gateViewer, "GET /api/history", s.handleHistory)
		s.route(mux, gateViewer, "GET /api/history/tiles/{day}/{dimension}/{zoom}/{x}/{y}", s.handleHistoryTile)
		s.route(mux, gateViewer, "GET /api/history/markers/{day}", s.handleHistoryMarkers)
		s.route(mux, gateViewer, "GET /api/history/changes/{day}", s.handleHistoryChanges)
	}
	s.route(mux, gateViewer, "GET /api/search", s.handleSearch)
	if s.Sessions != nil {
		s.route(mux, gateViewer, "GET /api/me", s.handleMe)
		s.route(mux, gatePublic, "POST /auth/start", s.handleStart)
		s.route(mux, gatePublic, "GET /auth/status", s.handleStatus)
		s.route(mux, gatePublic, "POST /auth/logout", s.handleLogout)
	}
	if s.xboxOn() {
		declareXboxMetrics()
		s.route(mux, gatePublic, "GET /auth/xbox/start", s.handleXboxStart)
		s.route(mux, gatePublic, "GET /auth/xbox/callback", s.handleXboxCallback)
	}
	if s.passkeysOn() {
		s.route(mux, gatePublic, "POST /auth/passkey/options", s.handlePasskeyOptions)
		s.route(mux, gatePublic, "POST /auth/passkey/finish", s.handlePasskeyFinish)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/options", s.handleRegisterOptions)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/finish", s.handleRegisterFinish)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/confirm", s.handleRegisterConfirm)
		s.route(mux, gateAdmin, "POST /auth/passkey/register/cancel", s.handleRegisterCancel)
		s.route(mux, gatePlayer, "GET /api/passkeys", s.handleListPasskeys)
		s.route(mux, gatePlayer, "DELETE /api/passkeys/{id}", s.handleRemovePasskey)
		// Only the account panel asks for it, and that panel is passkeys;
		// with them off, !map logout is the way to end every session.
		if s.Sessions.Revoked != nil {
			s.route(mux, gatePlayer, "POST /api/logout-all", s.handleLogoutAll)
		}
	}
	// Written out one by one, as every route is, for the list in service_test.go.
	s.route(mux, gatePublic, "GET /about", s.handlePage)
	s.route(mux, gatePublic, "GET /guide", s.handlePage)
	s.route(mux, gatePublic, "GET /privacy", s.handlePage)
	s.route(mux, gatePublic, "GET /terms", s.handlePage)
	s.route(mux, gatePublic, "GET /credits", s.handlePage)
	static := http.FileServerFS(filesOnly{s.Static})
	s.route(mux, gatePublic, "GET /", func(w http.ResponseWriter, r *http.Request) {
		// The page changes only with a release; embedded files carry no
		// modification time to revalidate against.
		w.Header().Set("Cache-Control", "max-age=300")
		static.ServeHTTP(w, r)
	})
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
	// Levels are the cuts of the dimension that can be drawn, the default
	// first, and only where there is more than the default.
	Levels []levelJSON `json:"levels,omitempty"`
	*render.Info
}

// defaultLevelID is what the page calls the cut a tile request names with no
// level at all. It is never a valid level in a request: the default is the
// absence of one.
const defaultLevelID = "default"

type levelJSON struct {
	ID    string `json:"id"`
	Y     int    `json:"y"`
	Label string `json:"label"`
	// RenderedAt is when this cut was last drawn. The levels are drawn
	// after the dimensions, so it is not the dimension's own time.
	RenderedAt *time.Time `json:"renderedAt,omitempty"`
}

// levels lists the nether's cuts that have been drawn at least once. A level
// whose first render has not finished is left out, so the page never offers
// a map of nothing.
func (s *Server) levels(renderedAt map[string]time.Time) []levelJSON {
	at := func(name string) *time.Time {
		if t, ok := renderedAt[name]; ok {
			return &t
		}
		return nil
	}
	var out []levelJSON
	for _, l := range s.NetherLevels {
		if _, ok := s.info(l.Dir()); ok {
			out = append(out, levelJSON{ID: l.ID(), Y: l.TopY, Label: l.Label, RenderedAt: at(l.Dir())})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append([]levelJSON{{ID: defaultLevelID, Y: s.NetherTop, Label: render.DefaultNetherLabel, RenderedAt: at("nether")}}, out...)
}

// level finds the configured cut a request names. Only what was configured
// can match, and the directory comes from the match and never from the
// request, so no value of it can reach another path.
func (s *Server) level(id string) (render.NetherLevel, bool) {
	for _, l := range s.NetherLevels {
		if l.ID() == id {
			return l, true
		}
	}
	return render.NetherLevel{}, false
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
			if id == "nether" {
				d.Levels = s.levels(status.RenderedAt)
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

// tileAddress reads the zoom, x and y of a tile route, and the extension
// the y carries.
func tileAddress(r *http.Request) (tile render.Tile, ext string, ok bool) {
	zoom, errZ := strconv.Atoi(r.PathValue("zoom"))
	x, errX := strconv.Atoi(r.PathValue("x"))
	name, ext, _ := strings.Cut(r.PathValue("y"), ".")
	y, errY := strconv.Atoi(name)
	if errZ != nil || errX != nil || errY != nil ||
		zoom < minTileZoom || zoom > maxTileZoom ||
		x < -maxTileCoordinate || x > maxTileCoordinate || y < -maxTileCoordinate || y > maxTileCoordinate {
		return render.Tile{}, "", false
	}
	return render.Tile{Zoom: zoom, X: x, Y: y}, ext, true
}

func (s *Server) handleTile(w http.ResponseWriter, r *http.Request) {
	dimension := r.PathValue("dimension")
	if !slices.Contains(render.Dimensions, dimension) {
		http.NotFound(w, r)
		return
	}
	tile, ext, ok := tileAddress(r)
	if !ok {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}

	// The directory is of the dimension, or of the configured level a
	// request names, and a request that names a level of any other
	// dimension, or one that is not configured, asks for nothing there is.
	name := dimension
	if id := r.URL.Query().Get("level"); id != "" {
		level, ok := s.level(id)
		if !ok || dimension != "nether" {
			http.NotFound(w, r)
			return
		}
		name = level.Dir()
	}
	dir := filepath.Join(s.MapsDir, name)
	info, ok := s.info(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ext != info.Format {
		http.Error(w, "bad tile address", http.StatusBadRequest)
		return
	}
	f, err := os.Open(s.Renderer.TilePath(dir, info.Format, tile.Zoom, tile.X, tile.Y))
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

// sparse lets something happen at most once a minute.
type sparse struct {
	mu   sync.Mutex
	last time.Time
}

func (s *sparse) Allow(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.last.IsZero() && now.Sub(s.last) < time.Minute {
		return false
	}
	s.last = now
	return true
}
