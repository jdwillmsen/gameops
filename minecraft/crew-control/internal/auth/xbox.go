package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/pkg/xbox"
)

const (
	viaXbox = "xbox"

	// XboxStateCookie carries the sign-in's state and PKCE verifier between
	// the browser leaving for Microsoft and coming back.
	XboxStateCookie = "__Host-crew_xbox"
	// A label of its own keeps the state key from being the session key and
	// from verifying the map's cookies.
	xboxStateLabel = "crew xbox sign-in state v1"

	xboxCallbackPath = "/auth/xbox/callback"
	maxXboxAccounts  = 16
	maxXboxParam     = 2048

	// Callbacks that reach Microsoft: a burst, then one per refill.
	xboxBurst  = 10
	xboxRefill = 6 * time.Second
	// Refusals are written down within a budget because anyone with a
	// Microsoft account can cause one.
	xboxLogBudget = 20
)

// The results of a sign-in with Xbox that ends without a session. Each is a
// word of ours, the label of a log line and the query of the page the
// browser is sent back to, and none is ever something a caller sent.
const (
	xboxNotAllowed    = "not_allowed"
	xboxCancelled     = "cancelled"
	xboxMicrosoft     = "microsoft_error"
	xboxExpired       = "expired"
	xboxStateMismatch = "state_mismatch"
	xboxReplayed      = "replayed"
	xboxBadRequest    = "bad_request"
	xboxFault         = "fault"
	xboxBusy          = "busy"
)

// XboxProvider is Microsoft and Xbox as the callback needs them.
type XboxProvider interface {
	AuthorizeURL(state, challenge string) string
	Resolve(ctx context.Context, code, verifier string) (xbox.Profile, error)
}

// XboxConfig turns on signing in with an Xbox account. The zero value is off.
type XboxConfig struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	// Accounts is the CREW_XBOX_ACCOUNTS JSON: which XUID is which operator.
	Accounts string
	// Provider stands in for Microsoft in tests.
	Provider XboxProvider
}

var (
	guid      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	xuidShape = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// minXboxSecret is well under a generated client secret's length, so it
// refuses only a placeholder or a value cut short.
const minXboxSecret = 16

// XboxConfigFromEnv reads the Xbox sign-in settings. The secret comes from
// the file a mounted Secret makes, or from the environment for a local run,
// and not from both. Messages name the variable and never the value.
func XboxConfigFromEnv(getenv func(string) string) (XboxConfig, error) {
	raw := getenv("XBOX_LOGIN_ENABLED")
	if raw == "" {
		raw = "false"
	}
	on := raw == "true" || raw == "1"
	if !on && raw != "false" && raw != "0" {
		return XboxConfig{}, errors.New("XBOX_LOGIN_ENABLED must be true or false")
	}
	if !on {
		return XboxConfig{}, nil
	}
	cfg := XboxConfig{Enabled: true, ClientID: getenv("XBOX_CLIENT_ID"), Accounts: getenv("CREW_XBOX_ACCOUNTS")}
	if !guid.MatchString(cfg.ClientID) {
		return XboxConfig{}, errors.New("XBOX_CLIENT_ID must be the app registration's application (client) ID, a GUID")
	}
	path, direct := getenv("XBOX_CLIENT_SECRET_FILE"), getenv("XBOX_CLIENT_SECRET")
	if path != "" && direct != "" {
		return XboxConfig{}, errors.New("set XBOX_CLIENT_SECRET_FILE or XBOX_CLIENT_SECRET, not both")
	}
	secret := direct
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return XboxConfig{}, fmt.Errorf("XBOX_CLIENT_SECRET_FILE: %w", err)
		}
		secret = strings.TrimRight(string(b), "\r\n")
	}
	if len(secret) < minXboxSecret || strings.ContainsAny(secret, " \t\r\n") {
		return XboxConfig{}, fmt.Errorf("the Xbox client secret (XBOX_CLIENT_SECRET_FILE or XBOX_CLIENT_SECRET) must be the app's secret value: at least %d characters, no whitespace", minXboxSecret)
	}
	cfg.ClientSecret = secret
	return cfg, nil
}

// XboxAccount allows one Xbox account to sign in as one operator. The XUID
// is the account's number and does not change when its gamertag does.
type XboxAccount struct {
	Operator string `json:"operator"`
	XUID     string `json:"xuid"`
}

// ParseXboxAccounts reads CREW_XBOX_ACCOUNTS. Every account must belong to a
// listed operator, so that removing an operator removes its Xbox sign-in
// with it and a typo cannot grant access to nobody in particular.
func ParseXboxAccounts(raw string, ops []Operator) ([]XboxAccount, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var accts []XboxAccount
	if err := dec.Decode(&accts); err != nil {
		return nil, fmt.Errorf("xbox accounts: %w", err)
	}
	if dec.More() {
		return nil, errors.New("xbox accounts: trailing data")
	}
	if len(accts) > maxXboxAccounts {
		return nil, fmt.Errorf("xbox accounts: at most %d", maxXboxAccounts)
	}
	listed := map[string]bool{}
	for _, o := range ops {
		listed[o.ID] = true
	}
	seen := map[string]bool{}
	for _, a := range accts {
		switch {
		case !listed[a.Operator]:
			return nil, fmt.Errorf("xbox accounts: operator %q is not in the operator list", a.Operator)
		case !xuidShape.MatchString(a.XUID):
			return nil, fmt.Errorf("xbox accounts: the xuid for %q is not a number", a.Operator)
		case seen[a.XUID]:
			return nil, fmt.Errorf("xbox accounts: an xuid is listed twice (operator %q)", a.Operator)
		}
		seen[a.XUID] = true
	}
	return accts, nil
}

// xboxLogin is the Xbox sign-in of an Auth that has it on.
type xboxLogin struct {
	provider XboxProvider
	flow     *xbox.Flow
	accounts []XboxAccount

	limit bucket
	logs  bucket
}

func newXboxLogin(cfg XboxConfig, origin string, ops []Operator, key []byte, now func() time.Time) (*xboxLogin, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	accts, err := ParseXboxAccounts(cfg.Accounts, ops)
	if err != nil {
		return nil, err
	}
	if len(accts) == 0 {
		return nil, errors.New("xbox sign-in is on but CREW_XBOX_ACCOUNTS lists nobody")
	}
	flow, err := xbox.NewFlow(XboxStateCookie, xboxStateLabel, key, now)
	if err != nil {
		return nil, err
	}
	provider := cfg.Provider
	if provider == nil {
		provider = xbox.New(xbox.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURI: origin + xboxCallbackPath})
	}
	return &xboxLogin{
		provider: provider, flow: flow, accounts: accts,
		limit: bucket{burst: xboxBurst, refill: xboxRefill},
		logs:  bucket{burst: xboxLogBudget, refill: time.Minute / xboxLogBudget},
	}, nil
}

func (x *xboxLogin) operatorFor(xuid string) (string, bool) {
	for _, a := range x.accounts {
		if a.XUID == xuid {
			return a.Operator, true
		}
	}
	return "", false
}

// xboxKey names an account in a session without carrying its XUID.
func xboxKey(xuid string) string { return KeyOf([]byte("xbox:" + xuid)) }

// active is whether an Xbox session still has an account behind it, so that
// removing the account from the list ends the session at the next request.
func (x *xboxLogin) active(op, key string) bool {
	for _, a := range x.accounts {
		if a.Operator == op && xboxKey(a.XUID) == key {
			return true
		}
	}
	return false
}

func (a *Auth) xboxStart(w http.ResponseWriter, r *http.Request) {
	state, challenge := a.xbox.flow.Begin(w)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, a.xbox.provider.AuthorizeURL(state, challenge), http.StatusFound)
}

// xboxRefused ends a sign-in without a session. The browser goes back to the
// sign-in page with the result, one word of a closed list that the page
// turns into a sentence.
func (a *Auth) xboxRefused(w http.ResponseWriter, r *http.Request, result string, extra logging.Fields) {
	if a.xbox.logs.Allow(a.now()) {
		f := logging.Fields{"result": result}
		for k, v := range extra {
			f[k] = v
		}
		a.event("xbox_login_refused", f)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/signin?"+url.Values{"xbox": {result}}.Encode(), http.StatusSeeOther)
}

// xboxCallback is where Microsoft sends the browser back. Nothing is
// believed until the cookie this browser was given at the start, the state
// in the address and Microsoft's code all agree.
func (a *Auth) xboxCallback(w http.ResponseWriter, r *http.Request) {
	x := a.xbox
	q := r.URL.Query()
	state := q.Get("state")
	if len(state) > maxXboxParam {
		state = ""
	}
	verifier, err := x.flow.Finish(w, r, state)
	switch {
	case errors.Is(err, xbox.ErrNoState):
		a.xboxRefused(w, r, xboxExpired, nil)
		return
	case errors.Is(err, xbox.ErrMismatch):
		a.xboxRefused(w, r, xboxStateMismatch, nil)
		return
	case errors.Is(err, xbox.ErrReplayed):
		a.xboxRefused(w, r, xboxReplayed, nil)
		return
	case err != nil:
		a.xboxRefused(w, r, xboxFault, nil)
		return
	}
	if e := q.Get("error"); e != "" {
		// Microsoft's description is for a developer and may carry anything;
		// only whether the person said no is used.
		if e == "access_denied" {
			a.xboxRefused(w, r, xboxCancelled, nil)
		} else {
			a.xboxRefused(w, r, xboxMicrosoft, nil)
		}
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > maxXboxParam {
		a.xboxRefused(w, r, xboxBadRequest, nil)
		return
	}
	// Each callback that gets this far costs Microsoft three requests, so a
	// script with a valid cookie each time could have the app throttled. The
	// service sees only the gateway's address, so the bucket is global.
	if !x.limit.Allow(a.now()) {
		a.xboxRefused(w, r, xboxBusy, nil)
		return
	}
	profile, err := x.provider.Resolve(r.Context(), code, verifier)
	var failure *xbox.Failure
	switch {
	case errors.As(err, &failure):
		a.xboxRefused(w, r, failure.Result, logging.Fields{"detail": failure.Detail})
		return
	case err != nil:
		a.xboxRefused(w, r, xboxFault, nil)
		return
	}
	if !xuidShape.MatchString(profile.XUID) {
		a.xboxRefused(w, r, xbox.ResultBadProfile, nil)
		return
	}
	opID, ok := x.operatorFor(profile.XUID)
	if !ok {
		a.xboxRefused(w, r, xboxNotAllowed, logging.Fields{"xuid": profile.XUID})
		return
	}
	if _, ok := a.operator(opID); !ok {
		a.xboxRefused(w, r, xboxNotAllowed, logging.Fields{"xuid": profile.XUID})
		return
	}
	a.issueVia(w, opID, xboxKey(profile.XUID), viaXbox)
	// Sessions are not stored, so this line is the only record of who was
	// let in and when. It names the XUID and not the gamertag.
	a.event("operator_login", logging.Fields{"operator": opID, "method": viaXbox, "xuid": profile.XUID})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// methods tells the sign-in page which ways in to offer.
func (a *Auth) methods(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"passkey": true, "xbox": a.xbox != nil})
}

// bucket is a token bucket filled at its first use.
type bucket struct {
	burst  float64
	refill time.Duration

	mu       sync.Mutex
	tokens   float64
	refilled time.Time
}

func (b *bucket) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.refilled.IsZero() {
		b.tokens, b.refilled = b.burst, now
	}
	if now.After(b.refilled) {
		b.tokens = min(b.burst, b.tokens+now.Sub(b.refilled).Seconds()/b.refill.Seconds())
		b.refilled = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
