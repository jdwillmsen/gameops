package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/xbox/xboxtest"
)

const xboxRedirect = "https://map.example/auth/xbox/callback"

// Made up, like every identity in these tests.
var (
	alexXbox    = xboxtest.Account{XUID: "2535400000000042", Gamertag: "Alex Builds"}
	strangerBox = xboxtest.Account{XUID: "2535400000000099", Gamertag: "Passing Through"}
)

type rig struct {
	t     *testing.T
	s     *Server
	fake  *xboxtest.Fake
	h     http.Handler
	log   *bytes.Buffer
	known *auth.Known
}

type fakeAllowlist struct {
	xuids    map[string]bool
	answered bool
	asked    int
}

func (a *fakeAllowlist) Has(_ context.Context, xuid string) (bool, bool) {
	a.asked++
	return a.xuids[xuid], a.answered
}

func withXbox(t *testing.T) *rig {
	t.Helper()
	s := withLogin(t)
	fake := xboxtest.New(t, xboxRedirect)
	known, err := auth.LoadKnown(filepath.Join(t.TempDir(), "known.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := auth.NewXboxFlow(s.Sessions.Key, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s.Sessions.XboxOn, s.Sessions.XboxTTL = true, 24*time.Hour
	s.XboxClient, s.XboxFlow, s.Known = fake.Client(), flow, known
	var log bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&log, nil))
	return &rig{t: t, s: s, fake: fake, h: s.Handler(), log: &log, known: known}
}

func visit(h http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	u, _ := url.Parse(target)
	return do(h, "GET", u.RequestURI(), "", cookies)
}

// start is the browser clicking Sign in with Xbox.
func (r *rig) start() (state *http.Cookie, authorize string) {
	r.t.Helper()
	rec := visit(r.h, "/auth/xbox/start")
	if rec.Code != http.StatusFound {
		r.t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	return cookie(rec, auth.XboxStateCookie), rec.Header().Get("Location")
}

// signIn is a whole sign-in as a person at the fake; the result is the
// callback's answer and the cookie the browser held.
func (r *rig) signIn(a xboxtest.Account) *httptest.ResponseRecorder {
	r.t.Helper()
	r.fake.SignIn(a)
	state, authorize := r.start()
	return visit(r.h, r.fake.Approve(authorize, false), state)
}

func (r *rig) logged() string { return r.log.String() }

func result(rec *httptest.ResponseRecorder) string {
	u, _ := url.Parse(rec.Header().Get("Location"))
	return u.Query().Get("xbox")
}

func count(result string) float64 {
	return testutil.ToFloat64(metricXboxLogins.WithLabelValues(result))
}

// refused checks a sign-in ended as a refusal and gave nobody a session.
func refused(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || result(rec) != want || !strings.HasPrefix(rec.Header().Get("Location"), "/?xbox=") {
		t.Errorf("answer = %d %s, want a redirect with xbox=%s", rec.Code, rec.Header().Get("Location"), want)
	}
	if c := cookie(rec, "__Host-mcmap_session"); c != nil {
		t.Errorf("a refusal set a session: %+v", c)
	}
}

func TestXbox_AKnownPlayerSignsInAndGetsADaySession(t *testing.T) {
	r := withXbox(t)
	// Known the way a player becomes known: by a claim the agent reports.
	start := do(r.s.Handler(), "POST", "/auth/start", "", nil)
	if start.Code != 200 {
		t.Fatal(start.Body)
	}
	var code struct{ Code string }
	_ = jsonInto(start.Body.Bytes(), &code)
	claim := do(r.s.InternalHandler(), "POST", "/internal/v1/claims", `{"code":"`+code.Code+`","xuid":"`+alexXbox.XUID+`","gamertag":"Alex Builds"}`, nil, "Authorization", "Bearer "+internalToken)
	if claim.Code != http.StatusNoContent || !r.known.Has(alexXbox.XUID) {
		t.Fatalf("claim = %d, known = %v", claim.Code, r.known.Has(alexXbox.XUID))
	}

	before := count(xboxOK)
	rec := r.signIn(alexXbox)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	sess := cookie(rec, "__Host-mcmap_session")
	if sess == nil || sess.MaxAge != 86400 || !sess.HttpOnly || !sess.Secure {
		t.Fatalf("session cookie = %+v", sess)
	}
	me := do(r.h, "GET", "/api/me", "", []*http.Cookie{sess})
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"gamertag":"Alex Builds"`) {
		t.Errorf("/api/me = %d %s", me.Code, me.Body)
	}
	if count(xboxOK) != before+1 {
		t.Error("the sign-in was not counted")
	}
	if !strings.Contains(r.logged(), `"msg":"map login issued"`) || !strings.Contains(r.logged(), `"method":"xbox"`) || !strings.Contains(r.logged(), alexXbox.XUID) {
		t.Errorf("no line saying who was let in: %s", r.logged())
	}
	if c := cookie(rec, auth.XboxStateCookie); c == nil || c.MaxAge >= 0 {
		t.Error("the state cookie was left in the browser")
	}
}

func TestXbox_AStrangerIsRefusedWithNoSession(t *testing.T) {
	r := withXbox(t)
	before := count(xboxStranger)
	rec := r.signIn(strangerBox)
	refused(t, rec, "stranger")
	if count(xboxStranger) != before+1 {
		t.Error("the refusal was not counted")
	}
	if !strings.Contains(r.logged(), strangerBox.XUID) || strings.Contains(r.logged(), strangerBox.Gamertag) {
		t.Errorf("the log should name the XUID and not the gamertag: %s", r.logged())
	}
}

func TestXbox_TheAllowlistLetsAPlayerInOnlyWhenTurnedOnAndAnswering(t *testing.T) {
	r := withXbox(t)
	list := &fakeAllowlist{xuids: map[string]bool{alexXbox.XUID: true}, answered: true}

	refused(t, r.signIn(alexXbox), "stranger") // no allowlist configured: not consulted

	r.s.Allowlist = list
	rec := r.signIn(alexXbox)
	if rec.Header().Get("Location") != "/" || cookie(rec, "__Host-mcmap_session") == nil {
		t.Errorf("an allowlisted player: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	refused(t, r.signIn(strangerBox), "stranger")

	list.answered, list.xuids = false, nil // the bridge is down
	refused(t, r.signIn(alexXbox), "allowlist_unavailable")
}

func TestXbox_KnownIsCheckedBeforeTheBridgeIsAsked(t *testing.T) {
	r := withXbox(t)
	list := &fakeAllowlist{}
	r.s.Allowlist = list
	_ = r.known.Record(alexXbox.XUID)
	if rec := r.signIn(alexXbox); rec.Header().Get("Location") != "/" {
		t.Fatal("a known player was refused")
	}
	if list.asked != 0 {
		t.Error("the bridge was asked about a player already known")
	}
}

func TestXbox_AClaimThatCannotBeRecordedStillLogsThePlayerIn(t *testing.T) {
	r := withXbox(t)
	r.s.Known = failingKnown{}
	start := do(r.h, "POST", "/auth/start", "", nil)
	var code struct{ Code string }
	_ = jsonInto(start.Body.Bytes(), &code)
	claim := do(r.s.InternalHandler(), "POST", "/internal/v1/claims", `{"code":"`+code.Code+`","xuid":"`+alexXbox.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	if claim.Code != http.StatusNoContent {
		t.Errorf("claim = %d", claim.Code)
	}
	if !strings.Contains(r.logged(), "map known player not recorded") {
		t.Errorf("the failure was not written down: %s", r.logged())
	}
}

type failingKnown struct{}

func (failingKnown) Has(string) bool     { return false }
func (failingKnown) Record(string) error { return errors.New("disk full") }
func (failingKnown) Forget(string) error { return errors.New("disk full") }

func TestXbox_AFailedClaimRecordsNobody(t *testing.T) {
	r := withXbox(t)
	do(r.s.InternalHandler(), "POST", "/internal/v1/claims", `{"code":"NOSUCH","xuid":"`+alexXbox.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	do(r.s.InternalHandler(), "POST", "/internal/v1/claims", `{"code":"ABCDEF","xuid":"console"}`, nil, "Authorization", "Bearer "+internalToken)
	if r.known.Has(alexXbox.XUID) || r.known.Has("console") {
		t.Error("a claim that failed made someone known")
	}
}

func TestXbox_TheStartSendsTheBrowserToMicrosoftWithAStateItHolds(t *testing.T) {
	r := withXbox(t)
	state, authorize := r.start()
	if state == nil || state.SameSite != http.SameSiteLaxMode || !state.HttpOnly || !state.Secure || state.Name != "__Host-mcmap_xbox" {
		t.Fatalf("state cookie = %+v", state)
	}
	u, _ := url.Parse(authorize)
	if u.Query().Get("state") == "" || strings.Contains(state.Value, u.Query().Get("code_challenge")) {
		t.Errorf("authorize = %s", authorize)
	}
	// Two starts never share a state.
	_, other := r.start()
	if ou, _ := url.Parse(other); ou.Query().Get("state") == u.Query().Get("state") {
		t.Error("two sign-ins shared a state")
	}
}

func TestXbox_ACallbackWithoutTheCookieIsRefused(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	r.fake.SignIn(alexXbox)
	_, authorize := r.start()
	// Someone else's browser being fed a callback: no cookie to match.
	refused(t, visit(r.h, r.fake.Approve(authorize, false)), "expired")
	if r.fake.Calls["token"] != 0 {
		t.Error("Microsoft was asked to redeem a code with no cookie to match it")
	}
}

func TestXbox_AStateThatIsNotTheCookiesIsRefused(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	r.fake.SignIn(alexXbox)
	cookieA, _ := r.start()
	_, authorizeB := r.start()
	refused(t, visit(r.h, r.fake.Approve(authorizeB, false), cookieA), "state_mismatch")
	if r.fake.Calls["token"] != 0 {
		t.Error("a code was redeemed under a state that did not match")
	}
}

func TestXbox_AnExpiredCookieIsRefused(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	now := time.Now()
	flow, _ := auth.NewXboxFlow(r.s.Sessions.Key, func() time.Time { return now })
	r.s.XboxFlow = flow
	r.h = r.s.Handler()
	r.fake.SignIn(alexXbox)
	state, authorize := r.start()
	now = now.Add(auth.XboxStateTTL + time.Second)
	refused(t, visit(r.h, r.fake.Approve(authorize, false), state), "expired")
}

func TestXbox_ACallbackCannotBePlayedTwice(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	r.fake.SignIn(alexXbox)
	state, authorize := r.start()
	back := r.fake.Approve(authorize, false)
	if rec := visit(r.h, back, state); rec.Header().Get("Location") != "/" {
		t.Fatalf("first = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// The same request again, cookie and all, as an attacker who kept both.
	refused(t, visit(r.h, back, state), "replayed")
	if r.fake.Calls["token"] != 1 {
		t.Errorf("the code was redeemed %d times", r.fake.Calls["token"])
	}
}

func TestXbox_ACodeFromAnotherSignInFailsPKCE(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	r.fake.SignIn(alexXbox)
	cookieA, authorizeA := r.start()
	_, authorizeB := r.start()
	// Microsoft's answer to B carries B's code, challenged with B's verifier;
	// A's browser, holding A's verifier, presents it under A's state.
	stateA := mustQuery(authorizeA, "state")
	codeB := mustQuery(r.fake.Approve(authorizeB, false), "code")
	forged := "/auth/xbox/callback?" + url.Values{"state": {stateA}, "code": {codeB}}.Encode()
	refused(t, visit(r.h, forged, cookieA), "exchange_failed")
	if r.fake.Calls["user"] != 0 {
		t.Error("went on to Xbox after Microsoft refused the verifier")
	}
}

func TestXbox_CancellingAndMicrosoftErrors(t *testing.T) {
	r := withXbox(t)
	state, authorize := r.start()
	refused(t, visit(r.h, r.fake.Approve(authorize, true), state), "cancelled")

	state, authorize = r.start()
	stateValue := mustQuery(authorize, "state")
	hostile := "/auth/xbox/callback?" + url.Values{"state": {stateValue}, "error": {"<script>alert(1)</script>"}, "error_description": {"x"}}.Encode()
	rec := visit(r.h, hostile, state)
	refused(t, rec, "microsoft_error")
	if strings.Contains(rec.Header().Get("Location"), "script") || strings.Contains(r.logged(), "script") {
		t.Error("Microsoft's error text was passed on")
	}
}

func TestXbox_ACallbackWithNoCodeIsABadRequest(t *testing.T) {
	r := withXbox(t)
	state, authorize := r.start()
	bare := "/auth/xbox/callback?" + url.Values{"state": {mustQuery(authorize, "state")}}.Encode()
	refused(t, visit(r.h, bare, state), "bad_request")
	state, authorize = r.start()
	long := "/auth/xbox/callback?" + url.Values{"state": {mustQuery(authorize, "state")}, "code": {strings.Repeat("a", 5000)}}.Encode()
	refused(t, visit(r.h, long, state), "bad_request")
	if r.fake.Calls["token"] != 0 {
		t.Error("a missing or absurd code reached Microsoft")
	}
}

func TestXbox_FailuresOfTheChainAreEachTheirOwnResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*rig)
		want string
	}{
		{"token endpoint refuses the app", func(r *rig) {
			r.fake.TokenErr = &xboxtest.Reply{Status: 400, Body: `{"error":"invalid_client","error_codes":[7000215]}`}
		}, "exchange_failed"},
		{"token endpoint is down", func(r *rig) { r.fake.TokenErr = &xboxtest.Reply{Status: 503} }, "xbox_unavailable"},
		{"no Xbox profile", func(r *rig) { r.fake.SignIn(xboxtest.Account{XUID: alexXbox.XUID, XErr: 2148916233}) }, "no_xbox_profile"},
		{"a child account", func(r *rig) { r.fake.SignIn(xboxtest.Account{XUID: alexXbox.XUID, XErr: 2148916238}) }, "child_account"},
		{"another XErr", func(r *rig) { r.fake.SignIn(xboxtest.Account{XUID: alexXbox.XUID, XErr: 2148916227}) }, "xbox_refused"},
		{"XSTS with no xid", func(r *rig) {
			r.fake.XSTSErr = &xboxtest.Reply{Status: 200, Body: `{"Token":"t","DisplayClaims":{"xui":[{"gtg":"x"}]}}`}
		}, "bad_identity"},
		{"XSTS with a xid that is not a number", func(r *rig) {
			r.fake.XSTSErr = &xboxtest.Reply{Status: 200, Body: `{"Token":"t","DisplayClaims":{"xui":[{"xid":"abc","gtg":"x"}]}}`}
		}, "bad_identity"},
	} {
		r := withXbox(t)
		_ = r.known.Record(alexXbox.XUID)
		r.fake.SignIn(alexXbox)
		tc.set(r)
		state, authorize := r.start()
		before := count(tc.want)
		refused(t, visit(r.h, r.fake.Approve(authorize, false), state), tc.want)
		if count(tc.want) != before+1 {
			t.Errorf("%s: not counted as %s", tc.name, tc.want)
		}
	}
}

func TestXbox_AGamertagThatClaimsTheServicePrefixIsRefused(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	refused(t, r.signIn(xboxtest.Account{XUID: alexXbox.XUID, Gamertag: "service:e2e"}), "bad_identity")
}

func TestXbox_SwitchedOffThereAreNoRoutesAndNoButton(t *testing.T) {
	for name, off := range map[string]func(*rig){
		"switch":  func(r *rig) { r.s.Sessions.XboxOn = false },
		"client":  func(r *rig) { r.s.XboxClient = nil },
		"flow":    func(r *rig) { r.s.XboxFlow = nil },
		"known":   func(r *rig) { r.s.Known = nil },
		"session": func(r *rig) { r.s.Sessions = nil },
	} {
		r := withXbox(t)
		off(r)
		h := r.s.Handler()
		for _, path := range []string{"/auth/xbox/start", "/auth/xbox/callback?state=a&code=b"} {
			if rec := visit(h, path); rec.Code != http.StatusNotFound {
				t.Errorf("%s: %s = %d, want 404", name, path, rec.Code)
			}
		}
		if rec := visit(h, "/api/config"); strings.Contains(rec.Body.String(), "xbox") {
			t.Errorf("%s: /api/config = %s", name, rec.Body)
		}
	}
}

func TestXbox_ConfigSaysSoOnlyWhenOn(t *testing.T) {
	r := withXbox(t)
	if rec := visit(r.h, "/api/config"); !strings.Contains(rec.Body.String(), `"xbox":true`) {
		t.Errorf("config = %s", rec.Body)
	}
}

func TestXbox_ARevokedPlayersXboxSessionIsRejected(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	sess := cookie(r.signIn(alexXbox), "__Host-mcmap_session")
	if me := do(r.h, "GET", "/api/me", "", []*http.Cookie{sess}); me.Code != 200 {
		t.Fatalf("before: %d", me.Code)
	}
	time.Sleep(2 * time.Millisecond)
	rev := do(r.s.InternalHandler(), "POST", "/internal/v1/revocations", `{"xuid":"`+alexXbox.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rev.Code)
	}
	if me := do(r.h, "GET", "/api/me", "", []*http.Cookie{sess}); me.Code != http.StatusUnauthorized {
		t.Errorf("after !map logout the Xbox session = %d, want 401", me.Code)
	}
	// A revocation makes the player unknown; a fresh claim lets them sign in.
	refused(t, r.signIn(alexXbox), "stranger")
	_ = r.known.Record(alexXbox.XUID)
	time.Sleep(2 * time.Millisecond)
	if again := cookie(r.signIn(alexXbox), "__Host-mcmap_session"); again == nil {
		t.Error("could not sign in after a revocation")
	}
}

func TestXbox_SwitchingItOffEndsItsSessions(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	sess := cookie(r.signIn(alexXbox), "__Host-mcmap_session")
	r.s.Sessions.XboxOn = false
	if me := do(r.h, "GET", "/api/me", "", []*http.Cookie{sess}); me.Code != http.StatusUnauthorized {
		t.Errorf("with the switch off the session = %d", me.Code)
	}
}

func TestXbox_NothingSecretReachesTheLog(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	state, authorize := r.start()
	r.fake.SignIn(alexXbox)
	back := r.fake.Approve(authorize, false)
	ok := visit(r.h, back, state)
	if ok.Header().Get("Location") != "/" {
		t.Fatal("sign-in failed")
	}
	// Failures too: the wrong secret makes Microsoft write the code into
	// its error text, which must not travel.
	r.fake.TokenErr = &xboxtest.Reply{Status: 400, Body: `{"error":"invalid_client","error_description":"SECRET-DESCRIPTION","error_codes":[7000215]}`}
	state2, authorize2 := r.start()
	back2 := r.fake.Approve(authorize2, false)
	visit(r.h, back2, state2)
	r.fake.TokenErr = nil
	r.fake.SignIn(strangerBox)
	state3, authorize3 := r.start()
	visit(r.h, r.fake.Approve(authorize3, false), state3)

	logs := r.logged()
	secrets := append([]string{}, r.fake.Secrets...)
	for _, c := range []*http.Cookie{state, state2, state3, cookie(ok, "__Host-mcmap_session")} {
		secrets = append(secrets, c.Value)
	}
	for _, u := range []string{authorize, back, authorize2, back2} {
		parsed, _ := url.Parse(u)
		for _, k := range []string{"state", "code", "code_challenge"} {
			if v := parsed.Query().Get(k); v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	secrets = append(secrets, "SECRET-DESCRIPTION", alexXbox.Gamertag, strangerBox.Gamertag, xboxtest.ClientSecret)
	for _, secret := range secrets {
		if strings.Contains(logs, secret) {
			t.Errorf("the log carries %q:\n%s", secret, logs)
		}
	}
	if logs == "" {
		t.Error("nothing was logged at all")
	}
}

func TestXbox_TheCSPIsByteForByteWhatItWasOnTheNewRoutes(t *testing.T) {
	const want = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
	r := withXbox(t)
	state, authorize := r.start()
	for _, path := range []string{"/auth/xbox/start", "/auth/xbox/callback", "/auth/xbox/callback?state=x&code=y", "/api/config", "/"} {
		if got := visit(r.h, path, state).Header().Get("Content-Security-Policy"); got != want {
			t.Errorf("%s: CSP = %q", path, got)
		}
	}
	_ = authorize
}

func TestXbox_EveryResultIsAtZeroFromTheStart(t *testing.T) {
	r := withXbox(t)
	_ = r.s.Handler()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "mcmap_xbox_logins_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			if len(m.GetLabel()) != 1 || m.GetLabel()[0].GetName() != "result" {
				t.Fatalf("labels = %v", m.GetLabel())
			}
			got[m.GetLabel()[0].GetValue()] = true
		}
	}
	for _, want := range xboxResults {
		if !got[want] {
			t.Errorf("result %q is not declared", want)
		}
	}
	if len(got) != len(xboxResults) {
		t.Errorf("series = %v", got)
	}
	_ = r
}

func mustQuery(target, key string) string {
	u, _ := url.Parse(target)
	return u.Query().Get(key)
}

func jsonInto(raw []byte, into any) error { return json.Unmarshal(raw, into) }

func TestXbox_LogoutEverywhereAlsoMakesThePlayerUnknown(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	rev := do(r.s.InternalHandler(), "POST", "/internal/v1/revocations", `{"xuid":"`+alexXbox.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	if rev.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", rev.Code)
	}
	if r.known.Has(alexXbox.XUID) {
		t.Error("still known after !map logout")
	}
	time.Sleep(2 * time.Millisecond)
	refused(t, r.signIn(alexXbox), "stranger")
	// A fresh code login makes them known again.
	_ = r.known.Record(alexXbox.XUID)
	if rec := r.signIn(alexXbox); rec.Header().Get("Location") != "/" {
		t.Error("could not sign in after a fresh claim")
	}
}

func TestXbox_ALogoutThatCannotForgetFails(t *testing.T) {
	r := withXbox(t)
	r.s.Known = failingKnown{}
	rev := do(r.s.InternalHandler(), "POST", "/internal/v1/revocations", `{"xuid":"`+alexXbox.XUID+`"}`, nil, "Authorization", "Bearer "+internalToken)
	if rev.Code != http.StatusInternalServerError {
		t.Errorf("revoke = %d, want 500 so the agent tries again", rev.Code)
	}
}

func TestXbox_ForgetRouteRemovesAPlayerAtRuntimeForTheAgentOnly(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	h := r.s.InternalHandler()
	body := `{"xuid":"` + alexXbox.XUID + `"}`
	if rec := do(h, "POST", "/internal/v1/known/forget", body, nil); rec.Code != http.StatusUnauthorized || !r.known.Has(alexXbox.XUID) {
		t.Errorf("no token = %d", rec.Code)
	}
	for _, bad := range []string{`{}`, `{"xuid":"console"}`, `{"xuid":"1","extra":1}`} {
		if rec := do(h, "POST", "/internal/v1/known/forget", bad, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, rec.Code)
		}
	}
	if rec := do(h, "POST", "/internal/v1/known/forget", body, nil, "Authorization", "Bearer "+internalToken); rec.Code != http.StatusNoContent {
		t.Fatalf("forget = %d", rec.Code)
	}
	if r.known.Has(alexXbox.XUID) {
		t.Error("still known")
	}
	if rec := do(r.s.Handler(), "POST", "/internal/v1/known/forget", body, nil, "Authorization", "Bearer "+internalToken); rec.Code == http.StatusNoContent {
		t.Error("the public listener serves it")
	}
}

func TestXbox_AnAllowlistThatDoesNotAnswerIsNotAStranger(t *testing.T) {
	r := withXbox(t)
	r.s.Allowlist = &fakeAllowlist{answered: false}
	before, strangers := count("allowlist_unavailable"), count(xboxStranger)
	refused(t, r.signIn(strangerBox), "allowlist_unavailable")
	if count("allowlist_unavailable") != before+1 || count(xboxStranger) != strangers {
		t.Error("counted as the wrong thing")
	}
	if strings.Contains(r.logged(), strangerBox.XUID) {
		t.Errorf("the log names the XUID: %s", r.logged())
	}
	if !strings.Contains(r.logged(), "allowlist") {
		t.Errorf("not written down: %s", r.logged())
	}
	// A bridge that answers "no" is a stranger.
	r.s.Allowlist = &fakeAllowlist{answered: true}
	refused(t, r.signIn(strangerBox), "stranger")
}

func TestXbox_ACallbackFloodIsStoppedBeforeMicrosoftIsAsked(t *testing.T) {
	r := withXbox(t)
	_ = r.known.Record(alexXbox.XUID)
	now := time.Now()
	r.s.clock = func() time.Time { return now }
	r.fake.SignIn(alexXbox)
	var busy int
	for range 40 {
		state, authorize := r.start()
		rec := visit(r.h, r.fake.Approve(authorize, false), state)
		if result(rec) == "busy" {
			busy++
			if cookie(rec, "__Host-mcmap_session") != nil {
				t.Fatal("a busy answer set a session")
			}
		}
	}
	asked := r.fake.Calls["token"]
	if busy == 0 || asked > xboxBurst || asked+busy != 40 {
		t.Errorf("busy %d, asked Microsoft %d times of 40", busy, asked)
	}
	now = now.Add(xboxRefill)
	state, authorize := r.start()
	if rec := visit(r.h, r.fake.Approve(authorize, false), state); rec.Header().Get("Location") != "/" {
		t.Errorf("after the refill = %s", rec.Header().Get("Location"))
	}
}
