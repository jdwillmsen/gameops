package xbox

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCookie = "__Host-test_xbox"

func xboxFlow(t *testing.T, now *time.Time) *Flow {
	t.Helper()
	f, err := NewFlow(testCookie, "test state", []byte(strings.Repeat("k", 32)), func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func startXbox(t *testing.T, f *Flow) (state, challenge string, c *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	state, challenge = f.Begin(rec)
	for _, got := range rec.Result().Cookies() {
		if got.Name == testCookie {
			c = got
		}
	}
	if c == nil {
		t.Fatal("no cookie set")
	}
	return state, challenge, c
}

func callback(f *Flow, state string, c *http.Cookie) (string, error) {
	req := httptest.NewRequest("GET", "/auth/xbox/callback", nil)
	if c != nil {
		req.AddCookie(c)
	}
	return f.Finish(httptest.NewRecorder(), req, state)
}

func TestFlow_TheCookieIsAHostLaxHttpOnlySecureOne(t *testing.T) {
	now := time.Now()
	_, _, c := startXbox(t, xboxFlow(t, &now))
	if c.Name != testCookie || c.SameSite != http.SameSiteLaxMode || !c.HttpOnly || !c.Secure || c.Path != "/" || c.Domain != "" || c.MaxAge != 600 {
		t.Errorf("cookie = %+v", c)
	}
}

func TestFlow_TheChallengeIsTheVerifiersS256AndTheVerifierComesBackOnce(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, challenge, c := startXbox(t, f)
	verifier, err := callback(f, state, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 {
		t.Errorf("verifier is %d characters; a verifier is at least 43", len(verifier))
	}
	if want := s256Challenge(verifier); want != challenge {
		t.Errorf("challenge %q is not S256 of the verifier", challenge)
	}
	if _, err := callback(f, state, c); err != ErrReplayed {
		t.Errorf("a second finish = %v, want replayed", err)
	}
}

// A published pair of verifier and S256 challenge, so that the challenge is
// checked against something this code did not make.
func TestFlow_ChallengeMatchesAPublishedPair(t *testing.T) {
	if got := s256Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("challenge = %q", got)
	}
}

func TestFlow_Refusals(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, _, c := startXbox(t, f)
	if _, err := callback(f, state, nil); err != ErrNoState {
		t.Errorf("no cookie = %v", err)
	}
	if _, err := callback(f, "other", c); err != ErrMismatch {
		t.Errorf("another state = %v", err)
	}
	if _, err := callback(f, "", c); err != ErrMismatch {
		t.Errorf("an empty state = %v", err)
	}
	forged := *c
	forged.Value = c.Value[:len(c.Value)-2] + "AA"
	if _, err := callback(f, state, &forged); err != ErrNoState {
		t.Errorf("a cookie with a bad signature = %v", err)
	}
	payload, _, _ := strings.Cut(c.Value, ".")
	unsigned := *c
	unsigned.Value = payload
	if _, err := callback(f, state, &unsigned); err != ErrNoState {
		t.Errorf("an unsigned cookie = %v", err)
	}
	// Signed under another session key: another map's cookie.
	other, _ := NewFlow(testCookie, "test state", []byte(strings.Repeat("z", 32)), func() time.Time { return now })
	ostate, _, oc := startXbox(t, other)
	if _, err := callback(f, ostate, oc); err != ErrNoState {
		t.Errorf("another key's cookie = %v", err)
	}
	// Refusals use nothing up: the honest finish still works.
	if _, err := callback(f, state, c); err != nil {
		t.Errorf("after refusals = %v", err)
	}
}

func TestFlow_ExpiresAfterTenMinutes(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	state, _, c := startXbox(t, f)
	now = now.Add(StateTTL + time.Second)
	if _, err := callback(f, state, c); err != ErrNoState {
		t.Errorf("an old cookie = %v", err)
	}
}

func TestFlow_FinishClearsTheCookieWhateverTheAnswer(t *testing.T) {
	now := time.Now()
	f := xboxFlow(t, &now)
	rec := httptest.NewRecorder()
	_, _ = f.Finish(rec, httptest.NewRequest("GET", "/", nil), "x")
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == testCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Error("the cookie was left in place")
	}
}

func TestFlow_TheMemoryOfUsedStatesIsBounded(t *testing.T) {
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
	now = now.Add(StateTTL + time.Minute)
	state, _, c := startXbox(t, f)
	_, _ = callback(f, state, c)
	if len(f.used) != 1 {
		t.Errorf("%d states remembered after they all expired", len(f.used))
	}
}

func TestFlow_NeedsTheSessionKey(t *testing.T) {
	if _, err := NewFlow(testCookie, "test state", []byte("short"), time.Now); err == nil {
		t.Error("a short key was accepted")
	}
}
