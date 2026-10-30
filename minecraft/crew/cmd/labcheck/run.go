package main

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// checkDef is a named check.
type checkDef struct {
	name string
	run  func(context.Context, *Env) Result
}

// allChecks is the order the checks run in: the cheapest and least intrusive
// first, the duel last.
var allChecks = []checkDef{
	{"tp", checkTP},
	{"zombie", checkZombie},
	{"creeper", checkCreeper},
	{"phantom", checkPhantom},
	{"sprint", checkSprint},
}

// checkNames lists the names in run order.
func checkNames() []string {
	names := make([]string, len(allChecks))
	for i, c := range allChecks {
		names[i] = c.name
	}
	return names
}

// Report is a whole run.
type Report struct {
	Lab     string   `json:"lab"`
	Results []Result `json:"results"`
	Cleanup Cleanup  `json:"cleanup"`
	// Stopped is why the run ended before every check ran; empty when it ran
	// to the end.
	Stopped string `json:"stopped,omitempty"`
	// Refused is set when a stranger on the server kept the run from going on.
	Refused bool `json:"-"`
}

// Limits are the caps of a run.
type Limits struct {
	CheckTimeout time.Duration
	MaxWall      time.Duration
}

// Run runs the selected checks one after another and always cleans up what
// they summoned. Each check gets its own timeout and the run its wall-clock
// cap; an operator's stop, an interrupt or a stranger online ends the run
// after the check in hand has given up.
func Run(ctx context.Context, e *Env, selected []string, lim Limits) (rep Report) {
	rep = Report{Results: []Result{}}
	defer func() { rep.Cleanup = e.cleanup() }()

	// Anything an earlier run left behind goes first, so a crash cannot
	// leave a creeper standing for the next run to find.
	e.cleanup()

	start := e.Clock.Now()
	for _, c := range allChecks {
		if !slices.Contains(selected, c.name) {
			continue
		}
		if why := e.runStopped(ctx, start, lim); why != "" {
			rep.Stopped = why
			break
		}
		cctx, cancel := context.WithTimeout(ctx, min(lim.CheckTimeout, lim.MaxWall-e.Clock.Now().Sub(start)))
		began := e.Clock.Now()
		res := c.run(cctx, e)
		cancel()
		res.Check = c.name
		res.Seconds = e.Clock.Now().Sub(began).Seconds()
		rep.Results = append(rep.Results, res)
		e.logf("labcheck: %s: %s: %s", c.name, res.Status, res.Summary)
		if e.stopped != nil {
			break
		}
	}
	if e.stopped != nil {
		rep.Stopped = e.stopped.Error()
		var busy *arena.NotAloneError
		rep.Refused = errors.As(e.stopped, &busy)
	}
	return rep
}

// runStopped says why no further check may start.
func (e *Env) runStopped(ctx context.Context, start time.Time, lim Limits) string {
	switch {
	case ctx.Err() != nil:
		return "interrupted"
	case e.Halted != nil && e.Halted() != "":
		return e.Halted()
	case e.Clock.Now().Sub(start) >= lim.MaxWall:
		return "wall-clock cap reached"
	}
	return ""
}
