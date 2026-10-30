package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func TestBossesAreFought(t *testing.T) {
	for _, mob := range []string{"warden", "wither", "elder_guardian", "minecraft:warden"} {
		if kindOf(mob) != hostile {
			t.Errorf("%s: kind %v, want hostile", mob, kindOf(mob))
		}
	}
}

func TestKindOfNamespaces(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want kind
	}{
		{"gameops:werewolf", provoked},
		{"gameops:training_dummy_brute", provoked},
		{"gameops:ground_dragon", hostile},
		{"zombie", hostile},
		{"minecraft:zombie", hostile},
		{"minecraft:iron_golem", provoked},
		{"minecraft:creeper", flee},
		{"werewolf", other},
		{"gameops:zombie", other},
		{"gameops:unknown", other},
		{"minecraft:cow", other},
		{"", other},
	} {
		if got := kindOf(tc.id); got != tc.want {
			t.Errorf("kindOf(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestPackMobsAreNotBosses(t *testing.T) {
	for _, id := range []string{"gameops:werewolf", "gameops:training_dummy_brute"} {
		if _, ok := bossOf(id); ok {
			t.Errorf("%s: unexpected boss profile", id)
		}
	}
}

func TestGroundDragonIsFocusedWithoutHavingHurtTheUnit(t *testing.T) {
	q := focusQuery{tick: 1}
	e := belief.Entity{ID: 5, Type: "gameops:ground_dragon", Pos: mgl32.Vec3{6, 0, 0}}
	if !q.mobThreat(e) {
		t.Error("ground dragon is not a threat until it hurts the unit; a squad that never got hit would idle")
	}
}

func TestGroundDragonStaysFocusedWhenTheLeaderHasStrayed(t *testing.T) {
	p, ok := bossOf("gameops:ground_dragon")
	if !ok {
		t.Fatal("no boss profile")
	}
	e := belief.Entity{ID: 5, Type: "gameops:ground_dragon", Pos: mgl32.Vec3{25, 0, 0}}
	if (focusQuery{tick: 1}).mobThreat(e) {
		t.Fatal("setup: the shipped 12-block range should lose a dragon 25 blocks from the leader")
	}
	q := focusQuery{tick: 1, focusRange: func(belief.Entity) float64 { return p.focusRange }}
	if !q.mobThreat(e) {
		t.Errorf("dragon 25 blocks from the leader is dropped with the boss range %.0f", p.focusRange)
	}
}

func TestGroundDragonProfileBandsAreOrdered(t *testing.T) {
	p, _ := bossOf("gameops:ground_dragon")
	if !(p.strikerNear > bossReach && p.strikerFar < p.rangerNear && p.rangerFar < p.focusRange) {
		t.Errorf("bands out of order: striker %.0f-%.0f, ranger %.0f-%.0f, focus %.0f, reach %.1f",
			p.strikerNear, p.strikerFar, p.rangerNear, p.rangerFar, p.focusRange, bossReach)
	}
	if p.meleeEvery != 0 || p.boom != 0 || p.darkness != 0 {
		t.Errorf("the dragon has no boom, darkness or timed melee: %+v", p)
	}
}

func TestWildlifeIsNeverASuspect(t *testing.T) {
	for _, id := range []string{
		"gameops:tiger", "gameops:cheetah", "gameops:lion",
		"gameops:black_leopard", "gameops:brown_bear", "gameops:hyena",
	} {
		if kindOf(id) != other {
			t.Errorf("%s: kind %v, want other", id, kindOf(id))
		}
		if Threat(id) {
			t.Errorf("%s: Threat true, want false for a neutral mob", id)
		}
		r := newRig(t, defaults, start)
		r.spawn(72, id, mgl32.Vec3{21.5, 1, 20.5})
		r.hurt()
		r.step()
		if r.l.attacker != 0 {
			t.Errorf("%s: a hurt unit blamed the animal beside it (%d)", id, r.l.attacker)
		}
		if len(r.out.attacks()) != 0 {
			t.Errorf("%s: attacked the animal", id)
		}
	}
}
