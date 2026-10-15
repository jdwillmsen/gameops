package server

import (
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// statusJSON is what the server says about itself to whoever can reach the
// internal listener, which is every pod in the cluster while the namespace
// is not enforced. Every field is a number, a time, a boolean or a word
// from a fixed list: there is no field a name, a place or a path could be
// put in.
type statusJSON struct {
	Build      string               `json:"build"`
	Go         string               `json:"go"`
	Now        time.Time            `json:"now"`
	Problem    string               `json:"problem"`
	Quiet      bool                 `json:"quiet"`
	Features   map[string]bool      `json:"features"`
	SnapshotAt *time.Time           `json:"snapshotAt,omitempty"`
	RenderedAt map[string]time.Time `json:"renderedAt"`
	Assets     map[string]string    `json:"assets"`
	Operators  string               `json:"operators"`
}

type adminLiveJSON struct {
	LastFrameAgeSeconds map[string]float64 `json:"lastFrameAgeSeconds"`
}

type adminCountsJSON struct {
	Issued        int64 `json:"issued"`
	PasskeyLogins int64 `json:"passkeyLogins"`
	Refused       int64 `json:"refused"`
}

type adminStatusJSON struct {
	statusJSON
	Live     *adminLiveJSON  `json:"live,omitempty"`
	Sessions adminCountsJSON `json:"sessions"`
}

func (s *Server) status() statusJSON {
	snap := s.Status.Snapshot()
	now := s.now().UTC()
	out := statusJSON{
		Now: now, Problem: snap.Problem, Quiet: s.Quiet.Contains(now),
		RenderedAt: map[string]time.Time{}, Assets: s.assets(), Operators: string(operators.None),
		Features: map[string]bool{
			"login": s.Sessions != nil, "live": s.Live != nil, "markers": s.Markers != nil,
			"waypoints": s.Waypoints != nil && s.Sessions != nil, "icons": s.MobIcons != nil || s.Heads != nil || s.Art != nil,
			"structures": s.Structures != nil, "biomes": s.Biomes != nil, "trails": s.Trails != nil,
			"history": s.History != nil, "admin": s.adminOn(), "passkeys": s.passkeysOn(),
		},
	}
	out.Build, out.Go = buildInfo()
	if !snap.SnapshotAt.IsZero() {
		at := snap.SnapshotAt.UTC()
		out.SnapshotAt = &at
	}
	for _, id := range render.Dimensions {
		if at, ok := snap.RenderedAt[id]; ok {
			out.RenderedAt[id] = at.UTC()
		}
	}
	if s.adminOn() {
		out.Operators = string(s.Operators.Source())
	}
	return out
}

func (s *Server) handleInternalStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status())
}

func (s *Server) handleAdminStatus(w http.ResponseWriter, _ *http.Request) {
	out := adminStatusJSON{statusJSON: s.status(), Sessions: adminCountsJSON{
		Issued: issuedSessions.Load(), PasskeyLogins: passkeyLogins.Load(), Refused: passkeyRefused.Load(),
	}}
	if s.Live != nil {
		now := s.now()
		live := &adminLiveJSON{LastFrameAgeSeconds: map[string]float64{}}
		for _, id := range render.Dimensions {
			if f := s.Live.Store.Snapshot(id, now); !f.At.IsZero() {
				live.LastFrameAgeSeconds[id] = now.Sub(f.At).Seconds()
			}
		}
		out.Live = live
	}
	writeJSON(w, http.StatusOK, out)
}
