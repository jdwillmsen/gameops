package auth

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/atomicfile"
)

const (
	PendingFor    = 2 * time.Minute
	MaxPerPlayer  = 5
	MaxWrongCodes = 3
)

var (
	ErrPendingExists  = errors.New("a passkey is already waiting to be confirmed")
	ErrTooMany        = errors.New("that player holds as many passkeys as are allowed")
	ErrDuplicate      = errors.New("that credential is already registered")
	ErrNothingPending = errors.New("there is no passkey waiting to be confirmed")
	ErrWrongCode      = errors.New("that code is not the one sent")
	ErrAbandoned      = errors.New("too many wrong codes; start again")
	ErrCounter        = errors.New("the authenticator's counter did not move forward")
	ErrUnknownKey     = errors.New("no such passkey")
	ErrWrongHandle    = errors.New("that credential names a different user handle")
)

type Passkey struct {
	ID             []byte    `json:"id"`
	PublicKey      []byte    `json:"publicKey"`
	UserHandle     []byte    `json:"userHandle"`
	SignCount      uint32    `json:"signCount"`
	BackupEligible bool      `json:"backupEligible"`
	BackupState    bool      `json:"backupState"`
	XUID           string    `json:"xuid"`
	Gamertag       string    `json:"gamertag"`
	Label          string    `json:"label"`
	Created        time.Time `json:"created"`
	LastUsed       time.Time `json:"lastUsed"`
}

type Listed struct {
	Key      string    `json:"id"`
	Label    string    `json:"label"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"lastUsed,omitzero"`
	Pending  bool      `json:"pending,omitempty"`
}

type waitingKey struct {
	key         Passkey
	issuedMilli int64
	expires     time.Time
	code        string
	wrong       int
}

// Passkeys holds every passkey the map knows. Active ones are on disk;
// pending ones, and their codes, only in memory, so a restart drops a
// registration half made and the page starts it again.
type Passkeys struct {
	path string
	now  func() time.Time
	// Counted is told how many of each kind there are, after every change.
	Counted func(pending, active int)
	// Expired is told of each pending credential that ran out unconfirmed.
	Expired func()

	mu      sync.Mutex
	active  map[string]*Passkey    // by KeyOf(ID)
	handles map[string][]byte      // XUID to user handle
	waiting map[string]*waitingKey // by XUID
}

func LoadPasskeys(path string, now func() time.Time) (*Passkeys, error) {
	p := &Passkeys{path: path, now: now, active: map[string]*Passkey{}, handles: map[string][]byte{}, waiting: map[string]*waitingKey{}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Passkey
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("passkeys %s: %w", path, err)
	}
	for i := range list {
		k := list[i]
		p.active[KeyOf(k.ID)] = &k
		p.handles[k.XUID] = k.UserHandle
	}
	return p, nil
}

func sum(id []byte) [sha256.Size]byte { return sha256.Sum256(id) }

func KeyOf(id []byte) string { s := sum(id); return encoding.EncodeToString(s[:16]) }

func Fingerprint(id []byte) string {
	s := sum(id)
	h := strings.ToUpper(hex.EncodeToString(s[:4]))
	return h[:4] + "-" + h[4:]
}

func LogName(id []byte) string { s := sum(id); return hex.EncodeToString(s[:4]) }

func (p *Passkeys) Active(key, xuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[key]
	return ok && k.XUID == xuid
}

func (p *Passkeys) UserHandle(xuid string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.handles[xuid]; ok {
		return h
	}
	h := randomBytes(32)
	p.handles[xuid] = h
	return h
}

func (p *Passkeys) Existing(xuid string) []Passkey {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Passkey
	for _, k := range p.active {
		if k.XUID == xuid {
			out = append(out, *k)
		}
	}
	return out
}

// prune drops pending registrations past their two minutes. Called with
// the lock held.
func (p *Passkeys) prune() {
	now := p.now()
	gone := false
	for xuid, w := range p.waiting {
		if !now.Before(w.expires) {
			delete(p.waiting, xuid)
			gone = true
			if p.Expired != nil {
				p.Expired()
			}
		}
	}
	if gone {
		p.counted()
	}
}

func (p *Passkeys) StartPending(k Passkey, issuedMilli int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	if _, ok := p.waiting[k.XUID]; ok {
		return ErrPendingExists
	}
	if _, held := p.active[KeyOf(k.ID)]; held {
		return ErrDuplicate
	}
	for _, w := range p.waiting {
		if bytes.Equal(w.key.ID, k.ID) {
			return ErrDuplicate
		}
	}
	n := 0
	for _, a := range p.active {
		if a.XUID == k.XUID {
			n++
		}
	}
	if n >= MaxPerPlayer {
		return ErrTooMany
	}
	// The handle belongs to the XUID, not to the caller: a credential made
	// under another one would re-point the player and lock out their passkeys.
	h, ok := p.handles[k.XUID]
	if !ok || len(h) == 0 {
		h = randomBytes(32)
		p.handles[k.XUID] = h
	}
	if len(k.UserHandle) != 0 && subtle.ConstantTimeCompare(k.UserHandle, h) != 1 {
		return ErrWrongHandle
	}
	k.UserHandle = h
	k.Created = p.now().UTC()
	p.waiting[k.XUID] = &waitingKey{key: k, issuedMilli: issuedMilli, expires: p.now().Add(PendingFor)}
	p.counted()
	return nil
}

func (p *Passkeys) Cancel(xuid string, issuedMilli int64) (Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok || w.issuedMilli != issuedMilli {
		return Passkey{}, false
	}
	delete(p.waiting, xuid)
	p.counted()
	return w.key, true
}

// IssueCode returns the code and the ID of the credential it confirms, from
// which the caller makes the fingerprint and the log name.
func (p *Passkeys) IssueCode(xuid string) (string, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok {
		return "", nil, ErrNothingPending
	}
	code := random(codeLength)
	for code == w.code {
		code = random(codeLength)
	}
	w.code = code
	return code, w.key.ID, nil
}

// Confirm activates the pending credential only for the session that made
// it, the credential that session's page created, and the code sent for
// that credential.
func (p *Passkeys) Confirm(xuid string, issuedMilli int64, credentialID []byte, code string) (Passkey, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	w, ok := p.waiting[xuid]
	if !ok || w.issuedMilli != issuedMilli {
		return Passkey{}, 0, ErrNothingPending
	}
	right := w.code != "" && bytes.Equal(w.key.ID, credentialID) &&
		subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(w.code)) == 1
	if !right {
		w.wrong++
		if w.wrong >= MaxWrongCodes {
			delete(p.waiting, xuid)
			p.counted()
			return Passkey{}, 0, ErrAbandoned
		}
		return Passkey{}, MaxWrongCodes - w.wrong, ErrWrongCode
	}
	delete(p.waiting, xuid)
	k := w.key
	p.active[KeyOf(k.ID)] = &k
	if err := p.save(); err != nil {
		delete(p.active, KeyOf(k.ID))
		return Passkey{}, 0, err
	}
	p.counted()
	return k, 0, nil
}

func (p *Passkeys) IsPending(id []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	for _, w := range p.waiting {
		if bytes.Equal(w.key.ID, id) {
			return true
		}
	}
	return false
}

func (p *Passkeys) HasPending(xuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	_, ok := p.waiting[xuid]
	return ok
}

// Lookup finds the credential an assertion names, active or pending, among
// the credentials of the player whose handle it carries and no other's.
func (p *Passkeys) Lookup(handle, id []byte) (string, Passkey, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	for xuid, h := range p.handles {
		if len(handle) == 0 || subtle.ConstantTimeCompare(h, handle) != 1 {
			continue
		}
		if k, ok := p.active[KeyOf(id)]; ok && k.XUID == xuid {
			return xuid, *k, false, true
		}
		if w, ok := p.waiting[xuid]; ok && bytes.Equal(w.key.ID, id) {
			return xuid, w.key, true, true
		}
		return "", Passkey{}, false, false
	}
	return "", Passkey{}, false, false
}

func (p *Passkeys) ByHandle(handle []byte) (string, []Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for xuid, h := range p.handles {
		if len(handle) == 0 || subtle.ConstantTimeCompare(h, handle) != 1 {
			continue
		}
		var keys []Passkey
		for _, k := range p.active {
			if k.XUID == xuid {
				keys = append(keys, *k)
			}
		}
		return xuid, keys, len(keys) > 0
	}
	return "", nil, false
}

// Use records a login. Reading, judging and writing the count are one step
// under the lock, so two logins with one count cannot both pass.
func (p *Passkeys) Use(id []byte, signCount uint32, backupEligible, backupState bool) (Passkey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[KeyOf(id)]
	if !ok {
		return Passkey{}, ErrUnknownKey
	}
	if backupEligible != k.BackupEligible {
		return Passkey{}, ErrCounter
	}
	if (signCount != 0 || k.SignCount != 0) && signCount <= k.SignCount {
		return Passkey{}, ErrCounter
	}
	before := *k
	k.SignCount, k.BackupState, k.LastUsed = signCount, backupState, p.now().UTC()
	if err := p.save(); err != nil {
		*k = before
		return Passkey{}, err
	}
	return *k, nil
}

func (p *Passkeys) List(xuid string) []Listed {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	var out []Listed
	for key, k := range p.active {
		if k.XUID == xuid {
			out = append(out, Listed{Key: key, Label: k.Label, Created: k.Created, LastUsed: k.LastUsed})
		}
	}
	if w, ok := p.waiting[xuid]; ok {
		out = append(out, Listed{Key: KeyOf(w.key.ID), Label: w.key.Label, Created: w.key.Created, Pending: true})
	}
	return out
}

func (p *Passkeys) Remove(xuid, key string) (Passkey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k, ok := p.active[key]
	if !ok || k.XUID != xuid {
		return Passkey{}, ErrUnknownKey
	}
	delete(p.active, key)
	if err := p.save(); err != nil {
		p.active[key] = k
		return Passkey{}, err
	}
	p.counted()
	return *k, nil
}

func (p *Passkeys) RemoveAll(xuid string) ([]Passkey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var gone []Passkey
	removed := map[string]*Passkey{}
	for key, k := range p.active {
		if k.XUID == xuid {
			gone = append(gone, *k)
			removed[key] = k
			delete(p.active, key)
		}
	}
	if err := p.save(); err != nil {
		for key, k := range removed {
			p.active[key] = k
		}
		return nil, err
	}
	if w, ok := p.waiting[xuid]; ok {
		gone = append(gone, w.key)
		delete(p.waiting, xuid)
	}
	p.counted()
	return gone, nil
}

func (p *Passkeys) RefreshGamertag(xuid, gamertag string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	changed := false
	for _, k := range p.active {
		if k.XUID == xuid && k.Gamertag != gamertag {
			k.Gamertag, changed = gamertag, true
		}
	}
	if changed {
		_ = p.save()
	}
}

func (p *Passkeys) Counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	return len(p.waiting), len(p.active)
}

func (p *Passkeys) counted() {
	if p.Counted != nil {
		p.Counted(len(p.waiting), len(p.active))
	}
}

func (p *Passkeys) save() error {
	list := make([]Passkey, 0, len(p.active))
	for _, k := range p.active {
		list = append(list, *k)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return atomicfile.Write(p.path, raw)
}
