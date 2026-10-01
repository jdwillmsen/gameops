package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Revocations is the one piece of login state kept server-side. A session is
// only a signed cookie, so it cannot be withdrawn by itself; what can be
// withdrawn is everything a player was issued up to a moment. That is enough
// for a player who typed a code off someone else's screen, and for removing
// a player who has left.
//
// One entry per player who has ever logged out this way, so the file stays
// as small as the server's player list.
type Revocations struct {
	path string

	mu        sync.RWMutex
	notBefore map[string]int64 // XUID to Unix milliseconds
}

// LoadRevocations reads the list at path; a missing file is an empty list.
func LoadRevocations(path string) (*Revocations, error) {
	r := &Revocations{path: path, notBefore: map[string]int64{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &r.notBefore); err != nil {
		return nil, fmt.Errorf("revocations %s: %w", path, err)
	}
	return r, nil
}

// Revoke ends every session xuid was issued up to at. It is on disk before
// it returns, so a restart cannot bring those sessions back.
func (r *Revocations) Revoke(xuid string, at time.Time) error {
	if !IsXUID(xuid) {
		return errors.New("a player's XUID is a number")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, had := r.notBefore[xuid]
	r.notBefore[xuid] = max(previous, at.UnixMilli())
	raw, err := json.Marshal(r.notBefore)
	if err == nil {
		err = writeWhole(r.path, raw)
	}
	if err != nil {
		if had {
			r.notBefore[xuid] = previous
		} else {
			delete(r.notBefore, xuid)
		}
		return fmt.Errorf("revocations %s: %w", r.path, err)
	}
	return nil
}

func (r *Revocations) covers(xuid string, issuedMilli int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	notBefore, ok := r.notBefore[xuid]
	return ok && issuedMilli <= notBefore
}

// writeWhole leaves either the old file or the new one at path, never part
// of one: a crash mid-write must not leave something that stops the service
// starting until a person deletes it.
func writeWhole(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// CreateTemp makes the file 0600, which is what a key needs.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
