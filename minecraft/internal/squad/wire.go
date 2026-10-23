package squad

import (
	"sort"
	"time"
)

// Squad is the model the design speaks of: a named group of units, the
// roles they claimed on hello, and whoever leads.
type Squad struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Members   []Member `json:"members"`
	Leader    string   `json:"leader"`
	Battalion string   `json:"battalion,omitempty"`
}

// Member is one unit in its squad. Its identity is the roster id it posted
// with; the role is what it claimed, and the whole squad sees one claim.
type Member struct {
	UnitID string `json:"unit"`
	Role   string `json:"role"`
}

// Battalion is the nesting a squad can sit under, data only: no behaviour
// is built into battalions yet, and Leader stays empty until something can
// claim it.
type Battalion struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Squads []string `json:"squads"`
	Leader string   `json:"leader"`
}

// MemberView is what the console and a unit's poll see of a member: its
// claim plus the newest report it gave. Alive means not reported dead; a
// member with no report yet counts as alive, as the chain of command does.
type MemberView struct {
	Unit   string    `json:"unit"`
	Role   string    `json:"role"`
	HP     float64   `json:"hp"`
	Food   int       `json:"food"`
	Weapon string    `json:"weapon"`
	Pos    position  `json:"pos"`
	Alive  bool      `json:"alive"`
	Fresh  bool      `json:"fresh"`
	At     time.Time `json:"at,omitempty"`
}

// View is a squad as one read: the model plus every member's last report,
// both in chain-of-command order, so a reader pairs them up by position.
type View struct {
	Squad
	Members []MemberView `json:"members"`
}

// Delta is what a unit's poll returns: the new cursor to read against, the
// revision of what the leader line said when the unit last asked, the
// standing order, the leader and where it stands, and everything appended
// since the unit's cursor.
type Delta struct {
	// Cursor is the squad's newest seq value.
	Cursor int64 `json:"cursor"`
	// Rev changes whenever the leader identity or its position does, so a
	// unit that echoes back the revision it last saw answers at once when
	// the chain of command moves, even with no new messages.
	Rev      int64         `json:"rev"`
	Leader   *LeaderView   `json:"leader,omitempty"`
	Order    *wireOrder    `json:"order"`
	Orders   []wireOrder   `json:"orders"`
	Callouts []wireCallout `json:"callouts"`
}

// LeaderView names the leader and where it was last seen standing. Pos is
// absent when the leader has not reported yet, so a unit waits for one
// rather than guessing at the origin.
type LeaderView struct {
	Unit string    `json:"unit"`
	Pos  *position `json:"pos,omitempty"`
}

// wireOrder and wireCallout match the shapes squadmsg shares on both route
// ends; body and read sites at the unit and here must rename in step.
type wireOrder struct {
	Kind   string    `json:"kind"`
	Target string    `json:"target,omitempty"`
	By     string    `json:"by,omitempty"`
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
}

type wireCallout struct {
	Kind string    `json:"kind"`
	From string    `json:"from"`
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
}

// chainOrder sorts members by the chain of command: rank, then unit id, so
// two reads of the same state agree.
func chainOrder(sq *squadState) []*memberState {
	mems := make([]*memberState, 0, len(sq.members))
	for _, m := range sq.members {
		mems = append(mems, m)
	}
	sort.Slice(mems, func(i, j int) bool {
		if rank(mems[i].role) != rank(mems[j].role) {
			return rank(mems[i].role) < rank(mems[j].role)
		}
		return mems[i].unit < mems[j].unit
	})
	return mems
}

// render builds the squad's view with the mutex held.
func (st *Store) render(sq *squadState, now time.Time) View {
	name := sq.name
	if name == "" {
		name = sq.id
	}
	v := View{Squad: Squad{
		ID:        sq.id,
		Name:      name,
		Battalion: sq.battalion,
		Leader:    st.leaderOf(sq, now),
	}}
	v.Members = make([]MemberView, 0, len(sq.members))
	for _, m := range chainOrder(sq) {
		v.Squad.Members = append(v.Squad.Members, Member{UnitID: m.unit, Role: string(m.role)})
		v.Members = append(v.Members, MemberView{
			Unit:   m.unit,
			Role:   string(m.role),
			HP:     m.hp,
			Food:   m.food,
			Weapon: m.weapon,
			Pos:    m.pos,
			Alive:  !m.hasHP || m.hp > 0,
			Fresh:  now.Sub(m.heard) <= leaderGrace,
			At:     m.heard,
		})
	}
	return v
}

// Get one squad's view, pruned to what is still speaking.
func (st *Store) Get(id string) (View, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok {
		return View{}, false
	}
	return st.render(sq, now), true
}

// List lists every squad in id order.
func (st *Store) List() []View {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	ids := make([]string, 0, len(st.squads))
	for id := range st.squads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]View, 0, len(ids))
	for _, id := range ids {
		out = append(out, st.render(st.squads[id], now))
	}
	return out
}

// Battalions groups live squads under their parents, data only. Names come
// from a hello; a parent with no name yet displays its id.
func (st *Store) Battalions() []Battalion {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	return st.battalionsLocked()
}

// battalionsLocked is the console snapshot's share of Battalions. Callers
// hold the mutex.
func (st *Store) battalionsLocked() []Battalion {
	grouped := map[string][]string{}
	for _, sq := range st.squads {
		if sq.battalion == "" {
			continue
		}
		grouped[sq.battalion] = append(grouped[sq.battalion], sq.id)
	}
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Battalion, 0, len(ids))
	for _, id := range ids {
		squads := grouped[id]
		sort.Strings(squads)
		name := st.bats[id]
		if name == "" {
			name = id
		}
		out = append(out, Battalion{ID: id, Name: name, Squads: squads})
	}
	return out
}

// Has reports whether a squad with that id exists right now.
func (st *Store) Has(squadID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	_, ok := st.squads[squadID]
	return ok
}

// Member reports whether the unit currently claims a place in the squad.
// Call-outs ride on it: a voice that does not belong to the squad is not
// the squad's news.
func (st *Store) Member(squadID, unit string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	sq, ok := st.squads[squadID]
	return ok && sq.members[unit] != nil
}

// Delta builds one unit-facing poll answer. ok is false for a squad the
// store has never heard of.
func (st *Store) Delta(id string, since int64) (Delta, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	st.prune(now)
	sq, ok := st.squads[id]
	if !ok {
		return Delta{}, false
	}
	// A cursor past the squad's own log belongs to a unit that read a log
	// this server no longer has: a restart of crew-control winds seq back
	// to 0 while the unit keeps its cursor, and no message would ever
	// carry new seq past it again. Read it as a fresh resync instead.
	if since > sq.seq {
		since = 0
	}
	d := Delta{Cursor: sq.seq, Rev: sq.rev}
	// A cursor older than what is kept would deliver a hole as "nothing
	// new"; back it up to the oldest kept entry so the resyncing unit gets
	// every message that still exists, standing order included.
	oldest := sq.seq
	for _, o := range sq.orders {
		oldest = min(oldest, o.seq)
	}
	for _, c := range sq.callouts {
		oldest = min(oldest, c.seq)
	}
	since = max(since, oldest-1)
	if len(sq.orders) > 0 {
		last := sq.orders[len(sq.orders)-1]
		d.Order = &wireOrder{Kind: last.kind, Target: last.target, By: last.by, Seq: last.seq, At: last.at}
	}
	for _, o := range sq.orders {
		if o.seq > since {
			d.Orders = append(d.Orders, wireOrder{Kind: o.kind, Target: o.target, By: o.by, Seq: o.seq, At: o.at})
		}
	}
	for _, c := range sq.callouts {
		if c.seq > since {
			d.Callouts = append(d.Callouts, wireCallout{Kind: c.kind, From: c.from, Seq: c.seq, At: c.at})
		}
	}
	d.Leader = st.leaderView(sq, now)
	return d, true
}

// leaderView names the leader and where it stands, for the unit-facing
// poll. A leader with no status report yet has no position.
func (st *Store) leaderView(sq *squadState, now time.Time) *LeaderView {
	id := st.leaderOf(sq, now)
	if id == "" {
		return nil
	}
	m, ok := sq.members[id]
	if !ok || !m.hasHP {
		return &LeaderView{Unit: id}
	}
	p := m.pos
	return &LeaderView{Unit: id, Pos: &p}
}

// touchState bumps a squad's rev whenever what a poll's leader line says
// changed: a different leader, or the same one standing elsewhere. Orders
// and call-outs move the cursor instead. Callers hold the mutex.
func (st *Store) touchState(sq *squadState, now time.Time) {
	var s stateStamp
	if lv := st.leaderView(sq, now); lv != nil {
		s.unit = lv.Unit
		if lv.Pos != nil {
			s.hasPos = true
			s.pos = *lv.Pos
		}
	}
	if s != sq.lastLead {
		sq.rev++
		sq.lastLead = s
	}
}
