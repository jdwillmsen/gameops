package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	maxInviteFailures = 10
	inviteWindow      = time.Minute
	maxInviteBytes    = 256
)

// Invites checks one-time codes against a mounted Secret directory. A
// missing or empty directory is not an error: it means nobody can enrol.
type Invites struct {
	dir  string
	path string
	now  func() time.Time

	mu    sync.Mutex
	used  map[string]bool // hex SHA-256 of codes already spent
	fails []time.Time
}

func loadInvites(dir, usedPath string, now func() time.Time) (*Invites, error) {
	iv := &Invites{dir: dir, path: usedPath, now: now, used: map[string]bool{}}
	raw, err := os.ReadFile(usedPath)
	if errors.Is(err, os.ErrNotExist) {
		return iv, nil
	}
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	for _, h := range list {
		iv.used[h] = true
	}
	return iv, nil
}

func codeHash(code string) string {
	s := sha256.Sum256([]byte(code))
	return hex.EncodeToString(s[:])
}

// matches reports whether code is the operator's current invite, spent or
// not. It compares digests, so the lengths are equal and the comparison is
// constant time whatever the guess; a missing file compares against zeros
// so that it costs the same as a wrong code.
func (iv *Invites) matches(op, code string) bool {
	var want [sha256.Size]byte
	have := false
	if iv.dir != "" && operatorID.MatchString(op) {
		if raw, err := os.ReadFile(filepath.Join(iv.dir, op)); err == nil && len(raw) <= maxInviteBytes {
			if v := strings.TrimSpace(string(raw)); v != "" {
				want, have = sha256.Sum256([]byte(v)), true
			}
		}
	}
	got := sha256.Sum256([]byte(code))
	match := subtle.ConstantTimeCompare(want[:], got[:]) == 1
	return have && code != "" && match
}

// valid reports whether code is the operator's current invite and unspent.
// Called with the lock held.
func (iv *Invites) valid(op, code string) bool {
	return iv.matches(op, code) && !iv.used[codeHash(code)]
}

func (iv *Invites) Valid(op, code string) bool {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	return iv.valid(op, code)
}

// Spend re-checks the code and marks it used in one step, so two
// ceremonies holding the same code cannot both finish.
func (iv *Invites) Spend(op, code string) (bool, error) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	if !iv.valid(op, code) {
		return false, nil
	}
	h := codeHash(code)
	iv.used[h] = true
	if err := iv.save(); err != nil {
		delete(iv.used, h)
		return false, err
	}
	return true, nil
}

// Unspend gives a code back when the enrolment it paid for did not happen.
func (iv *Invites) Unspend(code string) {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	h := codeHash(code)
	delete(iv.used, h)
	if err := iv.save(); err != nil {
		iv.used[h] = true // spent is the safe side
	}
}

func (iv *Invites) save() error {
	list := make([]string, 0, len(iv.used))
	for h := range iv.used {
		list = append(list, h)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return writeAtomic(iv.path, raw)
}

// Throttled is true after too many wrong codes in a short while. Codes are
// long and random, so this only bounds the cost of guessing, not the odds.
func (iv *Invites) Throttled() bool {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	iv.trim()
	return len(iv.fails) >= maxInviteFailures
}

func (iv *Invites) Failed() {
	iv.mu.Lock()
	defer iv.mu.Unlock()
	iv.trim()
	iv.fails = append(iv.fails, iv.now())
}

func (iv *Invites) trim() {
	cut := iv.now().Add(-inviteWindow)
	i := 0
	for i < len(iv.fails) && !iv.fails[i].After(cut) {
		i++
	}
	iv.fails = iv.fails[i:]
}
