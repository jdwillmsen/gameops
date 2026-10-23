package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// fakeSquad is the squad the warrior engine is fed in tests: one leader,
// one standing order, and the callouts it heard.
type fakeSquad struct {
	leaderID string
	leader   mgl32.Vec3
	leaderOK bool
	order    Order
	callouts []CalloutKind
}

func (f *fakeSquad) Leader() (string, mgl32.Vec3, bool) { return f.leaderID, f.leader, f.leaderOK }
func (f *fakeSquad) Orders() Order                      { return f.order }
func (f *fakeSquad) Callout(c Callout)                  { f.callouts = append(f.callouts, c.Kind) }

func (f *fakeSquad) heard(k CalloutKind) bool {
	for _, c := range f.callouts {
		if c == k {
			return true
		}
	}
	return false
}

// newWarrior rigs a warrior of the given role. The squad is a leaderless
// one until a test wires one in.
func newWarrior(t *testing.T, role string, feet mgl32.Vec3) *rig {
	t.Helper()
	cfg := defaults
	cfg.Role = role
	return newRig(t, cfg, feet)
}

func (r *rig) setSquad(t *testing.T, f *fakeSquad) {
	t.Helper()
	r.l.SetSquad(f)
}

// uses is the item-use transactions sent on a hotbar slot.
func (s *sink) uses(slot int) int {
	n := 0
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.UseItemTransactionData); ok && int(d.HotBarSlot) == slot {
				n++
			}
		}
	}
	return n
}

// releases is the item-release transactions sent on a hotbar slot.
func (s *sink) releases(slot int) int {
	n := 0
	for _, pk := range s.got {
		if tx, ok := pk.(*packet.InventoryTransaction); ok {
			if d, ok := tx.TransactionData.(*protocol.ReleaseItemTransactionData); ok && int(d.HotBarSlot) == slot {
				n++
			}
		}
	}
	return n
}

// startUsingInputs counts the inputs that began an item use.
func (s *sink) startUsingInputs() int {
	n := 0
	for _, in := range s.inputs() {
		if in.InputData.Load(packet.InputFlagStartUsingItem) {
			n++
		}
	}
	return n
}

func spawnZombie(r *rig, id uint64, at mgl32.Vec3) { r.spawn(id, "minecraft:zombie", at) }

func TestFocusPrefersLowestHealthThenEntityID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		healthOf map[uint64]float32
		wantType string
		want     uint64
	}{
		{name: "equal health falls to the id", healthOf: nil, want: 50},
		{name: "lower health wins over the id", healthOf: map[uint64]float32{50: 12, 51: 3}, want: 51},
		{name: "unknown health counts as full", healthOf: map[uint64]float32{50: 18}, want: 50},
		{name: "the focus order outranks health", healthOf: map[uint64]float32{50: 2, 51: 18}, wantType: "skeleton", want: 51},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ents := belief.NewEntities()
			ents.Apply(addActor(50, "minecraft:zombie", mgl32.Vec3{22, 1, 20}))
			ents.Apply(addActor(51, "minecraft:skeleton", mgl32.Vec3{23, 1, 20}))
			ents.Apply(addActor(60, "minecraft:cow", mgl32.Vec3{21, 1, 20}))
			ents.Apply(addPlayer(61, mgl32.Vec3{20, 1, 19}))
			ents.Apply(addActor(62, "minecraft:creeper", mgl32.Vec3{20, 1, 18}))
			ents.Apply(addActor(63, "minecraft:zombie", mgl32.Vec3{22, 9, 20}))
			ents.Apply(addActor(64, "minecraft:zombie", mgl32.Vec3{22, 1, 35}))
			q := focusQuery{anchor: start, self: start, wantType: tc.wantType}
			if tc.healthOf != nil {
				h := tc.healthOf
				q.healthOf = func(id uint64) (float32, bool) {
					v, ok := h[id]
					return v, ok
				}
			}
			best, ok := focusTarget(ents, q)
			if !ok {
				t.Fatal("no focus found")
			}
			if best.ID != tc.want {
				t.Fatalf("focus = %d, want %d", best.ID, tc.want)
			}
		})
	}
}

// addActor and addPlayer build the packets a belief feed is seeded with.
func addActor(id uint64, typ string, at mgl32.Vec3) *packet.AddActor {
	return &packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at}
}

func addPlayer(id uint64, at mgl32.Vec3) *packet.AddPlayer {
	return &packet.AddPlayer{EntityRuntimeID: id, AbilityData: protocol.AbilityData{EntityUniqueID: int64(id)}, Position: at}
}

func TestProvokedMobsAreFocusedOnlyWhileSuspect(t *testing.T) {
	ents := belief.NewEntities()
	ents.Apply(addActor(70, "minecraft:enderman", mgl32.Vec3{21, 1, 20}))
	q := focusQuery{anchor: start, self: start}
	if _, ok := focusTarget(ents, q); ok {
		t.Fatal("focused an enderman that had done nothing")
	}
	q.attacker, q.attackerUntil = 70, 10
	q.tick = 5
	best, ok := focusTarget(ents, q)
	if !ok || best.ID != 70 {
		t.Fatalf("focus = %d ok=%v, want the suspect enderman", best.ID, ok)
	}
	q.tick = 11
	if _, ok := focusTarget(ents, q); ok {
		t.Fatal("kept focusing the enderman after the suspect window")
	}
}

func TestThreeWarriorsAgreeOnTheSharedTargetWithoutMessages(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true}
	var targets []uint64
	for i, at := range []mgl32.Vec3{{22.5, 1, 18.5}, {18.5, 1, 22.5}, {23.5, 1, 23.5}} {
		r := newWarrior(t, "striker", at)
		r.setSquad(t, squad)
		spawnZombie(r, 52, mgl32.Vec3{22, 1, 22})
		spawnZombie(r, 50, mgl32.Vec3{21, 1, 21})
		spawnZombie(r, 51, mgl32.Vec3{22, 1, 21.5})
		r.player(60, mgl32.Vec3{21.5, 1, 20.5})
		if !r.step() {
			t.Fatalf("unit %d did not engage", i)
		}
		targets = append(targets, r.l.target.ID)
	}
	for _, id := range targets {
		if id != 50 {
			t.Fatalf("targets = %v, want all three on the lowest id 50", targets)
		}
	}
}

func TestWarriorFocusesThreatsInLeaderReachBeyondItsOwn(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: mgl32.Vec3{20.5, 1, 20.5}, leaderOK: true}

	w := newWarrior(t, "striker", start)
	w.setSquad(t, squad)
	spawnZombie(w, 50, mgl32.Vec3{27.5, 1, 20.5}) // 7 from the leader, past the old 6-block threat range
	if !w.step() {
		t.Fatal("warrior ignored a threat near its leader")
	}
	if w.l.target.ID != 50 {
		t.Fatalf("target = %d, want 50", w.l.target.ID)
	}

	d := newRig(t, defaults, start)
	spawnZombie(d, 50, mgl32.Vec3{27.5, 1, 20.5})
	if d.step() {
		t.Error("reactive defence engaged a mob beyond its threat range")
	}
}

func TestUnknownRoleLeavesTheReactiveDefenceInCharge(t *testing.T) {
	r := newRig(t, Config{Wander: true, Radius: 8, Defend: true, Eat: true, Role: "wizard"}, start)
	if r.l.warrior != nil {
		t.Fatal("a role string that names no warrior built one")
	}
	spawnZombie(r, 50, mgl32.Vec3{21.5, 1, 20.5})
	r.run(5)
	if !r.l.engaged || len(r.out.attacks()) == 0 {
		t.Fatal("the reactive defence did not answer a zombie at arm's length")
	}
}

func TestHoldOrderKeepsTheWarriorAtPosition(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderHold}}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	spawnZombie(r, 50, mgl32.Vec3{28.5, 1, 20.5})
	for i := 0; i < 60; i++ {
		r.step()
		if d := dist(r.feet(), start); d > 1.0 {
			t.Fatalf("tick %d: hold order walked %.1f blocks", i, d)
		}
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks on a mob the hold order kept the unit from", n)
	}
}

func TestRetreatOrderBacksTheWarriorOff(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderRetreat}}
	r := newWarrior(t, "vanguard", start)
	r.setSquad(t, squad)
	r.give(0, 301, 1)
	z := mgl32.Vec3{22.5, 1, 20.5}
	spawnZombie(r, 50, z)
	for i := 0; i < 40; i++ {
		r.step()
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks under a retreat order", n)
	}
	if d := dist(r.feet(), z); d < 5 {
		t.Errorf("%.1f blocks from the mob after retreating, want ground gained", d)
	}
}

func TestFocusOrderPrefersTheNamedType(t *testing.T) {
	squad := &fakeSquad{leaderID: "crew-18", leader: start, leaderOK: true, order: Order{Kind: OrderFocus, Target: "skeleton"}}
	r := newWarrior(t, "striker", start)
	r.setSquad(t, squad)
	spawnZombie(r, 50, mgl32.Vec3{21, 1, 20})
	r.spawn(51, "minecraft:skeleton", mgl32.Vec3{22, 1, 20})
	if !r.step() {
		t.Fatal("did not engage")
	}
	if r.l.target.ID != 51 {
		t.Fatalf("target = %d, want the ordered skeleton 51", r.l.target.ID)
	}
}
