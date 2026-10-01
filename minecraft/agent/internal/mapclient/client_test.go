package mapclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func server(t *testing.T, status int, seen *http.Request, body *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		raw, _ := io.ReadAll(r.Body)
		*body = string(raw)
		if status != http.StatusNoContent {
			http.Error(w, "detail that must not reach a player", status)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "internal-token", time.Second)
}

func TestClaim_SendsWhoTypedTheCode(t *testing.T) {
	var seen http.Request
	var body string
	c := server(t, http.StatusNoContent, &seen, &body)

	if err := c.Claim(context.Background(), "ABC234", "2535412345678901", "Steve Builds"); err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != "/internal/v1/claims" {
		t.Errorf("request = %s %s", seen.Method, seen.URL.Path)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer internal-token" {
		t.Errorf("Authorization = %q", got)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["code"] != "ABC234" || sent["xuid"] != "2535412345678901" || sent["gamertag"] != "Steve Builds" || len(sent) != 3 {
		t.Errorf("body = %s", body)
	}
}

func TestClaim_UnknownCodeIsItsOwnError(t *testing.T) {
	var seen http.Request
	var body string
	c := server(t, http.StatusNotFound, &seen, &body)
	if err := c.Claim(context.Background(), "ABC234", "1", "Steve"); !errors.Is(err, ErrUnknownCode) {
		t.Fatalf("err = %v, want ErrUnknownCode", err)
	}
}

func TestClaim_OtherFailuresAreErrorsThatNameTheStatus(t *testing.T) {
	var seen http.Request
	var body string
	c := server(t, http.StatusUnauthorized, &seen, &body)
	err := c.Claim(context.Background(), "ABC234", "1", "Steve")
	if err == nil || errors.Is(err, ErrUnknownCode) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaim_TimesOutOnItsOwnClock(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })
	c := New(srv.URL, "t", 50*time.Millisecond)

	started := time.Now()
	if err := c.Claim(context.Background(), "ABC234", "1", "Steve"); err == nil {
		t.Fatal("a hung map service did not produce an error")
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Errorf("gave up after %s; the caller's context has no deadline, so the client's must", took)
	}
}

func TestRevoke_NamesThePlayerToLogOut(t *testing.T) {
	var seen http.Request
	var body string
	c := server(t, http.StatusNoContent, &seen, &body)

	if err := c.Revoke(context.Background(), "2535412345678901"); err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != "/internal/v1/revocations" {
		t.Errorf("request = %s %s", seen.Method, seen.URL.Path)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer internal-token" {
		t.Errorf("Authorization = %q", got)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent["xuid"] != "2535412345678901" || len(sent) != 1 {
		t.Errorf("body = %s", body)
	}
}

func TestRevoke_FailureIsAnErrorThatNamesTheStatus(t *testing.T) {
	var seen http.Request
	var body string
	c := server(t, http.StatusBadGateway, &seen, &body)
	if err := c.Revoke(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v", err)
	}
}

// The token goes only where it was configured to go. Following a redirect
// would hand it to wherever the answer pointed.
func TestClient_DoesNotFollowRedirects(t *testing.T) {
	var elsewhere int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "internal-token", time.Second)

	if err := c.Claim(context.Background(), "ABC234", "1", "Steve"); err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("Claim err = %v, want the redirect reported as a failure", err)
	}
	if err := c.Revoke(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("Revoke err = %v, want the redirect reported as a failure", err)
	}
	if elsewhere != 0 {
		t.Errorf("%d requests followed the redirect", elsewhere)
	}
}
