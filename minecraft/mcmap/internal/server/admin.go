package server

import (
	"context"
	"net/http"
	"strconv"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/operators"
)

// OperatorList is the game's operators, as far as they can be read.
type OperatorList interface {
	IsOperator(ctx context.Context, xuid string) (bool, operators.Source)
	Source() operators.Source
}

func (s *Server) adminOn() bool { return s.Admin && s.Sessions != nil && s.Operators != nil }

func (s *Server) passkeysOn() bool {
	return s.adminOn() && s.Passkeys != nil && s.Ceremonies != nil && s.Sessions.Passkeys != nil
}

func (s *Server) isAdmin(ctx context.Context, xuid string) bool {
	if !s.adminOn() {
		return false
	}
	ok, _ := s.Operators.IsOperator(ctx, xuid)
	return ok
}

// requireAdmin is gateAdmin past the session check. The answer is the
// operator list's at the moment of the request, so a demotion closes every
// admin route to cookies issued before it.
func (s *Server) requireAdmin(pattern string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		allowed := ok && s.isAdmin(r.Context(), id.XUID)
		s.metricSet().requests.WithLabelValues(pattern, strconv.FormatBool(allowed)).Inc()
		s.log().Info("map admin request", "xuid", id.XUID, "route", pattern, "allowed", allowed)
		if !allowed {
			refuseAdmin(w, r)
			return
		}
		next(w, r)
	}
}

func refuseAdmin(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "not open to this player", http.StatusForbidden)
}
