package main

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadclient"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/squadmsg"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

const duelToken = "wt-duel-e2e"

// flatWorld is dirt at y<=0 and air above, bounded: a floor to fight on.
type flatWorld struct{}

func (flatWorld) Block(p protocol.BlockPos) (string, bool) {
	if p.X() < 0 || p.Z() < 0 || p.X() >= 40 || p.Z() >= 40 || p.Y() < -2 || p.Y() > 8 {
		return "", false
	}
	if p.Y() <= 0 {
		return "dirt", true
	}
	return "air", true
}

type packetSink struct {
	got     []packet.Packet
	tracker *belief.Tracker
}

func (s *packetSink) WritePacket(pk packet.Packet) error {
	s.got = append(s.got, pk)
	if m, ok := pk.(*packet.MobEquipment); ok {
		s.tracker.SetHeld(int(m.HotBarSlot))
	}
	return nil
}

// attacksOn counts the melee hits sent at an entity.
func (s *packetSink) attacksOn(id uint64) int {
	n := 0
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemOnEntityTransactionData); ok && d.TargetEntityRuntimeID == id {
				n++
			}
		}
	}
	return n
}

const swordNetworkID = 303

// duelist is a real warrior Life with its own belief and actuator, standing
// where it can see the others.
type duelist struct {
	name    string
	tracker *belief.Tracker
	act     *act.Actuator
	life    *life.Life
	out     *packetSink
}

func newDuelist(t *testing.T, name string, feet mgl32.Vec3) *duelist {
	t.Helper()
	tracker := belief.New(time.Now)
	tracker.Spawn(1, feet.Add(mgl32.Vec3{0, 1.62, 0}), 0, 0, 0)
	items := []protocol.ItemEntry{{Name: "minecraft:diamond_sword", RuntimeID: swordNetworkID}}
	tracker.ItemNames(items)
	tracker.Apply(&packet.InventorySlot{WindowID: 0, Slot: 0, NewItem: protocol.ItemInstance{
		StackNetworkID: 500,
		Stack:          protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: swordNetworkID}, Count: 1},
	}})
	out := &packetSink{tracker: tracker}
	a := act.New(out, 1, act.Pose{Feet: feet, OnGround: true}, path.NewSolid(flatWorld{}))
	a.ItemNames(items)
	lf := life.New(life.Config{Wander: true, Radius: 8, Defend: true, Eat: true, Role: "striker"},
		flatWorld{}, tracker.Snapshot, nopLog{}, rand.New(rand.NewSource(7)))
	lf.Spawn(1)
	return &duelist{name: name, tracker: tracker, act: a, life: lf, out: out}
}

func (d *duelist) see(id uint64, name string, at mgl32.Vec3) {
	d.life.Apply(&packet.AddPlayer{EntityRuntimeID: id, Username: name, Position: at,
		AbilityData: protocol.AbilityData{EntityUniqueID: int64(id)}})
}

// tick is one runner tick: the life decides and sends the tick's one input.
func (d *duelist) tick(t *testing.T) {
	t.Helper()
	snap := d.tracker.Snapshot()
	pose := d.act.Pose()
	var err error
	switch {
	case d.life.Engaged(pose, snap):
		err = d.life.Fight(context.Background(), d.act, snap)
	case d.life.Swimming(pose):
		err = d.life.Swim(context.Background(), d.act)
	case d.life.Tending(snap, false):
		err = d.life.Tend(context.Background(), d.act, snap)
	default:
		err = d.life.Idle(context.Background(), d.act)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// duelUnit is one lab unit end to end: the real squad client, the duel sync
// and the Life it drives.
type duelUnit struct {
	*duelist
	sync *duelSync
}

func startDuelUnit(t *testing.T, base *url.URL, d *duelist, unitID, squadID, dir string) *duelUnit {
	t.Helper()
	sc := squadclient.New(base, unitID, squadID, "", squadmsg.RoleStriker, duelToken, nopLog{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go sc.Run(ctx)
	cfg := duelConfig(dir)
	cfg.Squad, cfg.Username = squadID, d.name
	ds := newDuelSync(cfg, sc, d.life, nopLog{})
	if ds == nil {
		t.Fatal("no duel sync for a lab warrior")
	}
	return &duelUnit{duelist: d, sync: ds}
}

// settle runs the unit's sync until check holds: the squad's poll wakes on
// the director's PUT, so the unit hears it within moments, not at a fixed time.
func settle(t *testing.T, units []*duelUnit, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, u := range units {
			u.sync.step(context.Background())
		}
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the units never heard the squad")
}

func fightFor(t *testing.T, units []*duelUnit, ticks int) {
	t.Helper()
	for i := 0; i < ticks; i++ {
		for _, u := range units {
			u.tick(t)
		}
	}
}

// Two real warriors on two lab squads, a bystander beside each, and the
// director's own SquadPolicy over the real squad routes: nobody attacks before
// go, each hunts only its enemy after it, an eliminated enemy is dropped, and
// over ends it.
func TestDuelEndToEndOnRealSquadsAndRealLives(t *testing.T) {
	store := squad.NewStore()
	mux := http.NewServeMux()
	squad.Routes(mux, store, e2eUnitSet{"crew-r": "Rusty", "crew-b": "Tinder", "crew-a": "Agate"}, duelToken)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	dir := controlDir(t, true)

	// Rusty (red) and Tinder (blue) stand a sword apart. Rusty also sees
	// Agate, Tinder's squad-mate, and both see Steve, a player outside the
	// roster.
	rusty := newDuelist(t, "Rusty", mgl32.Vec3{20.5, 1, 20.5})
	tinder := newDuelist(t, "Tinder", mgl32.Vec3{21.5, 1, 20.5})
	const rustyID, tinderID, agateID, steveID = 11, 12, 13, 14
	rusty.see(tinderID, "Tinder", mgl32.Vec3{21.5, 1, 20.5})
	rusty.see(agateID, "Agate", mgl32.Vec3{20.5, 1, 22.5})
	rusty.see(steveID, "Steve", mgl32.Vec3{19.5, 1, 20.5})
	tinder.see(rustyID, "Rusty", mgl32.Vec3{20.5, 1, 20.5})
	tinder.see(steveID, "Steve", mgl32.Vec3{22.5, 1, 20.5})
	units := []*duelUnit{
		startDuelUnit(t, base, rusty, "crew-r", "lab-red", dir),
		startDuelUnit(t, base, tinder, "crew-b", "lab-blue", dir),
	}
	// Agate is Tinder's squad-mate; the squad service only lets a duel name
	// units it has heard from.
	agate := squadclient.New(base, "crew-a", "lab-blue", "", squadmsg.RoleStriker, duelToken, nopLog{})
	agateCtx, stopAgate := context.WithCancel(context.Background())
	t.Cleanup(stopAgate)
	go agate.Run(agateCtx)
	waitFor(t, func() bool {
		return store.Has("lab-red") && store.Has("lab-blue") && store.Member("lab-blue", "crew-a")
	})

	policy, err := arena.NewSquadPolicy(base, "lab-red", "lab-blue", duelToken,
		[]string{"Rusty"}, []string{"Tinder", "Agate"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	roster := arena.Roster{Red: []string{"Rusty"}, Blue: []string{"Tinder", "Agate"}}
	t.Cleanup(func() { _ = policy.Disarm() })

	// Before any signal, and while only armed, nobody fights.
	fightFor(t, units, 60)
	if err := policy.Arm(roster); err != nil {
		t.Fatal(err)
	}
	settle(t, units, func() bool { return units[0].sync.installed && units[1].sync.installed })
	fightFor(t, units, 60)
	for _, u := range units {
		for _, id := range []uint64{rustyID, tinderID, agateID, steveID} {
			if n := u.out.attacksOn(id); n != 0 {
				t.Fatalf("%s attacked %d before go (%d times)", u.name, id, n)
			}
		}
	}

	if err := policy.Go(); err != nil {
		t.Fatal(err)
	}
	settle(t, units, func() bool {
		return units[0].sync.pol.MayTarget("Tinder") && units[1].sync.pol.MayTarget("Rusty")
	})
	fightFor(t, units, 80)
	if rusty.out.attacksOn(tinderID) == 0 || tinder.out.attacksOn(rustyID) == 0 {
		t.Fatalf("after go: Rusty hit Tinder %d times, Tinder hit Rusty %d times",
			rusty.out.attacksOn(tinderID), tinder.out.attacksOn(rustyID))
	}
	for _, u := range units {
		if n := u.out.attacksOn(steveID); n != 0 {
			t.Errorf("%s attacked the bystander %d times", u.name, n)
		}
	}

	// Tinder falls: Rusty drops it as a target and turns on Agate.
	if err := policy.Eliminated("Tinder"); err != nil {
		t.Fatal(err)
	}
	settle(t, units, func() bool { return !units[0].sync.pol.MayTarget("Tinder") })
	hitsOnTinder := rusty.out.attacksOn(tinderID)
	fightFor(t, units, 80)
	if got := rusty.out.attacksOn(tinderID); got != hitsOnTinder {
		t.Errorf("Rusty kept hitting the eliminated Tinder: %d -> %d", hitsOnTinder, got)
	}
	if rusty.out.attacksOn(agateID) == 0 {
		t.Error("Rusty did not turn on the remaining enemy")
	}

	if err := policy.Eliminated("Agate"); err != nil {
		t.Fatal(err)
	}
	settle(t, units, func() bool { return !units[0].sync.pol.MayTarget("Agate") })
	fightFor(t, units, 10)
	rustyHits := [...]int{rusty.out.attacksOn(tinderID), rusty.out.attacksOn(agateID)}
	fightFor(t, units, 80)
	if got := [...]int{rusty.out.attacksOn(tinderID), rusty.out.attacksOn(agateID)}; got != rustyHits {
		t.Errorf("Rusty attacked with every enemy out: %v -> %v", rustyHits, got)
	}

	// Over ends all targeting, for the unit that was still hunting too.
	if err := policy.Disarm(); err != nil {
		t.Fatal(err)
	}
	settle(t, units, func() bool { return units[0].sync.pol.view.Load() == nil && units[1].sync.pol.view.Load() == nil })
	fightFor(t, units, 10)
	before := tinder.out.attacksOn(rustyID)
	fightFor(t, units, 80)
	if got := tinder.out.attacksOn(rustyID); got != before {
		t.Errorf("Tinder kept hunting after over: %d -> %d", before, got)
	}
	if units[0].sync.installed || units[1].sync.installed {
		t.Error("the policy was not removed after over")
	}
}

// e2eUnitSet is the roster: unit id to the in-game name it plays under.
type e2eUnitSet map[string]string

func (u e2eUnitSet) Has(id string) bool { _, ok := u[id]; return ok }

func (u e2eUnitSet) NameOf(id string) (string, bool) { n, ok := u[id]; return n, ok }
