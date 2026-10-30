package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
)

func want(t *testing.T, r Result, s Status, summaryHas string) {
	t.Helper()
	if r.Status != s || !strings.Contains(r.Summary, summaryHas) {
		t.Fatalf("%s = %s %q, want %s with %q (evidence %v)", r.Check, r.Status, r.Summary, s, summaryHas, r.Evidence)
	}
}

func cleanAfter(t *testing.T, l *fakeLab, rep Report) {
	t.Helper()
	if !rep.Cleanup.OK || l.survivors() != 0 {
		t.Errorf("cleanup = %+v with %d mob(s) alive", rep.Cleanup, l.survivors())
	}
}

// --- tp -------------------------------------------------------------------

func TestTPPassesWhenTheFacingTakes(t *testing.T) {
	l := newLab()
	r, rep := runOne(testEnv(l, nil), "tp")
	want(t, r, Pass, "turned Rusty")
	cleanAfter(t, l, rep)
	if got := l.units["Rusty"].Yaw; got != 90 {
		t.Errorf("yaw = %v, want the unit turned 90 degrees", got)
	}
}

func TestTPUsesTheSyntaxTheDuelRunnerUses(t *testing.T) {
	l := newLab()
	runOne(testEnv(l, nil), "tp")
	var tp string
	for _, c := range l.cmds {
		if strings.HasPrefix(c, "tp ") {
			tp = c
		}
	}
	if !regexp.MustCompile(`^tp "Rusty" 10\.5 64 10\.5 90 0$`).MatchString(tp) {
		t.Errorf("tp command = %q, want tp \"Name\" x y z yaw pitch at the unit's own spot", tp)
	}
}

func TestTPFailsWithTheConsoleReplyWhenRefused(t *testing.T) {
	l := newLab()
	l.tp, l.tpReply = "refuse", `Syntax error: Unexpected "90" at position 31`
	r, rep := runOne(testEnv(l, nil), "tp")
	want(t, r, Fail, `Syntax error: Unexpected "90"`)
	if r.Evidence["reply"] != l.tpReply || r.Evidence["command"] == nil {
		t.Errorf("evidence = %v, want the reply and the command", r.Evidence)
	}
	cleanAfter(t, l, rep)
}

func TestTPFailsWhenAcceptedButTheUnitKeepsItsFacing(t *testing.T) {
	l := newLab()
	l.tp = "ignore"
	r, _ := runOne(testEnv(l, nil), "tp")
	want(t, r, Fail, "still faces")
}

func TestTPIsInconclusiveWhenTheUnitWalkedOff(t *testing.T) {
	l := newLab()
	l.tp = "ignore"
	reads := 0
	l.onCmd = func(c string) {
		if strings.HasPrefix(c, "querytarget @a") {
			reads++
			if reads > 2 {
				l.units["Rusty"].X += 5
			}
		}
	}
	r, _ := runOne(testEnv(l, nil), "tp")
	want(t, r, Inconclusive, "moved after the tp")
}

func TestTPIsInconclusiveWhenTheUnitIsNotInTheWorld(t *testing.T) {
	l := newLab()
	delete(l.units, "Rusty")
	r, _ := runOne(testEnv(l, nil), "tp")
	want(t, r, Inconclusive, "cannot read Rusty")
	if l.count("tp ") != 0 {
		t.Error("a teleport was sent for a unit that is not there")
	}
}

// --- phantom ---------------------------------------------------------------

func TestPhantomPassesWhenItClosesOnTheWarrior(t *testing.T) {
	l := newLab()
	l.path = map[string]func(time.Duration) [3]float64{"phantom": func(el time.Duration) [3]float64 {
		return [3]float64{20.5 - el.Seconds()*0.6, 70, 10.5}
	}}
	r, rep := runOne(testEnv(l, nil), "phantom")
	want(t, r, Pass, "closed")
	cleanAfter(t, l, rep)
}

func TestPhantomPassesWhenItSweepsAboutWithoutClosing(t *testing.T) {
	l := newLab()
	l.path = map[string]func(time.Duration) [3]float64{"phantom": func(el time.Duration) [3]float64 {
		s := el.Seconds()
		if int(s/2)%2 == 0 {
			return [3]float64{20.5, 70, 10.5 + s*0.1}
		}
		return [3]float64{20.5, 70, 10.5 - s*0.1}
	}}
	e := testEnv(l, nil)
	e.Opts.PhantomMove = 0.5
	e.Opts.PhantomClose = 50
	r, _ := runOne(e, "phantom")
	want(t, r, Pass, "flew")
}

func TestPhantomFailsWhenItHangsWhereItWasPut(t *testing.T) {
	l := newLab()
	r, rep := runOne(testEnv(l, nil), "phantom")
	want(t, r, Fail, "idled")
	if r.Evidence["samples"].(int) < minSamples {
		t.Errorf("evidence = %v", r.Evidence)
	}
	cleanAfter(t, l, rep)
}

func TestPhantomIsInconclusiveWhenItIsGoneAfterOneReading(t *testing.T) {
	l := newLab()
	l.lifespan = map[string]time.Duration{"phantom": 3 * time.Second}
	r, rep := runOne(testEnv(l, nil), "phantom")
	want(t, r, Inconclusive, "was gone")
	if r.Evidence["vanished"] != true {
		t.Errorf("evidence = %v", r.Evidence)
	}
	cleanAfter(t, l, rep)
}

func TestPhantomSummonRefusedIsInconclusiveWithTheReply(t *testing.T) {
	l := newLab()
	l.summonReply = "Unable to summon object"
	r, _ := runOne(testEnv(l, nil), "phantom")
	want(t, r, Inconclusive, "Unable to summon object")
}

func TestPhantomThatCannotBeTaggedIsStillSweptAway(t *testing.T) {
	l := newLab()
	l.noTag = true
	r, rep := runOne(testEnv(l, nil), "phantom")
	want(t, r, Inconclusive, "found none to tag")
	if l.count(`kill @e[name=labcheck,type=!player]`) == 0 || !rep.Cleanup.OK {
		t.Errorf("the untagged phantom was not removed by name: cleanup %+v, survivors %d", rep.Cleanup, l.survivors())
	}
}

// --- creeper ---------------------------------------------------------------

func flagUnits(after int, flags ...life.FlagSeen) *fakeUnits {
	return &fakeUnits{state: func(_ string, call int) (UnitState, error) {
		if call <= after {
			return UnitState{Seen: seen(nil, nil)}, nil
		}
		return UnitState{Seen: seen(nil, flags)}, nil
	}}
}

func TestCreeperPassesWhenTheUnitSawChargedAndTheCreeperWasFrozen(t *testing.T) {
	l := newLab()
	e := testEnv(l, flagUnits(2, life.FlagSeen{Seq: 1, Type: "creeper", Charged: true}))
	r, rep := runOne(e, "creeper")
	want(t, r, Pass, "Charged")
	if r.Evidence["fresh"] != true {
		t.Errorf("evidence = %v, want a fresh sighting", r.Evidence)
	}
	if len(l.mobs) != 1 || l.mobs[0].event != "minecraft:charged_creeper" {
		t.Errorf("mobs = %+v, want one creeper born with the charged event", l.mobs)
	}
	if l.count("effect @e[tag=labcheck,type=creeper] slowness 300 255") != 1 {
		t.Error("the creeper was not frozen")
	}
	cleanAfter(t, l, rep)
}

func TestCreeperWithAnEarlierChargedSightingIsInconclusiveAndSummonsNothing(t *testing.T) {
	l := newLab()
	e := testEnv(l, &fakeUnits{state: func(string, int) (UnitState, error) {
		return UnitState{Seen: seen(nil, []life.FlagSeen{{Seq: 1, Type: "creeper", Charged: true}})}, nil
	}})
	r, rep := runOne(e, "creeper")
	want(t, r, Inconclusive, "already seen this session, restart the unit or use a fresh mob type")
	if len(l.mobs) != 0 {
		t.Errorf("mobs = %+v, want none summoned for a sighting that cannot be fresh", l.mobs)
	}
	cleanAfter(t, l, rep)
}

func TestCreeperFailsWhenTheUnitSawItWithoutTheFlag(t *testing.T) {
	l := newLab()
	e := testEnv(l, flagUnits(2, life.FlagSeen{Seq: 1, Type: "creeper"}))
	r, rep := runOne(e, "creeper")
	want(t, r, Fail, "never showed")
	cleanAfter(t, l, rep)
}

func TestCreeperIsInconclusiveWhenTheUnitNeverLoggedOne(t *testing.T) {
	l := newLab()
	e := testEnv(l, flagUnits(1000))
	r, rep := runOne(e, "creeper")
	want(t, r, Inconclusive, "no new creeper sighting")
	cleanAfter(t, l, rep)
}

func TestCreeperIsInconclusiveOnABuildWithoutSightings(t *testing.T) {
	l := newLab()
	e := testEnv(l, &fakeUnits{state: func(string, int) (UnitState, error) { return UnitState{}, nil }})
	r, _ := runOne(e, "creeper")
	want(t, r, Inconclusive, "predates")
	if len(l.mobs) != 0 {
		t.Error("a creeper was summoned for a unit that cannot report it")
	}
}

func TestCreeperIsSummonedInFrontOfTheWarrior(t *testing.T) {
	l := newLab()
	l.units["Rusty"].Yaw = 90
	e := testEnv(l, flagUnits(1, life.FlagSeen{Seq: 1, Type: "creeper", Charged: true}))
	runOne(e, "creeper")
	if len(l.mobs) != 1 || l.mobs[0].pos != [3]float64{1, 64, 11} {
		t.Errorf("mobs = %+v: yaw 90 looks along -x, so the creeper belongs 10 blocks west of x=10.5", l.mobs)
	}
}

// --- zombie ----------------------------------------------------------------

func healthUnits(after int, h ...life.HealthSeen) *fakeUnits {
	return &fakeUnits{state: func(_ string, call int) (UnitState, error) {
		if call <= after {
			return UnitState{Seen: seen(nil, nil)}, nil
		}
		return UnitState{Seen: seen(h, nil)}, nil
	}}
}

func TestZombiePassesWhenHealthSeenShowsUp(t *testing.T) {
	l := newLab()
	e := testEnv(l, healthUnits(2, life.HealthSeen{Seq: 1, Entity: "zombie", Health: 20}))
	r, rep := runOne(e, "zombie")
	want(t, r, Pass, "zombie's health")
	cleanAfter(t, l, rep)
}

func TestZombieWithAnEarlierZombieSightingIsInconclusiveAndSummonsNothing(t *testing.T) {
	l := newLab()
	e := testEnv(l, &fakeUnits{state: func(string, int) (UnitState, error) {
		return UnitState{Seen: seen([]life.HealthSeen{{Seq: 1, Entity: "zombie", Health: 20}}, nil)}, nil
	}})
	r, rep := runOne(e, "zombie")
	want(t, r, Inconclusive, "already seen this session, restart the unit or use a fresh mob type")
	if len(l.mobs) != 0 {
		t.Errorf("mobs = %+v, want none summoned", l.mobs)
	}
	cleanAfter(t, l, rep)
}

func TestZombiePassIsAlwaysFresh(t *testing.T) {
	l := newLab()
	e := testEnv(l, healthUnits(2, life.HealthSeen{Seq: 4, Entity: "zombie", Health: 20}))
	r, _ := runOne(e, "zombie")
	want(t, r, Pass, "zombie's health")
	if r.Evidence["fresh"] != true {
		t.Errorf("evidence = %v, want fresh=true", r.Evidence)
	}
}

func TestZombieFailsWhenNoHealthIsEverSeen(t *testing.T) {
	l := newLab()
	e := testEnv(l, healthUnits(1000))
	r, rep := runOne(e, "zombie")
	want(t, r, Fail, "never logged a zombie's health")
	cleanAfter(t, l, rep)
}

func TestZombieIgnoresOtherMobsHealth(t *testing.T) {
	l := newLab()
	e := testEnv(l, healthUnits(0, life.HealthSeen{Seq: 1, Entity: "skeleton", Health: 20}))
	r, _ := runOne(e, "zombie")
	want(t, r, Fail, "never logged")
}

func TestZombieIsInconclusiveWhenTheStateCannotBeRead(t *testing.T) {
	l := newLab()
	e := testEnv(l, &fakeUnits{state: func(string, int) (UnitState, error) { return UnitState{}, errors.New("status 502") }})
	r, _ := runOne(e, "zombie")
	want(t, r, Inconclusive, "status 502")
}

// --- sprint ----------------------------------------------------------------

// counters returns units whose totals follow the duel: before it the first
// reading, after it the grown one.
func counterUnits(d *fakeDuel, sprint, corrections uint64) *fakeUnits {
	return &fakeUnits{state: func(unit string, _ int) (UnitState, error) {
		if d.calls == 0 {
			return UnitState{SprintTicks: u64(100), Corrections: u64(4)}, nil
		}
		return UnitState{SprintTicks: u64(100 + sprint), Corrections: u64(4 + corrections)}, nil
	}}
}

func duelThat(rounds []arena.DuelRound, err error) *fakeDuel {
	return &fakeDuel{run: func(context.Context) ([]arena.DuelRound, error) { return rounds, err }}
}

var decided = []arena.DuelRound{{Round: 1, Outcome: arena.DuelDecided, Winner: arena.SideRed}}

func sprintEnv(l *fakeLab, d *fakeDuel, sprint, corrections uint64) *Env {
	e := testEnv(l, counterUnits(d, sprint, corrections))
	e.Duel = d
	return e
}

func TestSprintPassesWhenUnitsSprintAndFewCorrections(t *testing.T) {
	l := newLab()
	d := duelThat(decided, nil)
	r, _ := runOne(sprintEnv(l, d, 120, 3), "sprint")
	want(t, r, Pass, "240 sprint ticks drew 6 corrections")
	if d.calls != 1 {
		t.Errorf("duel ran %d times, want 1", d.calls)
	}
}

func TestSprintFailsWhenCorrectionsSpikeWhileSprinting(t *testing.T) {
	l := newLab()
	r, _ := runOne(sprintEnv(l, duelThat(decided, nil), 120, 40), "sprint")
	want(t, r, Fail, "out of step")
	if r.Evidence["movement_corrections"] != uint64(80) {
		t.Errorf("evidence = %v", r.Evidence)
	}
}

func TestSprintIsInconclusiveWhenNoUnitSprinted(t *testing.T) {
	l := newLab()
	r, _ := runOne(sprintEnv(l, duelThat(decided, nil), 0, 0), "sprint")
	want(t, r, Inconclusive, "no unit sprinted")
}

func TestSprintIsInconclusiveWhenTheDuelFailedAndNothingGrew(t *testing.T) {
	l := newLab()
	r, _ := runOne(sprintEnv(l, duelThat(nil, errors.New("arena: duelist Slate is not in the world")), 0, 0), "sprint")
	want(t, r, Inconclusive, "duelist Slate is not in the world")
}

func TestSprintMeasuresWhatGrewEvenWhenTheDuelEndedInAnError(t *testing.T) {
	l := newLab()
	r, _ := runOne(sprintEnv(l, duelThat(nil, arena.ErrDuelistGone), 50, 1), "sprint")
	want(t, r, Pass, "sprint ticks")
}

func TestSprintIsInconclusiveOnABuildWithoutTheCounters(t *testing.T) {
	l := newLab()
	d := duelThat(decided, nil)
	e := testEnv(l, &fakeUnits{state: func(string, int) (UnitState, error) { return UnitState{Seen: seen(nil, nil)}, nil }})
	e.Duel = d
	r, _ := runOne(e, "sprint")
	want(t, r, Inconclusive, "predates")
	if d.calls != 0 {
		t.Error("a duel was run for units that cannot be measured")
	}
}

func TestSprintTouchesNoInventory(t *testing.T) {
	l := newLab()
	runOne(sprintEnv(l, duelThat(decided, nil), 10, 0), "sprint")
	assertNoInventoryCommands(t, l)
}

func assertNoInventoryCommands(t *testing.T, l *fakeLab) {
	t.Helper()
	for _, c := range l.cmds {
		for _, bad := range []string{"clear", "give", "replaceitem", "function kits", "kit "} {
			if strings.Contains(c, bad) {
				t.Errorf("command %q touches an inventory", c)
			}
		}
	}
}

// --- cleanup, stops and caps -----------------------------------------------

func TestEveryCheckSendsOnlyWhatItIsAllowedTo(t *testing.T) {
	l := newLab()
	units := &fakeUnits{state: func(string, int) (UnitState, error) {
		return UnitState{SprintTicks: u64(1), Corrections: u64(0), Seen: seen(
			[]life.HealthSeen{{Seq: 1, Entity: "zombie"}}, []life.FlagSeen{{Seq: 2, Type: "creeper", Charged: true}})}, nil
	}}
	e := testEnv(l, units)
	e.Duel = duelThat(decided, nil)
	rep := Run(context.Background(), e, checkNames(), testLimits)
	if len(rep.Results) != len(allChecks) {
		t.Fatalf("%d results, want %d", len(rep.Results), len(allChecks))
	}
	assertNoInventoryCommands(t, l)
	for _, c := range l.cmds {
		if strings.HasPrefix(c, "kill") && !strings.Contains(c, "labcheck") {
			t.Errorf("kill %q is not scoped to the check's own mobs", c)
		}
		if strings.HasPrefix(c, "tp ") && !strings.HasPrefix(c, `tp "Rusty"`) {
			t.Errorf("tp %q moves a unit other than the subject", c)
		}
	}
	cleanAfter(t, l, rep)
}

func TestCleanupSweepsLeftoversBeforeAndAfter(t *testing.T) {
	l := newLab()
	l.mobs = []*fakeMob{{typ: "creeper", tagged: true, named: true}}
	rep := Run(context.Background(), testEnv(l, nil), []string{"tp"}, testLimits)
	if l.cmds[0] != "kill @e[tag=labcheck,type=!player]" {
		t.Errorf("first command = %q, want a sweep of an earlier run's mobs", l.cmds[0])
	}
	cleanAfter(t, l, rep)
}

func TestCleanupRetriesAMobThatSurvivesTheFirstKill(t *testing.T) {
	l := newLab()
	l.stubborn = 3
	_, rep := runOne(testEnv(l, healthUnits(1, life.HealthSeen{Seq: 1, Entity: "zombie", Health: 20})), "zombie")
	cleanAfter(t, l, rep)
}

func TestCleanupThatCannotRemoveEverythingIsReportedAndFailsTheRun(t *testing.T) {
	l := newLab()
	l.stubborn = 1000
	_, rep := runOne(testEnv(l, healthUnits(1, life.HealthSeen{Seq: 1, Entity: "zombie", Health: 20})), "zombie")
	if rep.Cleanup.OK || rep.Cleanup.Remaining == 0 {
		t.Fatalf("cleanup = %+v, want a failure that counts the survivors", rep.Cleanup)
	}
	if code := exitCode(rep); code != exitFail {
		t.Errorf("exit = %d, want %d: a mob left in the lab fails the run", code, exitFail)
	}
}

func TestCleanupThatCannotCountIsAFailure(t *testing.T) {
	l := newLab()
	l.unreadable = true
	rep := Run(context.Background(), testEnv(l, nil), []string{"tp"}, testLimits)
	if rep.Cleanup.OK || !rep.Cleanup.Unknown {
		t.Fatalf("cleanup = %+v, want unknown", rep.Cleanup)
	}
}

func TestACheckThatTimesOutIsInconclusiveAndTheMobsGoAnyway(t *testing.T) {
	l := newLab()
	l.clk.block = true
	d := duelThat(decided, nil)
	e := sprintEnv(l, d, 10, 0)
	rep := Run(context.Background(), e, []string{"phantom", "sprint"}, Limits{CheckTimeout: 40 * time.Millisecond, MaxWall: time.Minute})
	if len(rep.Results) != 2 || d.calls != 1 {
		t.Fatalf("results = %+v, want the run to go on to the next check after a timeout", rep.Results)
	}
	want(t, rep.Results[0], Inconclusive, "timed out")
	if rep.Stopped != "" {
		t.Errorf("stopped = %q: a timeout of one check does not stop the run", rep.Stopped)
	}
	if l.survivors() != 0 {
		t.Error("the phantom outlived the timeout")
	}
}

func TestACancelledRunStopsAndCleansUp(t *testing.T) {
	l := newLab()
	ctx, cancel := context.WithCancel(context.Background())
	l.clk.onSleep = func() {
		if len(l.mobs) > 0 {
			cancel()
		}
	}
	d := duelThat(decided, nil)
	rep := Run(ctx, sprintEnv(l, d, 10, 0), []string{"phantom", "sprint"}, testLimits)
	if len(rep.Results) != 1 || rep.Stopped == "" || d.calls != 0 {
		t.Fatalf("results %+v, stopped %q: a cancel must end the run after the check in hand", rep.Results, rep.Stopped)
	}
	want(t, rep.Results[0], Inconclusive, "stopped")
	cleanAfter(t, l, rep)
	if code := exitCode(rep); code != exitStopped {
		t.Errorf("exit = %d, want %d", code, exitStopped)
	}
}

func TestTheStopFileAppearingMidCheckEndsTheRunAndCleansUp(t *testing.T) {
	l := newLab()
	d := duelThat(decided, nil)
	e := sprintEnv(l, d, 10, 0)
	halt := ""
	e.Halted = func() string { return halt }
	l.clk.onSleep = func() {
		if len(l.mobs) > 0 {
			halt = "stop file /tmp/stop exists"
		}
	}
	rep := Run(context.Background(), e, []string{"phantom", "sprint"}, testLimits)
	if len(rep.Results) != 1 || rep.Stopped != "stop file /tmp/stop exists" || d.calls != 0 {
		t.Fatalf("results %+v, stopped %q", rep.Results, rep.Stopped)
	}
	cleanAfter(t, l, rep)
}

func TestAnAlreadyHaltedRunStartsNoCheck(t *testing.T) {
	l := newLab()
	e := testEnv(l, nil)
	e.Halted = func() string { return "the training switch is off" }
	rep := Run(context.Background(), e, checkNames(), testLimits)
	if len(rep.Results) != 0 || rep.Stopped == "" {
		t.Fatalf("results %+v, stopped %q", rep.Results, rep.Stopped)
	}
	if l.count("summon") != 0 || l.count("tp ") != 0 {
		t.Error("the server was touched after the switch was off")
	}
}

func TestAPlayerJoiningMidRunEndsItAndIsRefused(t *testing.T) {
	l := newLab()
	d := duelThat(decided, nil)
	e := sprintEnv(l, d, 10, 0)
	l.clk.onSleep = func() {
		if len(l.mobs) > 0 {
			l.online = "Rusty, Steve99"
		}
	}
	rep := Run(context.Background(), e, []string{"phantom", "sprint"}, testLimits)
	// The phantom check does not re-read the list while it watches, so the
	// next check is the one that is refused.
	if len(rep.Results) != 2 || !rep.Refused || d.calls != 0 {
		t.Fatalf("results %+v, refused %v, stopped %q", rep.Results, rep.Refused, rep.Stopped)
	}
	want(t, rep.Results[1], Inconclusive, "Steve99")
	cleanAfter(t, l, rep)
	if code := exitCode(rep); code != exitRefused {
		t.Errorf("exit = %d, want %d", code, exitRefused)
	}
}

func TestTheWallClockCapStopsTheRunBetweenChecks(t *testing.T) {
	l := newLab()
	d := duelThat(decided, nil)
	rep := Run(context.Background(), sprintEnv(l, d, 10, 0), []string{"phantom", "sprint"}, Limits{CheckTimeout: time.Minute, MaxWall: 15 * time.Second})
	if len(rep.Results) != 1 || rep.Stopped != "wall-clock cap reached" || d.calls != 0 {
		t.Fatalf("results %+v, stopped %q", rep.Results, rep.Stopped)
	}
}

func TestExitCodes(t *testing.T) {
	ok := Cleanup{OK: true}
	for name, tc := range map[string]struct {
		rep  Report
		want int
	}{
		"all pass":     {Report{Results: []Result{{Status: Pass}}, Cleanup: ok}, 0},
		"a fail":       {Report{Results: []Result{{Status: Pass}, {Status: Fail}, {Status: Inconclusive}}, Cleanup: ok}, exitFail},
		"inconclusive": {Report{Results: []Result{{Status: Pass}, {Status: Inconclusive}}, Cleanup: ok}, exitInconclusive},
		"stopped":      {Report{Results: []Result{{Status: Pass}}, Stopped: "stop file", Cleanup: ok}, exitStopped},
		"refused":      {Report{Stopped: "x", Refused: true, Cleanup: ok}, exitRefused},
		"dirty":        {Report{Results: []Result{{Status: Pass}}}, exitFail},
	} {
		if got := exitCode(tc.rep); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", name, got, tc.want)
		}
	}
}
