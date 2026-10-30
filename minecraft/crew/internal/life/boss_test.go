package life

import "testing"

func TestBossProfilesAreChosenByEntityType(t *testing.T) {
	for _, mob := range []string{"warden", "minecraft:warden", "wither", "minecraft:elder_guardian", "gameops:ground_dragon"} {
		if _, ok := bossOf(mob); !ok {
			t.Errorf("%s: no boss profile", mob)
		}
	}
	for _, mob := range []string{"zombie", "minecraft:guardian", "minecraft:wither_skeleton", "player", ""} {
		if _, ok := bossOf(mob); ok {
			t.Errorf("%s: fought as a boss", mob)
		}
	}
}

func TestWardenProfileKeepsTheRangerOutsideDarknessAndBoom(t *testing.T) {
	w, _ := bossOf("minecraft:warden")
	if w.rangerNear <= w.darkness || w.rangerNear <= wardenBoomRange {
		t.Errorf("ranger band starts at %.0f, inside darkness %.0f or boom %.0f", w.rangerNear, w.darkness, wardenBoomRange)
	}
	if w.rangerFar >= w.focusRange {
		t.Errorf("ranger band ends at %.0f, past the %.0f the squad keeps the boss focused at", w.rangerFar, w.focusRange)
	}
	if w.strikerFar >= w.rangerNear || w.strikerNear <= meleeReach {
		t.Errorf("striker band %.0f-%.0f overlaps the ranger's or melee", w.strikerNear, w.strikerFar)
	}
	if w.meleeEvery == 0 {
		t.Error("the warden's melee cadence is unknown, so the vanguard cannot time its shield")
	}
}
