// Package allowlist says which XUIDs the game server's own allowlist.json
// names, read through the console bridge.
package allowlist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/auth"
)

// CacheFor is how long an answer stands. A sign-in is rare and a removal
// from the allowlist should not wait long.
const CacheFor = 30 * time.Second

// Bridge reads the allowlist from the console bridge.
type Bridge struct {
	URL, Token string
	HTTP       *http.Client
}

func NewBridge(baseURL, token string) *Bridge {
	return &Bridge{URL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type entry struct {
	XUID string `json:"xuid"`
}

// XUIDs are the allowlist's players. A name added by hand that has not yet
// joined has no XUID and is not one.
func (b *Bridge) XUIDs(ctx context.Context) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/allowlist", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("allowlist: bridge answered %d", resp.StatusCode)
	}
	var entries []entry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("allowlist: %w", err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		if auth.IsXUID(e.XUID) {
			out[e.XUID] = true
		}
	}
	return out, nil
}

type Lister interface {
	XUIDs(ctx context.Context) (map[string]bool, error)
}

// List asks the bridge when its last answer has run out. It has no
// last-known-good: with the bridge down the allowlist names nobody, which
// can only turn a sign-in away.
type List struct {
	lister Lister
	now    func() time.Time

	asking sync.Mutex // one lookup for the requests that arrive together

	mu      sync.Mutex
	xuids   map[string]bool
	askedAt time.Time
	ok      bool
}

func New(l Lister, now func() time.Time) *List { return &List{lister: l, now: now} }

// Has reports whether xuid is on the allowlist, and whether the answer
// could be had at all, for the caller to log.
func (l *List) Has(ctx context.Context, xuid string) (on, answered bool) {
	if !l.fresh() {
		l.asking.Lock()
		if !l.fresh() {
			// Shared by whoever is waiting, so not ended with this request.
			got, err := l.lister.XUIDs(context.WithoutCancel(ctx))
			l.mu.Lock()
			l.xuids, l.ok, l.askedAt = got, err == nil, l.now()
			l.mu.Unlock()
		}
		l.asking.Unlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ok && l.xuids[xuid], l.ok
}

func (l *List) fresh() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.askedAt.IsZero() && l.now().Sub(l.askedAt) < CacheFor
}
