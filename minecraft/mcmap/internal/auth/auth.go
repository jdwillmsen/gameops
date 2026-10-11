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
	// The game allows no colon in a gamertag, so this costs no player a
	// login; it is here so that the name a service session goes by can
	// never be one a claim was made under.
	if strings.HasPrefix(strings.ToLower(id.Gamertag), servicePrefix) {
		return errors.New("that name is kept for service sessions")
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

	// ServiceKey signs the sessions of automated checks, and ServiceTTL is
	// how long one lasts. With no key there are none: none is issued and a
	// cookie claiming to be one is refused. See service.go.
	ServiceKey []byte
	ServiceTTL time.Duration
	// ServiceSeen, if set, is told of every request a service session
	// makes and whether it was let through.
	ServiceSeen func(allowed bool)
}

type claims struct {
	Identity
	IssuedMilli int64 `json:"i"`
	Expires     int64 `json:"e"`
	// Service marks the session of an automated check. It is no player's:
	// it carries no XUID, and is signed with a key of its own.
	Service bool `json:"s,omitempty"`
}

var encoding = base64.RawURLEncoding

func sign(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return encoding.EncodeToString(mac.Sum(nil))
}

// Issue logs the browser in as id.
func (s *Sessions) Issue(w http.ResponseWriter, id Identity) {
	s.issue(w, s.Key, s.TTL, claims{Identity: id})
}

// issue is the one place a session cookie is made, so that a service
// session's cookie cannot differ from a player's in anything but what it
// says and how long it lasts.
func (s *Sessions) issue(w http.ResponseWriter, key []byte, ttl time.Duration, c claims) time.Time {
	now := s.Now()
	expires := now.Add(ttl)
	c.IssuedMilli, c.Expires = now.UnixMilli(), expires.Unix()
	raw, _ := json.Marshal(c)
	payload := encoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    payload + "." + sign(key, payload),
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return time.Unix(c.Expires, 0)
}

// Clear logs the browser out.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// Verify returns the player the request is logged in as, if any. A service
// session is nobody's login.
func (s *Sessions) Verify(r *http.Request) (Identity, bool) {
	c, ok := s.verify(r)
	if !ok || c.Service {
		return Identity{}, false
	}
	return c.Identity, true
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
	if !ok || payload == "" {
		return claims{}, false
	}
	// Which key signed it decides what kind of session it may be; what
	// the cookie says of itself decides nothing.
	var service bool
	switch {
	case hmac.Equal([]byte(sig), []byte(sign(s.Key, payload))):
	case len(s.ServiceKey) >= keyBytes && hmac.Equal([]byte(sig), []byte(sign(s.ServiceKey, payload))):
		service = true
	default:
		return claims{}, false
	}
	raw, err := encoding.DecodeString(payload)
	if err != nil {
		return claims{}, false
	}
	var c claims
	if err := json.Unmarshal(raw, &c); err != nil || c.Service != service {
		return claims{}, false
	}
	// The cookie's own Max-Age is the browser's business; this is the check
	// that counts.
	expires := time.Unix(c.Expires, 0)
	if !s.Now().Before(expires) {
		return claims{}, false
	}
	if service {
		if !c.serviceShaped() || expires.Sub(time.UnixMilli(c.IssuedMilli)) > MaxServiceTTL+time.Second {
			return claims{}, false
		}
		return c, true
	}
	if c.XUID == "" {
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

// IsService reports whether the session Require let through is an automated
// check's and not a player's.
func IsService(ctx context.Context) bool {
	c, ok := ctx.Value(contextKey{}).(claims)
	return ok && c.Service
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

// Require lets a request through only with a player's session. It is the
// gate to reach for: a service session is refused here, so whatever is put
// behind it later is out of an automated check's reach without anyone
// having to remember to say so.
func (s *Sessions) Require(next http.Handler) http.Handler {
	return s.require(next, false)
}

// RequireViewer also lets a service session read. It is for what every
// logged-in player is shown alike, and for nothing that is one player's
// own or that changes anything.
func (s *Sessions) RequireViewer(next http.Handler) http.Handler {
	return s.require(next, true)
}

func (s *Sessions) require(next http.Handler, viewer bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.verify(r)
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "login required", http.StatusUnauthorized)
			return
		}
		if c.Service {
			// Reading only, whatever the route: a service session
			// looks and does nothing else.
			allowed := viewer && (r.Method == http.MethodGet || r.Method == http.MethodHead)
			if s.ServiceSeen != nil {
				s.ServiceSeen(allowed)
			}
			if !allowed {
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "not open to a service session", http.StatusForbidden)
				return
			}
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
