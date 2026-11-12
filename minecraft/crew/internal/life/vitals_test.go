package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const idGapple = 363

// testVitals is what a deployment gets with no setting.
var testVitals = Vitals{RetreatHealth: 6, EmergencyHealth: 4, RestBelow: 10, TaskMinHealth: 12, TaskMinFood: 16}

func vitalsConfig(v Vitals) Config {
	c := defaults
	c.Vitals = v
	return c
}

func (r *rig) setSaturation(v float32) {
	pk := &packet.UpdateAttributes{EntityRuntimeID: selfID, Attributes: []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: "minecraft:player.saturation", Value: v}}}}
	r.tracker.Apply(pk)
	r.l.Apply(pk)
}

func item(slot int, name string) belief.Item { return belief.Item{Slot: slot, Name: name, Count: 4} }

func TestPickFoodWastesLeastThenKeepsHungerUpLongest(t *testing.T) {
	for name, tc := range map[string]struct {
		hunger float32
		inv    []belief.Item
		want   string
	}{
		"a small gap takes the small food":  {17, []belief.Item{item(0, "cooked_beef"), item(1, "carrot")}, "carrot"},
		"a big gap takes a food that fits":  {8, []belief.Item{item(0, "carrot"), item(1, "cooked_beef")}, "cooked_beef"},
		"equal waste goes to saturation":    {12, []belief.Item{item(0, "bread"), item(1, "cooked_chicken")}, "cooked_chicken"},
		"saturation tie goes to the larger": {10, []belief.Item{item(0, "apple"), item(1, "carrot")}, "carrot"},
		"ties go to the lower slot":         {10, []belief.Item{item(3, "bread"), item(2, "bread")}, "bread"},
		"one food is eaten at any gap":      {19, []belief.Item{item(4, "cooked_beef")}, "cooked_beef"},
		"a golden apple is never chosen":    {5, []belief.Item{item(0, "golden_apple"), item(1, "cookie")}, "cookie"},
		"off the hotbar is out of reach":    {5, []belief.Item{item(12, "cooked_beef"), item(1, "apple")}, "apple"},
		"rotten flesh is no food":           {5, []belief.Item{item(0, "rotten_flesh")}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := pickFood(tc.inv, tc.hunger)
			if got.Name != tc.want || ok != (tc.want != "") {
				t.Errorf("picked %q (ok %v), want %q", got.Name, ok, tc.want)
			}
		})
	}
	a, _ := pickFood([]belief.Item{item(3, "bread"), item(2, "bread")}, 10)
	if a.Slot != 2 {
		t.Errorf("tie went to slot %d, want 2", a.Slot)
	}
}

func TestMealLimitsFollowHealthSaturationAndWhatIsAboutToStart(t *testing.T) {
	for name, tc := range map[string]struct {
		hunger, health, sat float32
		satKnown, working   bool
		minFood             int
		eats                bool
	}{
		"unhurt eats from 14":                       {14, 20, 0, false, true, 16, true},
		"unhurt at 15 working waits":                {15, 20, 0, false, true, 16, false},
		"before a task it tops up to the bar":       {16, 20, 0, false, false, 16, true},
		"before a task above the bar it does not":   {17, 20, 0, false, false, 16, false},
		"no bar keeps the old limit":                {16, 20, 0, false, false, 0, false},
		"hurt eats from 17":                         {17, 15, 0, false, true, 16, true},
		"hurt at 18 with saturation left waits":     {18, 15, 3, true, true, 16, false},
		"hurt at 18 with none left eats":            {18, 15, 0, true, true, 16, true},
		"hurt at 18 and saturation unknown waits":   {18, 15, 0, false, true, 16, false},
		"hurt at 19 with none left still waits":     {19, 15, 0, true, true, 16, false},
		"full health with none left keeps the base": {18, 20, 0, true, true, 16, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := eatingRig(t, tc.hunger, tc.health)
			r.l.cfg.Vitals = Vitals{TaskMinFood: tc.minFood}
			if tc.satKnown {
				r.setSaturation(tc.sat)
			}
			r.l.working = tc.working
			if _, ok := r.l.wantsMeal(r.tracker.Snapshot()); ok != tc.eats {
				t.Errorf("wants a meal = %v, want %v", ok, tc.eats)
			}
		})
	}
}

func TestAGoldenAppleIsEatenOnlyInAnEmergency(t *testing.T) {
	for name, tc := range map[string]struct {
		hunger, health float32
		emergency      int
		apples, bread  bool
		want           string
	}{
		"an emergency at full hunger":              {20, 4, 4, true, true, "golden_apple"},
		"an emergency ignores the bread":           {10, 3, 4, true, true, "golden_apple"},
		"just above the line it is bread":          {10, 5, 4, true, true, "bread"},
		"just above the line and fed it waits":     {20, 5, 4, true, true, ""},
		"an emergency with no apple eats bread":    {10, 2, 4, false, true, "bread"},
		"an emergency with no apple and fed waits": {20, 2, 4, false, true, ""},
		"the rule off keeps apples back":           {10, 2, 0, true, true, "bread"},
		"only apples and no emergency":             {10, 12, 4, true, false, ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, vitalsConfig(Vitals{EmergencyHealth: tc.emergency}), start)
			if tc.bread {
				r.give(1, idBread, 3)
			}
			if tc.apples {
				r.give(2, idGapple, 2)
			}
			r.setHunger(tc.hunger)
			r.setHealth(tc.health)
			r.l.working = true
			got, ok := r.l.wantsMeal(r.tracker.Snapshot())
			if got.Name != tc.want || ok != (tc.want != "") {
				t.Errorf("eats %q (ok %v), want %q", got.Name, ok, tc.want)
			}
		})
	}
}

func TestTwoGoldenApplesAreNotEatenBackToBack(t *testing.T) {
	r := newRig(t, vitalsConfig(testVitals), start)
	r.give(2, idGapple, 2)
	r.setHunger(20)
	r.setHealth(3)
	r.run(5)
	r.finishMeal(2, idGapple, 1, 20)
	r.run(2)
	if !r.logged("eat") || r.uses() != 1 {
		t.Fatalf("uses %d, log %+v", r.uses(), r.log.lines)
	}
	r.run(gappleCooldown - 10)
	if r.uses() != 1 {
		t.Errorf("a second apple was eaten inside the cooldown (uses %d)", r.uses())
	}
	r.run(30)
	if r.uses() != 2 {
		t.Errorf("uses = %d after the cooldown with health still 3, want a second apple", r.uses())
	}
}

func TestRetreatHealthIsConfigurable(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg    Vitals
		health float32
		flees  bool
	}{
		"the default":          {Vitals{}, 6, true},
		"the default above":    {Vitals{}, 7, false},
		"a raised line":        {Vitals{RetreatHealth: 10}, 9, true},
		"a raised line above":  {Vitals{RetreatHealth: 10}, 11, false},
		"a lowered line":       {Vitals{RetreatHealth: 3}, 5, false},
		"a lowered line below": {Vitals{RetreatHealth: 3}, 3, true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, vitalsConfig(tc.cfg), start)
			r.give(0, 301, 1)
			r.setHealth(tc.health)
			z := mgl32.Vec3{22.5, 1, 20.5}
			r.spawn(50, "minecraft:zombie", z)
			for i := 0; i < 5; i++ {
				r.step()
			}
			if got := r.l.mode == modeRetreat; got != tc.flees {
				t.Errorf("retreating = %v at %v health, want %v", got, tc.health, tc.flees)
			}
		})
	}
}

func restRig(t *testing.T, v Vitals, health float32) *rig {
	t.Helper()
	r := newRig(t, vitalsConfig(v), start)
	r.tracker.Apply(&packet.MovePlayer{EntityRuntimeID: selfID, Position: start.Add(mgl32.Vec3{0, 1.62, 0})})
	r.setHunger(20)
	r.setHealth(health)
	return r
}

func TestAHurtUnitRestsStillUntilHealed(t *testing.T) {
	r := restRig(t, testVitals, 9)
	home := r.feet()
	r.run(40)
	if !r.logged("rest") || r.logged("rest_done") {
		t.Fatalf("log = %+v", r.log.lines)
	}
	if dist(r.feet(), home) > 0.01 {
		t.Error("the unit walked while resting")
	}
	r.setHealth(18)
	r.run(2)
	if !r.logged("rest_done") || r.l.rest.active {
		t.Errorf("rest did not end when healed: %+v", r.log.lines)
	}
	n := len(r.log.lines)
	r.run(50)
	if len(r.log.lines) != n {
		t.Errorf("rested again at 18 health: %+v", r.log.lines[n:])
	}
}

func TestRestThresholdsDependOnWhetherATaskIsRunning(t *testing.T) {
	for name, tc := range map[string]struct {
		v       Vitals
		health  float32
		working bool
		rests   bool
	}{
		"idle at the gate":                 {testVitals, 11, false, true},
		"idle above the gate":              {testVitals, 12, false, false},
		"mid-task below the rest line":     {testVitals, 9, true, true},
		"mid-task between the lines":       {testVitals, 11, true, false},
		"no gate leaves an idle unit":      {Vitals{RestBelow: 10}, 11, false, false},
		"no rest line leaves a task alone": {Vitals{TaskMinHealth: 12}, 5, true, false},
		"zero values never rest":           {Vitals{}, 2, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := restRig(t, tc.v, tc.health)
			if got := r.l.Tending(r.tracker.Snapshot(), tc.working); got != tc.rests {
				t.Errorf("tending = %v, want %v", got, tc.rests)
			}
		})
	}
}

func TestRestNeedsRegenerationAndNoMobNear(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.setHunger(17)
	if r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("rested with food under 18 and nothing to eat: regeneration is off, standing heals nothing")
	}
	r = restRig(t, testVitals, 5)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{20.5, 1, 20.5 + 12})
	if r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("rested with a zombie 12 blocks off")
	}
	r = restRig(t, testVitals, 5)
	r.spawn(51, "minecraft:cow", mgl32.Vec3{22.5, 1, 20.5})
	if !r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("a cow stopped the rest")
	}
	r = restRig(t, testVitals, 5)
	snap := r.tracker.Snapshot()
	snap.PositionKnown = false
	if r.l.Tending(snap, false) {
		t.Error("rested without knowing where it stood")
	}
	snap.PositionKnown = true
	snap.Creative = true
	if r.l.Tending(snap, false) {
		t.Error("a creative unit rested")
	}
}

func TestAMobArrivingEndsARestAndItResumesAfter(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.run(10)
	r.spawn(50, "minecraft:skeleton", mgl32.Vec3{20.5, 1, 20.5 + 14})
	r.run(2)
	if r.l.rest.active || !r.logged("rest_done") {
		t.Fatalf("rest went on with a skeleton in view: %+v", r.log.lines)
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(restThreatBackoff + 5)
	if !r.l.rest.active {
		t.Error("did not rest again once the skeleton was gone")
	}
}

func TestARestThatCannotHealGivesUpAndLetsWorkGo(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.run(restMaxTicks + 5)
	if r.l.rest.active || !r.logged("rest_done") {
		t.Fatalf("rest never ended: %+v", r.log.lines)
	}
	if r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("tending claims the unit again right after a timed-out rest: a task never starts")
	}
	r.l.tick += restBackoff
	if !r.l.Tending(r.tracker.Snapshot(), false) {
		t.Error("never rested again after the back-off")
	}
}

func TestAMealComesBeforeARest(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.give(1, idBread, 3)
	r.setHunger(16)
	r.run(3)
	if r.uses() != 1 {
		t.Errorf("uses = %d, want the unit to eat first", r.uses())
	}
	if r.logged("rest") {
		t.Error("rested before eating")
	}
}

func TestRestLogsCarryNoCoordinates(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.run(3)
	r.setHealth(19)
	r.run(2)
	for _, l := range r.log.lines {
		for k := range l.fields {
			switch k {
			case "health", "hunger", "working", "reason", "ticks":
			default:
				t.Errorf("%s logged %q", l.event, k)
			}
		}
	}
}

func TestResetEndsARest(t *testing.T) {
	r := restRig(t, testVitals, 5)
	r.run(3)
	r.l.Reset()
	if r.l.rest != (restState{}) {
		t.Errorf("rest = %+v after a reset", r.l.rest)
	}
}
