package life

import (
	"errors"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

var homeCell = protocol.BlockPos{5, 1, 5}

func homeRig(t *testing.T, home *protocol.BlockPos) *rig {
	t.Helper()
	cfg := defaults
	cfg.Wander, cfg.Home = false, home
	r := newRig(t, cfg, start)
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: start.Add(mgl32.Vec3{0, 1.62, 0})})
	return r
}

func TestAUnitAwayFromHomeWalksThereBeforeAnythingElse(t *testing.T) {
	r := homeRig(t, &homeCell)
	r.run(2)
	if r.count("home_started") != 1 {
		t.Fatalf("log = %+v", r.log.lines)
	}
	if !r.l.Tending(r.tracker.Snapshot(), true) {
		t.Fatal("the walk is not claiming the unit's ticks, so a queued task would run first")
	}
	if d := r.until(1500, func() bool { return dist(r.feet(), mgl32.Vec3{5.5, 1, 5.5}) < 2.5 }); d >= 1500 {
		t.Fatalf("never got home; at %v", r.feet())
	}
	r.run(5)
	if r.count("home_done") != 1 || r.count("home_failed") != 0 {
		t.Fatalf("log = %+v", r.log.lines)
	}
	if r.l.Tending(r.tracker.Snapshot(), true) {
		t.Error("still claiming the unit at home: the work plan cannot start")
	}
	r.run(300)
	if r.count("home_started") != 1 {
		t.Errorf("walked home %d times for one arrival", r.count("home_started"))
	}
}

func TestWithoutAHomeTheUnitStaysWhereItCameBack(t *testing.T) {
	r := homeRig(t, nil)
	r.run(100)
	if r.count("home_started") != 0 || r.l.Tending(r.tracker.Snapshot(), true) {
		t.Errorf("acted with no home: %+v", r.log.lines)
	}
}

func TestAUnitAlreadyHomeDoesNotWalk(t *testing.T) {
	near := protocol.BlockPos{20, 1, 22}
	r := homeRig(t, &near)
	r.run(30)
	if r.count("home_started") != 0 {
		t.Errorf("walked from %v to a home beside it: %+v", r.feet(), r.log.lines)
	}
	// And a later outing is not undone: the work that takes the unit away
	// from home does not call it back.
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: mgl32.Vec3{35, 2.62, 35}})
	r.run(30)
	if r.count("home_started") != 0 {
		t.Error("called the unit back from an errand")
	}
}

func TestADeathSendsTheUnitHomeAgain(t *testing.T) {
	r := homeRig(t, &homeCell)
	if d := r.until(1500, func() bool { return r.count("home_done") == 1 }); d >= 1500 {
		t.Fatalf("never got home: %+v", r.log.lines)
	}
	r.run(5)
	r.die()
	// It respawns at the world spawn.
	r.respawnTo(mgl32.Vec3{30.5, 1, 30.5})
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: mgl32.Vec3{30.5, 2.62, 30.5}})
	if d := r.until(2000, func() bool { return r.count("home_done") == 2 }); d >= 2000 {
		t.Fatalf("did not walk home after the death: %+v", r.log.lines)
	}
}

func TestAFailingWalkIsRetriedAndThenGivenUp(t *testing.T) {
	r := homeRig(t, &homeCell)
	made := 0
	r.l.makeHome = func() skill.Skill {
		made++
		return &fakeVisit{name: "go_home", doneAfter: 2, err: errors.New("no loaded ground toward home")}
	}
	r.run(homeMaxFails * (homeRetryBackoff + 10))
	if made != homeMaxFails || r.count("home_failed") != homeMaxFails {
		t.Fatalf("made %d walks, %d failures; want %d", made, r.count("home_failed"), homeMaxFails)
	}
	r.run(homeRetryBackoff * 3)
	if made != homeMaxFails {
		t.Errorf("tried again after giving up: %d walks", made)
	}
	// A new life starts the count afresh.
	r.l.Reset()
	r.run(5)
	if made != homeMaxFails+1 {
		t.Errorf("made %d walks after a reset, want %d", made, homeMaxFails+1)
	}
}

func TestAFightPausesTheWalkHomeAndItResumes(t *testing.T) {
	r := homeRig(t, &homeCell)
	v := &fakeVisit{name: "go_home", doneAfter: 1 << 30}
	r.l.makeHome = func() skill.Skill { return v }
	r.run(5)
	r.spawn(70, "zombie", start.Add(mgl32.Vec3{2, 0, 0}))
	engaged := 0
	for i := 0; i < 30; i++ {
		if r.step() {
			engaged++
		}
	}
	if engaged == 0 {
		t.Fatal("never fought")
	}
	r.l.ents.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	steps := v.steps
	r.run(60)
	if v.resumes != 1 || v.steps <= steps {
		t.Errorf("resumed %d times, %d steps after the fight", v.resumes, v.steps-steps)
	}
}

func TestAWalkThatNeverArrivesIsEndedAtItsLimit(t *testing.T) {
	r := homeRig(t, &homeCell)
	r.l.makeHome = func() skill.Skill { return &fakeVisit{name: "go_home", doneAfter: 1 << 30} }
	r.run(homeLimit + 50)
	if r.count("home_failed") != 1 || r.l.home.visit != nil || r.l.home.pending {
		t.Errorf("log = %+v, pending %v", r.log.lines, r.l.home.pending)
	}
}

func TestRecoveryOfDropsComesBeforeTheWalkHome(t *testing.T) {
	cfg := defaults
	cfg.Wander, cfg.Home, cfg.Recover = false, &homeCell, true
	r := newRig(t, cfg, start)
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: start.Add(mgl32.Vec3{0, 1.62, 0})})
	if d := r.until(1500, func() bool { return r.count("home_done") == 1 }); d >= 1500 {
		t.Fatalf("never got home: %+v", r.log.lines)
	}
	r.perish()
	r.dropAt(70, start.Add(mgl32.Vec3{1, 0, -1}), idStoneSwrd, 1)
	r.respawnTo(respawnAt)
	r.run(2)
	if r.count("recover_started") != 1 || r.l.home.visit != nil {
		t.Errorf("recovery %d, home visit %v; log = %+v", r.count("recover_started"), r.l.home.visit != nil, r.log.lines)
	}
}

func TestATripIsNotOwedFromAnotherDimension(t *testing.T) {
	r := homeRig(t, &homeCell)
	r.tracker.Apply(&packet.ChangeDimension{Dimension: 1, Position: mgl32.Vec3{30, 70, 30}})
	r.run(10)
	r.tracker.Apply(&packet.ChangeDimension{Dimension: 0, Position: mgl32.Vec3{30, 2.62, 30}})
	r.run(30)
	if r.count("home_started") != 0 {
		t.Errorf("walked home on returning from another dimension: %+v", r.log.lines)
	}
}

func TestAWalkHomeEndsWhenTheUnitGoesCreative(t *testing.T) {
	r := homeRig(t, &homeCell)
	v := &fakeVisit{name: "go_home", doneAfter: 1 << 30}
	r.l.makeHome = func() skill.Skill { return v }
	r.run(5)
	if v.steps == 0 {
		t.Fatal("the trip never started")
	}
	r.tracker.SetUniqueID(5)
	r.tracker.Apply(&packet.UpdatePlayerGameType{GameType: 1, PlayerUniqueID: 5})
	if !r.tracker.Snapshot().Creative {
		t.Fatal("could not put the unit in creative with this packet")
	}
	steps := v.steps
	r.run(20)
	if v.steps != steps || r.l.home.visit != nil {
		t.Errorf("the trip carried on in creative: %d steps more", v.steps-steps)
	}
}
