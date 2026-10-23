package life

import (
	"context"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// rangerArmed says whether the Ranger can shoot: a bow on the hotbar and an
// arrow somewhere in the inventory.
func (l *Life) rangerArmed(s belief.Snapshot) bool {
	if bowAt(s).Name == "" {
		return false
	}
	for _, it := range s.Inventory {
		if it.Name == "arrow" && it.Count > 0 {
			return true
		}
	}
	return false
}

// bowAt is the bow on the hotbar, when the kit gave one.
func bowAt(s belief.Snapshot) belief.Item {
	for _, it := range s.Inventory {
		if it.Slot < hotbarSlots && it.Name == "bow" {
			return it
		}
	}
	return belief.Item{}
}

// rangerShoot is the Ranger's fight: draw on the cadence, release at full
// charge, and give ground facing what closes inside kiting range, drawing
// all the while — a step back per arrow is the whole idea of the bow.
func (l *Life) rangerShoot(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	w := l.warrior
	bow := bowAt(s)
	if bow.Name == "" {
		return l.warriorMelee(ctx, a, p, e, s)
	}
	l.lowerShield(a)
	threat := l.enemyNear(p.Feet, kiteBackoff)
	in := l.face(p, e)
	switch {
	case w.drawing:
		w.drawTicks++
		if w.drawTicks >= bowDraw {
			if err := a.ReleaseItem(bow.Slot, stackAt(s, bow.Slot, bow.Name)); err != nil {
				return err
			}
			w.drawing, w.drawTicks, w.lastShot = false, 0, l.tick
			w.struck = e.ID
		}
		if threat {
			// A mob in the face does not stop a drawn bow: back off and
			// let the release happen on the way.
			in.Forward = -1
		}
		return a.Tick(in)
	case threat:
		// Nothing is drawn and something is close: gain ground first; a
		// new draw waits until there is room to stand and shoot.
		in.Forward = -1
		return a.Tick(in)
	case w.lastShot == 0 || l.tick-w.lastShot >= shotEvery:
		// lastShot zero is the fight's start, not a release at tick 0:
		// the first draw does not wait out a cadence that never ran.
		if err := a.SelectHotbar(bow.Slot, bow); err != nil {
			return err
		}
		if err := a.UseItem(bow.Slot, bow); err != nil {
			return err
		}
		w.drawing, w.drawTicks = true, 0
		in.StartUsing = true
		return a.Tick(in)
	}
	return a.Tick(in)
}
