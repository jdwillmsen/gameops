package server

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// maxAssets bounds how many page files the report lists, whatever the
// embedded tree holds.
const maxAssets = 64

// diagnosticsJSON is what the page's own troubleshooting report asks the
// server for. It says what this server is and offers, and how old its
// snapshots are; it holds nothing about any player and nothing secret.
type diagnosticsJSON struct {
	Build          string               `json:"build,omitempty"`
	Go             string               `json:"go,omitempty"`
	Now            time.Time            `json:"now"`
	Role           string               `json:"role"`
	Features       map[string]bool      `json:"features"`
	RefreshSeconds int                  `json:"refreshSeconds"`
	SnapshotAt     *time.Time           `json:"snapshotAt,omitempty"`
	RenderedAt     map[string]time.Time `json:"renderedAt"`
	Failing        bool                 `json:"failing"`
	// Assets is a short fingerprint of each file of the page as this
	// server holds it, for the page to tell which of the scripts it has
	// loaded are older than the page.
	Assets map[string]string `json:"assets"`
}

// fingerprint is FNV-1a over the bytes, as eight hex digits. The page
// computes the same over what it loaded.
func fingerprint(b []byte) string {
	h := fnv.New32a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%08x", h.Sum32())
}

// assets fingerprints the page's own files, which sit at the top of the
// tree. They only change with a release, so it is done once.
func (s *Server) assets() map[string]string {
	s.assetsOnce.Do(func() {
		s.assetPrints = map[string]string{}
		entries, err := fs.ReadDir(s.Static, ".")
		if err != nil {
			return
		}
		for _, e := range entries {
			ext := path.Ext(e.Name())
			if e.IsDir() || (ext != ".js" && ext != ".css" && ext != ".html") || len(s.assetPrints) >= maxAssets {
				continue
			}
			if b, err := fs.ReadFile(s.Static, e.Name()); err == nil {
				s.assetPrints[e.Name()] = fingerprint(b)
			}
		}
	})
	return s.assetPrints
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	status := s.Status.Snapshot()
	now := time.Now()
	if s.clock != nil {
		now = s.clock()
	}
	out := diagnosticsJSON{
		Now:            now.UTC(),
		Role:           "open",
		RefreshSeconds: int(s.Refresh.Seconds()),
		Failing:        status.Problem != "",
		RenderedAt:     map[string]time.Time{},
		Assets:         s.assets(),
		Features: map[string]bool{
			"login":      s.Sessions != nil,
			"live":       s.Live != nil,
			"markers":    s.Markers != nil,
			"waypoints":  s.Waypoints != nil && s.Sessions != nil,
			"icons":      s.MobIcons != nil || s.Heads != nil || s.Art != nil,
			"structures": s.Structures != nil,
			"biomes":     s.Biomes != nil,
			"trails":     s.Trails != nil,
		},
	}
	if s.Sessions != nil {
		out.Role = "player"
		if viewer, _ := auth.ViewerFromContext(r.Context()); viewer.Service {
			out.Role = "service"
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		out.Go = info.GoVersion
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				out.Build = strings.TrimSpace(setting.Value)
				if len(out.Build) > 12 {
					out.Build = out.Build[:12]
				}
			}
		}
	}
	if !status.SnapshotAt.IsZero() {
		out.SnapshotAt = &status.SnapshotAt
	}
	for _, id := range render.Dimensions {
		if at, ok := status.RenderedAt[id]; ok {
			out.RenderedAt[id] = at
		}
	}
	writeJSON(w, http.StatusOK, out)
}
