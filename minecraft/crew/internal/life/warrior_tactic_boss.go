package life

import (
	"context"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// The boss tactics are variants of the one boss fight a profile can name, so
// the trainer can score them against each other.
const (
	bossStandardName  = "boss/standard"
	bossKiteBurstName = "boss/kiteburst"
)

// Kite-and-burst numbers. Distances are blocks from the boss, durations
// ticks of 50 ms.
const (
	// kiteStandOff is where the Vanguard waits out a due blow: a block past
	// bossReach, which is already the warden's melee box with slack, so a
	// lagging position report still leaves it clear.
	kiteStandOff = bossReach + 1.0
	// kiteBand is how loosely it holds that stand-off; a tight band would
	// have it shuffle back and forth over a block of ground.
	kiteBand = 1.5
	// kiteLeave is how many ticks before a blow is due the Vanguard starts
	// backing out. A walk covers about 0.22 blocks a tick, and from circling
	// range (orbitMin to meleeReach) out to the stand-off is up to 3.5
	// blocks: 16 ticks.
	kiteLeave = 16
	// kiteBoomMargin is how far past the boom's reach a waiting Striker
	// stands. The boom ignores shield, armour and walls, so range is the
	// only thing that stops it and a position report can be a block stale.
	kiteBoomMargin = 1.0
)

func init() {
	RegisterTactic(bossStandard{})
	RegisterTactic(bossKiteBurst{})
}

// bossStandard names the engine's own boss fight: the Vanguard guards the
// beat of the boss's melee, the Striker joins under cover, the Ranger shoots
// from outside the boom. It declines every tick so that fight runs, which
// keeps it identical to naming no tactic, and gives a profile a name for the
// baseline a variant is scored against.
type bossStandard struct{}

func (bossStandard) Name() string           { return bossStandardName }
func (bossStandard) Family() tactics.Family { return tactics.FamilyBoss }

func (bossStandard) Engage(context.Context, *Fight) (bool, error) { return false, nil }

// bossKiteBurst is an experiment on the warden: instead of meeting each melee
// blow on a shield the blow then disables for 5 s, the Vanguard swings while
// the warden's melee is recovering and backs out of its reach before the next
// blow is due, so nothing lands. It never backs further than the stand-off,
// since stepping out of melee range only offers the boom. A Striker with no
// cover to join under waits past the boom's reach rather than inside it.
//
// It takes the tick only for those two jobs, in a plain fight: a unit that
// is eating, regrouping, under a squad order, or without the beat of a blow
// that landed leaves the tick to the standard fight, as does every role and
// boss it has no better idea for.
type bossKiteBurst struct{}

func (bossKiteBurst) Name() string           { return bossKiteBurstName }
func (bossKiteBurst) Family() tactics.Family { return tactics.FamilyBoss }

func (bossKiteBurst) Engage(ctx context.Context, f *Fight) (bool, error) {
	l := f.Life
	w := l.warrior
	if w == nil || !kiteApplies(l, f) {
		return false, nil
	}
	switch w.role {
	case RoleVanguard:
		return l.kiteVanguard(ctx, f)
	case RoleStriker:
		return l.kiteStriker(f)
	}
	return false, nil
}

// kiteApplies says the tick is a plain boss fight against a boss with a melee
// worth timing: not a meal, a regroup, a retreat or a squad order, whose
// handling stays the standard fight's.
func kiteApplies(l *Life, f *Fight) bool {
	w := l.warrior
	b := &w.boss
	return b.active && b.id == f.Target.ID && b.prof.meleeEvery > 0 &&
		l.mode == modeFight && !w.healWant && !b.regroup && w.order().Kind == OrderNone
}

// kiteVanguard swings through the part of the warden's beat where no blow is
// due and backs out of its reach for the part where one is. Without a landed
// blow there is no beat to time, and the standard fight's guard is what finds
// one.
func (l *Life) kiteVanguard(ctx context.Context, f *Fight) (bool, error) {
	b := &l.warrior.boss
	if b.hitAt == 0 {
		return false, nil
	}
	every := b.prof.meleeEvery
	since := l.tick - b.hitAt
	phase := since % every
	// A blow can land guardTail late, as the standard guard allows for.
	due := phase >= every-kiteLeave || (since >= every && phase < guardTail)

	l.cancelDraw(f.Actuator)
	if !due {
		return true, l.warriorMelee(ctx, f.Actuator, f.Pose, f.Target, f.Snapshot)
	}
	// A raised shield walks at a crawl, and this tick is for getting clear.
	l.lowerShield(f.Actuator)
	in, _ := l.bandStep(f.Pose, f.Target, kiteStandOff, kiteStandOff+kiteBand, false)
	return true, f.Actuator.Tick(in)
}

// kiteStriker holds a Striker the standard fight would leave in its own band
// past the boom instead. Joining the melee, and keeping clear when hurt, are
// the standard fight's.
func (l *Life) kiteStriker(f *Fight) (bool, error) {
	b := &l.warrior.boss
	if stance, _, _ := l.strikerPlan(f.Pose, f.Target, f.Snapshot); stance != stanceWait {
		return false, nil
	}
	near := b.prof.boom + kiteBoomMargin
	l.cancelDraw(f.Actuator)
	return true, l.bossKeep(f.Actuator, f.Pose, f.Target, f.Snapshot, near, near+b.prof.strikerFar-b.prof.strikerNear)
}
