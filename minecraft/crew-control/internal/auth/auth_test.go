package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	origin = "https://crew.example"
	rpID   = "crew.example"
	opJSON = `[{"id":"owner","name":"Owner"},{"id":"josh","name":"Josh"}]`
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	t       *testing.T
	clk     *clock
	data    string
	invites string
	a       *Auth
	mux     *http.ServeMux
}

func (e *env) build(operators string) {
	e.t.Helper()
	a, err := New(Config{Origin: origin, DataDir: e.data, Operators: operators, InvitesDir: e.invites, Now: e.clk.now, Log: logging.New("error")})
	if err != nil {
		e.t.Fatal(err)
	}
	e.a = a
	e.mux = http.NewServeMux()
	a.Mount(e.mux)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, clk: &clock{time.Unix(1_790_000_000, 0)}, data: t.TempDir(), invites: t.TempDir()}
	e.invite("owner", "owner-code-0001")
	e.invite("josh", "josh-code-0002")
	e.build(opJSON)
	return e
}

func (e *env) invite(op, code string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.invites, op), []byte(code+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// client is a browser's cookie jar for one test.
type client struct {
	e       *env
	cookies map[string]*http.Cookie
}

func (e *env) client() *client { return &client{e: e, cookies: map[string]*http.Cookie{}} }

func (c *client) do(method, path string, body []byte, hdr bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if hdr {
		req.Header.Set("X-Crew-Request", "1")
	}
	for _, ck := range c.cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	rec := httptest.NewRecorder()
	c.e.mux.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}
	return rec
}

func (c *client) post(path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	return c.do(http.MethodPost, path, raw, true)
}

func challengeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil || o.PublicKey.Challenge == "" {
		t.Fatalf("no challenge (status %d): %s", rec.Code, rec.Body)
	}
	return o.PublicKey.Challenge
}

func (c *client) finish(path string, body []byte) *httptest.ResponseRecorder {
	return c.do(http.MethodPost, path, body, true)
}

// enrol runs a whole registration and returns the response of the finish.
func (c *client) enrol(k *softKey, op, invite string) *httptest.ResponseRecorder {
	c.e.t.Helper()
	rec := c.post("/auth/register/options", map[string]string{"operator": op, "invite": invite})
	if rec.Code != http.StatusOK {
		return rec
	}
	return c.finish("/auth/register/finish", k.create(answer{rpID: rpID, origin: origin, challenge: challengeOf(c.e.t, rec)}))
}

func (c *client) login(k *softKey, handle []byte) *httptest.ResponseRecorder {
	c.e.t.Helper()
	rec := c.post("/auth/login/options", map[string]string{})
	if rec.Code != http.StatusOK {
		c.e.t.Fatalf("login options: %d %s", rec.Code, rec.Body)
	}
	return c.finish("/auth/login/finish", k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(c.e.t, rec)}, handle, 0))
}

func (c *client) handle() []byte {
	c.e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(c.e.data, "passkeys.json"))
	if err != nil {
		c.e.t.Fatal(err)
	}
	var list []struct {
		UserHandle []byte `json:"userHandle"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
		c.e.t.Fatalf("passkeys.json: %v %s", err, raw)
	}
	return list[0].UserHandle
}

func (c *client) me() *httptest.ResponseRecorder { return c.do(http.MethodGet, "/auth/me", nil, false) }

func mustOK(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", what, rec.Code, rec.Body)
	}
}

func TestRegisterWithInviteSignsIn(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	rec := c.enrol(newSoftKey(t), "owner", "owner-code-0001")
	mustOK(t, rec, "register")

	var sc *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "__Host-crew_session" {
			sc = ck
		}
	}
	if sc == nil || !sc.HttpOnly || !sc.Secure || sc.SameSite != http.SameSiteStrictMode || sc.Path != "/" || sc.MaxAge != 12*3600 || sc.Domain != "" {
		t.Fatalf("session cookie: %+v", sc)
	}
	me := c.me()
	mustOK(t, me, "me")
	if strings.TrimSpace(me.Body.String()) != `{"operator":{"id":"owner","name":"Owner"}}` {
		t.Fatalf("me: %s", me.Body)
	}
	used, _ := os.ReadFile(filepath.Join(e.data, "invites-used.json"))
	sum := sha256.Sum256([]byte("owner-code-0001"))
	if !bytes.Contains(used, []byte(hex.EncodeToString(sum[:]))) || bytes.Contains(used, []byte("owner-code-0001")) {
		t.Fatalf("invites-used.json must hold the hash and never the code: %s", used)
	}
}

func TestInviteIsSingleUse(t *testing.T) {
	e := newEnv(t)
	mustOK(t, e.client().enrol(newSoftKey(t), "owner", "owner-code-0001"), "first")
	rec := e.client().enrol(newSoftKey(t), "owner", "owner-code-0001")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reused invite: %d %s", rec.Code, rec.Body)
	}
}

func TestInviteRaceSpendsOnce(t *testing.T) {
	e := newEnv(t)
	a, b := e.client(), e.client()
	ka, kb := newSoftKey(t), newSoftKey(t)
	oa := a.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	ob := b.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	mustOK(t, oa, "a options")
	mustOK(t, ob, "b options")
	ra := a.finish("/auth/register/finish", ka.create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, oa)}))
	rb := b.finish("/auth/register/finish", kb.create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, ob)}))
	if (ra.Code == http.StatusOK) == (rb.Code == http.StatusOK) {
		t.Fatalf("exactly one finish may win, got %d and %d", ra.Code, rb.Code)
	}
}

func TestInviteForAnotherOperatorRefused(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ op, code string }{
		{"josh", "owner-code-0001"},
		{"owner", "josh-code-0002"},
		{"nobody", "owner-code-0001"},
		{"owner", ""},
		{"owner", "owner-code-0002"},
		{"../owner", "owner-code-0001"},
	} {
		rec := e.client().post("/auth/register/options", map[string]string{"operator": tc.op, "invite": tc.code})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%+v: status %d", tc, rec.Code)
		}
	}
	// An unlisted operator with a file in the directory still cannot enrol.
	e.invite("ghost", "ghost-code-0003")
	if rec := e.client().post("/auth/register/options", map[string]string{"operator": "ghost", "invite": "ghost-code-0003"}); rec.Code != http.StatusForbidden {
		t.Errorf("unlisted operator: %d", rec.Code)
	}
}

func TestMissingInvitesDirMeansNoEnrolment(t *testing.T) {
	e := &env{t: t, clk: &clock{time.Unix(1_790_000_000, 0)}, data: t.TempDir(), invites: filepath.Join(t.TempDir(), "absent")}
	e.build(opJSON)
	if rec := e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": "anything"}); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	e.invites = t.TempDir()
	e.invite("owner", "  \n")
	e.build(opJSON)
	if rec := e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": ""}); rec.Code != http.StatusForbidden {
		t.Fatalf("empty invite file must not match an empty code: %d", rec.Code)
	}
}

func TestInviteRotatedBetweenOptionsAndFinishRefused(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	rec := c.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	mustOK(t, rec, "options")
	e.invite("owner", "owner-code-9999")
	fin := c.finish("/auth/register/finish", newSoftKey(t).create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, rec)}))
	if fin.Code == http.StatusOK {
		t.Fatal("a rotated invite must not complete an older ceremony")
	}
}

func TestSixthPasskeyRefused(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxPasskeys; i++ {
		code := fmt.Sprintf("owner-code-%04d", 100+i)
		e.invite("owner", code)
		mustOK(t, e.client().enrol(newSoftKey(t), "owner", code), "enrol")
	}
	e.invite("owner", "owner-code-last")
	rec := e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-last"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("sixth passkey: %d %s", rec.Code, rec.Body)
	}
}

func TestLoginRoundTripAndCounter(t *testing.T) {
	e := newEnv(t)
	k := newSoftKey(t)
	mustOK(t, e.client().enrol(k, "owner", "owner-code-0001"), "enrol")

	c := e.client()
	h := c.handle()
	mustOK(t, c.login(k, h), "login")
	mustOK(t, c.me(), "me after login")

	// An assertion whose counter did not move forward is a cloned key.
	c2 := e.client()
	opts := c2.post("/auth/login/options", map[string]string{})
	rec := c2.finish("/auth/login/finish", k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 1))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale counter: %d", rec.Code)
	}
	if c2.me().Code != http.StatusUnauthorized {
		t.Fatal("refused login must not sign in")
	}
}

func TestLoginRefusals(t *testing.T) {
	e := newEnv(t)
	k := newSoftKey(t)
	mustOK(t, e.client().enrol(k, "owner", "owner-code-0001"), "enrol")
	h := e.client().handle()

	t.Run("ceremony is single use", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		cer := *c.cookies["__Host-crew_ceremony"]
		body := k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 0)
		mustOK(t, c.finish("/auth/login/finish", body), "first")
		c.cookies["__Host-crew_ceremony"] = &cer
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("replay: %d", rec.Code)
		}
	})
	t.Run("a bad answer spends the ceremony", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		cer := *c.cookies["__Host-crew_ceremony"]
		good := k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 0)
		if rec := c.finish("/auth/login/finish", []byte(`{}`)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("junk: %d", rec.Code)
		}
		c.cookies["__Host-crew_ceremony"] = &cer
		if rec := c.finish("/auth/login/finish", good); rec.Code != http.StatusUnauthorized {
			t.Fatalf("the good answer worked after a bad one: %d", rec.Code)
		}
	})
	t.Run("no ceremony cookie", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 0)
		delete(c.cookies, "__Host-crew_ceremony")
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("expired ceremony", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 0)
		e.clk.advance(ceremonyTTL + time.Second)
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("wrong origin", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := k.get(answer{rpID: rpID, origin: "https://evil.example", challenge: challengeOf(t, opts)}, h, 0)
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("another operator's handle", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := k.get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, bytes.Repeat([]byte{7}, 32), 0)
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := newSoftKey(t).get(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)}, h, 0)
		if rec := c.finish("/auth/login/finish", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("login ceremony cannot finish a registration", func(t *testing.T) {
		c := e.client()
		opts := c.post("/auth/login/options", map[string]string{})
		body := newSoftKey(t).create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, opts)})
		if rec := c.finish("/auth/register/finish", body); rec.Code == http.StatusOK {
			t.Fatal("registration finished on a login ceremony")
		}
	})
}

func TestRegistrationWrongOriginRefused(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	opts := c.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	rec := c.finish("/auth/register/finish", newSoftKey(t).create(answer{rpID: rpID, origin: "https://evil.example", challenge: challengeOf(t, opts)}))
	if rec.Code == http.StatusOK {
		t.Fatal("wrong origin accepted")
	}
	// The failed attempt must not have burned the invite.
	mustOK(t, e.client().enrol(newSoftKey(t), "owner", "owner-code-0001"), "retry")
}

func sessionOf(t *testing.T, c *client) string {
	t.Helper()
	ck := c.cookies["__Host-crew_session"]
	if ck == nil {
		t.Fatal("no session cookie")
	}
	return ck.Value
}

func TestSessionTamperingAndExpiry(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	val := sessionOf(t, c)
	body, sig, _ := strings.Cut(val, ".")

	forged, _ := json.Marshal(map[string]any{"op": "josh", "key": "x", "iat": e.clk.now().Unix(), "exp": e.clk.now().Add(time.Hour).Unix()})
	for name, v := range map[string]string{
		"flipped signature": body + "." + sig[:len(sig)-2] + "AA",
		"swapped body":      b64.EncodeToString(forged) + "." + sig,
		"no signature":      body,
		"empty":             "",
		"garbage":           "a.b.c",
	} {
		cc := e.client()
		cc.cookies["__Host-crew_session"] = &http.Cookie{Name: "__Host-crew_session", Value: v}
		if cc.me().Code != http.StatusUnauthorized {
			t.Errorf("%s accepted", name)
		}
	}

	e.clk.advance(12*time.Hour - time.Second)
	mustOK(t, c.me(), "just inside 12h")
	e.clk.advance(2 * time.Second)
	if c.me().Code != http.StatusUnauthorized {
		t.Fatal("expired session accepted")
	}
}

func TestSessionSignedWithAnotherKeyRefused(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	val := sessionOf(t, c)

	other := newEnv(t)
	cc := other.client()
	cc.cookies["__Host-crew_session"] = &http.Cookie{Name: "__Host-crew_session", Value: val}
	if cc.me().Code != http.StatusUnauthorized {
		t.Fatal("a cookie from another deployment's key was accepted")
	}
}

func TestOperatorRemovedLosesSession(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	if _, ok := e.a.Operator(requestWith(c)); !ok {
		t.Fatal("operator not recognised")
	}
	e.build(`[{"id":"josh","name":"Josh"}]`)
	if _, ok := e.a.Operator(requestWith(c)); ok {
		t.Fatal("removed operator still has a session")
	}
	if c.me().Code != http.StatusUnauthorized {
		t.Fatal("me answered for a removed operator")
	}
}

func requestWith(c *client) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	for _, ck := range c.cookies {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	return r
}

func TestDeletedPasskeyEndsSession(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	mustOK(t, c.me(), "before")

	// The owner removes a passkey by editing the file, as the README says.
	p := filepath.Join(e.data, "passkeys.json")
	if err := os.WriteFile(p, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if c.me().Code != http.StatusUnauthorized {
		t.Fatal("session survived its passkey")
	}
}

func TestSessionSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	e.build(opJSON)
	mustOK(t, c.me(), "after restart")
}

func TestRequire(t *testing.T) {
	e := newEnv(t)
	var called int
	h := e.a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := e.a.Operator(r); !ok {
			t.Error("operator missing inside Require")
		}
		called++
	}))
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")

	do := func(method string, hdr map[string]string, session bool) int {
		r := httptest.NewRequest(method, "/v1/work/x", strings.NewReader("{}"))
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if session {
			r.AddCookie(&http.Cookie{Name: "__Host-crew_session", Value: sessionOf(t, c)})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	hdr := map[string]string{"X-Crew-Request": "1"}
	for _, tc := range []struct {
		name    string
		method  string
		hdr     map[string]string
		session bool
		want    int
	}{
		{"ok", "POST", hdr, true, 200},
		{"no header", "POST", nil, true, 403},
		{"wrong header value", "POST", map[string]string{"X-Crew-Request": "0"}, true, 403},
		{"GET", "GET", hdr, true, 403},
		{"no header or session", "POST", nil, false, 403},
		{"no session", "POST", hdr, false, 401},
	} {
		if got := do(tc.method, tc.hdr, tc.session); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	if called != 1 {
		t.Fatalf("handler ran %d times", called)
	}
}

func TestAuthPostsNeedTheHeader(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	for _, p := range []string{"/auth/register/options", "/auth/register/finish", "/auth/login/options", "/auth/login/finish", "/auth/logout"} {
		if rec := c.do(http.MethodPost, p, []byte("{}"), false); rec.Code != http.StatusForbidden {
			t.Errorf("%s without header: %d", p, rec.Code)
		}
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	mustOK(t, c.enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	rec := c.post("/auth/logout", map[string]string{})
	mustOK(t, rec, "logout")
	var cleared bool
	for _, ck := range rec.Result().Cookies() {
		cleared = cleared || (ck.Name == "__Host-crew_session" && ck.MaxAge < 0)
	}
	if !cleared || c.me().Code != http.StatusUnauthorized {
		t.Fatal("logout left the session")
	}
}

func TestDisabledLocksEverything(t *testing.T) {
	a := Disabled()
	if _, ok := a.Operator(httptest.NewRequest(http.MethodPost, "/", nil)); ok {
		t.Fatal("disabled auth knows an operator")
	}
	h := a.Require(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler ran") }))
	for _, method := range []string{"POST", "GET"} {
		r := httptest.NewRequest(method, "/", nil)
		r.Header.Set("X-Crew-Request", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", method, rec.Code)
		}
	}
	var nilAuth *Auth
	if _, ok := nilAuth.Operator(httptest.NewRequest("POST", "/", nil)); ok {
		t.Fatal("nil auth knows an operator")
	}
}

func TestCeremoniesAreBounded(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < maxLoginCeremonies*3; i++ {
		mustOK(t, e.client().post("/auth/login/options", map[string]string{}), "options")
	}
	if n := e.a.ceremonyCount(); n > maxLoginCeremonies {
		t.Fatalf("%d ceremonies held", n)
	}
	e.clk.advance(ceremonyTTL + time.Second)
	mustOK(t, e.client().post("/auth/login/options", map[string]string{}), "options")
	if n := e.a.ceremonyCount(); n != 1 {
		t.Fatalf("expired ceremonies kept: %d", n)
	}
}

func TestLoginFloodKeepsPendingRegistration(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	rec := c.post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"})
	mustOK(t, rec, "register options")
	for i := 0; i < 100; i++ {
		mustOK(t, e.client().post("/auth/login/options", map[string]string{}), "login options")
	}
	mustOK(t, c.finish("/auth/register/finish", newSoftKey(t).create(answer{rpID: rpID, origin: origin, challenge: challengeOf(t, rec)})), "register finish")
}

func TestWrongInvitesAreThrottled(t *testing.T) {
	e := newEnv(t)
	var last int
	for i := 0; i < maxInviteFailures+3; i++ {
		last = e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": fmt.Sprintf("guess-%d", i)}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("guessing not throttled: %d", last)
	}
	if rec := e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("throttle must hold even for the right code: %d", rec.Code)
	}
	e.clk.advance(inviteWindow + time.Second)
	mustOK(t, e.client().post("/auth/register/options", map[string]string{"operator": "owner", "invite": "owner-code-0001"}), "after window")
}

func TestFilesAreOwnerOnlyAndAtomic(t *testing.T) {
	e := newEnv(t)
	mustOK(t, e.client().enrol(newSoftKey(t), "owner", "owner-code-0001"), "enrol")
	for _, name := range []string{"session.key", "passkeys.json", "invites-used.json"} {
		st, err := os.Stat(filepath.Join(e.data, name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", name, st.Mode().Perm())
		}
	}
	key, _ := os.ReadFile(filepath.Join(e.data, "session.key"))
	if len(key) != 32 {
		t.Fatalf("session.key is %d bytes", len(key))
	}
	left, _ := filepath.Glob(filepath.Join(e.data, ".tmp-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	// A write that cannot rename must leave the old file.
	p := filepath.Join(e.data, "x.json")
	if err := writeAtomic(p, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(e.data, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(e.data, "dir"), []byte("new")); err == nil {
		t.Fatal("renaming over a directory succeeded")
	}
	if got, _ := os.ReadFile(p); string(got) != "old" {
		t.Fatalf("old file damaged: %s", got)
	}
}

func TestNewRefusesShortSessionKey(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.data, "session.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Origin: origin, DataDir: e.data, Operators: opJSON, InvitesDir: e.invites, Now: e.clk.now}); err == nil {
		t.Fatal("a short key was accepted")
	}
}

func TestParseOperators(t *testing.T) {
	long := func(n int) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, fmt.Sprintf(`{"id":"op%d","name":"N"}`, i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for _, tc := range []struct {
		name, in string
		want     int
		bad      bool
	}{
		{"empty", "", 0, false},
		{"blank list", "[]", 0, false},
		{"two", opJSON, 2, false},
		{"eight", long(8), 8, false},
		{"nine", long(9), 0, true},
		{"upper id", `[{"id":"Owner","name":"x"}]`, 0, true},
		{"digit first", `[{"id":"1ab","name":"x"}]`, 0, true},
		{"one char", `[{"id":"a","name":"x"}]`, 0, true},
		{"too long id", `[{"id":"abcdefghijklmnopq","name":"x"}]`, 0, true},
		{"path", `[{"id":"a/../b","name":"x"}]`, 0, true},
		{"duplicate", `[{"id":"ab","name":"x"},{"id":"ab","name":"y"}]`, 0, true},
		{"no name", `[{"id":"ab","name":""}]`, 0, true},
		{"not json", `owner`, 0, true},
		{"unknown field", `[{"id":"ab","name":"x","admin":true}]`, 0, true},
	} {
		got, err := ParseOperators(tc.in)
		if (err != nil) != tc.bad || len(got) != tc.want {
			t.Errorf("%s: %v, %d operators", tc.name, err, len(got))
		}
	}
}

func TestNewDisabledWhenUnconfigured(t *testing.T) {
	for _, cfg := range []Config{{Operators: opJSON}, {Origin: origin}, {Origin: origin, Operators: "[]", DataDir: t.TempDir()}} {
		a, err := New(cfg)
		if err != nil || a.Enabled() {
			t.Errorf("%+v: enabled=%v err=%v", cfg, a != nil && a.Enabled(), err)
		}
	}
	for _, o := range []string{"http://crew.example", "https://Crew.example", "https://crew.example/x", "crew.example"} {
		if _, err := New(Config{Origin: o, Operators: opJSON, DataDir: t.TempDir()}); err == nil {
			t.Errorf("origin %q accepted", o)
		}
	}
}

func TestStoreLimitHoldsOnItsOwn(t *testing.T) {
	p, err := loadPasskeys(filepath.Join(t.TempDir(), "passkeys.json"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	h := bytes.Repeat([]byte{1}, 32)
	for i := 0; i < maxPasskeys; i++ {
		if err := p.Add(Passkey{ID: []byte{byte(i)}, Operator: "owner", UserHandle: h}); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Add(Passkey{ID: []byte{99}, Operator: "owner", UserHandle: h}); err != ErrTooMany {
		t.Fatalf("sixth: %v", err)
	}
	if err := p.Add(Passkey{ID: []byte{99}, Operator: "josh", UserHandle: h}); err != nil {
		t.Fatalf("another operator's first: %v", err)
	}
	if err := p.Add(Passkey{ID: []byte{98}, Operator: "owner", UserHandle: bytes.Repeat([]byte{2}, 32)}); err == nil {
		t.Fatal("a second handle for one operator was accepted")
	}
}
