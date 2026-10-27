package life

import "github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"

// PvPPolicy decides which players a warrior may fight. It is per unit: the
// policy already knows who the unit is, so the engine only names the player.
// Without one a warrior never targets a player.
type PvPPolicy interface {
	// MayTarget reports whether the named player is a legitimate target right
	// now. It must stay false until the fight is on, for a player on the unit's
	// own side and for one already eliminated; the engine holds fire on false.
	// It runs on the stepping goroutine every tick, so it must not block.
	MayTarget(player string) bool
}

// SetPvPPolicy installs the policy, or removes it with nil, after which the
// warrior is back to mob-only targeting.
func (l *Life) SetPvPPolicy(p PvPPolicy) {
	if p == nil {
		l.pvp.Store(nil)
		return
	}
	l.pvp.Store(&p)
}

// mayFightPlayer says whether e is a player the policy lets this unit fight.
func (l *Life) mayFightPlayer(e belief.Entity) bool {
	p := l.pvp.Load()
	return p != nil && e.Player && (*p).MayTarget(e.Name)
}
