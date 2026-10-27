package arena

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// ErrFightHalted is returned when the kill switch ends a scenario fight.
var ErrFightHalted = errors.New("arena: scenario fight stopped by the kill switch")

// scenarioSeenMisses is how many counts that find nothing are tolerated
// before any mob has been seen: a summon that did not land must not read as a
// clear.
const scenarioSeenMisses = 3

// FightResult is how one scenario fight ended, in the scorecard's outcomes.
type FightResult struct {
	Outcome string
	Seconds float64
}

// ScenarioFight runs one scenario from the training book against the squad:
// the mobs are summoned on the scenario's ring and the fight ends when they
// are all down, the squad is, or the timeout passes. It reuses the director's
// tested console routines, so deaths and kills reach the Observer the same way
// a wave run's do.
type ScenarioFight struct {
	Console  Console
	Layout   Layout
	Book     *ScenarioBook
	Scenario Scenario
	Seed     int64

	Warriors    []string
	WarriorKits []string
	CrewNames   []string

	// Timeout caps the fight; zero uses the scenario's own.
	Timeout time.Duration
	Poll    time.Duration
	// Settle is how long the arena's chunks get to load after the ticking
	// areas are added; Bedrock refuses a summon in an unloaded chunk.
	Settle    time.Duration
	PlaceWait time.Duration

	Observer FightObserver
	// Halted is polled every poll; true ends the fight.
	Halted func() bool

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)
}

func (f *ScenarioFight) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *ScenarioFight) pollEvery() time.Duration {
	if f.Poll > 0 {
		return f.Poll
	}
	return 3 * time.Second
}

// wait sleeps unless ctx ends first. An injected Sleep cannot be cut short,
// so ctx is checked once it returns.
func (f *ScenarioFight) wait(ctx context.Context, dur time.Duration) error {
	if dur <= 0 {
		return ctx.Err()
	}
	if f.Sleep != nil {
		f.Sleep(dur)
		return ctx.Err()
	}
	t := time.NewTimer(dur)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (f *ScenarioFight) validate() error {
	switch {
	case f.Console == nil:
		return fmt.Errorf("arena: no console")
	case f.Book == nil:
		return fmt.Errorf("arena: no scenario book")
	case len(f.Warriors) == 0:
		return fmt.Errorf("arena: a scenario fight needs at least one warrior")
	}
	for _, w := range f.Warriors {
		if err := validateWarriorName(w); err != nil {
			return err
		}
	}
	return ValidateWarriorKits(f.Warriors, f.WarriorKits)
}

// Run checks the server is crew-only, loads the arena, fights the scenario
// once and reports how it ended. Mobs and ticking areas are removed on every
// exit path, and the floor is repaired so a creeper scenario does not leave
// craters for the next fight. A fight cut short by ctx, the kill switch or a
// player joining returns the error and no result.
func (f *ScenarioFight) Run(ctx context.Context) (FightResult, error) {
	if err := f.validate(); err != nil {
		return FightResult{}, err
	}
	if err := CheckAlone(f.Console, f.CrewNames); err != nil {
		return FightResult{}, err
	}
	cmds, err := f.Book.Expand(f.Layout, f.Scenario, f.Seed)
	if err != nil {
		return FightResult{}, err
	}
	d := &Director{
		Console: f.Console, Layout: f.Layout, Warriors: f.Warriors, WarriorKits: f.WarriorKits,
		WarriorMisses: 3, PlaceSquad: true, Observer: f.Observer,
		Out: f.Out, Now: f.Now, Sleep: f.Sleep,
	}
	if err := d.addTickingAreas(); err != nil {
		return FightResult{}, err
	}
	defer func() {
		_, _ = d.exec(killWaveCommand())
		d.removeTickingAreas()
	}()
	if err := f.wait(ctx, f.Settle); err != nil {
		return FightResult{}, err
	}
	if err := d.repairFloor(); err != nil {
		return FightResult{}, err
	}
	defer func() { _ = d.repairFloor() }()
	// Mobs left by an earlier aborted fight would be counted as this one's.
	if _, err := d.exec(killWaveCommand()); err != nil {
		return FightResult{}, err
	}
	for i, name := range f.Warriors {
		if err := d.placeWarrior(name, i); err != nil {
			return FightResult{}, err
		}
	}
	if err := f.wait(ctx, f.PlaceWait); err != nil {
		return FightResult{}, err
	}
	if err := d.kitWarriors(); err != nil {
		return FightResult{}, err
	}
	states, _, err := d.warriorBaseline()
	if err != nil {
		return FightResult{}, err
	}

	start := f.now()
	if f.Observer != nil {
		f.Observer.FightStart(start)
	}
	for _, cmd := range cmds {
		out, err := d.exec(cmd)
		if err != nil {
			return FightResult{}, err
		}
		if strings.Contains(out, "Unable to summon") || strings.Contains(out, " not found.") {
			return FightResult{}, fmt.Errorf("arena: scenario %q spawn failed: %s", f.Scenario.Name, strings.TrimSpace(out))
		}
	}

	limit := f.Scenario.Timeout.Duration
	if f.Timeout > 0 {
		limit = min(limit, f.Timeout)
	}
	finish := func(outcome string) FightResult {
		return FightResult{Outcome: outcome, Seconds: roundTenth(f.now().Sub(start).Seconds())}
	}
	var res Result
	expected, misses := f.Scenario.total(), 0
	seen := false
	checked := f.now()
	for {
		if err := f.wait(ctx, f.pollEvery()); err != nil {
			return FightResult{}, err
		}
		if f.Halted != nil && f.Halted() {
			return FightResult{}, ErrFightHalted
		}
		if f.now().Sub(checked) >= playerCheckEvery {
			checked = f.now()
			if err := CheckAlone(f.Console, f.CrewNames); err != nil {
				return FightResult{}, err
			}
		}
		wiped, err := d.checkWarriors(states, &res)
		if err != nil {
			return FightResult{}, err
		}
		live, err := d.countCmd(countCommand(), "scenario mobs")
		if err != nil {
			return FightResult{}, err
		}
		if live > 0 {
			seen = true
		}
		if seen {
			d.noteKills(expected - live)
			expected = live
		}
		switch {
		case seen && live == 0:
			return finish(tactics.OutcomeWin), nil
		case wiped:
			return finish(tactics.OutcomeLoss), nil
		case !seen && live == 0:
			if misses++; misses >= scenarioSeenMisses {
				return FightResult{}, fmt.Errorf("arena: scenario %q summoned mobs but counted none", f.Scenario.Name)
			}
		}
		if f.now().Sub(start) >= limit {
			if f.Scenario.Pass == PassSurvive && !wiped {
				return finish(tactics.OutcomeWin), nil
			}
			return finish(tactics.OutcomeTimeout), nil
		}
	}
}
