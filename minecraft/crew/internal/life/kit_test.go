package life

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// visit stands in for the withdraw: it walks for a few ticks, then ends the
// way the test says, optionally stocking the unit as the chest would.
type visit struct {
	r       *rig
	ticks   int
	failure error
	stock   func()
	stepped int
	stopped int
	weapon  bool
	food    int
	full    bool
}

func (v *visit) Full() bool { return v.full }

func (v *visit) Name() string                { return "withdraw_chest" }
func (v *visit) Ready(belief.Snapshot) error { return nil }
func (v *visit) Stop(skill.Actuator)         { v.stopped++ }
func (v *visit) Step(_ context.Context, a skill.Actuator) (skill.Status, error) {
	v.stepped++
	if v.stepped <= v.ticks {
		return skill.Running, v.r.l.still(a)
	}
	if v.failure != nil {
		return skill.Failed, v.failure
	}
	if v.stock != nil {
		v.stock()
	}
	return skill.Done, nil
}

func kitRig(t *testing.T) (*rig, *[]*visit) {
	t.Helper()
	cfg := defaults
	cfg.Kit = &Kit{Chest: protocol.BlockPos{30, 0, 30}, Food: 6}
	r := newRig(t, cfg, start)
	visits := &[]*visit{}
	r.l.makeKit = func(weapon bool, food int) skill.Skill {
		v := &visit{r: r, ticks: 5, weapon: weapon, food: food}
		v.stock = func() { r.give(0, idStoneSwrd, 1); r.give(1, idBread, 6) }
		*visits = append(*visits, v)
		return v
	}
	return r, visits
}

func TestAnUnarmedHungryUnitVisitsTheKitOnceItHasSettled(t *testing.T) {
	r, visits := kitRig(t)
	r.run(kitSettleTicks - 5)
	if len(*visits) != 0 {
		t.Fatal("went to the chest before the inventory could have arrived")
	}
	r.run(60)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d, want 1", len(*visits))
	}
	if v := (*visits)[0]; !v.weapon || v.food != 6 {
		t.Errorf("asked for weapon=%v food=%d, want a weapon and 6 food", v.weapon, v.food)
	}
	if !r.logged("kit_restocked") || r.logged("kit_empty") {
		t.Errorf("log = %+v", r.log.lines)
	}
	r.run(500)
	if len(*visits) != 1 {
		t.Errorf("visits = %d after restocking, want 1", len(*visits))
	}
}

func TestOnlyWhatIsMissingIsAskedFor(t *testing.T) {
	r, visits := kitRig(t)
	r.give(2, idStoneSwrd, 1)
	r.run(kitSettleTicks + 10)
	if len(*visits) != 1 || (*visits)[0].weapon || (*visits)[0].food != 6 {
		t.Fatalf("visits = %+v, want food only", *visits)
	}
}

func TestAnArmedAndFedUnitStaysAway(t *testing.T) {
	r, visits := kitRig(t)
	r.give(2, idStoneSwrd, 1)
	r.give(3, idBread, 4)
	r.run(500)
	if len(*visits) != 0 {
		t.Error("visited the kit with nothing missing")
	}
}

func TestAUnitWithAFullHotbarStaysAway(t *testing.T) {
	r, visits := kitRig(t)
	for slot := 0; slot < 9; slot++ {
		r.give(slot, 3, 64)
	}
	r.give(20, idBread, 10)
	r.run(500)
	if len(*visits) != 0 {
		t.Errorf("visits = %d with no hotbar slot free, want 0", len(*visits))
	}
}

func TestAKitNeverInterruptsATask(t *testing.T) {
	r, _ := kitRig(t)
	snap := r.tracker.Snapshot()
	r.l.tick = kitSettleTicks * 3
	r.l.Tending(snap, true)
	r.l.tick += kitSettleTicks * 3
	if r.l.Tending(snap, true) {
		t.Error("wants the unit while a task is running")
	}
	if !r.l.Tending(snap, false) {
		t.Error("does not want an idle unit that has lacked its kit for a long time")
	}
}

func TestWithoutAKitTheUnitNeverLooksForOne(t *testing.T) {
	r := newRig(t, defaults, start)
	r.run(500)
	if r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("tending without a kit")
	}
}

func TestAnEmptyKitIsReportedAndBackedOffFor(t *testing.T) {
	r, visits := kitRig(t)
	r.l.makeKit = func(bool, int) skill.Skill {
		v := &visit{r: r, ticks: 3}
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + 30)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d", len(*visits))
	}
	var empty *logged
	for i := range r.log.lines {
		if r.log.lines[i].event == "kit_empty" {
			empty = &r.log.lines[i]
		}
	}
	if empty == nil {
		t.Fatalf("no kit_empty in %+v", r.log.lines)
	}
	if got := empty.fields["missing"].([]string); len(got) != 2 {
		t.Errorf("missing = %v, want weapon and food", got)
	}
	r.run(kitEmptyBackoff - 100)
	if len(*visits) != 1 {
		t.Errorf("went back %d times inside the back-off", len(*visits)-1)
	}
	r.run(300)
	if len(*visits) != 2 {
		t.Errorf("visits = %d, want a second try after the back-off", len(*visits))
	}
}

func TestAVisitThatFillsTheHotbarIsReportedAsNoRoom(t *testing.T) {
	r, visits := kitRig(t)
	for slot := 1; slot < 9; slot++ {
		r.give(slot, 3, 64)
	}
	r.l.makeKit = func(weapon bool, food int) skill.Skill {
		v := &visit{r: r, ticks: 3, weapon: weapon, food: food, full: true}
		v.stock = func() { r.give(0, idStoneSwrd, 1) }
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + 30)
	if len(*visits) != 1 {
		t.Fatalf("visits = %d", len(*visits))
	}
	if r.logged("kit_empty") || !r.logged("kit_no_room") {
		t.Errorf("log = %+v, want kit_no_room and no kit_empty", r.log.lines)
	}
}

func TestAFailedVisitIsLoggedWithoutPositionsAndBackedOff(t *testing.T) {
	r, visits := kitRig(t)
	r.l.makeKit = func(bool, int) skill.Skill {
		v := &visit{r: r, ticks: 2, failure: errors.New("no chest at the target")}
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + 30)
	if !r.logged("kit_failed") {
		t.Fatalf("log = %+v", r.log.lines)
	}
	for _, l := range r.log.lines {
		for k := range l.fields {
			if strings.Contains(k, "pos") || k == "x" || k == "y" || k == "z" || k == "chest" {
				t.Errorf("%s logged %q", l.event, k)
			}
		}
	}
	r.run(kitFailBackoff - 100)
	if len(*visits) != 1 {
		t.Errorf("retried %d times inside the back-off", len(*visits)-1)
	}
}

// A chest whose chunk is merely late, as on a cold join, costs the unit
// seconds, not the back-off: after two waits the unit is rearmed well inside
// half a minute of joining.
func TestAUnitRearmsSoonWhileTheKitChestsChunkIsArriving(t *testing.T) {
	r, visits := kitRig(t)
	late := 2
	r.l.makeKit = func(weapon bool, food int) skill.Skill {
		v := &visit{r: r, weapon: weapon, food: food}
		if late > 0 {
			late--
			// The withdraw itself waits the chunk out before giving up, so
			// a failing visit burns about as long as the back-off after it.
			v.ticks, v.failure = 100, skill.ErrChestChunkNotLoaded
		} else {
			v.ticks = 40
			v.stock = func() { r.give(0, idStoneSwrd, 1); r.give(1, idBread, 6) }
		}
		*visits = append(*visits, v)
		return v
	}
	if used := r.until(600, func() bool { return r.logged("kit_restocked") }); used >= 600 {
		t.Fatalf("not rearmed within %d ticks of joining: log %+v", used, r.log.lines)
	}
	if len(*visits) != 3 {
		t.Errorf("visits = %d, want two waits for the chunk then the re-arm", len(*visits))
	}
}

// Six waits in a row for a chunk that never arrives are each retried soon;
// the seventh is a failure like any other and waits the long back-off, so
// the unit is not kept walking back to a chest that is not coming.
func TestAKitChunkThatNeverLoadsBacksOffAfterSixShortRetries(t *testing.T) {
	r, visits := kitRig(t)
	r.l.makeKit = func(bool, int) skill.Skill {
		v := &visit{r: r, ticks: 2, failure: skill.ErrChestChunkNotLoaded}
		*visits = append(*visits, v)
		return v
	}
	// Each short retry costs a three-tick visit plus the short back-off.
	r.until(kitSettleTicks+7*(kitChunkBackoff+3), func() bool { return len(*visits) >= 7 })
	if len(*visits) != 7 {
		t.Fatalf("visits = %d in %d ticks; the short retries must allow six and a seventh", len(*visits), r.ticks)
	}
	for _, l := range r.log.lines {
		if l.event == "kit_failed" && l.fields["error"] != skill.ErrChestChunkNotLoaded.Error() {
			t.Errorf("kit_failed error = %v", l.fields["error"])
		}
	}
	r.run(kitFailBackoff - 100)
	if len(*visits) != 7 {
		t.Errorf("retried %d times inside the long back-off, want 7 visits", len(*visits))
	}
	r.run(300)
	if len(*visits) != 8 {
		t.Errorf("visits = %d, want an eighth try after the back-off", len(*visits))
	}
}

func TestAFightStopsAVisitAndItResumesLater(t *testing.T) {
	r, visits := kitRig(t)
	(*visits) = nil
	r.l.makeKit = func(weapon bool, food int) skill.Skill {
		v := &visit{r: r, ticks: 100, weapon: weapon, food: food}
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + 10)
	r.spawn(60, "minecraft:zombie", mgl32.Vec3{start.X() + 3, 1, start.Z()})
	if !r.step() {
		t.Fatal("not engaged")
	}
	if (*visits)[0].stopped != 1 {
		t.Errorf("stopped = %d, want the chest closed when the fight began", (*visits)[0].stopped)
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 60})
	r.run(graceTicks + kitAbortBackoff + 20)
	if len(*visits) < 2 {
		t.Errorf("visits = %d, want a new one after the fight", len(*visits))
	}
}

func TestAVisitThatNeverEndsIsAbandoned(t *testing.T) {
	r, visits := kitRig(t)
	r.l.makeKit = func(bool, int) skill.Skill {
		v := &visit{r: r, ticks: 1 << 30}
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + kitLimit + 50)
	if !r.logged("kit_failed") || (*visits)[0].stopped != 1 {
		t.Errorf("log %+v, stopped %d", r.log.lines, (*visits)[0].stopped)
	}
}

func TestAMealWaitsForAVisitInProgress(t *testing.T) {
	r, visits := kitRig(t)
	r.l.makeKit = func(weapon bool, food int) skill.Skill {
		v := &visit{r: r, ticks: 50}
		*visits = append(*visits, v)
		return v
	}
	r.run(kitSettleTicks + 5)
	r.give(0, idStoneSwrd, 1)
	r.give(1, idBread, 3)
	r.setHunger(3)
	r.run(20)
	if r.uses() != 0 {
		t.Error("started eating with a chest open")
	}
}
