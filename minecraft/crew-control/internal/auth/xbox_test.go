package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/pkg/xbox/xboxtest"
)

const (
	ownerXUID  = "2535400000000001"
	joshXUID   = "2535400000000002"
	strangerID = "2535400000000099"
	accounts   = `[{"operator":"owner","xuid":"` + ownerXUID + `"},{"operator":"josh","xuid":"` + joshXUID + `"}]`
)

type xboxEnv struct {
	*env
	fake *xboxtest.Fake
}

func newXboxEnv(t *testing.T, accts string) *xboxEnv {
	t.Helper()
	e := newEnv(t)
	x := &xboxEnv{env: e, fake: xboxtest.New(t, origin+xboxCallbackPath)}
	x.buildXbox(opJSON, accts)
	return x
}

func (x *xboxEnv) buildXbox(operators, accts string) {
	x.t.Helper()
	a, err := New(Config{
		Origin: origin, DataDir: x.data, Operators: operators, InvitesDir: x.invites, Now: x.clk.now, Log: logging.New("error"),
		Xbox: XboxConfig{Enabled: true, Accounts: accts, Provider: x.fake.Client()},
	})
	if err != nil {
		x.t.Fatal(err)
	}
	x.a = a
	x.mux = http.NewServeMux()
	a.Mount(x.mux)
}

// signIn is a whole sign-in as a person at the fake.
func (x *xboxEnv) signIn(c *client, acct xboxtest.Account) *httptest.ResponseRecorder {
	x.t.Helper()
	x.fake.SignIn(acct)
	start := c.do(http.MethodGet, "/auth/xbox/start", nil, false)
	if start.Code != http.StatusFound {
		x.t.Fatalf("start = %d %s", start.Code, start.Body)
	}
	back, err := url.Parse(x.fake.Approve(start.Header().Get("Location"), false))
	if err != nil {
		x.t.Fatal(err)
	}
	return c.do(http.MethodGet, back.RequestURI(), nil, false)
}

func resultOf(rec *httptest.ResponseRecorder) string {
	u, _ := url.Parse(rec.Header().Get("Location"))
	return u.Query().Get("xbox")
}

func TestXbox_AnAllowedAccountSignsInAsItsOperator(t *testing.T) {
	x := newXboxEnv(t, accounts)
	log := x.logs()
	c := x.client()
	rec := x.signIn(c, xboxtest.Account{XUID: joshXUID, Gamertag: "JoshGT"})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	me := c.me()
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"id":"josh"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body)
	}
	if strings.Contains(me.Body.String(), joshXUID) {
		t.Error("the account number was sent to the browser")
	}
	if post := c.post("/auth/logout", map[string]string{}); post.Code != http.StatusOK {
		t.Errorf("logout = %d", post.Code)
	}
	if c.me().Code != http.StatusUnauthorized {
		t.Error("still signed in after logout")
	}
	l := log()
	if !strings.Contains(l, `"method":"xbox"`) || !strings.Contains(l, joshXUID) {
		t.Errorf("login not recorded:\n%s", l)
	}
	lacks(t, l, "JoshGT")
	lacks(t, l, x.fake.Secrets...)
}

func TestXbox_AnUnlistedAccountGetsNoSession(t *testing.T) {
	x := newXboxEnv(t, accounts)
	c := x.client()
	rec := x.signIn(c, xboxtest.Account{XUID: strangerID, Gamertag: "Stranger"})
	if rec.Code != http.StatusSeeOther || resultOf(rec) != xboxNotAllowed || !strings.HasPrefix(rec.Header().Get("Location"), "/signin?") {
		t.Fatalf("callback = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == SessionCookie && ck.MaxAge >= 0 {
			t.Error("a session was issued")
		}
	}
	if c.me().Code != http.StatusUnauthorized {
		t.Error("an unlisted account is signed in")
	}
}

func TestXbox_RefusalsAreWordsOfOurOwn(t *testing.T) {
	x := newXboxEnv(t, accounts)
	c := x.client()
	x.fake.SignIn(xboxtest.Account{XUID: ownerXUID, Gamertag: "O"})
	start := c.do(http.MethodGet, "/auth/xbox/start", nil, false)
	authorize := start.Header().Get("Location")

	cancelled, _ := url.Parse(x.fake.Approve(authorize, true))
	if got := resultOf(c.do(http.MethodGet, cancelled.RequestURI(), nil, false)); got != xboxCancelled {
		t.Errorf("cancel = %q", got)
	}

	c = x.client()
	start = c.do(http.MethodGet, "/auth/xbox/start", nil, false)
	back, _ := url.Parse(x.fake.Approve(start.Header().Get("Location"), false))
	q := back.Query()
	q.Set("state", "someone-elses")
	if got := resultOf(c.do(http.MethodGet, "/auth/xbox/callback?"+q.Encode(), nil, false)); got != xboxStateMismatch {
		t.Errorf("mismatch = %q", got)
	}

	if got := resultOf(x.client().do(http.MethodGet, back.RequestURI(), nil, false)); got != xboxExpired {
		t.Errorf("no cookie = %q", got)
	}

	c = x.client()
	start = c.do(http.MethodGet, "/auth/xbox/start", nil, false)
	saved := *c.cookies[XboxStateCookie]
	back, _ = url.Parse(x.fake.Approve(start.Header().Get("Location"), false))
	if rec := c.do(http.MethodGet, back.RequestURI(), nil, false); rec.Header().Get("Location") != "/" {
		t.Fatalf("first use = %q", rec.Header().Get("Location"))
	}
	replay := x.client()
	replay.cookies[XboxStateCookie] = &saved
	if got := resultOf(replay.do(http.MethodGet, back.RequestURI(), nil, false)); got != xboxReplayed {
		t.Errorf("replay = %q", got)
	}

	c = x.client()
	if got := resultOf(c.do(http.MethodGet, "/auth/xbox/callback?state=x&error=server_error&error_description=%0Aevil", nil, false)); got != xboxExpired {
		t.Errorf("callback with no start = %q", got)
	}
}

func TestXbox_MicrosoftFailuresKeepTheirOwnWords(t *testing.T) {
	x := newXboxEnv(t, accounts)
	x.fake.SignIn(xboxtest.Account{XUID: ownerXUID, Gamertag: "O", XErr: 2148916233})
	c := x.client()
	start := c.do(http.MethodGet, "/auth/xbox/start", nil, false)
	back, _ := url.Parse(x.fake.Approve(start.Header().Get("Location"), false))
	rec := c.do(http.MethodGet, back.RequestURI(), nil, false)
	if got := resultOf(rec); got != "no_xbox_profile" {
		t.Errorf("result = %q", got)
	}
	if c.me().Code != http.StatusUnauthorized {
		t.Error("signed in despite the refusal")
	}
}

func TestXbox_RemovingTheAccountOrOperatorEndsTheSession(t *testing.T) {
	x := newXboxEnv(t, accounts)
	c := x.client()
	x.signIn(c, xboxtest.Account{XUID: joshXUID, Gamertag: "J"})
	if c.me().Code != http.StatusOK {
		t.Fatal("not signed in")
	}
	x.buildXbox(opJSON, `[{"operator":"owner","xuid":"`+ownerXUID+`"}]`)
	if c.me().Code != http.StatusUnauthorized {
		t.Error("session outlived the account")
	}
	x.buildXbox(opJSON, accounts)
	if c.me().Code != http.StatusOK {
		t.Fatal("account back, session should verify again")
	}
	x.buildXbox(`[{"id":"owner","name":"Owner"}]`, `[{"operator":"owner","xuid":"`+ownerXUID+`"}]`)
	if c.me().Code != http.StatusUnauthorized {
		t.Error("session outlived the operator")
	}
}

func TestXbox_AnXboxSessionCannotPassForAPasskeyOne(t *testing.T) {
	x := newXboxEnv(t, accounts)
	c := x.client()
	x.signIn(c, xboxtest.Account{XUID: ownerXUID, Gamertag: "O"})
	s, ok := x.a.verify(jarRequest(c))
	if !ok || s.Via != viaXbox {
		t.Fatalf("session = %+v", s)
	}
	// The same cookie body without its marker names no passkey.
	rec := httptest.NewRecorder()
	x.a.issue(rec, s.Op, s.Key)
	forged := httptest.NewRequest("GET", "/auth/me", nil)
	forged.AddCookie(rec.Result().Cookies()[0])
	if _, ok := x.a.Operator(forged); ok {
		t.Error("an xbox key was accepted as a passkey")
	}
}

func jarRequest(c *client) *http.Request {
	r := httptest.NewRequest("GET", "/auth/me", nil)
	for _, ck := range c.cookies {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	return r
}

func TestXbox_TheCallbackIsRateLimitedBeforeAskingMicrosoft(t *testing.T) {
	x := newXboxEnv(t, accounts)
	var busy bool
	for range xboxBurst + 2 {
		c := x.client()
		start := c.do(http.MethodGet, "/auth/xbox/start", nil, false)
		back, _ := url.Parse(x.fake.Approve(start.Header().Get("Location"), false))
		if resultOf(c.do(http.MethodGet, back.RequestURI(), nil, false)) == xboxBusy {
			busy = true
		}
	}
	if !busy {
		t.Error("a flood of callbacks was never turned away")
	}
}

func TestXbox_OffByDefault(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for _, p := range []string{"/auth/xbox/start", "/auth/xbox/callback"} {
		if rec := c.do(http.MethodGet, p, nil, false); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	if rec := c.do(http.MethodGet, "/auth/methods", nil, false); strings.Contains(rec.Body.String(), `"xbox":true`) {
		t.Errorf("methods = %s", rec.Body)
	}
}

func TestXbox_MethodsSaysWhatToOffer(t *testing.T) {
	x := newXboxEnv(t, accounts)
	rec := x.client().do(http.MethodGet, "/auth/methods", nil, false)
	if !strings.Contains(rec.Body.String(), `"xbox":true`) || !strings.Contains(rec.Body.String(), `"passkey":true`) {
		t.Errorf("methods = %s", rec.Body)
	}
}

func TestXbox_ConfigRefusesWhatCouldGrantAccessByMistake(t *testing.T) {
	ops := []Operator{{ID: "owner", Name: "Owner"}}
	for name, raw := range map[string]string{
		"unknown operator": `[{"operator":"nobody","xuid":"1"}]`,
		"gamertag":         `[{"operator":"owner","xuid":"JoshGT"}]`,
		"duplicate":        `[{"operator":"owner","xuid":"1"},{"operator":"owner","xuid":"1"}]`,
		"unknown field":    `[{"operator":"owner","xuid":"1","admin":true}]`,
		"trailing":         `[] []`,
	} {
		if _, err := ParseXboxAccounts(raw, ops); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got, err := ParseXboxAccounts("", ops); err != nil || got != nil {
		t.Errorf("empty = %v %v", got, err)
	}
	_, err := New(Config{Origin: origin, DataDir: t.TempDir(), Operators: opJSON, InvitesDir: t.TempDir(), Xbox: XboxConfig{Enabled: true}})
	if err == nil {
		t.Error("xbox sign-in with an empty allow-list started")
	}
}

func TestXbox_EnvConfig(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretFile, []byte("a-made-up-client-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "00000000-1111-2222-3333-444444444444"
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if cfg, err := XboxConfigFromEnv(env(nil)); err != nil || cfg.Enabled {
		t.Errorf("unset = %+v %v", cfg, err)
	}
	cfg, err := XboxConfigFromEnv(env(map[string]string{
		"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": id, "XBOX_CLIENT_SECRET_FILE": secretFile, "CREW_XBOX_ACCOUNTS": "[]",
	}))
	if err != nil || !cfg.Enabled || cfg.ClientSecret != "a-made-up-client-secret-value" || cfg.ClientID != id {
		t.Errorf("full = %+v %v", cfg, err)
	}
	for name, m := range map[string]map[string]string{
		"bad flag":    {"XBOX_LOGIN_ENABLED": "maybe"},
		"bad id":      {"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": "nope", "XBOX_CLIENT_SECRET": "a-made-up-client-secret-value"},
		"no secret":   {"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": id},
		"short":       {"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": id, "XBOX_CLIENT_SECRET": "short"},
		"both":        {"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": id, "XBOX_CLIENT_SECRET": "a-made-up-client-secret-value", "XBOX_CLIENT_SECRET_FILE": secretFile},
		"missingfile": {"XBOX_LOGIN_ENABLED": "true", "XBOX_CLIENT_ID": id, "XBOX_CLIENT_SECRET_FILE": filepath.Join(dir, "none")},
	} {
		_, err := XboxConfigFromEnv(env(m))
		if err == nil {
			t.Errorf("%s was accepted", name)
		} else if strings.Contains(err.Error(), "a-made-up-client-secret-value") {
			t.Errorf("%s: the error holds the secret: %v", name, err)
		}
	}
}
