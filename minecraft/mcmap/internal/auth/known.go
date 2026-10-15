package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/atomicfile"
)

// KnownFor is how long after a player was last seen typing a code in game
// they stay known. A player who has left, or been banned, then stops being
// able to sign in without anyone remembering to remove them.
const KnownFor = 180 * 24 * time.Hour

// knownRefresh keeps a player who logs in often from rewriting the file
// each time; it is far below KnownFor, so it moves no expiry that matters.
const knownRefresh = time.Hour

type knownEntry struct {
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// Known is the players who have been seen on the server: an XUID, and when
// it was first and last seen, and nothing about who they are. Signing in
// with an Xbox account is open to them and to no other stranger.
type Known struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	players map[string]knownEntry
}

// LoadKnown reads the list at path; a missing file is an empty list.
func LoadKnown(path string, now func() time.Time) (*Known, error) {
	k := &Known{path: path, now: now, players: map[string]knownEntry{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return k, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &k.players); err != nil {
		return nil, fmt.Errorf("known players %s: %w", path, err)
	}
	return k, nil
}

// Record notes that xuid was seen on the server now. It is on disk before
// it returns nil; on error nothing has changed.
func (k *Known) Record(xuid string) error {
	if !IsXUID(xuid) {
		return errors.New("a player's XUID is a number")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now().UTC()
	previous, had := k.players[xuid]
	next := knownEntry{First: now, Last: now}
	if had && !k.expired(previous, now) {
		if previous.Last.After(now.Add(-knownRefresh)) && !previous.Last.After(now) {
			return nil
		}
		next.First = previous.First
	}
	k.players[xuid] = next
	if err := k.save(); err != nil {
		if had {
			k.players[xuid] = previous
		} else {
			delete(k.players, xuid)
		}
		return err
	}
	return nil
}

// Forget removes xuid at once, on disk and in memory, so that the player is
// unknown until the next claim. The file is only read at start, so editing
// it by hand would change nothing while the map runs and the next write
// would put the entry back.
func (k *Known) Forget(xuid string) error {
	if !IsXUID(xuid) {
		return errors.New("a player's XUID is a number")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	previous, had := k.players[xuid]
	if !had {
		return nil
	}
	delete(k.players, xuid)
	if err := k.save(); err != nil {
		k.players[xuid] = previous
		return err
	}
	return nil
}

// Has reports whether xuid was seen within KnownFor.
func (k *Known) Has(xuid string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.players[xuid]
	return ok && !k.expired(e, k.now())
}

// A last-seen in the future, from a clock stepped back or a file edited by
// hand, is not evidence of anything and does not extend the period.
func (k *Known) expired(e knownEntry, now time.Time) bool {
	return e.Last.After(now) || now.Sub(e.Last) >= KnownFor
}

func (k *Known) save() error {
	raw, err := json.Marshal(k.players)
	if err == nil {
		err = atomicfile.Write(k.path, raw)
	}
	if err != nil {
		return fmt.Errorf("known players %s: %w", k.path, err)
	}
	return nil
}
