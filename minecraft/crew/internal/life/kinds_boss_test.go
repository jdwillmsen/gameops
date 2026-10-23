package life

import "testing"

func TestBossesAreFought(t *testing.T) {
	for _, mob := range []string{"warden", "wither", "elder_guardian", "minecraft:warden"} {
		if kindOf(mob) != hostile {
			t.Errorf("%s: kind %v, want hostile", mob, kindOf(mob))
		}
	}
}
