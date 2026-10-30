package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// Status is how a check ended. INCONCLUSIVE means the lab could not show
// either answer, so it is never read as a pass.
type Status string

const (
	Pass         Status = "PASS"
	Fail         Status = "FAIL"
	Inconclusive Status = "INCONCLUSIVE"
)

// Result is one check's verdict with the evidence that backs it.
type Result struct {
	Check    string         `json:"check"`
	Status   Status         `json:"status"`
	Summary  string         `json:"summary"`
	Evidence map[string]any `json:"evidence,omitempty"`
	Seconds  float64        `json:"seconds"`
}

// ev is the evidence of a result.
type ev = map[string]any

// Clock is time as the checks see it, so tests run a minute of watching in no
// time.
type Clock interface {
	Now() time.Time
	// Sleep waits d or until ctx ends, whichever is first.
	Sleep(ctx context.Context, d time.Duration) error
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// DuelRunner runs the one-round duel of the sprint check; *arena.Duel is one.
type DuelRunner interface {
	Run(ctx context.Context) ([]arena.DuelRound, error)
}

// Options are the thresholds and timings of the checks.
type Options struct {
	// Units are the crew warriors the checks may touch. The first is the
	// subject of every check; the sprint check duels the first two.
	Units     []string
	CrewNames []string

	// Window is how long a check watches for its effect to show.
	Window time.Duration
	Poll   time.Duration
	Settle time.Duration

	// PhantomMove is the path length in blocks, and PhantomClose the gain
	// in distance to the warrior, that count as a phantom hunting.
	PhantomMove  float64
	PhantomClose float64
	// MaxCorrections is how many server corrections a sprinting round may
	// draw before the sprint model is called out of step.
	MaxCorrections uint64
}

// Env is everything a check touches, all of it behind an interface.
type Env struct {
	Console arena.Console
	Units   UnitSource
	Duel    DuelRunner
	Clock   Clock
	Opts    Options
	// Halted says why the run must stop now, or "" to go on. It is where the
	// stop file and the training switch plug in.
	Halted func() string
	Out    io.Writer

	// stopped is the reason the whole run ended early: a halt, an interrupt
	// or a player who joined. A timeout of one check is not one.
	stopped error
}

// haltError is a stop the operator asked for.
type haltError struct{ reason string }

func (h *haltError) Error() string { return h.reason }

func (e *Env) logf(format string, args ...any) {
	if e.Out != nil {
		fmt.Fprintf(e.Out, format+"\n", args...)
	}
}

// guard says whether a check may touch the server: nobody asked to stop,
// the check still has time, and no player outside the crew has come online.
func (e *Env) guard(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Halted != nil {
		if reason := e.Halted(); reason != "" {
			return &haltError{reason}
		}
	}
	return arena.CheckAlone(e.Console, e.Opts.CrewNames)
}

// pause waits d, then re-checks the stop conditions that need no server.
func (e *Env) pause(ctx context.Context, d time.Duration) error {
	if err := e.Clock.Sleep(ctx, d); err != nil {
		return err
	}
	if e.Halted != nil {
		if reason := e.Halted(); reason != "" {
			return &haltError{reason}
		}
	}
	return ctx.Err()
}

// cut turns an error that ended a check early into its result, and records the
// ones that must end the run.
func (e *Env) cut(check, doing string, err error) Result {
	var halt *haltError
	var busy *arena.NotAloneError
	switch {
	case errors.As(err, &halt), errors.As(err, &busy), errors.Is(err, context.Canceled), errors.Is(err, arena.ErrDuelHalted):
		if e.stopped == nil {
			e.stopped = err
		}
		return inconclusive(check, "stopped while "+doing+": "+err.Error(), nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, arena.ErrDuelWallClock):
		return inconclusive(check, "timed out while "+doing, nil)
	}
	return inconclusive(check, "could not finish "+doing+": "+err.Error(), ev{"error": err.Error()})
}

func result(check string, s Status, summary string, evidence ev) Result {
	return Result{Check: check, Status: s, Summary: summary, Evidence: evidence}
}

func pass(check, summary string, evidence ev) Result {
	return result(check, Pass, summary, evidence)
}

func fail(check, summary string, evidence ev) Result {
	return result(check, Fail, summary, evidence)
}

func inconclusive(check, summary string, evidence ev) Result {
	return result(check, Inconclusive, summary, evidence)
}
