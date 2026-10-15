package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

const pendingCookie = auth.PendingCookie

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleConfig is the one thing the page may ask before logging in: whether
// there is a login to show. Even the world's name waits for a session.
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	out := map[string]any{"login": s.Sessions != nil}
	// Said only while on, so a map without passkeys answers as it always
	// has.
	if s.passkeysOn() {
		out["passkeys"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	viewer, _ := auth.ViewerFromContext(r.Context())
	out := map[string]any{"gamertag": viewer.Name}
	// Said outright, so that nothing has to tell a check from a player by
	// the shape of a name.
	if viewer.Service {
		out["service"] = true
	}
	if id, ok := auth.FromContext(r.Context()); ok && s.isAdmin(r.Context(), id.XUID) {
		out["admin"] = true
	}
	writeJSON(w, http.StatusOK, out)
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
		issuedSessions.Add(1)
		if s.Passkeys != nil {
			s.Passkeys.RefreshGamertag(id.XUID, id.Gamertag)
		}
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
	s.route(mux, gatePublic, "GET /healthz", healthy)
	s.route(mux, gatePublic, "GET /metrics", promhttp.Handler().ServeHTTP)
	if s.Chunks != nil && s.InternalToken != "" {
		s.route(mux, gateAgent, "GET /internal/v1/world", s.handleWorld)
		s.route(mux, gateAgent, "POST /internal/v1/world/acknowledge", s.handleAcknowledge)
	}
	if s.Heads != nil && s.InternalToken != "" {
		s.route(mux, gateAgent, "PUT /internal/v1/heads", s.handleHeads)
	}
	if s.Codes != nil && s.InternalToken != "" {
		s.route(mux, gateAgent, "POST /internal/v1/claims", s.handleClaim)
		if s.Sessions != nil && s.Sessions.Revoked != nil {
			s.route(mux, gateAgent, "POST /internal/v1/revocations", s.handleRevoke)
		}
		if s.passkeysOn() {
			s.route(mux, gateAgent, "POST /internal/v1/passkey-codes", s.handlePasskeyCode)
		}
	}
	// Here and never on the public listener: the secret is then of no use
	// to anyone who cannot already reach into the cluster, and a guess from
	// the internet has nowhere to be sent.
	if s.Service != nil && s.Sessions != nil && len(s.Sessions.ServiceKey) > 0 {
		declareServiceMetrics()
		s.route(mux, gateExchange, "POST /internal/v1/service-sessions", s.handleServiceSession)
	}
	return mux
}

// Where an exchange came from, as far as an address can say; the label
// values of the exchange metric. A scheduled check inside the cluster is
// private. Loopback is a port-forward, which is a person or an agent
// running the checks from outside. Anything else should not be able to
// reach this listener at all.
const (
	sourceLoopback = "loopback"
	sourcePrivate  = "private"
	sourceOther    = "other"
)

func exchangeSource(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	switch ip := net.ParseIP(host); {
	case ip == nil:
		return sourceOther
	case ip.IsLoopback():
		return sourceLoopback
	case ip.IsPrivate():
		return sourcePrivate
	}
	return sourceOther
}

// said is something a caller sent, made fit to be one field of one log
// line: no line breaks, and no longer than a user agent has any need to be.
// Whoever asks for a session chooses these words, and the record of who
// asked must not be theirs to write.
func said(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\n", " "), "\r", " ")
	if len(text) > 200 {
		text = text[:200]
	}
	return strings.ToValidUTF8(text, "?")
}

type serviceSessionJSON struct {
	Identity   string    `json:"identity"`
	ExpiresAt  time.Time `json:"expiresAt"`
	TTLSeconds int       `json:"ttlSeconds"`
}

// handleServiceSession exchanges the checks' secret for a session. Every
// attempt is one log line and one count, whichever way it went: sessions
// are not stored, so these are the only record that one was asked for.
// What was presented is in neither.
func (s *Server) handleServiceSession(w http.ResponseWriter, r *http.Request) {
	source := exchangeSource(r.RemoteAddr)
	agent := said(r.UserAgent())
	refuse := func(result string, status int) {
		metricServiceExchanges.WithLabelValues(result, source).Inc()
		w.Header().Set("Cache-Control", "no-store")
		defer http.Error(w, http.StatusText(status), status)
		// Past the budget every refusal is still counted, and one a
		// minute is written down: a flood of them must not be able to
		// bury the lines that matter under its own.
		if result == auth.ExchangeLimited && !s.limitedLog.Allow(s.now()) {
			return
		}
		s.log().Warn("map service session refused", "result", result, "source", source, "remote", r.RemoteAddr, "user_agent", agent)
	}
	// A secret in an address ends up in logs along the way, so a request
	// that carries anything there is turned away before it is read.
	if r.URL.RawQuery != "" {
		refuse(exchangeMalformed, http.StatusBadRequest)
		return
	}
	// As a bearer token and no other way. A header that is not one is a
	// wrong secret whatever it holds, the secret itself included: without
	// the scheme it is a value some other part of a client put there.
	presented, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer {
		presented = ""
	}
	switch result := s.Service.Attempt(presented); result {
	case auth.ExchangeOK:
		expires := s.Sessions.IssueService(w)
		metricServiceExchanges.WithLabelValues(result, source).Inc()
		s.log().Info("map service session issued", "identity", auth.ServiceName, "source", source, "remote", r.RemoteAddr, "user_agent", agent, "expires", expires.UTC().Format(time.RFC3339))
		writeJSON(w, http.StatusOK, serviceSessionJSON{Identity: auth.ServiceName, ExpiresAt: expires.UTC(), TTLSeconds: int(s.Sessions.ServiceTTL.Seconds())})
	case auth.ExchangeDenied:
		w.Header().Set("WWW-Authenticate", "Bearer")
		refuse(result, http.StatusUnauthorized)
	default:
		w.Header().Set("Retry-After", strconv.Itoa(int(s.Service.RetryAfter().Seconds())+1))
		refuse(result, http.StatusTooManyRequests)
	}
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
	if err := s.revokeEverywhere(req.XUID); err != nil {
		http.Error(w, "could not record the revocation", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
