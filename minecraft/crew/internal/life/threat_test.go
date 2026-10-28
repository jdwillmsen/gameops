package life

import "testing"

func TestThreatCoversHostileAndFleeMobsOnly(t *testing.T) {
	for typ, want := range map[string]bool{
		"minecraft:zombie": true, "creeper": true, "minecraft:skeleton": true,
		"minecraft:enderman": false, "minecraft:cow": false, "player": false,
	} {
		if got := Threat(typ); got != want {
			t.Errorf("Threat(%q) = %v, want %v", typ, got, want)
		}
	}
}
