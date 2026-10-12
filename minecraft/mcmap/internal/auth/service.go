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
)

const (
	// How many wrong secrets are answered as wrong at once, and how often
	// one more is. Past that a wrong one is turned away unjudged.
	exchangeBurst  = 5
	exchangeRefill = time.Minute
)

// Exchange decides whether a presented secret earns a service session.
//
// The right secret is always honoured. Nothing a caller without it does can
// hold up a caller with it: the internal listener is reachable by every
// workload in the cluster, and a budget or a lock that the right secret had
// to wait behind would let any of them stop the checks for good with one
// wrong request every few minutes. The secret is 48 generated characters,
// which no rate of guessing finds, so it needs no lock to keep it.
//
// What is bounded is wrong secrets: how many are judged and written down
// as such in a minute, so that a flood of them is a count and not a log.
type Exchange struct {
	// want is the secret's digest. Comparing digests keeps the comparison
	// constant-time whatever length is presented.
	want [sha256.Size]byte
	now  func() time.Time

	mu       sync.Mutex
	tokens   float64
	refilled time.Time
}

func NewExchange(secret string, now func() time.Time) *Exchange {
	return &Exchange{want: sha256.Sum256([]byte(secret)), now: now, tokens: exchangeBurst, refilled: now()}
}

// Attempt judges one presented secret.
func (e *Exchange) Attempt(presented string) string {
	got := sha256.Sum256([]byte(presented))
	right := subtle.ConstantTimeCompare(got[:], e.want[:]) == 1
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if right {
		// Whoever holds the secret has shown that the wrong ones before
		// were not theirs to answer for.
		e.tokens, e.refilled = exchangeBurst, now
		return ExchangeOK
	}
	e.tokens = min(exchangeBurst, e.tokens+now.Sub(e.refilled).Seconds()/exchangeRefill.Seconds())
	e.refilled = now
	if e.tokens < 1 {
		return ExchangeLimited
	}
	e.tokens--
	return ExchangeDenied
}

// RetryAfter is how long until another wrong secret would be judged.
func (e *Exchange) RetryAfter() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return max(time.Second, time.Duration((1-e.tokens)*float64(exchangeRefill))-e.now().Sub(e.refilled))
}
