package server

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

// maxPasskeyBody is far more than any credential a browser sends, and
// small enough that a flood of them costs little to turn away.
const maxPasskeyBody = 64 << 10

// Counts since start, for the admin status.
var issuedSessions, passkeyLogins, passkeyRefused atomic.Int64

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPasskeyBody))
	return b, err == nil && len(b) > 0
}

func (s *Server) handlePasskeyOptions(w http.ResponseWriter, _ *http.Request) {
	opts, err := s.Ceremonies.BeginLogin(w)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// logBudget decides whether a refusal anyone could cause is written down:
// a few at once, then one a minute, as the service-session exchange does,
// so that a flood is a count and cannot bury the lines that matter.
type logBudget struct {
	mu       sync.Mutex
	tokens   float64
	refilled time.Time
}

const refusalBurst = 5

func (b *logBudget) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refilled.IsZero() {
		b.tokens, b.refilled = refusalBurst, now
	}
	b.tokens = min(refusalBurst, b.tokens+now.Sub(b.refilled).Minutes())
	b.refilled = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// loginRefused counts every refusal. One made before the signature is
// verified is anyone's to make and is written down within logBudget; one
// after it is written down every time, with the XUID it was for.
func (s *Server) loginRefused(w http.ResponseWriter, result string, k *auth.Passkey) {
	s.metricSet().logins.WithLabelValues(result).Inc()
	passkeyRefused.Add(1)
	if k != nil {
		s.log().Warn("map passkey login refused", "result", result, "xuid", k.XUID, "credential", auth.LogName(k.ID))
	} else if s.refusedLog.Allow(s.now()) {
		s.log().Warn("map passkey login refused", "result", result)
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": result})
}

func (s *Server) handlePasskeyFinish(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		s.loginRefused(w, auth.ResultBadAssertion, nil)
		return
	}
	// The operator list is asked inside the ceremony, before the counter
	// is stored, so that a refused login moves nothing.
	k, err := s.Ceremonies.FinishLogin(w, r, body, func(xuid string) bool { return s.isAdmin(r.Context(), xuid) })
	var refused *auth.CeremonyError
	switch {
	case errors.As(err, &refused):
		switch refused.Result {
		case auth.ResultPending, auth.ResultNotOperator, auth.ResultCounter, auth.ResultReplayed:
		default:
			k = auth.Passkey{}
		}
		if k.XUID != "" {
			s.loginRefused(w, refused.Result, &k)
		} else {
			s.loginRefused(w, refused.Result, nil)
		}
		return
	case err != nil:
		// A fault of the map's own, such as a store that could not be
		// written: nobody is let in on it, and it is not the caller's
		// refusal to count.
		s.log().Error("map passkey login failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fault"})
		return
	}
	if s.Sessions.Passkeys == nil {
		// Wiring that left sessions unable to check passkeys would issue
		// a cookie that never verifies; it shows here and not as a riddle.
		s.loginRefused(w, "disabled", &k)
		return
	}
	s.Sessions.IssuePasskey(w, auth.Identity{XUID: k.XUID, Gamertag: k.Gamertag}, auth.KeyOf(k.ID))
	s.metricSet().logins.WithLabelValues("ok").Inc()
	passkeyLogins.Add(1)
	s.log().Info("map login issued", "xuid", k.XUID, "method", auth.MethodPasskey, "credential", auth.LogName(k.ID))
	writeJSON(w, http.StatusOK, map[string]string{"state": "ok", "gamertag": k.Gamertag})
}

func (s *Server) handleRegisterOptions(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		Label string `json:"label"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label", "message": auth.ErrLabel.Error()})
		return
	}
	opts, err := s.Ceremonies.BeginRegistration(sess.XUID, sess.IssuedMilli, req.Label)
	switch {
	case errors.Is(err, auth.ErrLabel):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "label", "message": err.Error()})
	case errors.Is(err, auth.ErrPendingExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pending"})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
	default:
		writeJSON(w, http.StatusOK, opts)
	}
}

func (s *Server) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	body, ok := readBody(w, r)
	if !ok {
		s.metricSet().registered.WithLabelValues("refused").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refused"})
		return
	}
	k, err := s.Ceremonies.FinishRegistration(sess.Identity, sess.IssuedMilli, body)
	switch {
	case errors.Is(err, auth.ErrRegistrationExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
	case errors.Is(err, auth.ErrPendingExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pending"})
	case errors.Is(err, auth.ErrTooMany):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "too_many"})
	case err != nil:
		s.metricSet().registered.WithLabelValues("refused").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refused"})
	default:
		s.metricSet().registered.WithLabelValues("pending").Inc()
		s.log().Info("map passkey pending", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
		writeJSON(w, http.StatusOK, map[string]any{"fingerprint": auth.Fingerprint(k.ID), "expiresIn": int(auth.PendingFor.Seconds())})
	}
}

func (s *Server) handleRegisterConfirm(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	var req struct {
		CredentialID string `json:"credentialId"`
		Code         string `json:"code"`
	}
	if err := decodeStrict(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "wrong_code"})
		return
	}
	id, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(req.CredentialID, "="))
	if err != nil {
		id = nil
	}
	k, left, err := s.Passkeys.Confirm(sess.XUID, sess.IssuedMilli, id, req.Code)
	switch {
	case errors.Is(err, auth.ErrWrongCode):
		s.metricSet().registered.WithLabelValues("code_wrong").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "wrong_code", "triesLeft": left})
	case errors.Is(err, auth.ErrAbandoned):
		s.metricSet().registered.WithLabelValues("abandoned").Inc()
		writeJSON(w, http.StatusGone, map[string]string{"error": "abandoned"})
	case errors.Is(err, auth.ErrNothingPending):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nothing_pending"})
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "busy"})
	default:
		s.metricSet().registered.WithLabelValues("confirmed").Inc()
		s.log().Info("map passkey registered", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleRegisterCancel(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.SessionFromContext(r.Context())
	if k, ok := s.Passkeys.Cancel(sess.XUID, sess.IssuedMilli); ok {
		s.metricSet().registered.WithLabelValues("cancelled").Inc()
		s.log().Info("map passkey cancelled", "xuid", sess.XUID, "credential", auth.LogName(k.ID))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePasskeyCode is the agent asking for the code to whisper to the
// player who typed !map passkey. The XUID is the one the server stamped
// on that chat line; the code goes to that player and nobody else.
func (s *Server) handlePasskeyCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		XUID string `json:"xuid"`
	}
	if err := decodeStrict(w, r, &req); err != nil || !auth.IsXUID(req.XUID) {
		http.Error(w, "a passkey code needs the player's xuid", http.StatusBadRequest)
		return
	}
	code, id, err := s.Passkeys.IssueCode(req.XUID)
	if errors.Is(err, auth.ErrNothingPending) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	s.metricSet().registered.WithLabelValues("code_issued").Inc()
	s.log().Info("map passkey code issued", "xuid", req.XUID, "credential", auth.LogName(id))
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expiresIn": int(auth.PendingFor.Seconds()), "fingerprint": auth.Fingerprint(id)})
}
