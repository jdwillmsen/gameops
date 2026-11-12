package life

import (
	"context"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

var respawnAt = mgl32.Vec3{3.5, 1, 3.5}

func recoverRig(t *testing.T, recover bool) *rig {
	t.Helper()
	cfg := defaults
	cfg.Wander, cfg.Recover = false, recover
	r := newRig(t, cfg, start)
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: start.Add(mgl32.Vec3{0, 1.62, 0})})
	return r
}

// perish kills a unit that holds some items, as the read loop sees it.
func (r *rig) perish() {
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 5)
	r.run(3)
	r.die()
}

// respawnTo brings the unit back with an empty inventory somewhere else.
func (r *rig) respawnTo(at mgl32.Vec3) {
	r.tracker.Apply(&packet.InventoryContent{WindowID: 0, Content: make([]protocol.ItemInstance, 36)})
	eye := at.Add(mgl32.Vec3{0, 1.62, 0})
	r.a.Apply(&packet.CorrectPlayerMovePrediction{Position: eye, OnGround: true})
	r.tracker.Apply(&packet.CorrectPlayerMovePrediction{Position: eye, OnGround: true})
	r.setHealth(20)
}

func (r *rig) dropAt(id uint64, at mgl32.Vec3, item int32, count uint16) {
	r.l.Apply(&packet.AddItemActor{EntityUniqueID: int64(id), EntityRuntimeID: id, Position: at,
		Item: protocol.ItemInstance{Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: item}, Count: count}}})
}

func (r *rig) count(event string) int { return len(r.events(event)) }

func reasons(r *rig) []any {
	var out []any
	for _, e := range r.events("recover_skipped") {
		out = append(out, e["reason"])
	}
	return out
}

func TestADeadUnitWalksBackToItsDropsAndPicksThemUp(t *testing.T) {
	r := recoverRig(t, true)
	r.perish()
	r.dropAt(70, start.Add(mgl32.Vec3{1, 0, -1}), idStoneSwrd, 1)
	r.respawnTo(respawnAt)
	r.run(2)
	if r.count("recover_started") != 1 {
		t.Fatalf("log = %+v", r.log.lines)
	}
	if !r.l.Tending(r.tracker.Snapshot(), true) {
		t.Fatal("the trip is not claiming the unit's ticks, even with a task running")
	}
	if d := r.until(600, func() bool { return dist(r.feet(), start) < 2.5 }); d >= 600 {
		t.Fatalf("never got back to the death point; at %v", r.feet())
	}
	// The server picks the stack up and gives it back.
	r.l.Apply(&packet.TakeItemActor{ItemEntityRuntimeID: 70, TakerEntityRuntimeID: selfID})
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 5)
	r.run(10)
	if r.count("recover_done") != 1 || r.count("recover_failed") != 0 {
		t.Fatalf("log = %+v", r.log.lines)
	}
	r.run(300)
	if r.count("recover_started") != 1 {
		t.Errorf("went back %d times for one death", r.count("recover_started"))
	}
}

func TestAnUnrecoveredDeathIsTriedOnlyOnce(t *testing.T) {
	r := recoverRig(t, true)
	r.perish()
	r.respawnTo(respawnAt)
	// Nothing was ever announced, so the trip ends on its own.
	r.run(900)
	if r.count("recover_started") != 1 || r.count("recover_done") != 1 {
		t.Fatalf("started %d, done %d; log = %+v", r.count("recover_started"), r.count("recover_done"), r.log.lines)
	}
}

func TestWithoutTheSettingADeathIsNotWalkedBackTo(t *testing.T) {
	r := recoverRig(t, false)
	r.perish()
	r.respawnTo(respawnAt)
	r.run(50)
	if r.count("recover_started")+r.count("recover_skipped") != 0 || dist(r.feet(), respawnAt) > 0.5 {
		t.Errorf("acted on a death with recovery off: %+v", r.log.lines)
	}
}

func TestADeathThatLostNothingIsSkipped(t *testing.T) {
	r := recoverRig(t, true)
	r.run(3)
	r.die()
	r.respawnTo(respawnAt)
	r.run(5)
	if got := reasons(r); len(got) != 1 || got[0] != "nothing_lost" {
		t.Errorf("skips = %v", got)
	}
}

func TestADeathTooFarOrInLavaOrAnotherDimensionIsSkipped(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*rig)
		at      mgl32.Vec3
		want    string
	}{
		{"far", nil, mgl32.Vec3{150, 1, 20}, "too_far"},
		{"lava", func(r *rig) { r.w.over[protocol.BlockPos{20, 0, 20}] = "lava" }, respawnAt, "hazard"},
		{"void", func(r *rig) { r.l.feet = mgl32.Vec3{20, -70, 20} }, respawnAt, "hazard"},
		{"unloaded", func(r *rig) { r.w.size = 10 }, mgl32.Vec3{3.5, 1, 3.5}, "chunk_not_loaded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := recoverRig(t, true)
			r.perish()
			if c.prepare != nil {
				c.prepare(r)
			}
			if c.name == "void" {
				r.l.mu.Lock()
				r.l.death.pos = mgl32.Vec3{20, -70, 20}
				r.l.mu.Unlock()
			}
			r.respawnTo(c.at)
			r.l.Tending(r.tracker.Snapshot(), false)
			r.l.Engaged(r.a.Pose(), r.tracker.Snapshot())
			r.l.Tending(r.tracker.Snapshot(), false)
			if got := reasons(r); len(got) != 1 || got[0] != c.want || r.count("recover_started") != 0 {
				t.Errorf("skips = %v, log = %+v", got, r.log.lines)
			}
		})
	}
	t.Run("dimension", func(t *testing.T) {
		r := recoverRig(t, true)
		r.perish()
		r.respawnTo(respawnAt)
		r.tracker.Apply(&packet.ChangeDimension{Dimension: 1, Position: respawnAt})
		r.l.Engaged(r.a.Pose(), r.tracker.Snapshot())
		r.l.Tending(r.tracker.Snapshot(), false)
		if got := reasons(r); len(got) != 1 || got[0] != "other_dimension" {
			t.Errorf("skips = %v", got)
		}
	})
}

func TestADeathIsNotWalkedBackToAfterTheDropsDespawned(t *testing.T) {
	r := recoverRig(t, true)
	r.perish()
	r.respawnTo(respawnAt)
	r.l.Engaged(r.a.Pose(), r.tracker.Snapshot())
	r.l.tick += recoverWindow + 1
	r.l.Tending(r.tracker.Snapshot(), false)
	if got := reasons(r); len(got) != 1 || got[0] != "too_late" {
		t.Errorf("skips = %v", got)
	}
}

// stroll stands in for the walk: it takes a fixed number of ticks.
type stroll struct {
	r       *rig
	left    int
	resumed int
}

func (s *stroll) Name() string                { return "recover" }
func (s *stroll) Ready(belief.Snapshot) error { return nil }
func (s *stroll) Resume()                     { s.resumed++ }
func (s *stroll) Step(_ context.Context, a skill.Actuator) (skill.Status, error) {
	if s.left--; s.left <= 0 {
		return skill.Done, nil
	}
	return skill.Running, s.r.l.still(a)
}

func TestAFightPausesTheTripAndThePlanStartsAfresh(t *testing.T) {
	r := recoverRig(t, true)
	v := &stroll{r: r, left: 400}
	r.l.makeRecover = func(lastDeath) skill.Skill { return v }
	r.perish()
	r.respawnTo(respawnAt)
	r.run(5)
	if r.count("recover_started") != 1 {
		t.Fatalf("log = %+v", r.log.lines)
	}
	left := v.left
	r.spawn(80, "minecraft:zombie", respawnAt.Add(mgl32.Vec3{2.5, 0, 0}))
	if engaged := r.step(); !engaged {
		t.Fatal("not engaged by a zombie at arm's length")
	}
	r.run(10)
	if v.left != left {
		t.Errorf("the trip advanced %d steps during the fight", left-v.left)
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 80})
	r.run(graceTicks + 5)
	if v.resumed != 1 {
		t.Errorf("resumed %d times, want once after the fight", v.resumed)
	}
	if v.left >= left {
		t.Error("the trip never carried on")
	}
	r.run(500)
	if r.count("recover_done") != 1 {
		t.Errorf("log = %+v", r.log.lines)
	}
}

func TestANewDeathDropsTheTripForTheOldOne(t *testing.T) {
	r := recoverRig(t, true)
	v := &stroll{r: r, left: 4000}
	r.l.makeRecover = func(lastDeath) skill.Skill { return v }
	r.perish()
	r.respawnTo(respawnAt)
	r.run(5)
	if r.l.rec.visit == nil {
		t.Fatal("no trip under way")
	}
	r.give(0, idStoneSwrd, 1)
	r.run(2)
	r.die()
	r.respawnTo(respawnAt)
	r.l.Engaged(r.a.Pose(), r.tracker.Snapshot())
	if r.l.rec.visit != nil {
		t.Error("the old trip survived a second death")
	}
}
