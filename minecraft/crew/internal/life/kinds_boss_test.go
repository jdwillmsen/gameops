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
