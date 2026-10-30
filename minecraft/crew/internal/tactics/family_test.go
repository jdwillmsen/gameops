package tactics

import "testing"

func TestFamilyOfTypeIgnoresThePrefixAndDefaultsTheRest(t *testing.T) {
	tests := map[string]Family{
		"zombie": FamilyZombie, "minecraft:zombie": FamilyZombie,
		"minecraft:ender_dragon": FamilyBoss, "iron_golem": FamilyBrute,
		"cow": FamilyDefault, "minecraft:": FamilyDefault, "": FamilyDefault,
		"minecraft:minecraft:zombie": FamilyDefault,
	}
	for typ, want := range tests {
		if got := FamilyOfType(typ); got != want {
			t.Errorf("FamilyOfType(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestEveryFamilyTableEntryIsAKnownFamily(t *testing.T) {
	known := map[Family]bool{
		FamilyZombie: true, FamilySkeleton: true, FamilyCreeper: true, FamilySpider: true, FamilyBlaze: true,
		FamilyEnderman: true, FamilyWitch: true, FamilyIllager: true, FamilyBrute: true, FamilyBoss: true, FamilyAir: true,
	}
	for typ, f := range families {
		if !known[f] {
			t.Errorf("%s files under %q, which is not a mob family", typ, f)
		}
	}
}

func TestAWitherSkeletonIsFiledWithTheSkeletonsOnPurpose(t *testing.T) {
	if got := FamilyOfType("minecraft:wither_skeleton"); got != FamilySkeleton {
		t.Errorf("wither_skeleton files under %q, want the skeleton family the tactics and scenarios expect", got)
	}
}
