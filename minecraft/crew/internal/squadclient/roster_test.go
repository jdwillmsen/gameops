package squadclient

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
)

func member(unit string, role squadmsg.Role, x, z float32, alive, fresh bool) squadmsg.Member {
	return squadmsg.Member{Unit: unit, Role: role, Pos: &squadmsg.Pos{X: x, Y: 64, Z: z}, Alive: alive, Fresh: fresh}
}

// rosterClient runs a client against the stub and returns a way to hand it
// one answer and wait until the cached state has taken it.
func rosterClient(t *testing.T, unit string) (*Client, func(a *pollAnswer)) {
	t.Helper()
	ri, srv := newRigServer(t)
	c := newTestClient(t, srv.URL, unit, newRecLog())
	runClient(t, c)
	var cursor int64
	return c, func(a *pollAnswer) {
		t.Helper()
		cursor++
		a.Cursor = cursor
		ri.setAnswer(a)
		waitFor(t, func() bool {
			c.mu.RLock()
			defer c.mu.RUnlock()
			return len(c.members) == len(a.Members) && c.hasStart == (a.Start != nil)
		}, "the answer to be applied")
		// The stub serves each answer once, so by now it has gone to the
		// client, and the next poll is the quiet 204 that keeps the cache.
		waitFor(t, func() bool { c, _ := ri.lastQuery(); return c == cursor }, "the next poll")
	}
}

func TestMemberIsTheRolesPositionWhileItIsUp(t *testing.T) {
	c, feed := rosterClient(t, "crew-19")
	feed(&pollAnswer{Members: []squadmsg.Member{
		member("crew-18", squadmsg.RoleVanguard, 4, 5, true, true),
		member("crew-19", squadmsg.RoleStriker, 7, 8, true, true),
		member("crew-20", squadmsg.RoleRanger, 9, 9, false, true),
	}})

	if p, ok := c.Member(squadmsg.RoleVanguard); !ok || p != (mgl32.Vec3{4, 64, 5}) {
		t.Errorf("vanguard = %v, %v, want its reported place", p, ok)
	}
	if _, ok := c.Member(squadmsg.RoleRanger); ok {
		t.Error("a dead ranger still has a place")
	}

	feed(&pollAnswer{Members: []squadmsg.Member{
		member("crew-18", squadmsg.RoleVanguard, 4, 5, true, false),
		{Unit: "crew-19", Role: squadmsg.RoleStriker, Alive: true, Fresh: true},
	}})
	if _, ok := c.Member(squadmsg.RoleVanguard); ok {
		t.Error("a vanguard quiet past the grace still has a place")
	}
	if _, ok := c.Member(squadmsg.RoleStriker); ok {
		t.Error("a striker that never reported has a place")
	}
	if _, ok := c.Member(squadmsg.RoleRanger); ok {
		t.Error("a role nobody claims has a place")
	}
}

func TestDownCountsTheDeadAndTheQuietButNotItself(t *testing.T) {
	c, feed := rosterClient(t, "crew-19")
	van := func(alive, fresh bool) squadmsg.Member {
		return member("crew-18", squadmsg.RoleVanguard, 1, 1, alive, fresh)
	}
	str := func(alive, fresh bool) squadmsg.Member {
		return member("crew-19", squadmsg.RoleStriker, 2, 2, alive, fresh)
	}
	ran := func(alive, fresh bool) squadmsg.Member {
		return member("crew-20", squadmsg.RoleRanger, 3, 3, alive, fresh)
	}

	for _, tc := range []struct {
		name    string
		members []squadmsg.Member
		want    int
	}{
		{"everyone up", []squadmsg.Member{van(true, true), str(true, true), ran(true, true)}, 0},
		{"one dead", []squadmsg.Member{van(false, true), str(true, true), ran(true, true)}, 1},
		{"one quiet", []squadmsg.Member{van(true, true), str(true, true), ran(true, false)}, 1},
		{"one dead, one quiet", []squadmsg.Member{van(false, true), str(true, true), ran(true, false)}, 2},
		{"the unit itself is not counted", []squadmsg.Member{van(true, true), str(false, false), ran(true, true)}, 0},
		{"a lone unit", []squadmsg.Member{str(true, true)}, 0},
	} {
		feed(&pollAnswer{Members: tc.members})
		if got := c.Down(); got != tc.want {
			t.Errorf("%s: Down() = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestStartIsKnownOnlyOnceTheServerSendsIt(t *testing.T) {
	c, feed := rosterClient(t, "crew-19")
	if _, ok := c.Start(); ok {
		t.Fatal("a client that has heard nothing knows a start")
	}
	feed(&pollAnswer{Members: []squadmsg.Member{member("crew-19", squadmsg.RoleStriker, 1, 1, true, true)}})
	if _, ok := c.Start(); ok {
		t.Error("an answer with no start gave one")
	}
	feed(&pollAnswer{
		Members: []squadmsg.Member{member("crew-19", squadmsg.RoleStriker, 1, 1, true, true)},
		Start:   &squadmsg.Pos{X: 10, Y: 64, Z: -20},
	})
	if p, ok := c.Start(); !ok || p != (mgl32.Vec3{10, 64, -20}) {
		t.Errorf("start = %v, %v, want the server's", p, ok)
	}
	feed(&pollAnswer{Members: []squadmsg.Member{member("crew-19", squadmsg.RoleStriker, 1, 1, true, true)}})
	if _, ok := c.Start(); ok {
		t.Error("a start that the server stopped sending is still known")
	}
}

func TestAnAnswerWithoutARosterLeavesNobodyDown(t *testing.T) {
	c, _ := rosterClient(t, "crew-19")
	if n := c.Down(); n != 0 {
		t.Errorf("Down() = %d before any answer", n)
	}
	if _, ok := c.Member(squadmsg.RoleVanguard); ok {
		t.Error("a member before any answer")
	}
}

func TestAMemberThatNeverReportedIsNotDown(t *testing.T) {
	c, feed := rosterClient(t, "crew-18")
	hello := squadmsg.Member{Unit: "crew-19", Role: squadmsg.RoleStriker, Alive: true, Fresh: false}
	hello2 := squadmsg.Member{Unit: "crew-20", Role: squadmsg.RoleRanger, Alive: true, Fresh: false}
	feed(&pollAnswer{Members: []squadmsg.Member{member("crew-18", squadmsg.RoleVanguard, 1, 1, true, true), hello, hello2}})
	if got := c.Down(); got != 0 {
		t.Errorf("two members that joined but never reported read as %d down; they must not trigger a regroup", got)
	}
	// Once one has spoken and fallen silent it does count.
	feed(&pollAnswer{Members: []squadmsg.Member{member("crew-18", squadmsg.RoleVanguard, 1, 1, true, true),
		member("crew-19", squadmsg.RoleStriker, 2, 2, true, false), hello2}})
	if got := c.Down(); got != 1 {
		t.Errorf("a member that reported and went quiet reads as %d down, want 1", got)
	}
}

func TestTheRosterLapsWhenPollsStopAnswering(t *testing.T) {
	c, feed := rosterClient(t, "crew-18")
	feed(&pollAnswer{
		Members: []squadmsg.Member{member("crew-19", squadmsg.RoleVanguard, 1, 1, false, true), member("crew-20", squadmsg.RoleRanger, 2, 2, false, true)},
		Start:   &squadmsg.Pos{X: 5, Y: 64, Z: 5},
	})
	if c.Down() != 2 {
		t.Fatalf("setup: Down() = %d, want 2", c.Down())
	}
	c.mu.Lock()
	c.lastOK = time.Now().Add(-2 * rosterStale)
	c.mu.Unlock()
	if got := c.Down(); got != 0 {
		t.Errorf("Down() = %d after the polls went quiet for longer than the roster is good for, want 0", got)
	}
	if _, ok := c.Member(squadmsg.RoleVanguard); ok {
		t.Error("a member is still reported after the roster lapsed")
	}
	if _, ok := c.Start(); ok {
		t.Error("a start point is still reported after the roster lapsed")
	}
}
