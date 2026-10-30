package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// duelConfig is a parsed `arenadirector duel` command line.
type duelConfig struct {
	lab, compose, layout string
	scores, stopFile     string
	squads               *duelSquads
	duel                 arena.Duel
}

// parseDuel reads the flags and applies every cap before anything touches a
// server. A target that is not the crew lab fails here.
func parseDuel(args []string, errOut io.Writer) (*duelConfig, error) {
	fs := flag.NewFlagSet("duel", flag.ContinueOnError)
	fs.SetOutput(errOut)
	t := common(fs)
	layoutPath := fs.String("layout", "", "layout JSON from labcourses arena-layout")
	red := fs.String("red", "", "comma-separated warriors on the red side in round 1")
	blue := fs.String("blue", "", "comma-separated warriors on the blue side in round 1; as many as --red")
	redProfile := fs.String("red-profile", "default", "profile name the red side's scorecards carry")
	blueProfile := fs.String("blue-profile", "default", "profile name the blue side's scorecards carry")
	kits := fs.String("kits", "", "comma-separated catalog kits (vanguard/t5), one per slot, handed to both sides")
	rounds := fs.Int("rounds", 2, fmt.Sprintf("rounds; the sides swap every round (at most %d)", arena.MaxDuelRounds))
	timeout := fs.Duration("timeout", 2*time.Minute, fmt.Sprintf("limit for one round (%s..%s)", arena.MinDuelRoundTimeout, arena.MaxDuelRoundTimeout))
	maxWall := fs.Duration("max-wall", 30*time.Minute, fmt.Sprintf("limit for the whole run (at most %s)", arena.MaxDuelWall))
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during a duel; says nothing about who may duel")
	allow := fs.String("duelists-allow", strings.Join(arena.DefaultDuelists, ","), "units that may duel; a duel clears their inventories and moves them")
	roster := fs.String("duelists-roster", "", "optional file of unit names, one per line; the lines starting with crew-lab join --duelists-allow")
	scores := fs.String("scores", "", "scorecard JSONL to append to (default ~/.local/state/duel-scorecards.jsonl)")
	squadURL := fs.String("squad-url", "", "crew-control base URL; delivers the duel to the lab squads (token from CONTROL_TOKEN)")
	redSquad := fs.String("red-squad", "", "lab squad id of the --red units, lab- prefixed; with --squad-url")
	blueSquad := fs.String("blue-squad", "", "lab squad id of the --blue units, lab- prefixed; with --squad-url")
	stopFile := fs.String("stop-file", "", "end the run when this file exists")
	runID := fs.String("run-id", "", "run id on every scorecard (default duel-<time>)")
	spacing := fs.Int("spacing", 12, "distance of each line from the floor centre")
	poll := fs.Duration("poll", 3*time.Second, "delay between standing checks")
	settle := fs.Duration("settle", 25*time.Second, "wait for the arena chunks to load")
	kitGap := fs.Duration("kit-gap", 3*time.Second, "wait after each kit")
	countdown := fs.Duration("countdown", 5*time.Second, "wait between the last setup step and the start signal")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := arena.ValidateLabConsole(*t.lab, *t.compose); err != nil {
		return nil, err
	}
	if *layoutPath == "" {
		return nil, errors.New("duel needs --layout")
	}
	duelists, err := duelistList(*allow, *roster)
	if err != nil {
		return nil, err
	}
	squads, err := parseDuelSquads(*squadURL, *redSquad, *blueSquad)
	if err != nil {
		return nil, err
	}
	id := *runID
	if id == "" {
		id = "duel-" + time.Now().UTC().Format("20060102T150405Z")
	}
	path := *scores
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".local", "state", "duel-scorecards.jsonl")
	}
	return &duelConfig{
		lab: *t.lab, compose: *t.compose, layout: *layoutPath, scores: path, stopFile: *stopFile, squads: squads,
		duel: arena.Duel{
			Red: splitNames(*red), Blue: splitNames(*blue),
			RedProfile: *redProfile, BlueProfile: *blueProfile,
			Kits: splitNames(*kits), Rounds: *rounds, RoundTimeout: *timeout, MaxWall: *maxWall,
			CrewNames: splitNames(*crew), Duelists: duelists, RunID: id, Spacing: *spacing,
			Poll: *poll, Settle: *settle, KitGap: *kitGap, Countdown: *countdown,
		},
	}, nil
}

// labRosterName is the shape of a roster line that may add a duelist.
var labRosterName = regexp.MustCompile(`^crew-lab[A-Za-z0-9_.-]*$`)

// duelistList is who may duel: the --duelists-allow names, plus the roster
// file's crew-lab names when the file is given and present. It is never built
// from --crew-names, which lets extra players stay online but must not make
// them fair game for a clear and a teleport.
func duelistList(allow, rosterPath string) ([]string, error) {
	names := splitNames(allow)
	if rosterPath != "" {
		raw, err := os.ReadFile(rosterPath)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			for _, line := range strings.Split(string(raw), "\n") {
				if line = strings.TrimSpace(line); labRosterName.MatchString(line) && !slices.Contains(names, line) {
					names = append(names, line)
				}
			}
		}
	}
	if len(names) == 0 {
		return nil, errors.New("duel needs at least one unit in --duelists-allow")
	}
	return names, nil
}

// fileSink appends each scorecard to a JSONL file.
type fileSink struct{ path string }

func (s fileSink) Record(c tactics.Scorecard) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := (arena.JSONLSink{W: f}).Record(c); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

func duel(args []string) {
	cfg, err := parseDuel(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	layout, err := arena.LoadLayout(cfg.layout)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	console, err := newConsole(cfg.lab, cfg.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	d := cfg.duel
	d.Console, d.Layout = console, layout
	if d.Policy, err = cfg.policy(console); err != nil {
		fail("arenadirector: " + err.Error())
	}
	d.Sink = fileSink{path: cfg.scores}
	d.Out = os.Stderr
	if cfg.stopFile != "" {
		d.Halted = func() bool { _, err := os.Stat(cfg.stopFile); return err == nil }
	}
	if cfg.squads == nil {
		fmt.Fprintln(os.Stderr, "arenadirector: no --squad-url: the units hear no start signal, so this run only tags and scores them")
	}

	// An interrupt lets the round finish its cleanup instead of leaving
	// units tagged and areas loaded.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	rounds, err := d.Run(ctx)
	enc := json.NewEncoder(os.Stdout)
	for _, r := range rounds {
		if encErr := enc.Encode(r); encErr != nil {
			fail("arenadirector: " + encErr.Error())
		}
	}
	var busy *arena.NotAloneError
	switch {
	case errors.As(err, &busy):
		fmt.Fprintln(os.Stderr, "arenadirector: refused: "+err.Error())
		os.Exit(2)
	case err != nil:
		fail("arenadirector: " + err.Error())
	}
}
