// Command labcreative puts named crew units in creative mode on a crew lab,
// and again every time one rejoins. It is lab-only.
//
// usage: labcreative --lab <lab> --units Agate,Basalt [--once] [options]
//
// <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>.
//
// A lab unit joins offline as a fresh survival player on every login, and the
// server's console is the only way to change a gamemode, so a unit that
// reconnects is back in survival. Without --once the tool watches the console's
// player list and runs `gamemode creative` for a unit each time it appears.
// It stops, always, at --max-duration, at --max-commands gamemode commands, or
// on an interrupt; --once makes a single pass and exits.
//
// Exit status: 0 done, 1 console failure, 2 refused or bad usage, 3 --once
// finished with a named unit that was not online.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

const (
	defaultCompose     = "minecraft/crew/lab/compose.yaml"
	defaultPoll        = 3 * time.Second
	defaultMaxDuration = 30 * time.Minute
	defaultMaxCommands = 100

	minPoll = time.Second
	// A forgotten watcher must not outlive a working day.
	maxDurationLimit = 12 * time.Hour
	maxCommandsLimit = 10000

	// listAttempts bounds how often an unfinished `list` reply is re-read.
	listAttempts = 3
)

// unitName is what a username may be: it is spliced into a console command,
// so selectors and quotes are refused.
var unitName = regexp.MustCompile(`^[A-Za-z0-9_ -]{1,16}$`)

type deps struct {
	console func(lab, compose string) (arena.Console, error)
	sleep   func(ctx context.Context, d time.Duration)
	now     func() time.Time
}

func realDeps() deps {
	return deps{
		console: arena.NewLabConsole,
		sleep: func(ctx context.Context, d time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(d):
			}
		},
		now: time.Now,
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, realDeps())
	stop()
	os.Exit(code)
}

type options struct {
	lab, confirmLab, compose, units string
	poll, maxDuration               time.Duration
	maxCommands                     int
	once                            bool
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("labcreative", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	fs.StringVar(&o.lab, "lab", "", "lab compose service or k8s:<namespace>/<pod>")
	fs.StringVar(&o.confirmLab, "confirm-lab", "", "repeat --lab here to use a target other than the crew lab")
	fs.StringVar(&o.compose, "compose", defaultCompose, "compose file for a docker lab")
	fs.StringVar(&o.units, "units", "", "comma-separated in-game names to put in creative")
	fs.BoolVar(&o.once, "once", false, "one pass over the player list, then exit")
	fs.DurationVar(&o.poll, "poll", defaultPoll, "how often to read the player list")
	fs.DurationVar(&o.maxDuration, "max-duration", defaultMaxDuration, "stop after this long")
	fs.IntVar(&o.maxCommands, "max-commands", defaultMaxCommands, "stop after this many gamemode commands")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	units, err := validate(o)
	if err != nil {
		fmt.Fprintln(stderr, "labcreative: "+err.Error())
		return 2
	}
	c, err := d.console(o.lab, o.compose)
	if err != nil {
		fmt.Fprintln(stderr, "labcreative: "+err.Error())
		return 2
	}
	w := &watcher{c: c, units: units, o: o, d: d, out: stdout}
	if err := w.run(ctx); err != nil {
		fmt.Fprintln(stderr, "labcreative: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "labcreative: stopped (%s): %d gamemode command(s)\n", w.why, w.sent)
	if o.once && len(w.pending()) > 0 {
		fmt.Fprintf(stderr, "labcreative: not online, left in survival: %s\n", strings.Join(w.pending(), ", "))
		return 3
	}
	return 0
}

func validate(o options) ([]string, error) {
	if o.lab == "" {
		return nil, errors.New("--lab is required: the server to command")
	}
	if err := arena.CheckLab(o.lab, o.confirmLab); err != nil {
		return nil, err
	}
	// Even a confirmed target is refused when it names the FWB server, whose
	// players are real and which has no business in creative.
	if strings.Contains(strings.ToLower(o.lab), "fwb") {
		return nil, fmt.Errorf("--lab %q names the FWB server; this tool never touches it", o.lab)
	}
	var units []string
	seen := map[string]bool{}
	for _, n := range strings.Split(o.units, ",") {
		if n = strings.TrimSpace(n); n == "" || seen[n] {
			continue
		}
		if !unitName.MatchString(n) {
			return nil, fmt.Errorf("unit name %q must match %s (no selectors)", n, unitName)
		}
		seen[n] = true
		units = append(units, n)
	}
	switch {
	case len(units) == 0:
		return nil, errors.New("--units is required: the names to put in creative")
	case o.poll < minPoll:
		return nil, fmt.Errorf("--poll %s is below the %s minimum", o.poll, minPoll)
	case o.maxDuration <= 0 || o.maxDuration > maxDurationLimit:
		return nil, fmt.Errorf("--max-duration must be above 0 and at most %s", maxDurationLimit)
	case o.maxCommands < 1 || o.maxCommands > maxCommandsLimit:
		return nil, fmt.Errorf("--max-commands must be between 1 and %d", maxCommandsLimit)
	}
	return units, nil
}

type watcher struct {
	c     arena.Console
	units []string
	o     options
	d     deps
	out   io.Writer

	// need is true while a unit has not been set since it last joined: all
	// start true, and a unit seen offline goes back to true.
	need map[string]bool
	sent int
	why  string
}

func (w *watcher) pending() []string {
	var names []string
	for _, u := range w.units {
		if w.need[u] {
			names = append(names, u)
		}
	}
	return names
}

func (w *watcher) run(ctx context.Context) error {
	w.need = map[string]bool{}
	for _, u := range w.units {
		w.need[u] = true
	}
	deadline := w.d.now().Add(w.o.maxDuration)
	for {
		if err := w.tick(ctx); err != nil {
			return err
		}
		switch {
		case w.o.once:
			w.why = "once"
			return nil
		case ctx.Err() != nil:
			w.why = "interrupted"
			return nil
		case w.sent >= w.o.maxCommands:
			w.why = "max-commands"
			return nil
		case !w.d.now().Add(w.o.poll).Before(deadline):
			w.why = "max-duration"
			return nil
		}
		w.d.sleep(ctx, w.o.poll)
		if ctx.Err() != nil {
			w.why = "interrupted"
			return nil
		}
	}
}

// tick reads who is online and sets creative for each named unit that joined
// since it was last set. A list that cannot be read changes nothing: a unit
// is never assumed gone or present on a guess.
func (w *watcher) tick(ctx context.Context) error {
	names, ok, err := w.online()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(w.out, "labcreative: could not read the player list; trying again")
		return nil
	}
	online := map[string]bool{}
	for _, n := range names {
		online[n] = true
	}
	for _, u := range w.units {
		if !online[u] {
			w.need[u] = true
			continue
		}
		if !w.need[u] || w.sent >= w.o.maxCommands || ctx.Err() != nil {
			continue
		}
		w.sent++
		out, err := w.c.Exec(fmt.Sprintf("gamemode creative %q", u))
		if err != nil {
			return err
		}
		// A unit that left between the list and the command is still pending.
		if strings.Contains(out, "No targets matched") {
			fmt.Fprintf(w.out, "labcreative: %s is not there yet\n", u)
			continue
		}
		w.need[u] = false
		fmt.Fprintf(w.out, "labcreative: %s is in creative\n", u)
	}
	return nil
}

func (w *watcher) online() (names []string, ok bool, err error) {
	var all string
	for range listAttempts {
		out, err := w.c.Exec("list")
		if err != nil {
			return nil, false, err
		}
		if all != "" {
			all += "\n"
		}
		all += out
		if names, ok := arena.OnlineNames(all); ok {
			return names, true, nil
		}
	}
	return nil, false, nil
}
