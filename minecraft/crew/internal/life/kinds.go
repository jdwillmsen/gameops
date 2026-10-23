package life

import "strings"

type kind uint8

const (
	// other is anything crew leaves alone: passive mobs, items, vehicles.
	other kind = iota
	// hostile mobs are fought whenever they come close.
	hostile
	// provoked mobs are neutral until they hurt the unit, then hostile.
	provoked
	// flee mobs are not worth meleeing: a creeper's blast costs more than
	// the kill gains, so the unit backs away from them instead.
	flee
)

var kinds = map[string]kind{
	"zombie": hostile, "husk": hostile, "drowned": hostile, "zombie_villager": hostile,
	"zombie_villager_v2": hostile,
	"skeleton":           hostile, "stray": hostile, "bogged": hostile, "wither_skeleton": hostile,
	"spider": hostile, "cave_spider": hostile, "witch": hostile, "pillager": hostile,
	"vindicator": hostile, "evoker": hostile, "evocation_illager": hostile, "ravager": hostile, "vex": hostile,
	"slime": hostile, "magma_cube": hostile, "phantom": hostile, "silverfish": hostile,
	"endermite": hostile, "blaze": hostile, "guardian": hostile, "piglin_brute": hostile,
	"hoglin": hostile, "zoglin": hostile, "breeze": hostile,
	"warden": hostile, "wither": hostile, "elder_guardian": hostile,

	"enderman": provoked, "zombie_pigman": provoked, "zombified_piglin": provoked,
	"piglin": provoked, "iron_golem": provoked, "wolf": provoked, "bee": provoked,
	"polar_bear": provoked, "llama": provoked, "panda": provoked, "goat": provoked,

	"creeper": flee,
}

// shooters hurt from beyond melee range, so a hit with none close is still
// worth a look for one of them.
var shooters = map[string]bool{
	"skeleton": true, "stray": true, "bogged": true, "pillager": true,
	"witch": true, "blaze": true, "breeze": true, "evoker": true, "evocation_illager": true,
	"drowned": true,
}

func name(entityType string) string { return strings.TrimPrefix(entityType, "minecraft:") }

func kindOf(entityType string) kind { return kinds[name(entityType)] }
