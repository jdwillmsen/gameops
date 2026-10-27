package life

import (
	"context"
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// spiderOrbitQuarter is the quarter turn of the heading that walks a
	// tangent around the target, so the feet keep moving between swings.
	spiderOrbitQuarter = 90
	// spiderOrbitTicks is how many orbit steps that moved nowhere mean the
	// walk is against a wall and must turn the other way, the same three the
	// shared circle uses.
	spiderOrbitTicks = 3
)

// spiderSwarm fights cave spiders on the move. Their poison is what makes a
// swarm dangerous, and a stationary unit takes every bite, so this variant
// orbits its one focus between swings, even in a role whose shared circle
// would plant it. Once no spider is inside the safe ring a hurt unit is left
// to eat and recover.
type spiderSwarm struct{}

func (spiderSwarm) Name() string { return "spider/swarm" }

func (spiderSwarm) Family() tactics.Family { return tactics.FamilySpider }

func (spiderSwarm) Engage(_ context.Context, f *Fight) (bool, error) {
	if !spiderTacticAllowed(f) || name(f.Target.Type) != "cave_spider" {
		return false, nil
	}
	p, e := f.Pose, f.Target
	if f.Snapshot.HealthKnown && float64(f.Snapshot.Health) <= f.Params.HealBelow &&
		!f.Life.enemyNear(p.Feet, f.Params.HealSafe) {
		// After the swarm: no spider close enough to interrupt a meal, so let
		// the role's own heal run instead of another swing.
		return false, nil
	}
	if !inMeleeReach(p, e) {
		// The approach belongs to the role; the orbit is for contact.
		return false, nil
	}
	f.Life.lowerShield(f.Actuator)
	if f.Life.tick-f.Life.lastAttack >= attackEvery {
		return true, f.Life.strike(f.Actuator, p, e, f.Snapshot)
	}
	return true, f.Life.spiderSwarmOrbit(f.Actuator, p, e, f.Params.OrbitMin)
}

// spiderSwarmOrbit walks a tangent around the focus. A Vanguard's shared
// circle plants it on purpose, so the swarm walks its own step to stay out of
// a poison stack's way. A tangent that has no safe floor ahead is skipped; a
// step that moves nowhere turns the orbit around.
func (l *Life) spiderSwarmOrbit(a skill.Actuator, p act.Pose, e belief.Entity, min float64) error {
	w := l.warrior
	yaw, _ := lookAt(p.Feet.Add(mgl32.Vec3{0, 1.62, 0}), e.Pos.Add(mgl32.Vec3{0, bodyCentre, 0}))
	if horizontal(p.Feet, e.Pos) < min {
		// Inside the orbit the spider swings free: step out facing it.
		if !l.spiderCanBackOff(p, e) {
			return a.Tick(act.Input{Yaw: yaw})
		}
		return a.Tick(act.Input{Yaw: yaw, Forward: -1})
	}
	turns := [2]float32{1, -1}
	if w.orbit < 0 {
		turns = [2]float32{-1, 1}
	}
	for _, turn := range turns {
		next := yaw + spiderOrbitQuarter*turn
		if !l.spiderCanOrbit(p, next) {
			continue
		}
		if err := a.Tick(act.Input{Yaw: next, Forward: 1}); err != nil {
			return err
		}
		if (mgl32.Vec2{p.Feet.X() - w.circX, p.Feet.Z() - w.circZ}).Len() < 0.05 {
			if w.circStuck++; w.circStuck >= spiderOrbitTicks {
				if w.orbit > 0 {
					w.orbit = -1
				} else {
					w.orbit = 1
				}
				w.circStuck = 0
			}
		} else {
			w.circStuck = 0
		}
		w.circX, w.circZ = p.Feet.X(), p.Feet.Z()
		return nil
	}
	// Every tangent is blocked: hold the spot rather than walk into a wall.
	return a.Tick(act.Input{Yaw: yaw})
}

// spiderCanOrbit says whether a tangent heading still has floor the planner
// accepts, so a circling swarm keeps out of lava, water and the void.
func (l *Life) spiderCanOrbit(p act.Pose, yaw float32) bool {
	r := float64(yaw) * math.Pi / 180
	x := int32(math.Floor(float64(p.Feet.X()) - math.Sin(r)*spiderStepAhead))
	z := int32(math.Floor(float64(p.Feet.Z()) + math.Cos(r)*spiderStepAhead))
	_, ok := cellAt(l.world, x, z, cellOf(p.Feet).Y())
	return ok
}

func init() { RegisterTactic(spiderSwarm{}) }
