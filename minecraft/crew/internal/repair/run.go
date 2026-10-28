package repair

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// ErrFightActive is returned when the director's tagged mobs are alive, or
// were a moment ago. Nothing has been sent when it is returned.
var ErrFightActive = errors.New("repair: a fight is in progress")

// DefaultQuiet is how long the arena must show no tagged mob before repair
// starts. A director run has zero tagged mobs between waves (the wave pause is
// 8 s by default), so a single empty count proves nothing; this is several
// pauses long. Two samples are still only probabilistic: until the director
// holds an arena_active marker, a long pause or a late first spawn can slip
// through, so run the bot only after the director has exited.
const DefaultQuiet = 30 * time.Second

// DefaultLoadWait is the pause after the ticking areas are added, so their
// chunks are loaded before the first fill.
const DefaultLoadWait = 5 * time.Second

// DefaultBatch is the commands sent between fight re-checks.
const DefaultBatch = 100

// Options controls Apply.
type Options struct {
	DryRun bool
	Crew   []string      // player names allowed online; anyone else blocks the run
	Quiet  time.Duration // zero means DefaultQuiet
	Batch  int           // zero means DefaultBatch
	// LoadWait is the pause after Setup; zero means DefaultLoadWait.
	LoadWait time.Duration
	Sleep    func(time.Duration)
	// Stop, when set, is polled between waits and commands; a non-nil result
	// ends the run cleanly so Teardown still happens.
	Stop func() error
}

// failureMarkers are console replies that mean a command did not do its job.
var failureMarkers = []string{"cannot place", "outside of the world", "not loaded", "syntax error", "unknown command", "too many ticking", "already exists"}

// Apply sends the plan through c unless DryRun. It first proves no fight is
// running and nobody but the crew is online, then re-proves the fight check
// between batches, stopping with ErrFightActive the moment a tagged mob shows.
// A dry run returns before any console call, so it does not show whether a
// real run would be refused. Teardown always runs after a Setup, even on
// failure; stale crew-repair-N areas are removed before Setup adds them. The
// server's 10 ticking-area cap is not checked against areas other tools hold:
// an add that the server rejects aborts the run through failureMarkers.
// rep.Status and rep.Error say how the run ended.
func Apply(c arena.Console, p Plan, rep *Report, o Options) (err error) {
	rep.Dry = o.DryRun
	rep.Status = StatusDryRun
	if !o.DryRun {
		rep.Status = StatusRefused
		defer func() {
			switch {
			case err == nil:
				rep.Status = StatusApplied
			case rep.RepairsRun > 0:
				rep.Status = StatusAborted
			}
			if err != nil {
				rep.Error = err.Error()
			}
		}()
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	quiet := o.Quiet
	if quiet == 0 {
		quiet = DefaultQuiet
	}
	batch := o.Batch
	if batch <= 0 {
		batch = DefaultBatch
	}
	loadWait := o.LoadWait
	if loadWait == 0 {
		loadWait = DefaultLoadWait
	}
	if o.DryRun {
		return nil
	}
	stopped := func() error {
		if o.Stop == nil {
			return nil
		}
		return o.Stop()
	}
	if err = arena.CheckAlone(c, o.Crew); err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	if err = fightClear(c); err != nil {
		return err
	}
	sleep(quiet)
	if err = stopped(); err != nil {
		return err
	}
	if err = fightClear(c); err != nil {
		return err
	}
	sent := func(cmd string) error {
		out, xerr := c.Exec(cmd)
		if xerr != nil {
			return fmt.Errorf("repair: %q: %w", cmd, xerr)
		}
		low := strings.ToLower(out)
		for _, m := range failureMarkers {
			if strings.Contains(low, m) {
				return fmt.Errorf("repair: %q: server said %q", cmd, strings.TrimSpace(out))
			}
		}
		rep.Executed++
		return nil
	}
	if len(p.Setup) > 0 {
		// A crashed larger run can leave higher-numbered areas behind that
		// count against the cap, so sweep every name this tool can have
		// used; the reply is irrelevant because "not found" is the normal case.
		for i := 0; i < maxTickingAreas; i++ {
			_, _ = c.Exec(fmt.Sprintf("tickingarea remove %s%d", tickingAreaPrefix, i))
		}
		defer func() {
			for _, cmd := range p.Teardown {
				if terr := sent(cmd); terr != nil && err == nil {
					err = terr
				}
			}
		}()
	}
	for _, cmd := range p.Setup {
		if err = sent(cmd); err != nil {
			return err
		}
	}
	if len(p.Setup) > 0 {
		sleep(loadWait)
	}
	for i, cmd := range p.Repairs {
		if err = stopped(); err != nil {
			return err
		}
		if i > 0 && i%batch == 0 {
			if err = arena.CheckAlone(c, o.Crew); err != nil {
				return fmt.Errorf("repair: %w", err)
			}
			if err = fightClear(c); err != nil {
				return err
			}
		}
		if err = sent(cmd); err != nil {
			return err
		}
		rep.RepairsRun++
	}
	return nil
}

// fightClear reads the director's two counts and refuses on any survivor. An
// unreadable reply is an error, not a pass.
func fightClear(c arena.Console) error {
	for _, cmd := range arena.FightCommands() {
		out, err := c.Exec(cmd)
		if err != nil {
			return fmt.Errorf("repair: %w", err)
		}
		n, err := arena.ParseCount(out)
		if err != nil {
			return fmt.Errorf("repair: cannot tell whether a fight is running: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("%w (%d tagged mobs)", ErrFightActive, n)
		}
	}
	return nil
}
