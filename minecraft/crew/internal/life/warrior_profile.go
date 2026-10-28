package life

import (
	"maps"
	"math"
	"slices"

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
	// stepping goroutine. The tactic lists are copied for the same reason.
	pr.Overrides = maps.Clone(pr.Overrides)
	if pr.Tactics != nil {
		own := make(map[tactics.Family][]string, len(pr.Tactics))
		for f, names := range pr.Tactics {
			own[f] = slices.Clone(names)
		}
		pr.Tactics = own
	}
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
// the profile's default. The profile is passed in so one phase of a tick reads
// one profile even if SetProfile lands in the middle of it.
func paramsFor(pr tactics.Profile, e belief.Entity) tactics.Params {
	if e.ID == 0 {
		return pr.Default
	}
	return pr.For(familyOf(e))
}

// ticksOf turns a tick count carried as a float into the clock's unit.
func ticksOf(v float64) uint64 {
	return uint64(math.Round(math.Max(0, v)))
}

// familyOf is the opponent family e is fought as.
func familyOf(e belief.Entity) tactics.Family {
	if e.Player {
		return tactics.FamilyPlayer
	}
	return tactics.FamilyOfType(e.Type)
}
