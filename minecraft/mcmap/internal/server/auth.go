package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

const pendingCookie = auth.PendingCookie

func (s *Server) gated(h http.HandlerFunc) http.Handler {
	if s.Sessions == nil {
		return h
	}
	return s.Sessions.Require(h)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleConfig is the one thing the page may ask before logging in: whether
// there is a login to show. Even the world's name waits for a session.
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"login": s.Sessions != nil})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"gamertag": id.Gamertag})
}

// handleStart gives the browser a code to show. The secret that later
// collects the login goes in a cookie script cannot read and that is never
// sent with a request another site started.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var held string
	if c, err := r.Cookie(pendingCookie); err == nil {
		held = c.Value
	}
	code, secret := s.Codes.Start(held)
	http.SetCookie(w, &http.Cookie{
		Name:     pendingCookie,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(s.Codes.TTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expiresIn": int(s.Codes.TTL.Seconds())})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(pendingCookie)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"state": "expired"})
		return
	}
	switch state, id := s.Codes.Poll(c.Value); state {
	case auth.Pending:
		writeJSON(w, http.StatusOK, map[string]string{"state": "pending"})
	case auth.Claimed:
		s.Sessions.Issue(w, id)
		// Sessions are not stored, so this line is the only record of
		// who was let in and when.
		s.log().Info("map login issued", "xuid", id.XUID, "gamertag", id.Gamertag)
		http.SetCookie(w, &http.Cookie{Name: pendingCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, http.StatusOK, map[string]string{"state": "ok", "gamertag": id.Gamertag})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"state": "expired"})
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	s.Sessions.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// InternalHandler is for the cluster only: metrics, and the agent's reports
// of who typed a login code or asked to be logged out. It is served on its
// own port so that a route publishing the page cannot publish these with it.
func (s *Server) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("GET /metrics", promhttp.Handler())
	if s.Codes != nil && s.InternalToken != "" {
		mux.HandleFunc("POST /internal/v1/claims", s.agentOnly(s.handleClaim))
		if s.Sessions != nil && s.Sessions.Revoked != nil {
			mux.HandleFunc("POST /internal/v1/revocations", s.agentOnly(s.handleRevoke))
		}
	}
	return mux
}

type claimRequest struct {
	Code     string `json:"code"`
	XUID     string `json:"xuid"`
	Gamertag string `json:"gamertag"`
}

func (s *Server) agentOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.InternalToken)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func decodeStrict(w http.ResponseWriter, r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(into)
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req claimRequest
	if err := decodeStrict(w, r, &req); err != nil || req.Code == "" || req.XUID == "" {
		http.Error(w, "a claim needs code and xuid", http.StatusBadRequest)
		return
	}
	switch err := s.Codes.Claim(req.Code, auth.Identity{XUID: req.XUID, Gamertag: req.Gamertag}); {
	case errors.Is(err, auth.ErrUnknownCode):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRevoke ends every session a player holds. The agent calls it for
// the player who typed !map logout, so a player can only ever end their own.
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		XUID string `json:"xuid"`
	}
	if err := decodeStrict(w, r, &req); err != nil || !auth.IsXUID(req.XUID) {
		http.Error(w, "a revocation needs the player's xuid", http.StatusBadRequest)
		return
	}
	if err := s.Sessions.Revoked.Revoke(req.XUID, s.Sessions.Now()); err != nil {
		s.log().Error("map logins not revoked", "xuid", req.XUID, "error", err.Error())
		http.Error(w, "could not record the revocation", http.StatusInternalServerError)
		return
	}
	s.log().Info("map logins revoked", "xuid", req.XUID)
	w.WriteHeader(http.StatusNoContent)
}
