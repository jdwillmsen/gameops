package life

import (
	"context"
	"math"

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
		l.cancelDraw(a)
		return l.warriorMelee(ctx, a, p, e, s)
	}
	l.lowerShield(a)
	threat := l.enemyNear(p.Feet, kiteBackoff)
	if !threat {
		w.meleeLast = false
	}
	if w.meleeLast {
		// Cornered once already: the sword stays out until there is room
		// to shoot again, or the unit would step back into the wall every
		// other tick.
		return l.warriorMelee(ctx, a, p, e, s)
	}
	in := l.face(p, e)
	switch {
	case w.drawing:
		w.drawTicks++
		if w.drawTicks >= bowDraw {
			if err := a.ReleaseItem(bow.Slot, stackAt(s, bow.Slot, bow.Name)); err != nil {
				return err
			}
			w.drawing, w.drawTicks, w.drawItem = false, 0, belief.Item{}
			w.lastShot = l.tick
			w.struck = e.ID
			l.noteSwing(e, true)
		}
		if threat {
			if !l.rangerCanBackOff(p) {
				// A drawn bow is worth nothing pressed against a wall:
				// the last resort is the sword.
				l.cancelDraw(a)
				w.backing, w.meleeLast = false, true
				return l.warriorMelee(ctx, a, p, e, s)
			}
			// A mob in the face does not stop a drawn bow: back off and
			// let the release happen on the way.
			in.Forward = -1
		}
		return a.Tick(in)
	case threat:
		// Nothing is drawn and something is close: gain ground first; a
		// new draw waits until there is room to stand and shoot.
		if !l.rangerCanBackOff(p) {
			w.backing, w.meleeLast = false, true
			return l.warriorMelee(ctx, a, p, e, s)
		}
		in.Forward = -1
		return a.Tick(in)
	case w.lastShot == 0 || l.tick-w.lastShot >= shotEvery:
		w.backing = false
		// lastShot zero is the fight's start, not a release at tick 0:
		// the first draw does not wait out a cadence that never ran.
		if err := a.SelectHotbar(bow.Slot, bow); err != nil {
			return err
		}
		if err := a.UseItem(bow.Slot, bow); err != nil {
			return err
		}
		w.drawing, w.drawTicks, w.drawItem = true, 0, bow
		in.StartUsing = true
		return a.Tick(in)
	}
	w.backing = false
	return a.Tick(in)
}

// cancelDraw lets go of a half-drawn bow the fight interrupted, without
// counting the release as a shot. It sends no input; the caller's own tick
// does.
func (l *Life) cancelDraw(a skill.Actuator) {
	w := l.warrior
	if w == nil || !w.drawing {
		return
	}
	if w.drawItem.Name != "" {
		_ = a.ReleaseItem(w.drawItem.Slot, w.drawItem)
	}
	w.drawing, w.drawTicks, w.drawItem = false, 0, belief.Item{}
}

// rangerCanBackOff says whether giving ground still works: false when a mob is
// already inside pressRange, where the bow cannot come up in time, or when the
// last few backing ticks moved nowhere, which is a wall or a body behind.
func (l *Life) rangerCanBackOff(p act.Pose) bool {
	w := l.warrior
	if l.enemyNear(p.Feet, pressRange) {
		return false
	}
	if !w.backing {
		w.backing, w.backStuck = true, 0
		w.backX, w.backZ = p.Feet.X(), p.Feet.Z()
		return true
	}
	moved := math.Hypot(float64(p.Feet.X()-w.backX), float64(p.Feet.Z()-w.backZ))
	w.backX, w.backZ = p.Feet.X(), p.Feet.Z()
	if moved < 0.05 {
		w.backStuck++
	} else {
		w.backStuck = 0
	}
	return w.backStuck < backStuckTicks
}
