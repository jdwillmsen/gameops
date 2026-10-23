// Package auth is crew-control's own operator sign-in: operators listed in
// the environment enrol a passkey with a one-time invite, and a signed
// cookie carries the session. Every request re-checks that the operator is
// still listed and the passkey still exists.
package auth

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	requestHeader = "X-Crew-Request"
	defaultData   = "/data"
	maxBody       = 64 << 10
)

type Config struct {
	// Origin is PUBLIC_ORIGIN; its host is the WebAuthn RP ID.
	Origin string
	// DataDir holds session.key, passkeys.json and invites-used.json.
	DataDir string
	// Operators is the CREW_OPERATORS JSON.
	Operators  string
	InvitesDir string
	Now        func() time.Time
	Log        *logging.Logger
}

type Auth struct {
	enabled bool
	origin  string
	rpID    string
	ops     []Operator
	key     []byte
	keys    *Passkeys
	invites *Invites
	wa      *webauthn.WebAuthn
	now     func() time.Time
	log     *logging.Logger
	reports *reportLimit

	cmu sync.Mutex
	cer map[string]*ceremony
}

// Disabled is the auth of a deployment with no sign-in configured: nobody
// is an operator and every protected route answers 401.
func Disabled() *Auth { return &Auth{} }

func (a *Auth) Enabled() bool { return a != nil && a.enabled }

// New returns a disabled Auth, not an error, when the origin or the operator
// list is empty: that is how a deployment without sign-in is configured.
func New(cfg Config) (*Auth, error) {
	ops, err := ParseOperators(cfg.Operators)
	if err != nil {
		return nil, err
	}
	if cfg.Origin == "" || len(ops) == 0 {
		return Disabled(), nil
	}
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() != u.Host || u.Host != lower(u.Host) ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || cfg.Origin != "https://"+u.Host {
		return nil, errors.New("PUBLIC_ORIGIN must be https://host in lower case, with no port or path")
	}
	dir := cfg.DataDir
	if dir == "" {
		dir = defaultData
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	key, err := loadSessionKey(filepath.Join(dir, "session.key"))
	if err != nil {
		return nil, err
	}
	keys, err := loadPasskeys(filepath.Join(dir, "passkeys.json"), now)
	if err != nil {
		return nil, err
	}
	keys.onBroken = func() {
		if cfg.Log != nil {
			cfg.Log.Info("passkeys_unreadable", logging.Fields{})
		}
	}
	invites, err := loadInvites(cfg.InvitesDir, filepath.Join(dir, "invites-used.json"), now)
	if err != nil {
		return nil, err
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: u.Host, RPDisplayName: "Crew console", RPOrigins: []string{cfg.Origin}})
	if err != nil {
		return nil, err
	}
	return &Auth{enabled: true, origin: cfg.Origin, rpID: u.Host, ops: ops, key: key, keys: keys, invites: invites, wa: wa,
		now: now, log: cfg.Log, reports: &reportLimit{now: now}, cer: map[string]*ceremony{}}, nil
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func (a *Auth) operator(id string) (Operator, bool) {
	for _, o := range a.ops {
		if o.ID == id {
			return o, true
		}
	}
	return Operator{}, false
}

func (a *Auth) event(name string, fields logging.Fields) {
	if a.log != nil {
		a.log.Info(name, fields)
	}
}

// Operator is the signed-in operator of r. The cookie alone is not enough:
// the operator must still be listed and the passkey it names must still
// exist, so removing either ends the session at the next request.
func (a *Auth) Operator(r *http.Request) (Operator, bool) {
	if !a.Enabled() {
		return Operator{}, false
	}
	s, ok := a.verify(r)
	if !ok {
		return Operator{}, false
	}
	op, ok := a.operator(s.Op)
	if !ok || !a.keys.Active(op.ID, s.Key) {
		return Operator{}, false
	}
	return op, true
}

// stateChanging is the CSRF rule for every POST: SameSite=Strict keeps the
// cookie off cross-site requests, and a custom header cannot be sent
// cross-origin without a preflight this server never grants.
func stateChanging(r *http.Request) bool {
	if r.Method != http.MethodPost || r.Header.Get(requestHeader) != "1" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func rejectStateChanging(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.Header.Get(requestHeader) == "1" {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			fail(w, http.StatusUnsupportedMediaType, "json_required")
			return
		}
	}
	fail(w, http.StatusForbidden, "bad_request")
}

// Require lets a request through only as a signed-in operator's POST.
func (a *Auth) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() {
			fail(w, http.StatusUnauthorized, "signed_out")
			return
		}
		if !stateChanging(r) {
			rejectStateChanging(w, r)
			return
		}
		if _, ok := a.Operator(r); !ok {
			fail(w, http.StatusUnauthorized, "signed_out")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// Mount registers the /auth routes.
func (a *Auth) Mount(mux *http.ServeMux) {
	if !a.Enabled() {
		return
	}
	mux.HandleFunc("POST /auth/register/options", a.registerOptions)
	mux.HandleFunc("POST /auth/register/finish", a.registerFinish)
	mux.HandleFunc("POST /auth/login/options", a.loginOptions)
	mux.HandleFunc("POST /auth/login/finish", a.loginFinish)
	mux.HandleFunc("POST /auth/logout", a.logout)
	mux.HandleFunc("POST /auth/report", a.report)
	mux.HandleFunc("GET /auth/me", a.me)
}

func (a *Auth) me(w http.ResponseWriter, r *http.Request) {
	op, ok := a.Operator(r)
	if !ok {
		fail(w, http.StatusUnauthorized, "signed_out")
		return
	}
	writeJSON(w, http.StatusOK, map[string]Operator{"operator": op})
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if !stateChanging(r) {
		rejectStateChanging(w, r)
		return
	}
	clearCookie(w, SessionCookie)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
