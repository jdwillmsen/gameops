package life

import (
	"maps"
	"math"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// defaultProfile is what a unit fights with until something sets another:
// the shipped constants, so an unconfigured unit is unchanged.
var defaultProfile = tactics.NewProfile("default")

// SetProfile swaps the tuning the warrior fights with. It is safe from any
// goroutine; the stepping goroutine reads it afresh every tick, so a swap
// takes hold on the next tick, mid-fight or not. A profile the engine cannot
// read is refused and the one in force stays.
func (l *Life) SetProfile(pr tactics.Profile) error {
	// Validate clamps the overrides in place: the caller's map is not ours
	// to write, and keeping our own copy stops later writes to it racing the
	// stepping goroutine. The tactic table is copied for the same reason.
	pr.Overrides = maps.Clone(pr.Overrides)
	pr.Tactics = maps.Clone(pr.Tactics)
	pr, err := pr.Validate()
	if err != nil {
		return err
	}
	l.profile.Store(&pr)
	return nil
}

// Profile is the tuning in force.
func (l *Life) Profile() tactics.Profile {
	if pr := l.profile.Load(); pr != nil {
		return *pr
	}
	return defaultProfile
}

// paramsFor is the tuning the fight against e reads; with no target it is
// the profile's default.
func (l *Life) paramsFor(e belief.Entity) tactics.Params {
	pr := l.Profile()
	if e.ID == 0 {
		return pr.Default
	}
	return pr.For(familyOf(e))
}

// ticksOf turns a tick count carried as a float into the clock's unit.
func ticksOf(v float64) uint64 {
	return uint64(math.Round(math.Max(0, v)))
}

// families maps entity types to the opponent family a strategy is learned
// for. Kin that fight alike share one: a husk is a zombie, a stray a
// skeleton. A type missing here fights under the default.
var families = map[string]tactics.Family{
	"zombie": tactics.FamilyZombie, "husk": tactics.FamilyZombie, "drowned": tactics.FamilyZombie,
	"zombie_villager": tactics.FamilyZombie, "zombie_villager_v2": tactics.FamilyZombie,
	"zombie_pigman": tactics.FamilyZombie, "zombified_piglin": tactics.FamilyZombie,
	// The scenario book trains a phantom against the zombie tactics.
	"phantom": tactics.FamilyZombie,

	"skeleton": tactics.FamilySkeleton, "stray": tactics.FamilySkeleton,
	"bogged": tactics.FamilySkeleton, "wither_skeleton": tactics.FamilySkeleton,

	"creeper": tactics.FamilyCreeper,

	"spider": tactics.FamilySpider, "cave_spider": tactics.FamilySpider,

	// A breeze keeps its distance and throws from it, as a blaze does.
	"blaze": tactics.FamilyBlaze, "breeze": tactics.FamilyBlaze,

	"enderman": tactics.FamilyEnderman,

	"witch": tactics.FamilyWitch,

	// The vex comes from an evoker and dies with the raid it came in.
	"pillager": tactics.FamilyIllager, "vindicator": tactics.FamilyIllager,
	"evoker": tactics.FamilyIllager, "evocation_illager": tactics.FamilyIllager,
	"vex": tactics.FamilyIllager,

	// Brutes are the heavy melee hitters that knock a unit back: no ranged
	// threat, just a swing worth staying out of.
	"piglin_brute": tactics.FamilyBrute, "ravager": tactics.FamilyBrute,
	"hoglin": tactics.FamilyBrute, "zoglin": tactics.FamilyBrute,
	"iron_golem": tactics.FamilyBrute,
}

// familyOf is the opponent family e is fought as. A boss is a boss whatever
// else it resembles, since the boss fight already runs its own geometry.
func familyOf(e belief.Entity) tactics.Family {
	if e.Player {
		return tactics.FamilyPlayer
	}
	if _, ok := bossOf(e.Type); ok {
		return tactics.FamilyBoss
	}
	if f, ok := families[name(e.Type)]; ok {
		return f
	}
	return tactics.FamilyDefault
}
