package life

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// Times of day, in ticks, that fall in each phase.
const (
	tickDay   = 6000
	tickDusk  = 12500
	tickNight = 15000
	tickDawn  = 23500
)

// hashWorld gives the test world the block hashes a night skill clicks with.
type hashWorld struct{ *world }

func (hashWorld) BlockHash(protocol.BlockPos) (uint32, bool) { return 1, true }

// fakeVisit is a skill that runs for a set number of steps and then ends.
type fakeVisit struct {
	name           string
	doneAfter      int
	err            error
	readyErr       error
	steps          int
	stops, resumes int
	inputOnStop    bool
}

func (v *fakeVisit) Name() string                { return v.name }
func (v *fakeVisit) Ready(belief.Snapshot) error { return v.readyErr }
func (v *fakeVisit) Resume()                     { v.resumes++ }
func (v *fakeVisit) Step(_ context.Context, a skill.Actuator) (skill.Status, error) {
	v.steps++
	p := a.Pose()
	if err := a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch}); err != nil {
		return skill.Failed, err
	}
	switch {
	case v.doneAfter > 0 && v.steps >= v.doneAfter && v.err != nil:
		return skill.Failed, v.err
	case v.doneAfter > 0 && v.steps >= v.doneAfter:
		return skill.Done, nil
	}
	return skill.Running, nil
}
func (v *fakeVisit) Stop(a skill.Actuator) {
	v.stops++
	if v.inputOnStop {
		p := a.Pose()
		_ = a.Tick(act.Input{Yaw: p.Yaw, Pitch: p.Pitch})
	}
}

// started is what the hook was asked to build.
type started struct {
	kind nightKind
	bed  protocol.BlockPos
}

type nightRig struct {
	*rig
	asked  []started
	visits []*fakeVisit
	// build makes each visit; tests set it before the night starts.
	build func(nightKind) *fakeVisit
}

func newNightRig(t *testing.T) *nightRig {
	t.Helper()
	cfg := defaults
	cfg.Wander, cfg.NightShelter = false, true
	r := newRig(t, cfg, start)
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: start.Add(mgl32.Vec3{0, 1.62, 0})})
	cfg.World = hashWorld{r.w}
	r.l.cfg = cfg
	nr := &nightRig{rig: r}
	nr.build = func(nightKind) *fakeVisit { return &fakeVisit{name: "night", doneAfter: 1 << 30} }
	r.l.makeNight = func(k nightKind, bed protocol.BlockPos) skill.Skill {
		nr.asked = append(nr.asked, started{k, bed})
		v := nr.build(k)
		nr.visits = append(nr.visits, v)
		return v
	}
	nr.clock(tickDay)
	return nr
}

func (nr *nightRig) clock(ticks int64) { nr.tracker.SetClock(0, false, int32(ticks)) }

func (nr *nightRig) setSpawn(p protocol.BlockPos) {
	nr.tracker.Apply(&packet.SetSpawnPosition{SpawnType: packet.SpawnTypePlayer, Position: p})
}

func (nr *nightRig) tending(working bool) bool {
	return nr.l.Tending(nr.tracker.Snapshot(), working)
}

func TestNightShelterIsOffUnlessSet(t *testing.T) {
	nr := newNightRig(t)
	nr.l.cfg.NightShelter = false
	nr.clock(tickNight)
	nr.run(50)
	if len(nr.asked) != 0 || nr.tending(false) {
		t.Errorf("acted at night with the setting off: %+v", nr.asked)
	}
}

func TestADuskWithNoBedSendsTheUnitToDigInAndDawnReturnsItToItsTask(t *testing.T) {
	nr := newNightRig(t)
	nr.clock(tickDay)
	nr.run(20)
	if len(nr.asked) != 0 {
		t.Fatalf("started by day: %+v", nr.asked)
	}
	nr.clock(tickDusk)
	nr.run(2)
	if len(nr.asked) != 1 || nr.asked[0].kind != nightShelter {
		t.Fatalf("asked = %+v, want one shelter", nr.asked)
	}
	// It claims the unit's ticks from a task that is running.
	if !nr.tending(true) {
		t.Fatal("the shelter is not claiming the unit's ticks while a task runs")
	}
	nr.clock(tickNight)
	nr.run(300)
	if nr.visits[0].steps < 250 || len(nr.asked) != 1 {
		t.Errorf("the shelter took %d steps, %d visits made", nr.visits[0].steps, len(nr.asked))
	}
	nr.visits[0].doneAfter = nr.visits[0].steps + 1
	nr.clock(tickDawn)
	nr.run(3)
	if nr.count("night_done") != 1 || nr.count("night_failed") != 0 {
		t.Fatalf("log = %+v", nr.log.lines)
	}
	if nr.tending(true) {
		t.Error("still claiming the unit at dawn: the task cannot resume")
	}
	nr.run(300)
	if len(nr.asked) != 1 {
		t.Errorf("%d visits in all", len(nr.asked))
	}
}

func TestAUnitWithABedNearItsRespawnPointGoesToTheBed(t *testing.T) {
	nr := newNightRig(t)
	bed := protocol.BlockPos{20, 1, 25}
	nr.w.over[bed] = "red_bed"
	nr.setSpawn(protocol.BlockPos{19, 1, 24})
	nr.clock(tickNight)
	nr.run(2)
	if len(nr.asked) != 1 || nr.asked[0] != (started{nightBed, bed}) {
		t.Fatalf("asked = %+v, want the bed at %v", nr.asked, bed)
	}
}

func TestWithoutAUsableBedTheUnitDigsIn(t *testing.T) {
	cases := []struct {
		name  string
		spawn protocol.BlockPos
		bed   *protocol.BlockPos
	}{
		{"no point ever set", protocol.BlockPos{math.MinInt32, math.MinInt32, math.MinInt32}, nil},
		{"the bed is gone", protocol.BlockPos{5, 1, 5}, nil},
		{"the bed is too far", protocol.BlockPos{39, 1, 39}, &protocol.BlockPos{39, 1, 38}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nr := newNightRig(t)
			if c.name == "the bed is too far" {
				nr.w.size = 200
				nr.l.cfg.World = hashWorld{nr.w}
				nr.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: mgl32.Vec3{2, 2.62, 2}})
				nr.a.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: mgl32.Vec3{2, 2.62, 2}})
				nr.setSpawn(protocol.BlockPos{100, 1, 100})
				nr.w.over[protocol.BlockPos{100, 1, 99}] = "red_bed"
			} else {
				nr.setSpawn(c.spawn)
				if c.bed != nil {
					nr.w.over[*c.bed] = "red_bed"
				}
			}
			nr.clock(tickNight)
			nr.run(2)
			if len(nr.asked) != 1 || nr.asked[0].kind != nightShelter {
				t.Fatalf("asked = %+v, want one shelter", nr.asked)
			}
		})
	}
}

func TestAFailedNightVisitIsNotRetriedForAMinute(t *testing.T) {
	nr := newNightRig(t)
	nr.build = func(nightKind) *fakeVisit {
		return &fakeVisit{name: "night", doneAfter: 2, err: errors.New("not enough blocks to shelter (need 10)")}
	}
	nr.clock(tickNight)
	nr.run(10)
	if nr.count("night_failed") != 1 || len(nr.asked) != 1 {
		t.Fatalf("failed %d, asked %d; log = %+v", nr.count("night_failed"), len(nr.asked), nr.log.lines)
	}
	if nr.tending(false) {
		t.Error("claims the unit's ticks during the back-off")
	}
	nr.run(nightFailBackoff)
	if len(nr.asked) != 2 {
		t.Errorf("asked %d times after the back-off, want 2", len(nr.asked))
	}
}

func TestANightVisitThatIsNotReadyIsSkippedAndLogged(t *testing.T) {
	nr := newNightRig(t)
	nr.build = func(nightKind) *fakeVisit {
		return &fakeVisit{name: "night", readyErr: errors.New("it is already day")}
	}
	nr.clock(tickNight)
	nr.run(20)
	if nr.count("night_skipped") != 1 || len(nr.asked) != 1 {
		t.Errorf("skipped %d, asked %d", nr.count("night_skipped"), len(nr.asked))
	}
	if nr.l.night.visit != nil {
		t.Error("a visit that was not ready is running")
	}
}

// A server that refuses sleep at dusk ends the first bed visits at once; they
// are repeated until the night comes, and only an endless refusal sends the
// unit to dig in.
func TestABedVisitRefusedAtDuskIsRepeatedThenGivenUp(t *testing.T) {
	nr := newNightRig(t)
	nr.w.over[protocol.BlockPos{20, 1, 25}] = "red_bed"
	nr.setSpawn(protocol.BlockPos{19, 1, 24})
	nr.build = func(k nightKind) *fakeVisit { return &fakeVisit{name: "night", doneAfter: 3} }
	nr.clock(tickDusk)
	nr.run(nightMaxBedVisits * (nightRetryBackoff + 6))
	beds, shelters := 0, 0
	for _, a := range nr.asked {
		if a.kind == nightBed {
			beds++
		} else {
			shelters++
		}
	}
	if beds != nightMaxBedVisits || shelters == 0 {
		t.Errorf("%d bed visits and %d shelters, want %d beds then a shelter", beds, shelters, nightMaxBedVisits)
	}
}

func TestADayResetsTheNightsBedVisits(t *testing.T) {
	nr := newNightRig(t)
	nr.w.over[protocol.BlockPos{20, 1, 25}] = "red_bed"
	nr.setSpawn(protocol.BlockPos{19, 1, 24})
	nr.l.night.bedVisits = nightMaxBedVisits
	nr.clock(tickDay)
	nr.run(5)
	nr.clock(tickNight)
	nr.run(2)
	if len(nr.asked) != 1 || nr.asked[0].kind != nightBed {
		t.Errorf("asked = %+v, want the bed again on a new night", nr.asked)
	}
}

func TestAFightTakesTheUnitOffTheNightVisitAndItResumesAfter(t *testing.T) {
	nr := newNightRig(t)
	nr.clock(tickNight)
	nr.run(5)
	v := nr.visits[0]
	v.inputOnStop = true
	nr.spawn(70, "zombie", start.Add(mgl32.Vec3{2, 0, 0}))
	engaged := 0
	for i := 0; i < 40; i++ {
		if nr.step() {
			engaged++
		}
	}
	if engaged == 0 || v.stops == 0 {
		t.Fatalf("engaged %d ticks, visit stopped %d times", engaged, v.stops)
	}
	// The mob leaves; the visit carries on, resumed once.
	nr.l.ents.Apply(&packet.RemoveActor{EntityUniqueID: 70})
	nr.run(120)
	if v.resumes != 1 || len(nr.asked) != 1 {
		t.Errorf("resumed %d times, %d visits made; log = %+v", v.resumes, len(nr.asked), nr.log.lines)
	}
	if v.steps < 10 {
		t.Errorf("the visit took %d steps after the fight", v.steps)
	}
}

func TestADeathEndsTheNightVisit(t *testing.T) {
	nr := newNightRig(t)
	nr.clock(tickNight)
	nr.run(5)
	nr.die()
	if nr.l.night.visit != nil {
		t.Error("the visit outlived the unit")
	}
}

func TestASleepingUnitDoesNotTryToEat(t *testing.T) {
	nr := newNightRig(t)
	nr.give(0, 340, 5)
	nr.tracker.Apply(&packet.UpdateAttributes{EntityRuntimeID: selfID, Attributes: []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.hunger", Value: 3, Max: 20}}}})
	if _, ok := nr.l.wantsMeal(nr.tracker.Snapshot()); !ok {
		t.Fatal("a hungry unit with bread wants no meal, so the test proves nothing")
	}
	nr.tracker.Apply(&packet.SetActorData{EntityRuntimeID: selfID, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFlagsTwo: int64(1) << (protocol.EntityDataFlagSleeping - 64)}})
	s := nr.tracker.Snapshot()
	if !s.Sleeping {
		t.Fatal("the tracker did not take the sleeping flag")
	}
	if _, ok := nr.l.wantsMeal(s); ok {
		t.Error("wants a meal in bed")
	}
}

// A bed that cannot be used sends the unit to dig in at once, not after the
// minute a failed shelter waits.
func TestAFailedBedVisitFallsBackToTheShelterSoon(t *testing.T) {
	nr := newNightRig(t)
	nr.w.over[protocol.BlockPos{20, 1, 25}] = "red_bed"
	nr.setSpawn(protocol.BlockPos{19, 1, 24})
	nr.build = func(k nightKind) *fakeVisit {
		if k == nightBed {
			return &fakeVisit{name: "night", doneAfter: 2, err: errors.New("no standing spot within reach of the bed")}
		}
		return &fakeVisit{name: "night", doneAfter: 1 << 30}
	}
	nr.clock(tickNight)
	nr.run(nightRetryBackoff + 20)
	if len(nr.asked) != 2 || nr.asked[0].kind != nightBed || nr.asked[1].kind != nightShelter {
		t.Fatalf("asked = %+v, want the bed and then a shelter", nr.asked)
	}
}

func TestANightVisitDoesNotUseUpTheWalkHomesTime(t *testing.T) {
	nr := newNightRig(t)
	home := protocol.BlockPos{5, 1, 5}
	nr.l.cfg.Home = &home
	nr.l.home = newHomeState(nr.l.cfg)
	trip := &fakeVisit{name: "go_home", doneAfter: 1 << 30}
	nr.l.makeHome = func() skill.Skill { return trip }
	nr.run(10)
	if trip.steps == 0 {
		t.Fatal("the trip never started")
	}
	// Dusk comes, and the night visit keeps the unit for longer than the trip's
	// budget would allow.
	night := &fakeVisit{name: "night", doneAfter: homeLimit + 100}
	nr.build = func(nightKind) *fakeVisit { return night }
	nr.l.night.blockedUntil = 0
	nr.clock(tickNight)
	nr.run(homeLimit + 150)
	if nr.count("night_done") != 1 {
		t.Fatalf("log = %+v", nr.log.lines)
	}
	nr.clock(tickDawn)
	steps := trip.steps
	nr.run(50)
	if nr.count("home_failed") != 0 || trip.steps <= steps {
		t.Errorf("the trip was dropped after the night: failed %d, steps %d -> %d", nr.count("home_failed"), steps, trip.steps)
	}
}
