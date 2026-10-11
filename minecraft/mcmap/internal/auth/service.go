package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"
)

// A service session is how an automated check sees the map without a player
// typing a code for it. It is nobody's login: it carries no XUID, goes by a
// name no gamertag can be, lasts minutes, and reads only what every player
// is shown alike.

// ServiceName is who a service session says it is, wherever a gamertag
// would be shown or logged.
const ServiceName = servicePrefix + "e2e"

// servicePrefix is kept from players: Identity.check refuses a claim under
// a name that starts with it.
const servicePrefix = "service:"

const (
	// MinServiceSecret is 32 characters of a generated password, which is
	// more than anything will guess at the rate the exchange allows.
	MinServiceSecret = 32
	// MaxServiceTTL keeps a session to one run of the checks. A leaked
	// cookie is good for this long at most, and cannot be renewed.
	MaxServiceTTL = 30 * time.Minute
	MinServiceTTL = time.Minute
)

// ServiceKey derives the key service sessions are signed with from the
// session key and the exchange secret. Being a key of its own is what makes
// a service cookie and a player's impossible to pass off as each other;
// being derived from the secret is what makes changing the secret end every
// session issued under the old one.
func ServiceKey(sessionKey []byte, secret string) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("mcmap service session v1\x00"))
	mac.Write([]byte(secret))
	return mac.Sum(nil)
}

// serviceShaped is what a service session's claims must look like. A cookie
// signed with the service key that names a player is refused rather than
// believed.
func (c claims) serviceShaped() bool {
	return c.Service && c.XUID == "" && c.Gamertag == ServiceName
}

// IssueService gives the caller a service session and reports when it ends.
// The caller has already proved it holds the secret.
func (s *Sessions) IssueService(w http.ResponseWriter) time.Time {
	return s.issue(w, s.ServiceKey, s.ServiceTTL, claims{Identity: Identity{Gamertag: ServiceName}, Service: true})
}

// How an attempt to exchange the secret for a session ended; the label
// values of the exchange metric.
const (
	ExchangeOK      = "ok"
	ExchangeDenied  = "denied"
	ExchangeLimited = "limited"
	ExchangeLocked  = "locked"
)

const (
	// A run of the checks is one exchange, and they run a few times an
	// hour, so this is generous to them and useless to a guesser.
	exchangeBurst  = 5
	exchangeRefill = time.Minute
	// Nothing that holds the secret ever presents a wrong one, so a run
	// of wrong ones is somebody guessing or a broken rollout, and neither
	// is helped by being answered quickly.
	exchangeMaxFailures = 5
	exchangeLockout     = 5 * time.Minute
)

// Exchange decides whether a presented secret earns a service session. It
// counts every attempt against one budget and locks after repeated wrong
// ones, for everybody: the listener it sits on sees cluster addresses that
// are cheap to change, so a budget per caller would be no budget.
type Exchange struct {
	// want is the secret's digest. Comparing digests keeps the comparison
	// constant-time whatever length is presented.
	want [sha256.Size]byte
	now  func() time.Time

	mu          sync.Mutex
	tokens      float64
	refilled    time.Time
	failures    int
	lockedUntil time.Time
}

func NewExchange(secret string, now func() time.Time) *Exchange {
	return &Exchange{want: sha256.Sum256([]byte(secret)), now: now, tokens: exchangeBurst, refilled: now()}
}

// Attempt judges one presented secret. While locked nothing is compared at
// all, so a guess made then learns nothing, not even at the rate allowed.
func (e *Exchange) Attempt(presented string) string {
	got := sha256.Sum256([]byte(presented))
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if now.Before(e.lockedUntil) {
		return ExchangeLocked
	}
	e.tokens = min(exchangeBurst, e.tokens+now.Sub(e.refilled).Seconds()/exchangeRefill.Seconds())
	e.refilled = now
	if e.tokens < 1 {
		return ExchangeLimited
	}
	e.tokens--
	if subtle.ConstantTimeCompare(got[:], e.want[:]) == 1 {
		e.failures = 0
		return ExchangeOK
	}
	e.failures++
	if e.failures >= exchangeMaxFailures {
		e.lockedUntil = now.Add(exchangeLockout)
		// One more wrong secret after the lock lifts locks it again.
		e.failures = exchangeMaxFailures - 1
	}
	return ExchangeDenied
}

// RetryAfter is how long a refused caller should wait before trying again.
func (e *Exchange) RetryAfter() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	if wait := e.lockedUntil.Sub(e.now()); wait > 0 {
		return wait
	}
	return exchangeRefill
}
