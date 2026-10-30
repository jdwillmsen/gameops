package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// readTimeout bounds the counter reads that bracket the duel; they run on a
// fresh context so a duel cut short is still measured.
const readTimeout = 10 * time.Second

// sprintCounts are the two lifetime counters of a unit.
type sprintCounts struct{ sprint, corrections uint64 }

func (e *Env) sprintCounts(ctx context.Context, unit string) (sprintCounts, error) {
	st, err := e.Units.State(ctx, unit)
	if err != nil {
		return sprintCounts{}, err
	}
	if st.SprintTicks == nil || st.Corrections == nil {
		return sprintCounts{}, fmt.Errorf("%s serves no sprint_ticks or movement_corrections: its build predates them", unit)
	}
	return sprintCounts{*st.SprintTicks, *st.Corrections}, nil
}

// checkSprint runs a one-round duel between the first two units and compares
// the sprint and correction counters across it. The player/strafe tactic
// sprints, so a unit that fought a player and never sprinted ran something
// else, and one the server kept correcting while it sprinted has a movement
// model that is out of step with the server's.
func checkSprint(ctx context.Context, e *Env) Result {
	const check = "sprint"
	if len(e.Opts.Units) < 2 || e.Duel == nil {
		return inconclusive(check, "the sprint check needs two units and a duel", nil)
	}
	units := e.Opts.Units[:2]
	if err := e.guard(ctx); err != nil {
		return e.cut(check, "starting", err)
	}
	before := map[string]sprintCounts{}
	for _, u := range units {
		c, err := e.sprintCounts(ctx, u)
		if err != nil {
			return e.cut(check, "reading "+u+"'s counters", err)
		}
		before[u] = c
	}

	rounds, duelErr := e.Duel.Run(ctx)

	var grew sprintCounts
	per := ev{}
	for _, u := range units {
		rctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		c, err := e.sprintCounts(rctx, u)
		cancel()
		if err != nil {
			return e.cut(check, "reading "+u+"'s counters after the duel", err)
		}
		d := sprintCounts{growth(c.sprint, before[u].sprint), growth(c.corrections, before[u].corrections)}
		per[u] = ev{"sprint_ticks": d.sprint, "movement_corrections": d.corrections}
		grew.sprint += d.sprint
		grew.corrections += d.corrections
	}
	evidence := ev{
		"units": per, "sprint_ticks": grew.sprint, "movement_corrections": grew.corrections,
		"max_corrections": e.Opts.MaxCorrections, "rounds": rounds,
	}
	if duelErr != nil {
		evidence["duel_error"] = duelErr.Error()
		// A duel that stopped for the operator or a stranger ends the run;
		// any other failure only leaves the counters short.
		if r, stop := e.stopOrNil(check, duelErr); stop {
			r.Evidence = evidence
			return r
		}
	}
	switch {
	case grew.corrections > e.Opts.MaxCorrections && grew.sprint > 0:
		return fail(check, fmt.Sprintf("the server corrected the units %d times over %d sprint ticks (limit %d): the sprint model is out of step",
			grew.corrections, grew.sprint, e.Opts.MaxCorrections), evidence)
	case grew.sprint == 0:
		why := "no unit sprinted during the duel"
		if duelErr != nil {
			why += " (the duel failed: " + duelErr.Error() + ")"
		} else if len(rounds) == 0 || rounds[0].Outcome == arena.DuelAborted {
			why += " (the round was aborted)"
		}
		return inconclusive(check, why+"; the units may not have heard the start signal or may not fight with player/strafe", evidence)
	}
	return pass(check, fmt.Sprintf("%d sprint ticks drew %d corrections (limit %d)", grew.sprint, grew.corrections, e.Opts.MaxCorrections), evidence)
}

func growth(now, was uint64) uint64 {
	if now < was {
		return 0
	}
	return now - was
}

// stopOrNil is cut for an error that may or may not end the run.
func (e *Env) stopOrNil(check string, err error) (Result, bool) {
	r := e.cut(check, "the duel", err)
	return r, e.stopped != nil
}
