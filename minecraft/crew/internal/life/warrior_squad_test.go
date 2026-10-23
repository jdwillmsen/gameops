package life

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

// This file runs the squad coordination end to end, offline: the real squad
// Store and routes crew-control serves, the real client the unit binary
// runs, and three real warrior Lifes on the fake world every other warrior
// test uses. Only the Bedrock server is missing, and every packet it would
// send is fed by hand. It is the proof that the two streams meet: focus
// needs no message at all, and a call-out crosses crew-control and comes
// back to the squad's message view.

const (
	e2eSquad = "alpha"
	e2eToken = "wt-e2e"
	// eyeAboveFeet is how the server's reported position sits over the
	// feet; the squad wire carries where a unit stands.
	eyeAboveFeet = 1.62
)

// e2eUnits is the roster the routes check hellos against: the design's
// three warrior slots.
type e2eUnits map[string]bool

func (u e2eUnits) Has(id string) bool { return u[id] }

// e2eLog takes the client's log lines; the test watches the flow.
type e2eLog struct{}

func (e2eLog) Info(string, map[string]any)  {}
func (e2eLog) Error(string, map[string]any) {}

// e2eSource reports a rig's own state from its belief, as the unit
// binary's wiring does.
func e2eSource(r *rig) squadclient.Source {
	return func() squadmsg.Status {
		s := r.tracker.Snapshot()
		return squadmsg.Status{
			HP:   float64(s.Health),
			Food: int(s.Hunger),
			Pos:  squadmsg.Pos{X: s.Position[0], Y: s.Position[1] - eyeAboveFeet, Z: s.Position[2]},
		}
	}
}

func TestThreeWarriorsAgreeThroughCrewControlAndHearCallouts(t *testing.T) {
	store := squad.NewStore()
	mux := http.NewServeMux()
	squad.Routes(mux, store, e2eUnits{"crew-18": true, "crew-19": true, "crew-20": true}, e2eToken)
	srv := httptest.NewServer(mux)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Cleanup runs LIFO: the context ends the clients' open long polls
	// before the server would wait for them to drain.
	t.Cleanup(srv.Close)
	t.Cleanup(cancel)

	type seated struct {
		id     string
		rig    *rig
		client *squadclient.Client
	}
	var warriors []seated
	for _, seat := range []struct {
		id, role string
		feet     mgl32.Vec3
	}{
		{"crew-18", "vanguard", mgl32.Vec3{20.5, 1, 20.5}},
		{"crew-19", "striker", mgl32.Vec3{23.5, 1, 22.5}},
		{"crew-20", "ranger", mgl32.Vec3{18.5, 1, 22.5}},
	} {
		r := newWarrior(t, seat.role, seat.feet)
		// A rig starts with no known health, and the squad must not read
		// that as a dead member before the first status beat.
		r.setHealth(20)
		c := squadclient.New(base, seat.id, e2eSquad, "", squadmsg.Role(seat.role), e2eToken, e2eLog{})
		c.SetSource(e2eSource(r))
		r.l.SetSquad(c)
		go c.Run(ctx)
		// The same world packets reach every unit, as one server's would.
		spawnZombie(r, 52, mgl32.Vec3{22, 1, 22})
		spawnZombie(r, 50, mgl32.Vec3{21, 1, 21})
		spawnZombie(r, 51, mgl32.Vec3{22, 1, 21.5})
		warriors = append(warriors, seated{id: seat.id, rig: r, client: c})
	}

	// Hellos land at once, but the leader's position waits for the first
	// status beat and the poll after it, all on the client's real cadence.
	deadline := time.Now().Add(20 * time.Second)
	for {
		known, leader := true, ""
		for _, w := range warriors {
			id, _, ok := w.client.Leader()
			if !ok {
				known = false
				break
			}
			leader = id
		}
		if known {
			if leader != "crew-18" {
				t.Fatalf("leader = %s, want the vanguard crew-18 by the chain of command", leader)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the squad never named a leader with a position")
		}
		time.Sleep(25 * time.Millisecond)
	}

	var targets []uint64
	for _, w := range warriors {
		for i := 0; ; i++ {
			if i > 100 {
				t.Fatalf("%s never engaged", w.id)
			}
			if w.rig.step() {
				break
			}
		}
		targets = append(targets, w.rig.l.target.ID)
	}
	for _, id := range targets {
		if id != 50 {
			t.Fatalf("targets = %v, want all three on the shared lowest id 50", targets)
		}
	}

	// One warrior's low-health call-out must reach the others' message view:
	// this reads the squad the way their next poll does.
	before := pollOrders(t, srv.URL, "0").Cursor
	warriors[1].rig.setHealth(10)
	warriors[1].rig.step()
	deadline = time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the striker's low_health callout never reached the squad's message view")
		}
		found := false
		for _, c := range pollOrders(t, srv.URL, fmt.Sprintf("%d", before)).Callouts {
			if c.Kind == squadmsg.CalloutLowHealth && c.From == "crew-19" {
				found = true
			}
		}
		if found {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pollOrders reads the squad's messages the way a unit's client does.
func pollOrders(t *testing.T, srvURL, since string) squad.Delta {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+"/v1/squads/"+e2eSquad+"/orders?wait=1s&since="+since, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return squad.Delta{}
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orders: status %d", resp.StatusCode)
	}
	var d squad.Delta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
