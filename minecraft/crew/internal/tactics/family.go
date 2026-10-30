package tactics

import "strings"

// families maps entity types to the opponent family a strategy is learned
// for. Kin that fight alike share one: a husk is a zombie, a stray a
// skeleton. A type missing here fights under the default. The trainer files a
// scenario's mobs and the warrior files the entity in front of it through this
// one table, so what is trained is what is later applied.
var families = map[string]Family{
	"zombie": FamilyZombie, "husk": FamilyZombie, "drowned": FamilyZombie,
	"zombie_villager": FamilyZombie, "zombie_villager_v2": FamilyZombie,
	"zombie_pigman": FamilyZombie, "zombified_piglin": FamilyZombie,
	// The scenario book trains a phantom against the zombie tactics.
	"phantom": FamilyZombie,

	"skeleton": FamilySkeleton, "stray": FamilySkeleton,
	// A wither skeleton is melee, but it stays here on purpose: the skeleton
	// tactics tell the bow carriers from it themselves, and the scenario
	// book trains it with the family.
	"bogged": FamilySkeleton, "wither_skeleton": FamilySkeleton,

	"creeper": FamilyCreeper,

	"spider": FamilySpider, "cave_spider": FamilySpider,

	// A breeze keeps its distance and throws from it, as a blaze does.
	"blaze": FamilyBlaze, "breeze": FamilyBlaze,

	// A ghast hangs in the air out of any sword's reach.
	"ghast": FamilyAir,

	"enderman": FamilyEnderman,

	"witch": FamilyWitch,

	// The vex comes from an evoker and dies with the raid it came in.
	"pillager": FamilyIllager, "vindicator": FamilyIllager,
	"evoker": FamilyIllager, "evocation_illager": FamilyIllager,
	"illusioner": FamilyIllager, "vex": FamilyIllager,

	// Brutes are the heavy melee hitters that knock a unit back: no ranged
	// threat, just a swing worth staying out of.
	"piglin": FamilyBrute, "piglin_brute": FamilyBrute, "ravager": FamilyBrute,
	"hoglin": FamilyBrute, "zoglin": FamilyBrute,
	"iron_golem": FamilyBrute,

	// A boss is a boss whatever else it resembles, since the boss fight
	// already runs its own geometry.
	"warden": FamilyBoss, "wither": FamilyBoss, "ender_dragon": FamilyBoss,
	"elder_guardian": FamilyBoss, "gameops:ground_dragon": FamilyBoss,
}

// FamilyOfType is the opponent family an entity type is fought as, with or
// without the minecraft: prefix. Players are not entity types here: whoever
// reads the entity decides that, and a type the table does not know is
// FamilyDefault.
func FamilyOfType(typeName string) Family {
	if f, ok := families[strings.TrimPrefix(typeName, "minecraft:")]; ok {
		return f
	}
	return FamilyDefault
}
