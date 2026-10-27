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
// pauses long.
const DefaultQuiet = 30 * time.Second

// DefaultBatch is the commands sent between fight re-checks.
const DefaultBatch = 100

// Options controls Apply.
type Options struct {
	DryRun bool
	Crew   []string      // player names allowed online; anyone else blocks the run
	Quiet  time.Duration // zero means DefaultQuiet
	Batch  int           // zero means DefaultBatch
	Sleep  func(time.Duration)
}

// failureMarkers are console replies that mean a command did not do its job.
var failureMarkers = []string{"cannot place", "outside of the world", "not loaded", "syntax error", "unknown command", "too many ticking"}

// Apply sends the plan through c unless DryRun. It first proves no fight is
// running and nobody but the crew is online, then re-proves the fight check
// between batches, stopping with ErrFightActive the moment a tagged mob shows.
// A dry run still reads the fight state, so it also shows whether a real run
// would be refused. Teardown always runs after a Setup, even on failure.
func Apply(c arena.Console, p Plan, rep *Report, o Options) (err error) {
	rep.Dry = o.DryRun
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
	if o.DryRun {
		return nil
	}
	if err = arena.CheckAlone(c, o.Crew); err != nil {
		return fmt.Errorf("repair: %w", err)
	}
	if err = fightClear(c); err != nil {
		return err
	}
	sleep(quiet)
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
	for i, cmd := range p.Repairs {
		if i > 0 && i%batch == 0 {
			if err = fightClear(c); err != nil {
				return err
			}
		}
		if err = sent(cmd); err != nil {
			return err
		}
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
