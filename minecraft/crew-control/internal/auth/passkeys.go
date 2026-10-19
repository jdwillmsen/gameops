package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

const maxPasskeys = 5

var (
	ErrTooMany     = errors.New("that operator holds as many passkeys as are allowed")
	ErrDuplicate   = errors.New("that credential is already registered")
	ErrUnknownKey  = errors.New("no such passkey")
	ErrWrongHandle = errors.New("that credential names a different user handle")
	ErrCounter     = errors.New("the authenticator's counter did not move forward")
)

type Passkey struct {
	ID             []byte    `json:"id"`
	PublicKey      []byte    `json:"publicKey"`
	UserHandle     []byte    `json:"userHandle"`
	Operator       string    `json:"operator"`
	SignCount      uint32    `json:"signCount"`
	BackupEligible bool      `json:"backupEligible"`
	BackupState    bool      `json:"backupState"`
	Created        time.Time `json:"created"`
	LastUsed       time.Time `json:"lastUsed"`
}

// KeyOf names a credential in a session without carrying its id.
func KeyOf(id []byte) string {
	s := sha256.Sum256(id)
	return b64.EncodeToString(s[:16])
}

type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

// Passkeys is the on-disk store. The owner removes a passkey by editing the
// file, so every call first notices a changed file and reloads it; without
// that a deleted passkey would keep its sessions until a restart.
type Passkeys struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	stamp   fileStamp
	byKey   map[string]*Passkey
	handles map[string][]byte // operator to user handle
}

func loadPasskeys(path string, now func() time.Time) (*Passkeys, error) {
	p := &Passkeys{path: path, now: now}
	if err := p.reload(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Passkeys) stat() fileStamp {
	st, err := os.Stat(p.path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: st.ModTime(), size: st.Size(), ok: true}
}

func (p *Passkeys) reload() error {
	p.byKey, p.handles = map[string]*Passkey{}, map[string][]byte{}
	p.stamp = p.stat()
	if !p.stamp.ok {
		return nil
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		return err
	}
	var list []Passkey
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("passkeys %s: %w", p.path, err)
	}
	for i := range list {
		k := list[i]
		p.byKey[KeyOf(k.ID)] = &k
		p.handles[k.Operator] = k.UserHandle
	}
	return nil
}

// sync is called with the lock held. A file that cannot be read leaves the
// store empty, which signs everyone out rather than trusting stale state.
func (p *Passkeys) sync() {
	if p.stat() != p.stamp {
		_ = p.reload()
	}
}

func (p *Passkeys) Active(op, key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	k, ok := p.byKey[key]
	return ok && k.Operator == op
}

func (p *Passkeys) Existing(op string) []Passkey {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	return p.existing(op)
}

func (p *Passkeys) existing(op string) []Passkey {
	var out []Passkey
	for _, k := range p.byKey {
		if k.Operator == op {
			out = append(out, *k)
		}
	}
	return out
}

// UserHandle is stable per operator: a credential made under another handle
// would re-point the operator and strand the passkeys they already hold.
func (p *Passkeys) UserHandle(op string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	if h, ok := p.handles[op]; ok {
		return h
	}
	return randomBytes(32)
}

func (p *Passkeys) Add(k Passkey) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	key := KeyOf(k.ID)
	if _, held := p.byKey[key]; held {
		return ErrDuplicate
	}
	if len(p.existing(k.Operator)) >= maxPasskeys {
		return ErrTooMany
	}
	if h, ok := p.handles[k.Operator]; ok && subtle.ConstantTimeCompare(h, k.UserHandle) != 1 {
		return ErrWrongHandle
	}
	if len(k.UserHandle) < 16 {
		return ErrWrongHandle
	}
	k.Created = p.now().UTC()
	p.byKey[key] = &k
	prev, hadHandle := p.handles[k.Operator]
	p.handles[k.Operator] = k.UserHandle
	if err := p.save(); err != nil {
		delete(p.byKey, key)
		if hadHandle {
			p.handles[k.Operator] = prev
		} else {
			delete(p.handles, k.Operator)
		}
		return err
	}
	return nil
}

// Lookup finds the credential an assertion names among the credentials of
// the operator whose handle it carries, and no other's.
func (p *Passkeys) Lookup(handle, id []byte) (Passkey, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	k, ok := p.byKey[KeyOf(id)]
	if !ok || len(handle) == 0 || subtle.ConstantTimeCompare(k.UserHandle, handle) != 1 || !bytes.Equal(k.ID, id) {
		return Passkey{}, false
	}
	return *k, true
}

// Use records a login. Reading, judging and writing the count are one step
// under the lock, so two logins with one count cannot both pass.
func (p *Passkeys) Use(id []byte, signCount uint32, backupEligible, backupState bool) (Passkey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sync()
	k, ok := p.byKey[KeyOf(id)]
	if !ok {
		return Passkey{}, ErrUnknownKey
	}
	if backupEligible != k.BackupEligible {
		return Passkey{}, ErrCounter
	}
	// Synced passkeys keep no counter and always send 0.
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

func (p *Passkeys) save() error {
	list := make([]Passkey, 0, len(p.byKey))
	for _, k := range p.byKey {
		list = append(list, *k)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := writeAtomic(p.path, raw); err != nil {
		return err
	}
	p.stamp = p.stat()
	return nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// A guessable secret is worse than no service.
		panic(err)
	}
	return b
}
