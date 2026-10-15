package xbox_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/xbox"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/xbox/xboxtest"
)

const redirect = "https://map.example/auth/xbox/callback"

var player = xboxtest.Account{XUID: "2535400000000001", Gamertag: "Alex Builds"}

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// codeFor does what the browser does: go to Microsoft and come back.
func codeFor(t *testing.T, f *xboxtest.Fake, c *xbox.Client, verifier string) string {
	t.Helper()
	back, _ := url.Parse(f.Approve(c.AuthorizeURL("state-1", challengeOf(verifier)), false))
	return back.Query().Get("code")
}

func TestResolve_TheChainGivesTheXUIDAndGamertag(t *testing.T) {
	f := xboxtest.New(t, redirect)
	f.SignIn(player)
	c := f.Client()
	got, err := c.Resolve(context.Background(), codeFor(t, f, c, "verifier-0123456789012345678901234567890123456789"), "verifier-0123456789012345678901234567890123456789")
	if err != nil || got.XUID != player.XUID || got.Gamertag != player.Gamertag {
		t.Fatalf("Resolve = %+v, %v", got, err)
	}
	if f.Calls["token"] != 1 || f.Calls["user"] != 1 || f.Calls["xsts"] != 1 {
		t.Errorf("calls = %v", f.Calls)
	}
}

func TestAuthorizeURL_AsksForExactlyTheXboxScopeWithPKCEAndNoRefreshToken(t *testing.T) {
	f := xboxtest.New(t, redirect)
	u, _ := url.Parse(f.Client().AuthorizeURL("s", "c"))
	q := u.Query()
	if q.Get("scope") != "XboxLive.signin" || strings.Contains(u.RawQuery, "offline_access") {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("redirect_uri") != redirect {
		t.Errorf("query = %v", q)
	}
}

func TestAuthorizeURL_ProductionGoesToTheConsumersTenant(t *testing.T) {
	c := xbox.New(xbox.Config{ClientID: xboxtest.ClientID, ClientSecret: xboxtest.ClientSecret, RedirectURI: redirect})
	if got := c.AuthorizeURL("s", "c"); !strings.HasPrefix(got, "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize?") {
		t.Errorf("url = %s", got)
	}
}

func failure(t *testing.T, err error) *xbox.Failure {
	t.Helper()
	var f *xbox.Failure
	if !errors.As(err, &f) {
		t.Fatalf("err = %v, want a Failure", err)
	}
	return f
}

func TestResolve_APKCEVerifierThatIsNotTheOneChallengedIsRefused(t *testing.T) {
	f := xboxtest.New(t, redirect)
	f.SignIn(player)
	c := f.Client()
	code := codeFor(t, f, c, "the-right-verifier")
	_, err := c.Resolve(context.Background(), code, "another-verifier")
	if got := failure(t, err); got.Result != xbox.ResultExchangeFailed || got.Detail != "invalid_grant AADSTS70008" {
		t.Errorf("failure = %+v", got)
	}
	if f.Calls["user"] != 0 {
		t.Error("went on to Xbox after Microsoft refused")
	}
}

func TestResolve_ACodeIsGoodOnce(t *testing.T) {
	f := xboxtest.New(t, redirect)
	f.SignIn(player)
	c := f.Client()
	code := codeFor(t, f, c, "v")
	if _, err := c.Resolve(context.Background(), code, "v"); err != nil {
		t.Fatal(err)
	}
	if got := failure(t, func() error { _, err := c.Resolve(context.Background(), code, "v"); return err }()); got.Result != xbox.ResultExchangeFailed {
		t.Errorf("second redeem = %+v", got)
	}
}

func TestResolve_AWrongSecretIsExchangeFailedAndSaysInvalidClient(t *testing.T) {
	f := xboxtest.New(t, redirect)
	c := xbox.NewWithEndpoints(xbox.Config{ClientID: xboxtest.ClientID, ClientSecret: "not-the-secret", RedirectURI: redirect}, f.Endpoints())
	_, err := c.Resolve(context.Background(), codeFor(t, f, f.Client(), "v"), "v")
	if got := failure(t, err); got.Result != xbox.ResultExchangeFailed || got.Detail != "invalid_client AADSTS7000215" {
		t.Errorf("failure = %+v", got)
	}
}

func TestResolve_XSTSRefusals(t *testing.T) {
	for _, tc := range []struct {
		xerr   int64
		result string
	}{
		{2148916233, xbox.ResultNoProfile},
		{2148916238, xbox.ResultChild},
		{2148916227, xbox.ResultRefused},
		{2148916235, xbox.ResultRefused},
		{1, xbox.ResultRefused},
	} {
		f := xboxtest.New(t, redirect)
		f.SignIn(xboxtest.Account{XErr: tc.xerr})
		c := f.Client()
		_, err := c.Resolve(context.Background(), codeFor(t, f, c, "v"), "v")
		if got := failure(t, err); got.Result != tc.result || !strings.Contains(got.Detail, "XErr") {
			t.Errorf("XErr %d: failure = %+v, want %s", tc.xerr, got, tc.result)
		}
	}
}

func TestResolve_UnavailableIsNotARefusal(t *testing.T) {
	for name, set := range map[string]func(*xboxtest.Fake){
		"token 503": func(f *xboxtest.Fake) { f.TokenErr = &xboxtest.Reply{Status: 503, Body: "busy"} },
		"user 502":  func(f *xboxtest.Fake) { f.UserErr = &xboxtest.Reply{Status: 502} },
		"xsts 500":  func(f *xboxtest.Fake) { f.XSTSErr = &xboxtest.Reply{Status: 500} },
	} {
		f := xboxtest.New(t, redirect)
		f.SignIn(player)
		set(f)
		c := f.Client()
		_, err := c.Resolve(context.Background(), codeFor(t, f, c, "v"), "v")
		if got := failure(t, err); got.Result != xbox.ResultUnavailable {
			t.Errorf("%s: failure = %+v", name, got)
		}
	}
}

func TestResolve_NobodyListeningIsUnavailable(t *testing.T) {
	f := xboxtest.New(t, redirect)
	c := f.Client()
	code := codeFor(t, f, c, "v")
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	ep := f.Endpoints()
	ep.Token = dead.URL
	_, err := xbox.NewWithEndpoints(xbox.Config{ClientID: xboxtest.ClientID, ClientSecret: xboxtest.ClientSecret, RedirectURI: redirect}, ep).Resolve(context.Background(), code, "v")
	if got := failure(t, err); got.Result != xbox.ResultUnavailable || got.Detail != "network" {
		t.Errorf("failure = %+v", got)
	}
}

func TestResolve_AnswersThatAreNotWhatWasAskedFor(t *testing.T) {
	for name, set := range map[string]func(*xboxtest.Fake){
		"token 200 with no token": func(f *xboxtest.Fake) { f.TokenErr = &xboxtest.Reply{Status: 200, Body: `{"token_type":"Bearer"}`} },
		"token not json":          func(f *xboxtest.Fake) { f.TokenErr = &xboxtest.Reply{Status: 200, Body: `<html>`} },
		"user 200 with no token":  func(f *xboxtest.Fake) { f.UserErr = &xboxtest.Reply{Status: 200, Body: `{}`} },
		"user 401":                func(f *xboxtest.Fake) { f.UserErr = &xboxtest.Reply{Status: 401, Body: ``} },
	} {
		f := xboxtest.New(t, redirect)
		f.SignIn(player)
		set(f)
		c := f.Client()
		if _, err := c.Resolve(context.Background(), codeFor(t, f, c, "v"), "v"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestResolve_XSTSWithoutAnXIDIsNotAProfile(t *testing.T) {
	for name, body := range map[string]string{
		"no claims":        `{"Token":"t","DisplayClaims":{"xui":[]}}`,
		"no xid":           `{"Token":"t","DisplayClaims":{"xui":[{"gtg":"x"}]}}`,
		"no DisplayClaims": `{"Token":"t"}`,
	} {
		f := xboxtest.New(t, redirect)
		f.XSTSErr = &xboxtest.Reply{Status: 200, Body: body}
		c := f.Client()
		got, err := c.Resolve(context.Background(), codeFor(t, f, c, "v"), "v")
		if name == "no xid" {
			// The claim is returned empty and the caller's identity check
			// refuses it; the client does not decide who is a player.
			if err != nil || got.XUID != "" {
				t.Errorf("%s: %+v %v", name, got, err)
			}
			continue
		}
		if f := failure(t, err); f.Result != xbox.ResultBadProfile {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

func TestResolve_ARedirectIsNotFollowed(t *testing.T) {
	f := xboxtest.New(t, redirect)
	var followed bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer elsewhere.Close()
	hop := httptest.NewServer(http.RedirectHandler(elsewhere.URL, http.StatusTemporaryRedirect))
	defer hop.Close()
	ep := f.Endpoints()
	ep.Token = hop.URL
	_, err := xbox.NewWithEndpoints(xbox.Config{ClientID: xboxtest.ClientID, ClientSecret: xboxtest.ClientSecret, RedirectURI: redirect}, ep).Resolve(context.Background(), "code", "v")
	if err == nil || followed {
		t.Errorf("err = %v, followed = %v", err, followed)
	}
}

// Errors reach logs; nothing the fake handed out or was shown may be in one.
func TestFailure_NeverCarriesATokenCodeOrSecret(t *testing.T) {
	f := xboxtest.New(t, redirect)
	f.SignIn(player)
	c := f.Client()
	code := codeFor(t, f, c, "v")
	_, err := xbox.NewWithEndpoints(xbox.Config{ClientID: xboxtest.ClientID, ClientSecret: "wrong-" + xboxtest.ClientSecret, RedirectURI: redirect}, f.Endpoints()).Resolve(context.Background(), code, "v")
	if err == nil {
		t.Fatal("no error")
	}
	for _, s := range append(f.Secrets, "wrong-"+xboxtest.ClientSecret, code) {
		if strings.Contains(err.Error(), s) {
			t.Errorf("the error carries %q", s)
		}
	}
}
