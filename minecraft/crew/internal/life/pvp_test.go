package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// fakePvP allows a fixed set of players once it is told to go.
type fakePvP struct {
	enemies map[string]bool
	out     map[string]bool
	live    bool
}

func (f *fakePvP) MayTarget(player string) bool {
	return f.live && f.enemies[player] && !f.out[player]
}

func newPvP(enemies ...string) *fakePvP {
	f := &fakePvP{enemies: map[string]bool{}, out: map[string]bool{}}
	for _, e := range enemies {
		f.enemies[e] = true
	}
	return f
}

func (r *rig) playerNamed(id uint64, name string, at mgl32.Vec3) {
	r.l.Apply(&packet.AddPlayer{
		EntityRuntimeID: id, Username: name, Position: at,
		AbilityData: protocol.AbilityData{EntityUniqueID: int64(id)},
	})
}

func armedWarrior(t *testing.T, pol PvPPolicy) *rig {
	t.Helper()
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.l.SetPvPPolicy(pol)
	return r
}

func TestNilPvPPolicyLeavesPlayersAloneAndPacketsUnchanged(t *testing.T) {
	run := func(set bool) []packet.Packet {
		r := newWarrior(t, "striker", start)
		r.give(0, 303, 1)
		if set {
			r.l.SetPvPPolicy(newPvP("rival"))
			r.l.SetPvPPolicy(nil)
		}
		r.playerNamed(60, "rival", mgl32.Vec3{21.5, 1, 20.5})
		r.spawn(50, "minecraft:zombie", mgl32.Vec3{23.5, 1, 20.5})
		r.run(60)
		for _, a := range r.out.attacks() {
			if a.TargetEntityRuntimeID == 60 {
				t.Fatal("attacked a player with no policy")
			}
		}
		return r.out.got
	}
	plain, cleared := run(false), run(true)
	if len(plain) == 0 || len(plain) != len(cleared) {
		t.Fatalf("packets %d vs %d", len(plain), len(cleared))
	}
	for i := range plain {
		if !reflect.DeepEqual(plain[i], cleared[i]) {
			t.Fatalf("packet %d differs: %+v vs %+v", i, plain[i], cleared[i])
		}
	}
}

func TestArmedPolicyTargetsOnlyTheEnemyPlayer(t *testing.T) {
	pol := newPvP("rival")
	r := armedWarrior(t, pol)
	r.playerNamed(60, "ally", mgl32.Vec3{21.5, 1, 20.5})
	r.playerNamed(61, "bystander", mgl32.Vec3{20.5, 1, 22})
	r.playerNamed(62, "rival", mgl32.Vec3{22.5, 1, 20.5})
	pol.live = true
	r.run(80)
	if !r.l.target.Player || r.l.target.ID != 62 {
		t.Fatalf("target = %+v, want the rival", r.l.target)
	}
	got := r.out.attacks()
	if len(got) == 0 {
		t.Fatal("never attacked the enemy player")
	}
	for _, a := range got {
		if a.TargetEntityRuntimeID != 62 {
			t.Fatalf("attacked %d, only the rival is fair game", a.TargetEntityRuntimeID)
		}
	}
}

func TestAnEnemyPlayerOutranksAMobAndFamilyIsPlayer(t *testing.T) {
	pol := newPvP("rival")
	pol.live = true
	r := armedWarrior(t, pol)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{21.5, 1, 20.5})
	r.playerNamed(62, "rival", mgl32.Vec3{24, 1, 20.5})
	r.run(3)
	if r.l.target.ID != 62 {
		t.Fatalf("target = %d, want the player over the nearer zombie", r.l.target.ID)
	}
	if f := familyOf(r.l.target); f != tactics.FamilyPlayer {
		t.Fatalf("family = %v", f)
	}
}

func TestNoAttackBeforeGo(t *testing.T) {
	pol := newPvP("rival")
	r := armedWarrior(t, pol)
	r.playerNamed(62, "rival", mgl32.Vec3{21.5, 1, 20.5})
	r.run(60)
	if n := len(r.out.attacks()); n != 0 || r.l.target.Player {
		t.Fatalf("%d attacks and target %+v before go", n, r.l.target)
	}
	pol.live = true
	r.run(40)
	if len(r.out.attacks()) == 0 {
		t.Fatal("did not attack after go")
	}
}

func TestAnEliminatedPlayerIsDropped(t *testing.T) {
	pol := newPvP("rival", "second")
	pol.live = true
	r := armedWarrior(t, pol)
	r.playerNamed(62, "rival", mgl32.Vec3{21.5, 1, 20.5})
	r.playerNamed(63, "second", mgl32.Vec3{22.5, 1, 21.5})
	r.run(20)
	if r.l.target.ID != 62 {
		t.Fatalf("target = %d, want the nearer rival first", r.l.target.ID)
	}
	pol.out["rival"] = true
	r.run(5)
	if r.l.target.ID != 63 {
		t.Fatalf("target = %d, want the second after the rival is out", r.l.target.ID)
	}
	pol.out["second"] = true
	r.run(40)
	if r.l.target.ID != 0 {
		t.Fatalf("target = %d with everyone out", r.l.target.ID)
	}
}

func TestEnemyPlayerIsAThreatOnlyWhileFairGame(t *testing.T) {
	pol := newPvP("rival")
	r := armedWarrior(t, pol)
	r.playerNamed(62, "rival", mgl32.Vec3{22.5, 1, 20.5})
	r.playerNamed(63, "ally", mgl32.Vec3{20.5, 1, 22})
	p := r.a.Pose()
	if r.l.enemyNear(p.Feet, 6) || r.l.mealThreatened(p) {
		t.Fatal("a player counts as a threat before go")
	}
	pol.live = true
	if !r.l.enemyNear(p.Feet, 6) || !r.l.mealThreatened(p) {
		t.Fatal("the enemy player is no threat after go")
	}
	pol.out["rival"] = true
	if r.l.enemyNear(p.Feet, 6) {
		t.Fatal("an eliminated player still counts as a threat")
	}
	r.l.SetPvPPolicy(nil)
	pol.out["rival"] = false
	if r.l.enemyNear(p.Feet, 6) {
		t.Fatal("a player counts as a threat without a policy")
	}
}

func TestLowHealthRetreatsFromAnEnemyPlayer(t *testing.T) {
	pol := newPvP("rival")
	pol.live = true
	r := armedWarrior(t, pol)
	r.playerNamed(62, "rival", mgl32.Vec3{21.2, 1, 20.5})
	r.setHealth(3)
	r.run(3)
	if r.l.mode != modeRetreat {
		t.Fatalf("mode = %v, want retreat at low health with an enemy player pressing", r.l.mode)
	}
}

func TestFocusIgnoresPlayersWithoutARule(t *testing.T) {
	ents := belief.NewEntities()
	ents.Apply(addPlayer(61, mgl32.Vec3{20, 1, 19}))
	if _, ok := focusTarget(ents, focusQuery{anchor: start, self: start}); ok {
		t.Fatal("focused a player with no player rule")
	}
}

func TestATypedNilPvPPolicyIsTheSameAsNone(t *testing.T) {
	var typed *fakePvP
	var m PvPPolicy = typed
	if m == nil {
		t.Fatal("a typed nil compares nil: the test proves nothing")
	}
	r := armedWarrior(t, newPvP("rival"))
	r.l.SetPvPPolicy(typed)
	if r.l.pvp.Load() != nil {
		t.Error("a typed-nil policy was stored")
	}
	rival := belief.Entity{ID: 62, Player: true, Name: "rival"}
	if r.l.mayFightPlayer(rival) {
		t.Error("a player is fightable with the policy removed")
	}
	r.playerNamed(62, "rival", mgl32.Vec3{21.2, 1, 20.5})
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{23.5, 1, 20.5})
	r.run(20)
}
