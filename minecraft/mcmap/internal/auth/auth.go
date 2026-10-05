// Package auth decides who may see the map. A login is a signed cookie
// naming the player; how a browser earns one is up to a provider, and the
// in-game code in codes.go is the first.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Identity is who a session belongs to. The XUID is the identity; the
// gamertag is what to call them, and can change.
type Identity struct {
	XUID     string `json:"x"`
	Gamertag string `json:"g"`
}

var xuidShape = regexp.MustCompile(`^[0-9]{1,20}$`)

// IsXUID reports whether s could be a player's XUID.
func IsXUID(s string) bool { return xuidShape.MatchString(s) }

const maxGamertag = 64

// check refuses anything that is not a player. The agent is the only caller
// that can supply an identity, but the console can speak in chat too and
// its stand-in XUID is not a number.
func (id Identity) check() error {
	if !IsXUID(id.XUID) {
		return errors.New("a player's XUID is a number")
	}
	if utf8.RuneCountInString(id.Gamertag) > maxGamertag || strings.ContainsFunc(id.Gamertag, unicode.IsControl) {
		return errors.New("that is not a gamertag")
	}
	return nil
}

// The __Host- prefix makes a browser refuse a cookie of this name unless
// this exact host set it over HTTPS, so no other site under the same parent
// domain can plant a login. It also means the cookies are always Secure;
// browsers accept those from localhost, which covers a port-forward.
const cookiePrefix = "__Host-"

const sessionCookie = cookiePrefix + "mcmap_session"

// PendingCookie names the cookie holding a login's secret between the page
// showing a code and collecting the session.
const PendingCookie = cookiePrefix + "mcmap_pending"

// Sessions issues and checks the login cookie. The cookie carries the
// identity, when it was issued and an expiry, signed with Key. No session is
// stored server-side, so a restart logs nobody out as long as the key
// survives it.
type Sessions struct {
	Key []byte
	TTL time.Duration
	Now func() time.Time
	// Revoked, if set, is consulted on every request.
	Revoked *Revocations
}

type claims struct {
	Identity
	IssuedMilli int64 `json:"i"`
	Expires     int64 `json:"e"`
}

var encoding = base64.RawURLEncoding

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte(payload))
	return encoding.EncodeToString(mac.Sum(nil))
}

// Issue logs the browser in as id.
func (s *Sessions) Issue(w http.ResponseWriter, id Identity) {
	now := s.Now()
	raw, _ := json.Marshal(claims{Identity: id, IssuedMilli: now.UnixMilli(), Expires: now.Add(s.TTL).Unix()})
	payload := encoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    payload + "." + s.sign(payload),
		Path:     "/",
		MaxAge:   int(s.TTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// Clear logs the browser out.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// Verify returns who the request is logged in as, if anyone.
func (s *Sessions) Verify(r *http.Request) (Identity, bool) {
	c, ok := s.verify(r)
	return c.Identity, ok
}

func (s *Sessions) verify(r *http.Request) (claims, bool) {
	// A key that was never loaded would sign cookies anyone could make.
	if len(s.Key) < keyBytes {
		return claims{}, false
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return claims{}, false
	}
	payload, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok || payload == "" || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return claims{}, false
	}
	raw, err := encoding.DecodeString(payload)
	if err != nil {
		return claims{}, false
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil || c.XUID == "" {
		return claims{}, false
	}
	// The cookie's own Max-Age is the browser's business; this is the check
	// that counts.
	if !s.Now().Before(time.Unix(c.Expires, 0)) {
		return claims{}, false
	}
	if s.Revoked != nil && s.Revoked.covers(c.XUID, c.IssuedMilli) {
		return claims{}, false
	}
	return c, true
}

type contextKey struct{}

// FromContext returns the identity Require attached to a request.
func FromContext(ctx context.Context) (Identity, bool) {
	c, ok := ctx.Value(contextKey{}).(claims)
	return c.Identity, ok
}

// ExpiryFromContext returns when the session Require let through runs out.
// A response that outlasts the request it answers has to end by then: the
// session is only checked when a request arrives.
func ExpiryFromContext(ctx context.Context) (time.Time, bool) {
	c, ok := ctx.Value(contextKey{}).(claims)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(c.Expires, 0), true
}

// Require lets a request through only with a valid session.
func (s *Sessions) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.verify(r)
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, c)))
	})
}

const keyBytes = 32

// LoadKey reads the signing key at path, creating it on first use. It lives
// on the data volume rather than in a secret so that the service needs no
// secret of its own; deleting the file logs everyone out.
func LoadKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != keyBytes {
			return nil, fmt.Errorf("session key %s is %d bytes, want %d; delete it to issue a new one", path, len(key), keyBytes)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, keyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := writeWhole(path, key); err != nil {
		return nil, err
	}
	return key, nil
}
