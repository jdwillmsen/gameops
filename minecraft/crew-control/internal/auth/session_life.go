package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	// sessionMax is the longest a sign-in lasts however often it is renewed.
	sessionMax = 30 * 24 * time.Hour
	// A session is renewed once less than half its lifetime is left, so the
	// cookie is not rewritten on every poll.
	renewBelow = sessionTTL / 2
	maxRevoked = 256
)

func (s session) started() int64 {
	if s.Start != 0 {
		return s.Start
	}
	return s.Iat
}

// revocations remembers signed-out sessions until they would have expired
// anyway. It is kept on disk so a restart does not bring a signed-out
// cookie back to life.
type revocations struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	sids map[string]int64
}

func loadRevocations(path string, now func() time.Time) *revocations {
	r := &revocations{path: path, now: now, sids: map[string]int64{}}
	// A missing or unreadable file starts empty: refusing to start would lock
	// every operator out over a list that only ever shortens a session.
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &r.sids); err != nil {
			log.Printf("auth: signed-out list unreadable, starting empty: %v", err)
			r.sids = map[string]int64{}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("auth: signed-out list unreadable, starting empty: %v", err)
	}
	return r
}

func (r *revocations) has(sid string) bool {
	if sid == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	exp, ok := r.sids[sid]
	return ok && r.now().Unix() < exp
}

func (r *revocations) add(sid string, exp int64) {
	if sid == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().Unix()
	for k, v := range r.sids {
		if v <= now {
			delete(r.sids, k)
		}
	}
	// Past the cap the oldest entry goes: it is the one closest to expiring.
	for len(r.sids) >= maxRevoked {
		oldest, at := "", int64(0)
		for k, v := range r.sids {
			if oldest == "" || v < at {
				oldest, at = k, v
			}
		}
		log.Printf("auth: signed-out list full, dropping a live sign-out")
		delete(r.sids, oldest)
	}
	r.sids[sid] = exp
	if raw, err := json.Marshal(r.sids); err == nil {
		if err := writeAtomic(r.path, raw); err != nil {
			log.Printf("auth: sign-out not saved, it will not survive a restart: %v", err)
		}
	}
}

// renew gives a signed-in session that is past half its life a fresh
// cookie, to the sign-in's absolute limit. Callers have already checked it.
func (a *Auth) renew(w http.ResponseWriter, s session) session {
	now := a.now()
	if time.Unix(s.Exp, 0).Sub(now) >= renewBelow {
		return s
	}
	limit := time.Unix(s.started(), 0).Add(sessionMax)
	exp := now.Add(sessionTTL)
	if exp.After(limit) {
		exp = limit
	}
	if !exp.After(time.Unix(s.Exp, 0)) {
		return s
	}
	s.Iat, s.Exp, s.Start = now.Unix(), exp.Unix(), s.started()
	// Cookies issued before sessions carried an id could not be signed out.
	if s.Sid == "" {
		s.Sid = b64.EncodeToString(randomBytes(12))
	}
	a.setSession(w, s)
	return s
}
