// Package xboxtest stands a fake of Microsoft's sign-in and Xbox Live in
// for tests. It checks what the real services check that matters here: the
// client, its secret, the registered redirect, the PKCE verifier, and that
// a code is good once.
package xboxtest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/xbox"
)

// Made up, like every credential in the tests.
const (
	ClientID     = "00000000-1111-2222-3333-444444444444"
	ClientSecret = "a-made-up-client-secret-value-0123456789"
)

// Account is who signs in at the fake and what Xbox says of them.
type Account struct {
	XUID, Gamertag string
	// XErr, if set, is the XSTS refusal for this account.
	XErr int64
}

type Fake struct {
	t           *testing.T
	srv         *httptest.Server
	RedirectURI string

	mu       sync.Mutex
	account  Account
	codes    map[string]grant
	access   map[string]bool
	userTok  map[string]bool
	Secrets  []string // every token, code and secret the fake ever handed out or was shown
	TokenErr *Reply   // if set, the token endpoint answers this
	UserErr  *Reply
	XSTSErr  *Reply
	Calls    map[string]int
}

// Reply is a canned answer.
type Reply struct {
	Status int
	Body   string
}

type grant struct{ challenge, redirect string }

func New(t *testing.T, redirectURI string) *Fake {
	t.Helper()
	f := &Fake{t: t, RedirectURI: redirectURI, codes: map[string]grant{}, access: map[string]bool{}, userTok: map[string]bool{}, Calls: map[string]int{}}
	f.Secrets = []string{ClientSecret}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("POST /user", f.user)
	mux.HandleFunc("POST /xsts", f.xsts)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *Fake) Endpoints() xbox.Endpoints {
	return xbox.Endpoints{Authorize: f.srv.URL + "/authorize", Token: f.srv.URL + "/token", UserAuth: f.srv.URL + "/user", XSTS: f.srv.URL + "/xsts"}
}

func (f *Fake) Client() *xbox.Client {
	return xbox.NewWithEndpoints(xbox.Config{ClientID: ClientID, ClientSecret: ClientSecret, RedirectURI: f.RedirectURI}, f.Endpoints())
}

// SignIn is the account the next person to approve is.
func (f *Fake) SignIn(a Account) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.account = a
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Approve is the person agreeing on Microsoft's page. It checks the
// authorize request as Microsoft would and returns the URL Microsoft
// sends the browser back to. cancel gives the access_denied answer.
func (f *Fake) Approve(authorizeURL string, cancel bool) string {
	f.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || !strings.HasPrefix(authorizeURL, f.srv.URL+"/authorize?") {
		f.t.Fatalf("the browser was sent to %q", authorizeURL)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id": ClientID, "response_type": "code", "redirect_uri": f.RedirectURI, "response_mode": "query",
		"scope": xbox.Scope, "code_challenge_method": "S256",
	} {
		if q.Get(k) != want {
			f.t.Fatalf("authorize %s = %q, want %q", k, q.Get(k), want)
		}
	}
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		f.t.Fatal("authorize without state or challenge")
	}
	back, _ := url.Parse(f.RedirectURI)
	out := url.Values{"state": {q.Get("state")}}
	if cancel {
		out.Set("error", "access_denied")
		out.Set("error_description", "the user canceled the authentication")
	} else {
		code := "code-" + random()
		f.mu.Lock()
		f.codes[code] = grant{q.Get("code_challenge"), q.Get("redirect_uri")}
		f.Secrets = append(f.Secrets, code)
		f.mu.Unlock()
		out.Set("code", code)
	}
	back.RawQuery = out.Encode()
	return back.String()
}

func (f *Fake) count(name string) {
	f.mu.Lock()
	f.Calls[name]++
	f.mu.Unlock()
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if s, ok := body.(string); ok {
		_, _ = w.Write([]byte(s))
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	f.count("token")
	if f.TokenErr != nil {
		reply(w, f.TokenErr.Status, f.TokenErr.Body)
		return
	}
	_ = r.ParseForm()
	fail := func(word string, code int) {
		reply(w, http.StatusBadRequest, map[string]any{"error": word, "error_description": "AADSTS: do not show this " + r.Form.Get("code"), "error_codes": []int{code}})
	}
	if r.Form.Get("client_id") != ClientID || r.Form.Get("client_secret") != ClientSecret {
		fail("invalid_client", 7000215)
		return
	}
	f.mu.Lock()
	g, ok := f.codes[r.Form.Get("code")]
	delete(f.codes, r.Form.Get("code"))
	f.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || g.redirect != r.Form.Get("redirect_uri") || g.challenge != base64.RawURLEncoding.EncodeToString(sum[:]) ||
		r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("scope") != xbox.Scope {
		fail("invalid_grant", 70008)
		return
	}
	tok := "msa-access-" + random()
	f.mu.Lock()
	f.access[tok] = true
	f.Secrets = append(f.Secrets, tok)
	f.mu.Unlock()
	reply(w, http.StatusOK, map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": 3599, "scope": xbox.Scope})
}

func (f *Fake) user(w http.ResponseWriter, r *http.Request) {
	f.count("user")
	if f.UserErr != nil {
		reply(w, f.UserErr.Status, f.UserErr.Body)
		return
	}
	var req struct {
		Properties   struct{ AuthMethod, SiteName, RpsTicket string }
		RelyingParty string
		TokenType    string
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ticket, ok := strings.CutPrefix(req.Properties.RpsTicket, "d=")
	f.mu.Lock()
	good := ok && f.access[ticket]
	f.mu.Unlock()
	if !good || req.Properties.AuthMethod != "RPS" || req.Properties.SiteName != "user.auth.xboxlive.com" ||
		req.RelyingParty != "http://auth.xboxlive.com" || req.TokenType != "JWT" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
		reply(w, http.StatusUnauthorized, "")
		return
	}
	tok := "xbl-" + random()
	f.mu.Lock()
	f.userTok[tok] = true
	f.Secrets = append(f.Secrets, tok)
	f.mu.Unlock()
	reply(w, http.StatusOK, map[string]any{"Token": tok, "DisplayClaims": map[string]any{"xui": []map[string]string{{"uhs": "hash"}}}})
}

func (f *Fake) xsts(w http.ResponseWriter, r *http.Request) {
	f.count("xsts")
	if f.XSTSErr != nil {
		reply(w, f.XSTSErr.Status, f.XSTSErr.Body)
		return
	}
	var req struct {
		Properties struct {
			SandboxID  string `json:"SandboxId"`
			UserTokens []string
		}
		RelyingParty string
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	good := len(req.Properties.UserTokens) == 1 && f.userTok[req.Properties.UserTokens[0]]
	a := f.account
	f.mu.Unlock()
	if !good || req.RelyingParty != "http://xboxlive.com" || req.Properties.SandboxID != "RETAIL" {
		reply(w, http.StatusUnauthorized, "")
		return
	}
	if a.XErr != 0 {
		reply(w, http.StatusUnauthorized, map[string]any{"Identity": "0", "XErr": a.XErr, "Message": "", "Redirect": "https://start.ui.xboxlive.com/AddChildToFamily"})
		return
	}
	tok := "xsts-" + random()
	f.mu.Lock()
	f.Secrets = append(f.Secrets, tok)
	f.mu.Unlock()
	reply(w, http.StatusOK, map[string]any{"Token": tok, "DisplayClaims": map[string]any{"xui": []map[string]string{{"xid": a.XUID, "gtg": a.Gamertag, "uhs": "hash"}}}})
}
