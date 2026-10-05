package icons

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxPlayers is far more than a server holds; a report naming more is
	// not the agent working as designed.
	MaxPlayers = 256

	// A head is the face of a skin: 8 pixels a side on a standard skin and
	// 16 on a double-resolution one. As a PNG that is a few hundred bytes.
	minHeadSide  = 8
	maxHeadSide  = 32
	maxHeadBytes = 8 << 10

	maxGamertag = 64

	// DefaultHeadsTTL is how long a report is good for. The agent repeats
	// its report every minute; one this old means the agent is gone, and a
	// list of who is online that nobody is keeping is not one to match
	// names against.
	DefaultHeadsTTL = 5 * time.Minute
)

var xuidShape = regexp.MustCompile(`^[0-9]{1,20}$`)

// Report is one online player as the agent sees them. Head is a PNG, or
// empty for a player whose skin the agent could not take a face from.
type Report struct {
	XUID     string
	Gamertag string
	Head     []byte
}

// Head is a player's head as served, with a version that changes when the
// picture does.
type Head struct {
	PNG     []byte
	Version string
}

// Heads holds the head of each player online now, keyed by XUID as the
// agent reports them. The live layer knows a player only by gamertag, so
// that is what a head is asked for by, and a gamertag is answered only
// while exactly one online player holds it.
type Heads struct {
	// TTL is how long a report stands without another. Zero uses
	// DefaultHeadsTTL.
	TTL time.Duration
	// Now is replaced in tests. Nil is the wall clock.
	Now func() time.Time

	mu     sync.RWMutex
	at     time.Time
	byXUID map[string]Head
	// xuidOf is the one player holding each gamertag, folded; a gamertag
	// two players hold is absent. nameOf is the same pairing reversed.
	xuidOf map[string]string
	nameOf map[string]string
}

func (h *Heads) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Fold is the form gamertags are compared in.
func Fold(gamertag string) string { return strings.ToLower(gamertag) }

// Replace takes the agent's report of everyone online, which replaces the
// one before it whole: a player who has left, or changed gamertag, is not
// remembered under what they were. A report that is not a list of players
// is refused whole. A head that is not a small square PNG costs only that
// head, and is counted; refused is how many there were.
func (h *Heads) Replace(reports []Report) (refused int, err error) {
	if len(reports) > MaxPlayers {
		return 0, fmt.Errorf("%d players is over the limit of %d", len(reports), MaxPlayers)
	}
	byXUID := map[string]Head{}
	holders := map[string][]string{}
	seen := map[string]bool{}
	for _, r := range reports {
		if !xuidShape.MatchString(r.XUID) {
			return 0, errors.New("a player's XUID is a number")
		}
		if seen[r.XUID] {
			return 0, errors.New("a player is reported twice")
		}
		seen[r.XUID] = true
		if r.Gamertag == "" || !utf8.ValidString(r.Gamertag) || utf8.RuneCountInString(r.Gamertag) > maxGamertag || strings.ContainsFunc(r.Gamertag, unicode.IsControl) {
			return 0, errors.New("that is not a gamertag")
		}
		name := Fold(r.Gamertag)
		holders[name] = append(holders[name], r.XUID)
		if len(r.Head) == 0 {
			continue
		}
		// The bytes are the agent's crop of a skin a player made. They are
		// decoded within bounds and encoded again here, so nothing of the
		// original encoding reaches a browser.
		if len(r.Head) > maxHeadBytes {
			refused++
			continue
		}
		png, err := Clean(r.Head, minHeadSide, maxHeadSide, true)
		if err != nil {
			refused++
			continue
		}
		sum := sha256.Sum256(png)
		byXUID[r.XUID] = Head{PNG: png, Version: hex.EncodeToString(sum[:8])}
	}
	xuidOf, nameOf := map[string]string{}, map[string]string{}
	for name, xuids := range holders {
		// Two players under one gamertag cannot be told apart by it, and a
		// marker with no head is better than one with somebody else's.
		if len(xuids) == 1 {
			xuidOf[name], nameOf[xuids[0]] = xuids[0], name
		}
	}
	h.mu.Lock()
	h.at, h.byXUID, h.xuidOf, h.nameOf = h.now(), byXUID, xuidOf, nameOf
	h.mu.Unlock()
	metricHeads.Set(float64(len(byXUID)))
	metricHeadsRefused.Add(float64(refused))
	return refused, nil
}

// fresh reports whether the last report still stands. The caller holds mu.
func (h *Heads) fresh() bool {
	ttl := h.TTL
	if ttl <= 0 {
		ttl = DefaultHeadsTTL
	}
	return !h.at.IsZero() && h.now().Sub(h.at) <= ttl
}

// ByGamertag is the head of the one online player holding this gamertag.
func (h *Heads) ByGamertag(gamertag string) (Head, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if !h.fresh() {
		return Head{}, false
	}
	xuid, ok := h.xuidOf[Fold(gamertag)]
	if !ok {
		return Head{}, false
	}
	head, ok := h.byXUID[xuid]
	return head, ok
}

// Listing is the version of every head that can be asked for, by folded
// gamertag, and the gamertag the player with this XUID is online under
// now, which is empty if they are not or share it.
func (h *Heads) Listing(xuid string) (versions map[string]string, self string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	versions = map[string]string{}
	if !h.fresh() {
		return versions, ""
	}
	for name, holder := range h.xuidOf {
		if head, ok := h.byXUID[holder]; ok {
			versions[name] = head.Version
		}
	}
	return versions, h.nameOf[xuid]
}
