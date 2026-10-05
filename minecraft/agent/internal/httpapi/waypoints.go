package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/waypoints"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

// WaypointLister reads one player's waypoints. Satisfied by waypoints.Store.
type WaypointLister interface {
	List(ctx context.Context, xuid string) ([]waypoints.Waypoint, error)
	Enabled() bool
}

const (
	// maxWaypointsServed caps one answer. Nothing limits how many a player
	// may save, and the map draws every one it is sent.
	maxWaypointsServed = 500
	// waypointsRequestTimeout keeps the query inside the server's
	// WriteTimeout, so a slow database produces a 503 rather than a dropped
	// connection.
	waypointsRequestTimeout = 5 * time.Second
	// maxXUIDDigits is the longest decimal a 64-bit XUID can be.
	maxXUIDDigits = 20
)

type waypointJSON struct {
	Name      string `json:"name"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Z         int    `json:"z"`
	Dimension string `json:"dimension"`
}

type waypointsResponse struct {
	Waypoints []waypointJSON `json:"waypoints"`
	// More is how many the player has beyond the ones listed.
	More int `json:"more"`
}

// MountWaypoints serves GET /v1/players/{xuid}/waypoints to the world map,
// and reports whether it did.
//
// token is the map's own internal token, which this agent already holds to
// report logins to it: the map is the only other holder, so presenting it
// here is how a request shows it comes from the map. Whoever holds it can
// already log in to the map as any player, so reading any player's
// waypoints with it gives away nothing it did not already reach.
//
// The route trusts its caller to ask only for the player it has
// authenticated. That is the map's session, never anything a browser sends:
// a waypoint is a base location, and this is the one place they leave the
// player's own chat.
//
// With no token or no store nothing is mounted, on the same terms as
// MountAnnouncements. Mounted for the process rather than for a turn as the
// live agent: it reads Postgres, which a standby reaches as well as the
// leader does.
func (s *Server) MountWaypoints(token string, store WaypointLister, log *logging.Logger) bool {
	if token == "" || store == nil || !store.Enabled() {
		return false
	}
	want := sha256.Sum256([]byte(token))
	s.mux.HandleFunc("GET /v1/players/{xuid}/waypoints", func(w http.ResponseWriter, r *http.Request) {
		// Before the path is read: an unauthenticated caller learns nothing
		// about which players exist.
		if !bearerMatches(r.Header.Get("Authorization"), want) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "a valid bearer token is required")
			return
		}
		xuid := r.PathValue("xuid")
		if !isXUID(xuid) {
			writeError(w, http.StatusBadRequest, "a player's XUID is a number")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), waypointsRequestTimeout)
		defer cancel()
		list, err := store.List(ctx, xuid)
		if err != nil {
			log.Error("waypoints_api_failed", logging.Fields{"error": err.Error()})
			writeError(w, http.StatusServiceUnavailable, "the waypoint store could not be read")
			return
		}
		resp := waypointsResponse{Waypoints: make([]waypointJSON, 0, min(len(list), maxWaypointsServed))}
		for _, wp := range list {
			if len(resp.Waypoints) == maxWaypointsServed {
				resp.More = len(list) - maxWaypointsServed
				break
			}
			resp.Waypoints = append(resp.Waypoints, waypointJSON{Name: wp.Name, X: wp.X, Y: wp.Y, Z: wp.Z, Dimension: wp.Dimension})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})
	return true
}

func isXUID(s string) bool {
	if s == "" || len(s) > maxXUIDDigits {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
