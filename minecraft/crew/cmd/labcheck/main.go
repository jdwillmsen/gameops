// Command labcheck runs the live checks that only the crew lab can answer and
// prints PASS, FAIL or INCONCLUSIVE for each, with the evidence behind it.
//
// usage: labcheck --lab <lab> --status-url http://<crew-control>/units/{name}/v1/state
//
//	[--layout <file> --squad-url <url> --red-squad <id> --blue-squad <id>]
//	[--units Rusty,Slate,Tinder] [--checks tp,zombie,creeper,phantom,sprint]
//	[--check-timeout 3m] [--max-wall 15m] [--stop-file <path>] [--json]
//
// The checks: tp (does `tp "Name" x y z yaw pitch` turn a unit), zombie (does
// a unit see a zombie's health), creeper (does it see a creeper's Charged
// flag), phantom (does a phantom hunt) and sprint (does a sprinting duel draw
// few corrections from the server).
//
// It is lab-only: --lab must be the crew lab, it refuses while a player
// outside --crew-names is online, and it moves and watches only the units in
// --units. It never touches an inventory, and everything it summons is
// tagged, killed on every exit path and counted afterwards.
//
// Exit codes: 0 every check passed, 1 a check failed or the cleanup left
// something behind, 4 nothing failed but a check was inconclusive, 2 refused
// or malformed, 3 stopped by the training switch, the stop file or an
// interrupt.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

const (
	defaultCompose = "minecraft/crew/lab/compose.yaml"

	// Hard ceilings on the flags: the run is bounded whatever they say.
	maxCheckTimeout = 15 * time.Minute
	maxWall         = time.Hour
	minWindow       = 5 * time.Second
	maxWindow       = 2 * time.Minute

	exitFail         = 1
	exitRefused      = 2
	exitStopped      = 3
	exitInconclusive = 4
)

// unitName is the shape of a unit allowed on the command line: no quote or
// control character that could end a quoted selector argument.
var unitName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

// Swapped by tests to put a fake lab behind the command.
var (
	newConsole       = func(lab, compose string) (arena.Console, error) { return arena.NewLabConsole(lab, compose) }
	newUnits         = func(template string) (UnitSource, error) { return NewHTTPUnits(template) }
	newDuel          = realDuel
	clock      Clock = systemClock{}
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := realMain(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// config is a parsed, validated command line.
type config struct {
	lab, compose string
	units        []string
	crew         []string
	checks       []string
	statusURL    string
	layout       string
	squads       *duelSquads
	controlDir   string
	stopFile     string
	lim          Limits
	opts         Options
	duelTimeout  time.Duration
	json         bool
}

func parse(args []string, errOut io.Writer) (*config, error) {
	fs := flag.NewFlagSet("labcheck", flag.ContinueOnError)
	fs.SetOutput(errOut)
	lab := fs.String("lab", "", "crew lab: a compose service or k8s:<namespace>/<pod>")
	compose := fs.String("compose", defaultCompose, "compose file for a docker lab")
	units := fs.String("units", strings.Join(arena.DefaultDuelists, ","), "crew warriors the checks may touch; the first is the subject, the first two duel")
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during the run")
	checks := fs.String("checks", strings.Join(checkNames(), ","), "checks to run")
	statusURL := fs.String("status-url", "", "a unit's /v1/state with {name} for the unit, e.g. http://crew-control/units/{name}/v1/state")
	layout := fs.String("layout", "", "arena layout JSON; the sprint check duels on its floor")
	squadURL := fs.String("squad-url", "", "crew-control base URL; delivers the duel's start signal to the lab squads (token from CONTROL_TOKEN)")
	redSquad := fs.String("red-squad", "", "lab squad of the first unit, lab- prefixed; with --squad-url")
	blueSquad := fs.String("blue-squad", "", "lab squad of the second unit, lab- prefixed; with --squad-url")
	controlDir := fs.String("control-dir", "", "tacticsctl state directory (default $TACTICSCTL_DIR or ~/.local/state/crew-tactics)")
	stopFile := fs.String("stop-file", "", "end the run when this file exists")
	checkTimeout := fs.Duration("check-timeout", 3*time.Minute, fmt.Sprintf("limit for one check (at most %s)", maxCheckTimeout))
	wall := fs.Duration("max-wall", 15*time.Minute, fmt.Sprintf("limit for the whole run (at most %s)", maxWall))
	window := fs.Duration("window", 20*time.Second, fmt.Sprintf("how long a check watches for its effect (%s..%s)", minWindow, maxWindow))
	poll := fs.Duration("poll", 2*time.Second, "delay between readings")
	settle := fs.Duration("settle", 2*time.Second, "wait for a unit to settle after a teleport")
	duelTimeout := fs.Duration("duel-timeout", 90*time.Second, fmt.Sprintf("limit for the sprint check's one round (%s..%s)", arena.MinDuelRoundTimeout, arena.MaxDuelRoundTimeout))
	maxCorrections := fs.Uint64("max-corrections", 10, "server corrections the sprinting round may draw before the sprint check fails")
	phantomMove := fs.Float64("phantom-move", 6, "blocks a phantom must fly to count as hunting")
	phantomClose := fs.Float64("phantom-close", 3, "blocks a phantom must close on the warrior to count as hunting")
	asJSON := fs.Bool("json", false, "print the report as one JSON object")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	// The lab rules come first: a wrong target must fail before anything
	// else is looked at, let alone touched.
	if err := arena.ValidateLabConsole(*lab, *compose); err != nil {
		return nil, err
	}
	if err := tacticsctl.LabOnly(*lab); err != nil {
		return nil, fmt.Errorf("--lab: %w", err)
	}
	c := &config{
		lab: *lab, compose: *compose, units: splitNames(*units), crew: splitNames(*crew),
		statusURL: *statusURL, layout: *layout, stopFile: *stopFile, json: *asJSON, duelTimeout: *duelTimeout,
		lim: Limits{CheckTimeout: *checkTimeout, MaxWall: *wall},
		opts: Options{
			Window: *window, Poll: *poll, Settle: *settle,
			PhantomMove: *phantomMove, PhantomClose: *phantomClose, MaxCorrections: *maxCorrections,
		},
		controlDir: *controlDir,
	}
	if c.controlDir == "" {
		c.controlDir = tacticsctl.DefaultDir()
	}
	switch {
	case c.lim.CheckTimeout < time.Second || c.lim.CheckTimeout > maxCheckTimeout:
		return nil, fmt.Errorf("--check-timeout must be 1s..%s", maxCheckTimeout)
	case c.lim.MaxWall <= 0 || c.lim.MaxWall > maxWall:
		return nil, fmt.Errorf("--max-wall must be positive and at most %s", maxWall)
	case c.opts.Window < minWindow || c.opts.Window > maxWindow:
		return nil, fmt.Errorf("--window must be %s..%s", minWindow, maxWindow)
	case c.opts.Poll <= 0 || c.opts.Poll > c.opts.Window:
		return nil, errors.New("--poll must be positive and at most --window")
	case c.opts.Settle < 0 || c.opts.Settle > c.opts.Window:
		return nil, errors.New("--settle must be at most --window")
	case c.duelTimeout < arena.MinDuelRoundTimeout || c.duelTimeout > arena.MaxDuelRoundTimeout:
		return nil, fmt.Errorf("--duel-timeout must be %s..%s", arena.MinDuelRoundTimeout, arena.MaxDuelRoundTimeout)
	}
	if len(c.units) == 0 {
		return nil, errors.New("--units names no unit")
	}
	for _, u := range c.units {
		if !unitName.MatchString(u) {
			return nil, fmt.Errorf("--units: %q is not a unit name", u)
		}
	}
	c.opts.Units, c.opts.CrewNames = c.units, c.crew

	var err error
	if c.checks, err = selectChecks(*checks); err != nil {
		return nil, err
	}
	needsState := slices.ContainsFunc(c.checks, func(n string) bool { return n != "tp" && n != "phantom" })
	if needsState && c.statusURL == "" {
		return nil, errors.New("the zombie, creeper and sprint checks read the units' state: pass --status-url")
	}
	if slices.Contains(c.checks, "sprint") {
		if len(c.units) < 2 {
			return nil, errors.New("the sprint check duels two units: --units needs two names")
		}
		if c.layout == "" {
			return nil, errors.New("the sprint check duels on the arena floor: pass --layout")
		}
	}
	if c.squads, err = parseDuelSquads(*squadURL, *redSquad, *blueSquad); err != nil {
		return nil, err
	}
	return c, nil
}

// selectChecks reads --checks and refuses a name it does not know.
func selectChecks(list string) ([]string, error) {
	var out []string
	for _, n := range splitNames(list) {
		if !slices.Contains(checkNames(), n) {
			return nil, fmt.Errorf("--checks: unknown check %q (want %s)", n, strings.Join(checkNames(), ", "))
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("--checks names no check")
	}
	return out, nil
}

func splitNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// duelSquads is where the sprint check's start signal goes.
type duelSquads struct {
	base      *url.URL
	red, blue string
	token     string
}

func parseDuelSquads(squadURL, red, blue string) (*duelSquads, error) {
	if squadURL == "" && red == "" && blue == "" {
		return nil, nil
	}
	if squadURL == "" || red == "" || blue == "" {
		return nil, errors.New("--squad-url, --red-squad and --blue-squad go together")
	}
	base, err := url.Parse(squadURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("--squad-url: want an http or https URL with a host")
	}
	for flag, id := range map[string]string{"--red-squad": red, "--blue-squad": blue} {
		if err := arena.ValidateLabSquad(id); err != nil {
			return nil, fmt.Errorf("%s: %w", flag, err)
		}
		if err := tacticsctl.LabOnly(id); err != nil {
			return nil, fmt.Errorf("%s: %w", flag, err)
		}
	}
	if red == blue {
		return nil, errors.New("--red-squad must differ from --blue-squad: a squad has one duel state")
	}
	token := strings.TrimSpace(os.Getenv("CONTROL_TOKEN"))
	if token == "" {
		return nil, errors.New("CONTROL_TOKEN is required with --squad-url")
	}
	return &duelSquads{base: base, red: red, blue: blue, token: token}, nil
}

// discardSink drops the duel's scorecards: the sprint check measures the
// units, not a profile, and must not feed the trainer.
type discardSink struct{}

func (discardSink) Record(tactics.Scorecard) error { return nil }

// realDuel builds the sprint check's one-round duel. It sets no kits, so the
// duel clears and hands out nothing.
func realDuel(c *config, console arena.Console, halted func() string) (DuelRunner, error) {
	layout, err := arena.LoadLayout(c.layout)
	if err != nil {
		return nil, err
	}
	d := &arena.Duel{
		Console: console, Layout: layout,
		Red: c.units[:1], Blue: c.units[1:2],
		RedProfile: "player/strafe", BlueProfile: "player/strafe",
		Rounds: 1, RoundTimeout: c.duelTimeout, MaxWall: c.lim.CheckTimeout,
		CrewNames: c.crew, Duelists: c.units[:2],
		Sink: discardSink{}, RunID: "labcheck-" + time.Now().UTC().Format("20060102T150405Z"),
		Spacing: 12, Poll: 3 * time.Second, Settle: 25 * time.Second, KitGap: 3 * time.Second, Countdown: 5 * time.Second,
		Halted: func() bool { return halted() != "" },
	}
	tags := &arena.ConsolePolicy{Console: console}
	d.Policy = tags
	if c.squads != nil {
		if d.Policy, err = arena.NewSquadPolicy(c.squads.base, c.squads.red, c.squads.blue, c.squads.token, d.Red, d.Blue, tags); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func realMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c, err := parse(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return 0
	case err != nil:
		fmt.Fprintln(stderr, "labcheck:", err)
		return exitRefused
	}

	// The switch is read before the server is touched and again between
	// every step: off means nothing runs.
	halted := func() string {
		if !tacticsctl.TrainingEnabled(c.controlDir) {
			return "the training switch is off (" + c.controlDir + ")"
		}
		if c.stopFile != "" {
			if _, err := os.Stat(c.stopFile); err == nil {
				return "stop file " + c.stopFile + " exists"
			}
		}
		return ""
	}
	if why := halted(); why != "" {
		fmt.Fprintln(stderr, "labcheck: refused:", why)
		return exitStopped
	}

	console, err := newConsole(c.lab, c.compose)
	if err != nil {
		fmt.Fprintln(stderr, "labcheck:", err)
		return exitRefused
	}
	if err := arena.CheckAlone(console, c.crew); err != nil {
		fmt.Fprintln(stderr, "labcheck: refused:", err)
		return exitRefused
	}

	env := &Env{Console: console, Clock: clock, Opts: c.opts, Halted: halted, Out: stderr}
	if c.statusURL != "" {
		if env.Units, err = newUnits(c.statusURL); err != nil {
			fmt.Fprintln(stderr, "labcheck:", err)
			return exitRefused
		}
	}
	if slices.Contains(c.checks, "sprint") {
		if c.squads == nil {
			fmt.Fprintln(stderr, "labcheck: no --squad-url: the units hear no start signal, so the sprint check will probably be inconclusive")
		}
		if env.Duel, err = newDuel(c, console, halted); err != nil {
			fmt.Fprintln(stderr, "labcheck:", err)
			return exitRefused
		}
	}

	rep := Run(ctx, env, c.checks, c.lim)
	rep.Lab = c.lab
	if err := render(stdout, rep, c.json); err != nil {
		fmt.Fprintln(stderr, "labcheck:", err)
		return exitFail
	}
	return exitCode(rep)
}

// exitCode is the run's verdict as a process status.
func exitCode(rep Report) int {
	switch {
	case rep.Refused:
		return exitRefused
	case rep.Stopped != "":
		return exitStopped
	}
	code := 0
	for _, r := range rep.Results {
		switch r.Status {
		case Fail:
			return exitFail
		case Inconclusive:
			code = exitInconclusive
		}
	}
	if !rep.Cleanup.OK {
		return exitFail
	}
	return code
}

func render(w io.Writer, rep Report, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(rep)
	}
	for _, r := range rep.Results {
		fmt.Fprintf(w, "%-12s %-8s %s\n", r.Status, r.Check, r.Summary)
		keys := make([]string, 0, len(r.Evidence))
		for k := range r.Evidence {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b, err := json.Marshal(r.Evidence[k])
			if err != nil {
				b = []byte(fmt.Sprint(r.Evidence[k]))
			}
			fmt.Fprintf(w, "    %s: %s\n", k, b)
		}
	}
	if rep.Stopped != "" {
		fmt.Fprintf(w, "STOPPED      %s\n", rep.Stopped)
	}
	switch {
	case rep.Cleanup.OK:
		fmt.Fprintln(w, "cleanup      ok: nothing the checks summoned is left")
	case rep.Cleanup.Unknown:
		fmt.Fprintf(w, "CLEANUP      FAILED: could not count what is left (%d seen); kill @e[tag=%s] by hand\n", rep.Cleanup.Remaining, labTag)
	default:
		fmt.Fprintf(w, "CLEANUP      FAILED: %d mob(s) tagged %s are still there; kill @e[tag=%s] by hand\n", rep.Cleanup.Remaining, labTag, labTag)
	}
	return nil
}
