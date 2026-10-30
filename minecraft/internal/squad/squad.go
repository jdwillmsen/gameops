// Package squad keeps the standing state of Crew squads in crew-control's
// memory, rebuilt from units' hellos and refreshed by their status reports.
// There is no database by design: the source of truth is whatever units
// said recently, so a restart empties the console until a hello arrives
// again, which every unit renews on its own.
package squad

// The wire types mirror what the unit side shares through its squadmsg
// package (which crew-control cannot import, being internal to crew); a
// field named here was named there, so renaming one means renaming both.
// They live in wire.go.

import (
	"sync"
	"time"
)

const (
	// A claim lapses this long after a member's last word. Units re-hello
	// every 30 s, so 90 s is three missed cycles: one lost report evicts
	// nobody, and a unit that stopped joining is truly gone.
	memberTTL = 90 * time.Second
	// A dead or silent leader holds command this long before the next in
	// the chain takes over, so one lost status beat does not hand
	// leadership back and forth. The design's N.
	leaderGrace = 8 * time.Second
	// How many orders and callouts are kept per squad. Pollers take deltas
	// from the newest cursor, so a log this deep serves a unit that
	// resynced after a long outage.
	logDepth = 32
)

// role mirrors the warrior role names a unit claims on the wire.
type role string

const (
	roleVanguard role = "vanguard"
	roleStriker  role = "striker"
	roleRanger   role = "ranger"
)

// rank orders roles by who leads first: the tank, then the damage, then the
// archer. A unit with no known role never leads, so it ranks last.
func rank(r role) int {
	switch r {
	case roleVanguard:
		return 0
	case roleStriker:
		return 1
	case roleRanger:
		return 2
	default:
		return 3
	}
}

// memberState is what the store holds per unit in a squad: its claim, its
// latest report, and when it last spoke.
type memberState struct {
	unit   string
	role   role
	hp     float64
	food   int
	weapon string
	pos    position
	// hasHP is false until the member's first status arrives, so a hello
	// without a report is not mistaken for a warrior with zero health.
	hasHP bool
	heard time.Time
}

// position is an x/y/z in world block coordinates, as read off the wire.
type position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// squadState is one squad in memory: the claims, the message log, the
// cursor that unit polls read deltas against, and the revision of what a
// poll names as its leader.
type squadState struct {
	id, name  string
	battalion string
	members   map[string]*memberState
	seq       int64
	orders    []orderLog
	callouts  []logEntry
	rev       int64
	lastLead  stateStamp
	// start is where the squad is to regroup, set only by an explicit post
	// (an automatic one from a report would go stale as the squad moves), and
	// hasStart says it is. It lapses after startTTL so a forgotten one cannot
	// outlive its fight: a wrong start is worse than none, since the engine
	// then regroups clear of the boss on its own.
	start    position
	hasStart bool
	startAt  time.Time

	// profile is the squad's current tactics profile, set only by the
	// profile route on a lab-marked squad; profileSeq versions it for the
	// unit polls, and like the store's whole life it is gone when the
	// squad's last claim lapses — the trainer renews it, as units renew
	// their claims.
	profile    *profileWire
	profileSeq int64

	// duel is the squad's current duel state, set by the duel route on a
	// lab-marked squad; duelSeq versions it for the unit polls and duelAt
	// lets it lapse.
	duel    *duelWire
	duelSeq int64
	duelAt  time.Time
}

// startTTL is how long a posted start point stays in the squad's answers.
const startTTL = 15 * time.Minute

// startPos is the start point while it is still in force.
func (sq *squadState) startPos(now time.Time) (position, bool) {
	if !sq.hasStart || now.Sub(sq.startAt) > startTTL {
		return position{}, false
	}
	return sq.start, true
}

// stateStamp is what the last read said about the leader and the roster:
// who leads and where it stands, who is down or silent, where the others
// stand to the block, and the start point. A change in it bumps the squad's
// rev, so a unit still polling with the old revision gets an answer instead
// of riding out its wait while the chain of command moves.
type stateStamp struct {
	unit     string
	hasPos   bool
	pos      position
	roster   string
	start    position
	hasStart bool
}

// logEntry is one call-out with its cursor value; orderLog adds what an
// order targets and who issued it. From names the unit that spoke.
type logEntry struct {
	seq  int64
	from string
	kind string
	at   time.Time
}

type orderLog struct {
	logEntry
	target string
	by     string
}

// Hello is a unit's claim to a place in a squad. It is the whole rebuild:
// a hello refreshes an existing member, adds a new one, and takes a unit
// that moved squads out of its old one.
type Hello struct {
	Unit          string
	Squad         string
	SquadName     string
	Role          role
	Battalion     string
	BattalionName string
}

// statusIn is one unit's report of how it is.
type statusIn struct {
	Unit   string
	Squad  string
	HP     float64
	Food   int
	Weapon string
	Pos    position
}

// calloutIn is a unit speaking up.
type calloutIn struct {
	Squad string
	Kind  string
	From  string
}

// orderIn is one instruction an operator gave the squad.
type orderIn struct {
	Squad  string
	Kind   string
	Target string
	By     string
}

// Store is the in-memory squad state. Safe for concurrent use; the state is
// small, so everything goes through one mutex.
type Store struct {
	now func() time.Time

	mu      sync.Mutex
	squads  map[string]*squadState
	unitsOf map[string]string // unit id -> squad id it currently claimed
	bats    map[string]string // battalion id -> display name
	// nameOf gives a unit id's in-game name, set once by Routes from the
	// roster. Nil means duels cannot name anyone.
	nameOf func(string) (string, bool)
}

func NewStore() *Store {
	return &Store{
		now:     time.Now,
		squads:  map[string]*squadState{},
		unitsOf: map[string]string{},
		bats:    map[string]string{},
	}
}

// prune drops members whose claim lapsed, then squads left empty, then
// battalion names nothing nests under any more. Callers hold the mutex.
func (st *Store) prune(now time.Time) {
	for id, sq := range st.squads {
		for unit, m := range sq.members {
			if now.Sub(m.heard) > memberTTL {
				delete(sq.members, unit)
				if st.unitsOf[unit] == id {
					delete(st.unitsOf, unit)
				}
			}
		}
		if len(sq.members) == 0 {
			delete(st.squads, id)
			continue
		}
		st.touchState(sq, now)
	}
	// A battalion's display name came from a hello; with no squad naming
	// the parent any more the name is dead weight nobody can resurrect
	// except another hello, which re-records it anyway.
	claimed := map[string]bool{}
	for _, sq := range st.squads {
		if sq.battalion != "" {
			claimed[sq.battalion] = true
		}
	}
	for id := range st.bats {
		if !claimed[id] {
			delete(st.bats, id)
		}
	}
}

// Hello applies a unit's claim; the API validated it first. A hello with an
// empty name lets the id stand in as the display name.
func (st *Store) Hello(h Hello) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	// A moved member leaves its old squad first, so it cannot sit on two
	// rosters after a redeploy.
	if old, ok := st.unitsOf[h.Unit]; ok && old != h.Squad {
		if sq, ok := st.squads[old]; ok {
			delete(sq.members, h.Unit)
			if len(sq.members) == 0 {
				delete(st.squads, old)
			}
		}
	}
	delete(st.unitsOf, h.Unit)

	sq, ok := st.squads[h.Squad]
	if !ok {
		sq = &squadState{id: h.Squad, members: map[string]*memberState{}}
		st.squads[h.Squad] = sq
	}
	// A hello that names no display name must not erase what an earlier
	// hello set: every member of the squad says the same claim, and only
	// one of them has to spell the name.
	if h.SquadName != "" {
		sq.name = h.SquadName
	}
	if h.Battalion != "" {
		sq.battalion = h.Battalion
	}
	m, ok := sq.members[h.Unit]
	if !ok {
		// A fresh member starts with no report: the hello refreshes the
		// claim, and the next status fills in the rest.
		m = &memberState{unit: h.Unit, heard: now}
		sq.members[h.Unit] = m
	}
	m.role = h.Role
	m.heard = now
	st.unitsOf[h.Unit] = h.Squad
	if h.Battalion != "" && h.BattalionName != "" {
		st.bats[h.Battalion] = h.BattalionName
	}
}

// Status applies one unit's report of how it is. Squads are rebuilt from
// hellos, so a report for a claim the store never saw is dropped.
func (st *Store) Status(s statusIn) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	// A report may name no squad: the unit's own claim says where it sits.
	if s.Squad == "" {
		id, ok := st.unitsOf[s.Unit]
		if !ok {
			return
		}
		s.Squad = id
	}
	sq, ok := st.squads[s.Squad]
	if !ok {
		return
	}
	m := sq.members[s.Unit]
	if m == nil {
		// Pruned or moved between the two lookups; preserve the
		// rebuild-from-hello rule rather than resurrecting membership.
		return
	}
	m.hp, m.food, m.weapon, m.pos = s.HP, s.Food, s.Weapon, s.Pos
	m.hasHP = true
	m.heard = now
}

// SetStart fixes the squad's start point, replacing any earlier one. It
// reports false for a squad the store does not hold.
func (st *Store) SetStart(squadID string, p position) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	sq, ok := st.squads[squadID]
	if !ok {
		return false
	}
	sq.start, sq.hasStart, sq.startAt = p, true, st.now()
	return true
}

// Callout records one unit's call-out.
func (st *Store) Callout(c calloutIn, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[c.Squad]
	if !ok {
		return
	}
	sq.seq++
	sq.callouts = append(sq.callouts, logEntry{seq: sq.seq, from: c.From, kind: c.Kind, at: at})
	if len(sq.callouts) > logDepth {
		sq.callouts = sq.callouts[len(sq.callouts)-logDepth:]
	}
}

// Order records one operator instruction to the squad.
func (st *Store) Order(o orderIn) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[o.Squad]
	if !ok {
		return
	}
	sq.seq++
	e := orderLog{logEntry: logEntry{seq: sq.seq, kind: o.Kind, at: now}, target: o.Target, by: o.By}
	sq.orders = append(sq.orders, e)
	if len(sq.orders) > logDepth {
		sq.orders = sq.orders[len(sq.orders)-logDepth:]
	}
}

// leadable reports whether m may lead right now: a report of zero health
// rules it out, as does silence after the grace, which is what gives the
// next in the chain time to take over without one lost status beat handing
// leadership back and forth. A member that has not reported yet leads by
// convention, so a squad of helloed units always has a leader.
func leadable(m *memberState, now time.Time, grace time.Duration) bool {
	if m.hasHP && m.hp <= 0 {
		return false
	}
	return now.Sub(m.heard) <= grace
}

// leaderOf picks the squad's leader: the fittest member by the chain of
// command, deterministic on rank and then unit id. Callers hold the mutex.
func (st *Store) leaderOf(sq *squadState, now time.Time) string {
	var best *memberState
	for _, m := range sq.members {
		if !leadable(m, now, leaderGrace) {
			continue
		}
		if best == nil || rank(m.role) < rank(best.role) ||
			(rank(m.role) == rank(best.role) && m.unit < best.unit) {
			best = m
		}
	}
	if best == nil {
		return ""
	}
	return best.unit
}
