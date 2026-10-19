package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/xbox"
)

// XboxProvider is Microsoft and Xbox as the callback needs them.
type XboxProvider interface {
	AuthorizeURL(state, challenge string) string
	Resolve(ctx context.Context, code, verifier string) (xbox.Profile, error)
}

// KnownPlayers is who has been seen on the server.
type KnownPlayers interface {
	Has(xuid string) bool
	Record(xuid string) error
	Forget(xuid string) error
}

// Allowlist is the game server's own list, when the map is told to count it.
type Allowlist interface {
	Has(ctx context.Context, xuid string) (on, answered bool)
}

// The results of a sign-in with Xbox; the label values of its metric. Each
// is a word of ours and none is ever something a caller sent.
const (
	xboxOK            = "ok"
	xboxStranger      = "stranger"
	xboxCancelled     = "cancelled"
	xboxMicrosoft     = "microsoft_error"
	xboxExpired       = "expired"
	xboxStateMismatch = "state_mismatch"
	xboxReplayed      = "replayed"
	xboxBadRequest    = "bad_request"
	xboxFault         = "fault"
	// xboxBusy is a callback turned away before Microsoft was asked, and
	// xboxAllowlistDown a refusal for want of an answer from the bridge:
	// neither is a stranger.
	xboxBusy          = "busy"
	xboxAllowlistDown = "allowlist_unavailable"
)

var xboxResults = []string{
	xboxOK, xboxStranger, xboxCancelled, xboxMicrosoft, xboxExpired, xboxStateMismatch, xboxReplayed, xboxBadRequest,
	xbox.ResultExchangeFailed, xbox.ResultNoProfile, xbox.ResultChild, xbox.ResultRefused, xbox.ResultUnavailable, xbox.ResultBadProfile,
	xboxFault, xboxBusy, xboxAllowlistDown,
}

var metricXboxLogins = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "mcmap_xbox_logins_total",
	Help: "Sign-ins with an Xbox account, by result: ok, stranger, cancelled, microsoft_error, expired, state_mismatch, replayed, bad_request, exchange_failed, no_xbox_profile, child_account, xbox_refused, xbox_unavailable, bad_identity, fault, busy, allowlist_unavailable.",
}, []string{"result"})

func declareXboxMetrics() {
	for _, r := range xboxResults {
		metricXboxLogins.WithLabelValues(r)
	}
}

func (s *Server) xboxOn() bool {
	return s.Sessions != nil && s.Sessions.XboxOn && s.XboxClient != nil && s.XboxFlow != nil && s.Known != nil
}

// handleXboxStart sends the browser to Microsoft. It changes nothing but
// the browser's own cookie, so a page on another site that sends someone
// here gains nothing.
func (s *Server) handleXboxStart(w http.ResponseWriter, r *http.Request) {
	state, challenge := s.XboxFlow.Begin(w)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.XboxClient.AuthorizeURL(state, challenge), http.StatusFound)
}

// xboxRefused ends a sign-in without a session. The browser is sent back to
// the page with the result, one of a closed list, which the page turns into
// a sentence: nothing a caller sent is ever put in the address.
func (s *Server) xboxRefused(w http.ResponseWriter, r *http.Request, result string, attrs ...any) {
	metricXboxLogins.WithLabelValues(result).Inc()
	// Anyone with a Microsoft account can cause these, so they are written
	// down within a budget; the count is of every one.
	if s.xboxRefusedLog.Allow(s.now()) {
		s.log().Warn("map xbox login refused", append([]any{"result", result}, attrs...)...)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?"+url.Values{"xbox": {result}}.Encode(), http.StatusSeeOther)
}

// maxXboxParam is far more than a state or a code are, and bounds what is
// carried to Microsoft's token endpoint.
const maxXboxParam = 2048

// handleXboxCallback is where Microsoft sends the browser back. Nothing is
// believed until the cookie this browser was given at the start, the state
// in the address and Microsoft's code all agree.
func (s *Server) handleXboxCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if len(state) > maxXboxParam {
		state = ""
	}
	verifier, err := s.XboxFlow.Finish(w, r, state)
	switch {
	case errors.Is(err, auth.ErrXboxNoState):
		s.xboxRefused(w, r, xboxExpired)
		return
	case errors.Is(err, auth.ErrXboxMismatch):
		s.xboxRefused(w, r, xboxStateMismatch)
		return
	case errors.Is(err, auth.ErrXboxReplayed):
		s.xboxRefused(w, r, xboxReplayed)
		return
	case err != nil:
		s.xboxRefused(w, r, xboxFault)
		return
	}
	if e := q.Get("error"); e != "" {
		// Microsoft's description is for a developer and may carry
		// anything; only whether the person said no is used.
		if e == "access_denied" {
			s.xboxRefused(w, r, xboxCancelled)
		} else {
			s.xboxRefused(w, r, xboxMicrosoft)
		}
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > maxXboxParam {
		s.xboxRefused(w, r, xboxBadRequest)
		return
	}
	// Every callback that gets this far costs Microsoft three requests, so a
	// script with a valid cookie each time could have the app throttled for
	// everyone. The service sees only the gateway's address, so the bucket
	// is global.
	if !s.xboxLimit.Allow(s.now()) {
		s.xboxRefused(w, r, xboxBusy)
		return
	}
	profile, err := s.XboxClient.Resolve(r.Context(), code, verifier)
	var failure *xbox.Failure
	switch {
	case errors.As(err, &failure):
		s.xboxRefused(w, r, failure.Result, "detail", failure.Detail)
		return
	case err != nil:
		s.xboxRefused(w, r, xboxFault)
		return
	}
	if !auth.IsXUID(profile.XUID) {
		s.xboxRefused(w, r, xbox.ResultBadProfile)
		return
	}
	id := auth.Identity{XUID: profile.XUID, Gamertag: profile.Gamertag}
	known, unavailable := s.knownPlayer(r.Context(), id.XUID)
	switch {
	case unavailable:
		// No XUID: nothing says this one was refused for the bridge's sake
		// rather than theirs, and a flood of these must not name anyone.
		s.xboxRefused(w, r, xboxAllowlistDown, "bridge", "no_answer")
		return
	case !known:
		s.xboxRefused(w, r, xboxStranger, "xuid", id.XUID)
		return
	}
	if _, err := s.Sessions.IssueXbox(w, id); err != nil {
		s.xboxRefused(w, r, xbox.ResultBadProfile, "xuid", id.XUID)
		return
	}
	metricXboxLogins.WithLabelValues(xboxOK).Inc()
	s.linkRoster(id)
	issuedSessions.Add(1)
	if s.Passkeys != nil {
		s.Passkeys.RefreshGamertag(id.XUID, id.Gamertag)
	}
	// Sessions are not stored, so this line is the only record of who was
	// let in and when. It names the XUID and not the gamertag.
	s.log().Info("map login issued", "xuid", id.XUID, "method", auth.MethodXbox)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// knownPlayer is the gate: seen on the server, or on its allowlist where
// the map is told to count that. unavailable is true only for a player who
// is not known and whom the bridge could not be asked about, so that the
// refusal is not taken for a stranger's.
func (s *Server) knownPlayer(ctx context.Context, xuid string) (known, unavailable bool) {
	if s.Known.Has(xuid) {
		return true, false
	}
	if s.Allowlist == nil {
		return false, false
	}
	on, answered := s.Allowlist.Has(ctx, xuid)
	return on, !answered && !on
}

// Callbacks that reach Microsoft: a burst, then one per refill.
const (
	xboxBurst  = 10
	xboxRefill = 6 * time.Second
)

// bucket is a token bucket for the callback, filled at start.
type bucket struct {
	mu       sync.Mutex
	tokens   float64
	refilled time.Time
}

func (b *bucket) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refilled.IsZero() {
		b.tokens, b.refilled = xboxBurst, now
	}
	if now.After(b.refilled) {
		b.tokens = min(xboxBurst, b.tokens+now.Sub(b.refilled).Seconds()/xboxRefill.Seconds())
		b.refilled = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
