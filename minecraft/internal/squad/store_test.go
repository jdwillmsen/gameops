package squad

import (
	"encoding/json"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func newSquadRig() (*Store, *fakeClock) {
	c := &fakeClock{t: time.Unix(0, 0)}
	st := NewStore()
	st.now = c.Now
	return st, c
}

func (c *fakeClock) after(d time.Duration) { c.t = c.t.Add(d) }

func (h *fakeClock) hello(st *Store, unit, squad, roleName string) {
	st.Hello(Hello{Unit: unit, Squad: squad, Role: role(roleName)})
}

func (h *fakeClock) status(st *Store, unit, squad string, hp float64) {
	st.Status(statusIn{Unit: unit, Squad: squad, HP: hp, Food: 10,
		Weapon: "diamond_sword", Pos: position{X: 1, Y: 2, Z: 3}})
}

func squadOf(t *testing.T, st *Store, id string) Squad {
	t.Helper()
	v, ok := st.Get(id)
	if !ok {
		t.Fatalf("squad %q missing", id)
	}
	return v.Squad
}

func TestRebuildFromHellosRosterAndLeader(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.hello(st, "crew-19", "alpha", "striker")
	cl.hello(st, "crew-20", "alpha", "ranger")

	s := squadOf(t, st, "alpha")
	if s.Name != "alpha" {
		t.Errorf("name %q, want the id standing in", s.Name)
	}
	if want := []string{"crew-18", "crew-19", "crew-20"}; len(s.Members) != 3 {
		t.Fatalf("members %v, want %v", s.Members, want)
	} else {
		for i, m := range s.Members {
			if m.UnitID != want[i] {
				t.Errorf("member[%d] = %s, want %s", i, m.UnitID, want[i])
			}
		}
	}
	if s.Leader != "crew-18" {
		t.Errorf("leader %q, want the vanguard", s.Leader)
	}
}

func TestHelloNamesTheSquadWithoutErasingIt(t *testing.T) {
	st, cl := newSquadRig()
	st.Hello(Hello{Unit: "crew-18", Squad: "alpha", Role: roleVanguard,
		SquadName: "First"})
	if s := squadOf(t, st, "alpha"); s.Name != "First" {
		t.Errorf("name %q, want the hello's display name", s.Name)
	}
	// A member that names no display name must not erase it.
	cl.hello(st, "crew-19", "alpha", "striker")
	if s := squadOf(t, st, "alpha"); s.Name != "First" {
		t.Errorf("name %q after a nameless hello, want First", s.Name)
	}
}

func TestChainOfCommand(t *testing.T) {
	classes := []struct{ unit, rank string }{
		{"crew-18", "vanguard"}, {"crew-19", "striker"}, {"crew-20", "ranger"},
	}
	join := func(st *Store, cl *fakeClock) {
		for _, m := range classes {
			cl.hello(st, m.unit, "alpha", m.rank)
		}
	}
	tests := []struct {
		name   string
		fixup  func(st *Store, cl *fakeClock)
		leader string
	}{
		{"healthy squad: vanguard leads", func(st *Store, cl *fakeClock) {}, "crew-18"},
		{"dead vanguard: striker leads", func(st *Store, cl *fakeClock) {
			cl.status(st, "crew-18", "alpha", 0)
		}, "crew-19"},
		{"dead vanguard and striker: ranger leads", func(st *Store, cl *fakeClock) {
			cl.status(st, "crew-18", "alpha", 0)
			cl.status(st, "crew-19", "alpha", 0)
		}, "crew-20"},
		{"silent leader past the grace: next in chain", func(st *Store, cl *fakeClock) {
			cl.after(leaderGrace + time.Second)
			// The other units keep reporting; only the leader goes quiet.
			cl.status(st, "crew-19", "alpha", 10)
			cl.status(st, "crew-20", "alpha", 10)
		}, "crew-19"},
		{"silent leader inside the grace keeps command", func(st *Store, cl *fakeClock) {
			cl.after(leaderGrace - time.Second)
			cl.status(st, "crew-19", "alpha", 10)
			cl.status(st, "crew-20", "alpha", 10)
		}, "crew-18"},
		{"a reported-alive vanguard keeps leading", func(st *Store, cl *fakeClock) {
			cl.status(st, "crew-18", "alpha", 4)
		}, "crew-18"},
		{"a wiped squad has no leader", func(st *Store, cl *fakeClock) {
			cl.status(st, "crew-18", "alpha", 0)
			cl.status(st, "crew-19", "alpha", 0)
			cl.status(st, "crew-20", "alpha", 0)
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, cl := newSquadRig()
			join(st, cl)
			tt.fixup(st, cl)
			if got := squadOf(t, st, "alpha").Leader; got != tt.leader {
				t.Errorf("leader %q, want %q", got, tt.leader)
			}
		})
	}
}

func TestHelloMovesUnitBetweenSquads(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.hello(st, "crew-19", "alpha", "striker")
	cl.hello(st, "crew-18", "beta", "vanguard")

	if got := squadOf(t, st, "alpha").Leader; got != "crew-19" {
		t.Errorf("alpha leader %q, want the remaining member", got)
	}
	if got := squadOf(t, st, "beta").Leader; got != "crew-18" {
		t.Errorf("beta leader %q, want the moved member", got)
	}
	if n := len(squadOf(t, st, "alpha").Members); n != 1 {
		t.Errorf("alpha kept %d members", n)
	}
}

func TestStatusFeedsTheViewNotTheClaim(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.status(st, "crew-18", "alpha", 12.5)

	v, ok := st.Get("alpha")
	if !ok {
		t.Fatal("squad gone")
	}
	m := v.Members[0]
	if m.HP != 12.5 || m.Food != 10 || m.Weapon != "diamond_sword" ||
		m.Pos.X != 1 || m.Pos.Y != 2 || m.Pos.Z != 3 || !m.Alive {
		t.Errorf("member view %+v does not carry the report", m)
	}
	// A report from a unit the store never heard hello is noise, not a
	// claim.
	cl.status(st, "crew-21", "alpha", 18)
	v, _ = st.Get("alpha")
	if len(v.Members) != 1 {
		t.Errorf("a status without hello created a member: %v", v.Members)
	}
}

func TestClaimsLapseAfterMemberTTL(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.after(memberTTL + time.Second)
	if _, ok := st.Get("alpha"); ok {
		t.Error("the squad kept a member three silence cycles past its claim")
	}
}

func TestStatusKeepsTheClaimFresh(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	for i := 0; i < 5; i++ {
		cl.after(30 * time.Second)
		cl.status(st, "crew-18", "alpha", 20)
	}
	cl.after(45 * time.Second) // inside the TTL thanks to the last status
	if _, ok := st.Get("alpha"); !ok {
		t.Error("a reporting warrior was pruned")
	}
	// Silence still costs leadership once the grace runs out.
	if got := squadOf(t, st, "alpha").Leader; got != "" {
		t.Errorf("leader %q, want none past the grace", got)
	}
}

func TestMessageLogDepthAndResyncingCursor(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	for i := 0; i < logDepth+3; i++ {
		st.Order(orderIn{Squad: "alpha", Kind: "focus", By: "owner"})
	}
	d, ok := st.Delta("alpha", 0)
	if !ok {
		t.Fatal("squad gone")
	}
	if n := len(d.Orders); n != logDepth {
		t.Errorf("a fresh cursor delivered %d orders, want the kept %d", n, logDepth)
	}
	older, _ := st.Delta("alpha", -100)
	if older.Cursor != d.Cursor || len(older.Orders) != len(d.Orders) {
		t.Errorf("a cursor older than the log changed the answer: %+v vs %+v", older, d)
	}
	if d.Order == nil || d.Order.Seq != d.Cursor {
		t.Errorf("standing order %+v, want the newest seq %d", d.Order, d.Cursor)
	}
}

func TestDeltaDistinguishesOrdersAndCallouts(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.hello(st, "crew-19", "alpha", "striker")
	st.Order(orderIn{Squad: "alpha", Kind: "regroup", By: "owner"})
	st.Callout(calloutIn{Squad: "alpha", Kind: "low_health", From: "crew-19"},
		time.Unix(7, 0))

	d, _ := st.Delta("alpha", 0)
	if d.Cursor != 2 {
		t.Errorf("cursor %d, want 2 for one order and one callout", d.Cursor)
	}
	if len(d.Orders) != 1 || d.Orders[0].Kind != "regroup" || d.Orders[0].By != "owner" {
		t.Errorf("orders %v", d.Orders)
	}
	if len(d.Callouts) != 1 || d.Callouts[0].Kind != "low_health" ||
		d.Callouts[0].From != "crew-19" {
		t.Errorf("callouts %v", d.Callouts)
	}
	// Everything is old at the new cursor; the standing order stays.
	d2, _ := st.Delta("alpha", 2)
	if len(d2.Orders) != 0 || len(d2.Callouts) != 0 {
		t.Errorf("deltas past the cursor: %v %v", d2.Orders, d2.Callouts)
	}
	if d2.Order == nil {
		t.Error("the standing order must ride a poll even with no delta")
	}
}

func TestLeaderViewCarriesPositionOnlyWhenReported(t *testing.T) {
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	d, _ := st.Delta("alpha", 0)
	if d.Leader == nil || d.Leader.Unit != "crew-18" || d.Leader.Pos != nil {
		t.Errorf("leader view %+v, want the unit alone before any status", d.Leader)
	}
	cl.status(st, "crew-18", "alpha", 15)
	d, _ = st.Delta("alpha", 0)
	if d.Leader == nil || d.Leader.Pos == nil || d.Leader.Pos.Y != 2 {
		t.Errorf("leader view %+v, want the reported position", d.Leader)
	}
}

func TestBattalionsCollectNamedParents(t *testing.T) {
	st, _ := newSquadRig()
	st.Hello(Hello{Unit: "crew-18", Squad: "alpha", Role: roleVanguard,
		Battalion: "bravo", BattalionName: "Second"})
	st.Hello(Hello{Unit: "crew-19", Squad: "gamma", Role: roleStriker,
		Battalion: "bravo", BattalionName: "Second"})
	st.Hello(Hello{Unit: "crew-20", Squad: "delta", Role: roleRanger})

	bats := st.Battalions()
	if len(bats) != 1 {
		t.Fatalf("battalions %+v", bats)
	}
	b := bats[0]
	if b.ID != "bravo" || b.Name != "Second" || len(b.Squads) != 2 || b.Leader != "" {
		t.Errorf("battalion %+v, want bravo named Second with two squads and no leader", b)
	}
	s := squadOf(t, st, "delta")
	if s.Battalion != "" {
		t.Errorf("unparented squad carries a battalion %q", s.Battalion)
	}
}

// The name of a battalion nothing nests under any more is dead weight: the
// last squad's claim lapses and the name goes with it, instead of a stale
// parent surviving in the console for ever.
func TestBattalionNamesGoWhenTheirLastSquadGoes(t *testing.T) {
	st, cl := newSquadRig()
	st.Hello(Hello{Unit: "crew-18", Squad: "alpha", Role: roleVanguard,
		Battalion: "bravo", BattalionName: "Second"})
	if n := len(st.Battalions()); n != 1 {
		t.Fatalf("battalions %d, want 1 while the squad lives", n)
	}
	cl.after(memberTTL + time.Second)
	st.Get("alpha") // a prune tick with nobody left to claim anything
	if n := len(st.Battalions()); n != 0 {
		t.Errorf("battalions %d, want the empty parent gone", n)
	}
	if len(st.bats) != 0 {
		t.Errorf("the store kept %d battalion names for squads that are gone", len(st.bats))
	}
}

func TestDeltaIsDecodableAsItsOwnWire(t *testing.T) {
	// The unit side reads these shapes without this package, so the real
	// check is that a delta survives marshalling and reads back named as
	// the design says.
	st, cl := newSquadRig()
	cl.hello(st, "crew-18", "alpha", "vanguard")
	cl.status(st, "crew-18", "alpha", 15)
	st.Order(orderIn{Squad: "alpha", Kind: "focus", Target: "skeleton-3", By: "owner"})
	d, _ := st.Delta("alpha", 0)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Cursor int64
		Leader *struct {
			Unit string
			Pos  *struct {
				X, Y, Z float64
			}
		}
		Order *struct {
			Kind, Target string
		}
		Orders   []any
		Callouts []any
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatalf("a delta is not decodable as its own wire: %s", err)
	}
	if wire.Leader == nil || wire.Leader.Pos == nil || wire.Leader.Pos.X != 1 {
		t.Errorf("the leader did not round-trip: %s", b)
	}
	if wire.Order == nil || wire.Order.Target != "skeleton-3" {
		t.Errorf("the order did not round-trip: %s", b)
	}
}
