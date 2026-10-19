package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// bodyCentre is how high above an entity's feet a swing is aimed.
const bodyCentre = 0.9

// Engaged advances the life's clock by one tick and says whether the unit
// must fight or flee instead of working or idling. The runner calls it once
// every tick, whatever the unit is doing.
func (l *Life) Engaged(p act.Pose, s belief.Snapshot) bool {
	l.tick++
	hurt := l.takeHurt()
	if !l.cfg.Defend {
		return false
	}
	if hurt {
		l.noteHurt(p)
	}

	var (
		best  belief.Entity
		bestD = math.MaxFloat64
	)
	l.ents.Each(func(e belief.Entity) {
		if e.Player {
			return
		}
		k := kindOf(e.Type)
		suspect := e.ID == l.attacker && !l.attackerPlayer && l.tick < l.attackerUntil
		if k == other || (k == provoked && !suspect) {
			return
		}
		if math.Abs(float64(e.Pos.Y()-p.Feet.Y())) > threatHeight {
			return
		}
		d := horizontal(p.Feet, e.Pos)
		limit := threatRange
		switch {
		case suspect:
			limit = shooterRange
		case k == flee:
			limit = creeperRange
		}
		if d > limit {
			return
		}
		inReach := inMeleeReach(p, e)
		if l.ignore[e.ID] > l.tick && !inReach {
			return
		}
		if d < bestD {
			best, bestD = e, d
		}
	})

	m := modeFight
	switch {
	case best.ID != 0:
		low := s.HealthKnown && s.Health <= lowHealth
		if (low || kindOf(best.Type) == flee) && l.retreating < retreatLimit {
			m = modeRetreat
		}
	case l.attackerPlayer && l.tick < l.attackerUntil && l.tick < l.stepBackEnd():
		if e, ok := l.ents.Get(l.attacker); ok && horizontal(p.Feet, e.Pos) <= attributionRange*2 {
			best, m = e, modeStepBack
		}
	}

	if best.ID != 0 {
		l.target, l.mode = best, m
		l.holdUntil = l.tick + graceTicks
		if !l.engaged {
			l.engaged, l.engagedAt, l.fighting, l.retreating = true, l.tick, 0, 0
			l.lastAttack = l.tick - attackEvery
			l.logEvent("defend_started", map[string]any{"entity": name(best.Type), "mode": modeName(m)})
		}
		l.fighting++
		if m == modeRetreat {
			l.retreating++
		}
		if l.fighting > fightLimit && !inMeleeReach(p, best) {
			l.giveUp(best.ID)
		}
		return true
	}

	l.target = belief.Entity{}
	if l.tick <= l.holdUntil {
		return true
	}
	if l.engaged {
		l.engaged = false
		l.chase, l.flee = nil, nil
		l.logEvent("defend_ended", map[string]any{"ticks": l.tick - l.engagedAt})
	}
	return false
}

func inMeleeReach(p act.Pose, e belief.Entity) bool {
	return horizontal(p.Feet, e.Pos) <= meleeReach &&
		math.Abs(float64(e.Pos.Y()-p.Feet.Y())) <= reachHeight
}

func modeName(m mode) string {
	switch m {
	case modeRetreat:
		return "retreat"
	case modeStepBack:
		return "step_back"
	}
	return "fight"
}

// stepBackEnd is when a player's blow stops being answered by backing away.
func (l *Life) stepBackEnd() uint64 { return l.attackerUntil - attackerTicks + stepBackTicks }

// noteHurt finds what most likely hit the unit: a hostile mob close by or, for
// a shooter, farther off, else a player beside it. A blow from nothing known
// (a fall, fire, hunger) leaves the unit's behaviour alone.
func (l *Life) noteHurt(p act.Pose) {
	var (
		mob, player         belief.Entity
		mobD, playerD       = math.MaxFloat64, math.MaxFloat64
		mobSeen, playerSeen bool
	)
	l.ents.Each(func(e belief.Entity) {
		d := horizontal(p.Feet, e.Pos)
		switch {
		case e.Player:
			if d <= attributionRange && d < playerD {
				player, playerD, playerSeen = e, d, true
			}
		case kindOf(e.Type) != other && (kindOf(e.Type) != provoked || inMeleeReach(p, e)):
			limit := attributionRange
			if shooters[name(e.Type)] {
				limit = shooterRange
			}
			if d <= limit && d < mobD {
				mob, mobD, mobSeen = e, d, true
			}
		}
	})
	switch {
	case mobSeen && (!playerSeen || mobD < playerD):
		l.attacker, l.attackerPlayer = mob.ID, false
		l.logEvent("hurt_by", map[string]any{"entity": name(mob.Type)})
	case playerSeen:
		l.attacker, l.attackerPlayer = player.ID, true
		l.logEvent("hurt_by", map[string]any{"entity": "player"})
	default:
		return
	}
	l.attackerUntil = l.tick + attackerTicks
}

// giveUp stops considering a mob a threat for a while: it cannot be reached,
// or the fight with it is going nowhere.
func (l *Life) giveUp(id uint64) {
	if len(l.ignore) >= maxIgnored {
		clear(l.ignore)
	}
	l.ignore[id] = l.tick + ignoreTicks
	l.chase = nil
	l.fighting = 0
}

// Fight sends this tick's input while engaged: it faces the threat, then
// closes in and strikes, backs away, or stands its ground.
func (l *Life) Fight(ctx context.Context, a skill.Actuator, s belief.Snapshot) error {
	l.idle = idleState{}
	e, ok := l.ents.Get(l.target.ID)
	if l.target.ID == 0 || !ok {
		return l.still(a)
	}
	p := a.Pose()
	switch l.mode {
	case modeRetreat:
		return l.retreat(ctx, a, p, e, 8)
	case modeStepBack:
		return l.retreat(ctx, a, p, e, 4)
	}
	if inMeleeReach(p, e) {
		return l.strike(a, p, e, s)
	}
	return l.approach(ctx, a, p, e)
}

func (l *Life) face(p act.Pose, e belief.Entity) act.Input {
	yaw, pitch := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
	return act.Input{Yaw: yaw, Pitch: pitch}
}

// strike turns to the mob, holds the best weapon and hits on the cadence.
func (l *Life) strike(a skill.Actuator, p act.Pose, e belief.Entity, s belief.Snapshot) error {
	slot, held := loadout(s)
	if (s.HeldKnown && s.HeldSlot != slot) || (!s.HeldKnown && held.Name != "") {
		if err := a.SelectHotbar(slot, held); err != nil {
			return err
		}
	}
	if err := a.Tick(l.face(p, e)); err != nil {
		return err
	}
	if l.tick-l.lastAttack >= attackEvery {
		l.lastAttack = l.tick
		// An item the server never listed cannot be quoted; the swing is
		// skipped rather than failing the whole fight.
		_ = a.Attack(e.ID, slot, held)
	}
	return nil
}

// approach walks toward the mob. Mobs mostly come to the unit, so the walk is
// replanned only when the mob has moved to another cell.
func (l *Life) approach(ctx context.Context, a skill.Actuator, p act.Pose, e belief.Entity) error {
	cell := cellOf(e.Pos)
	if l.chase == nil || (l.tick-l.chaseAt >= chaseEvery && l.chaseGoal != cell) {
		goal, ok := l.standNear(cell)
		if !ok {
			l.giveUp(e.ID)
			return l.stillFacing(a, p, e)
		}
		l.chase = skill.NewNavigate(l.world, l.self, goal, chaseWithin)
		l.chaseGoal, l.chaseAt = cell, l.tick
	}
	switch st, _ := l.chase.Step(ctx, a); st {
	case skill.Running:
		return nil
	case skill.Failed:
		l.giveUp(e.ID)
	}
	return l.stillFacing(a, p, e)
}

func (l *Life) stillFacing(a skill.Actuator, p act.Pose, e belief.Entity) error {
	return a.Tick(l.face(p, e))
}

// standNear finds a cell to walk to that is at or beside the mob's own.
func (l *Life) standNear(cell protocol.BlockPos) (protocol.BlockPos, bool) {
	if c, ok := cellAt(l.world, cell.X(), cell.Z(), cell.Y()); ok {
		return c, true
	}
	for _, d := range [...][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
		if c, ok := cellAt(l.world, cell.X()+d[0], cell.Z()+d[1], cell.Y()); ok {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}
