package arena

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func scenarioFightRig(t *testing.T, pass string, mobsLive bool) (*ScenarioFight, *FakeConsole, *Recorder) {
	t.Helper()
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := book.Scenario("four-zombies")
	sc.Pass = pass
	clock := newFakeClock()
	rec := &Recorder{}
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		switch {
		case cmd == "list":
			return "There are 1/10 players online:\nRusty", nil
		case strings.HasPrefix(cmd, "testfor @e[tag=arena_wave"):
			if mobsLive {
				return "Found Zombie, Zombie", nil
			}
			return "ERROR: No targets matched selector", nil
		case strings.HasPrefix(cmd, "testfor @e[name="):
			return "Found Rusty", nil
		}
		return "", nil
	}}
	layout := Layout{Center: Point{X: 0, Y: 65, Z: -760}, GroundY: 64, FloorRadius: 30}
	return &ScenarioFight{
		Console: f, Layout: layout, Book: book, Scenario: sc, Seed: 1,
		Warriors: []string{"Rusty"}, CrewNames: DefaultCrewNames, Poll: 5 * time.Second,
		Observer: rec, Now: clock.Now, Sleep: clock.Sleep,
	}, f, rec
}

func TestScenarioFightSurvivePassWinsAtTimeout(t *testing.T) {
	sf, _, _ := scenarioFightRig(t, PassSurvive, true)
	res, err := sf.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != tactics.OutcomeWin || res.Seconds < 90 {
		t.Errorf("result = %+v, want a win once the 90 s timeout passed with the squad up", res)
	}
}

func TestScenarioFightClearPassTimesOut(t *testing.T) {
	sf, f, _ := scenarioFightRig(t, PassClear, true)
	res, err := sf.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != tactics.OutcomeTimeout {
		t.Errorf("outcome = %q, want timeout", res.Outcome)
	}
	if f.Count("kill @e[tag=arena_wave]") < 2 {
		t.Error("mobs were not killed before and after the fight")
	}
}

func TestScenarioFightCustomTimeoutShortensIt(t *testing.T) {
	sf, _, _ := scenarioFightRig(t, PassClear, true)
	sf.Timeout = 20 * time.Second
	res, err := sf.Run(context.Background())
	if err != nil || res.Seconds > 30 {
		t.Errorf("result %+v err %v, want the 20 s cap to end it", res, err)
	}
}

func TestScenarioFightHaltedByKillSwitch(t *testing.T) {
	sf, f, _ := scenarioFightRig(t, PassClear, true)
	sf.Halted = func() bool { return true }
	if _, err := sf.Run(context.Background()); !errors.Is(err, ErrFightHalted) {
		t.Errorf("err = %v, want ErrFightHalted", err)
	}
	if f.Count("kill @e[tag=arena_wave]") < 2 {
		t.Error("a halted fight left its mobs behind")
	}
}

func TestScenarioFightRefusesNonCrew(t *testing.T) {
	sf, f, _ := scenarioFightRig(t, PassClear, true)
	f.Respond = func(string) (string, error) { return "There are 2/10 players online:\nRusty, Steve99", nil }
	var busy *NotAloneError
	if _, err := sf.Run(context.Background()); !errors.As(err, &busy) {
		t.Errorf("err = %v, want NotAloneError", err)
	}
	if f.Count("summon") != 0 {
		t.Error("a refused fight summoned mobs")
	}
}
