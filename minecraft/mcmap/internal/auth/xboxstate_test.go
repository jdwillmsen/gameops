package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func xboxFlow(t *testing.T, now *time.Time) *XboxFlow {
	t.Helper()
	f, err := NewXboxFlow([]byte(strings.Repeat("k", 32)), func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func startXbox(t *testing.T, f *XboxFlow) (state, challenge string, c *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	state, challenge = f.Begin(rec)
	for _, got := range rec.Result().Cookies() {
		if got.Name == XboxStateCookie {
			c = got
		}
	}
	if c == nil {
		t.Fatal("no cookie set")
	}
	return state, challenge, c
}

func callback(f *XboxFlow, state string, c *http.Cookie) (string, error) {
	req := httptest.NewRequest("GET", "/auth/xbox/callback", nil)
	if c != nil {
		req.AddCookie(c)
	}
	return f.Finish(httptest.NewRecorder(), req, state)
}

func TestXboxState_TheCookieIsAHostLaxHttpOnlySecureOne(t *testing.T) {
	now := time.Now()
	_, _, c := startXbox(t, xboxFlow(t, &now))
	if c.Name != "__Host-mcmap_xbox" || c.SameSite != http.SameSiteLaxMode || !c.HttpOnly || !c.Secure || c.Path != "/" || c.Domain != "" || c.MaxAge != 600 {
		t.Errorf("cookie = %+v", c)
	}
}

func TestXboxState_TheChallengeIsTheVerifiersS256AndTheVerifierComesBackOnce(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, challenge, c := startXbox(t, f)
	verifier, err := callback(f, state, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 {
		t.Errorf("verifier is %d characters; RFC 7636 wants at least 43", len(verifier))
	}
	if want := s256Challenge(verifier); want != challenge {
		t.Errorf("challenge %q is not S256 of the verifier", challenge)
	}
	if _, err := callback(f, state, c); err != ErrXboxReplayed {
		t.Errorf("a second finish = %v, want replayed", err)
	}
}

// RFC 7636 appendix B: the verifier and challenge it gives as an example.
func TestXboxState_ChallengeMatchesTheRFCExample(t *testing.T) {
	if got := s256Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("challenge = %q", got)
	}
}

func TestXboxState_Refusals(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, _, c := startXbox(t, f)
	if _, err := callback(f, state, nil); err != ErrXboxNoState {
		t.Errorf("no cookie = %v", err)
	}
	if _, err := callback(f, "other", c); err != ErrXboxMismatch {
		t.Errorf("another state = %v", err)
	}
	if _, err := callback(f, "", c); err != ErrXboxMismatch {
		t.Errorf("an empty state = %v", err)
	}
	forged := *c
	forged.Value = c.Value[:len(c.Value)-2] + "AA"
	if _, err := callback(f, state, &forged); err != ErrXboxNoState {
		t.Errorf("a cookie with a bad signature = %v", err)
	}
	payload, _, _ := strings.Cut(c.Value, ".")
	unsigned := *c
	unsigned.Value = payload
	if _, err := callback(f, state, &unsigned); err != ErrXboxNoState {
		t.Errorf("an unsigned cookie = %v", err)
	}
	// Signed under another session key: another map's cookie.
	other, _ := NewXboxFlow([]byte(strings.Repeat("z", 32)), func() time.Time { return now })
	ostate, _, oc := startXbox(t, other)
	if _, err := callback(f, ostate, oc); err != ErrXboxNoState {
		t.Errorf("another key's cookie = %v", err)
	}
	// Refusals use nothing up: the honest finish still works.
	if _, err := callback(f, state, c); err != nil {
		t.Errorf("after refusals = %v", err)
	}
}

func TestXboxState_ExpiresAfterTenMinutes(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, _, c := startXbox(t, f)
	now = now.Add(XboxStateTTL + time.Second)
	if _, err := callback(f, state, c); err != ErrXboxNoState {
		t.Errorf("an old cookie = %v", err)
	}
}

func TestXboxState_FinishClearsTheCookieWhateverTheAnswer(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	rec := httptest.NewRecorder()
	_, _ = f.Finish(rec, httptest.NewRequest("GET", "/", nil), "x")
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == XboxStateCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Error("the cookie was left in place")
	}
}

func TestXboxState_TheMemoryOfUsedStatesIsBounded(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	for range maxUsedStates + 10 {
		state, _, c := startXbox(t, f)
		if _, err := callback(f, state, c); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.used) > maxUsedStates || f.order.Len() > maxUsedStates {
		t.Errorf("%d states remembered", len(f.used))
	}
	now = now.Add(XboxStateTTL + time.Minute)
	state, _, c := startXbox(t, f)
	_, _ = callback(f, state, c)
	if len(f.used) != 1 {
		t.Errorf("%d states remembered after they all expired", len(f.used))
	}
}

func TestXboxState_NeedsTheSessionKey(t *testing.T) {
	if _, err := NewXboxFlow([]byte("short"), time.Now); err == nil {
		t.Error("a short key was accepted")
	}
}
