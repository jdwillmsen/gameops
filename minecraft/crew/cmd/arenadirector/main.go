// Command arenadirector drives the Colosseum wave book over a lab server's
// console. It is lab-only: mobs are buffed and geared with cheats, and the
// squad is never attacked by the director itself.
//
// usage: arenadirector run --lab <lab> --layout <file> [options]
//
//	arenadirector reset --lab <lab>
//	arenadirector status --lab <lab> [--warriors names]
//	arenadirector boss --lab <lab> --layout <file> --mob <id> [options]
//	arenadirector boss --summary [--log <path>]
//
// <lab> is the compose service of the local lab, or k8s:<namespace>/<pod>.
// run prints one JSON result line on stdout; progress goes to stderr.
//
// boss fights one named mob against the squad --runs times, prints one JSON
// record per run and appends each to the run log. It exits 2 when any player
// outside --crew-names is online: before touching the server, or, when one
// joins mid-fight, after killing the boss and logging the run as
// aborted_player_online. --summary reads the log and prints one JSON list of
// runs and medians per boss and effect set.
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
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

const defaultCompose = "minecraft/crew/lab/compose.yaml"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:])
	case "reset":
		reset(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "boss":
		boss(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: arenadirector run --lab <lab> --layout <file> [--tier N] [--waves <file>] [--warriors names] [--rally-after d] [--rally-every d] [--place-squad=false]\n"+
		"       arenadirector reset --lab <lab>\n"+
		"       arenadirector status --lab <lab> [--warriors names]\n"+
		"       arenadirector boss --lab <lab> --layout <file> --mob <id> [--name W] [--effects \"cmd1,cmd2\"] [--runs N] [--timeout 5m] [--warriors names] [--log <path>] [--summary]")
	os.Exit(2)
}

type target struct {
	lab     *string
	compose *string
}

func common(fs *flag.FlagSet) target {
	return target{
		lab:     fs.String("lab", "", "lab compose service or k8s:<namespace>/<pod>"),
		compose: fs.String("compose", defaultCompose, "compose file for a docker lab"),
	}
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	t := common(fs)
	layoutPath := fs.String("layout", "", "layout JSON from labcourses arena-layout")
	wavesPath := fs.String("waves", "", "wave book YAML (default embedded Gauntlet)")
	tier := fs.Int("tier", 5, "highest opposition tier to run (1-5)")
	warriors := fs.String("warriors", "", "comma-separated warrior names for wipe detection")
	timeout := fs.Duration("timeout", 30*time.Minute, "whole-run limit")
	waveTimeout := fs.Duration("wave-timeout", 0, "per-wave limit (0 uses the wave book)")
	poll := fs.Duration("poll", 2*time.Second, "delay between survivor counts")
	maxLive := fs.Int("max-live", 0, "live-mob cap (0 uses the wave book)")
	pause := fs.Duration("pause", 0, "pause between waves (0 uses the wave book)")
	rallyAfter := fs.Duration("rally-after", -1, "delay from a wave's first spawn before pulling survivors to the squad (-1 uses the wave book, 0 disables)")
	rallyEvery := fs.Duration("rally-every", -1, "interval between rallies while a wave lives (-1 uses the wave book)")
	settle := fs.Duration("settle", 25*time.Second, "wait for the arena chunks to load")
	openStart := fs.Bool("open-start-gate", true, "clear the squad start gate before wave one")
	placeSquad := fs.Bool("place-squad", true, "teleport the squad to its floor start point and back after a respawn")
	kits := fs.Bool("kits", false, "spawn the mob types the lab's kit catalog covers from it, in their tier's enchanted gear")
	warriorKits := fs.String("warrior-kits", "", "comma-separated catalog kits (vanguard/t5), one per --warriors name, handed out before wave one")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *rallyAfter < -1 {
		fail("arenadirector: --rally-after must be -1 or non-negative")
	}
	if *rallyEvery < -1 {
		fail("arenadirector: --rally-every must be -1 or non-negative")
	}
	requireLab(*t.lab)
	if *layoutPath == "" {
		fail("arenadirector: run needs --layout")
	}
	if err := arena.ValidateWarriorKits(splitNames(*warriors), splitNames(*warriorKits)); err != nil {
		fail("arenadirector: " + err.Error())
	}
	if *tier < 1 || *tier > 5 {
		fail("arenadirector: --tier must be 1..5")
	}

	book, err := loadBook(*wavesPath)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	applyRallyOverrides(book, *rallyAfter, *rallyEvery)
	layout, err := arena.LoadLayout(*layoutPath)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	console, err := newConsole(*t.lab, *t.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}

	d := &arena.Director{
		Console:       console,
		Layout:        layout,
		Book:          book,
		Tier:          *tier,
		MaxLive:       *maxLive,
		Pause:         *pause,
		Poll:          *poll,
		WaveTimeout:   *waveTimeout,
		RunTimeout:    *timeout,
		Warriors:      splitNames(*warriors),
		Settle:        *settle,
		OpenStartGate: *openStart,
		PlaceSquad:    *placeSquad,
		Kits:          *kits,
		WarriorKits:   splitNames(*warriorKits),
		Out:           os.Stderr,
	}
	result, err := d.Run()
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	writeJSON(result)
}

func reset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	t := common(fs)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	requireLab(*t.lab)
	console, err := newConsole(*t.lab, *t.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	if err := (&arena.Director{Console: console}).Reset(); err != nil {
		fail("arenadirector: " + err.Error())
	}
	fmt.Fprintln(os.Stdout, `{"reset":true}`)
}

func status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	t := common(fs)
	warriors := fs.String("warriors", "", "comma-separated warrior names")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	requireLab(*t.lab)
	console, err := newConsole(*t.lab, *t.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	st, err := (&arena.Director{Console: console, Warriors: splitNames(*warriors)}).Status()
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	writeJSON(st)
}

func boss(args []string) {
	fs := flag.NewFlagSet("boss", flag.ExitOnError)
	t := common(fs)
	layoutPath := fs.String("layout", "", "layout JSON from labcourses arena-layout")
	mob := fs.String("mob", "", "mob id to summon, e.g. warden")
	name := fs.String("name", "W", "name given to the boss, which the kill check searches for")
	event := fs.String("event", "", "spawn event run on summon, e.g. gameops:hp_500 for the ground dragon")
	effects := fs.String("effects", "", "comma-separated effect, damage or event commands run right after the summon; each must target only @e[name=<name>...]")
	runs := fs.Int("runs", 1, "number of fights")
	timeout := fs.Duration("timeout", 5*time.Minute, "limit for one fight")
	warriors := fs.String("warriors", "Rusty,Slate,Tinder", "comma-separated warrior names")
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during a test")
	logPath := fs.String("log", "", "run log (default ~/.local/state/arena-runs.jsonl)")
	summary := fs.Bool("summary", false, "print runs and medians from the log instead of fighting")
	poll := fs.Duration("poll", 3*time.Second, "delay between squad and boss checks")
	settle := fs.Duration("settle", 25*time.Second, "wait for the arena chunks to load")
	placeWait := fs.Duration("place-wait", 3*time.Second, "wait after placing the squad")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	path := *logPath
	if path == "" {
		var err error
		if path, err = defaultLogPath(); err != nil {
			fail("arenadirector: " + err.Error())
		}
	}
	if *summary {
		if err := printSummary(os.Stdout, os.Stderr, path); err != nil {
			fail("arenadirector: " + err.Error())
		}
		return
	}
	requireLab(*t.lab)
	if *layoutPath == "" {
		fail("arenadirector: boss needs --layout")
	}
	if *mob == "" {
		fail("arenadirector: boss needs --mob")
	}
	if *runs < 1 {
		fail("arenadirector: --runs must be at least 1")
	}
	layout, err := arena.LoadLayout(*layoutPath)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	console, err := newConsole(*t.lab, *t.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}

	b := &arena.BossTest{
		Console:   console,
		Layout:    layout,
		Mob:       *mob,
		Name:      *name,
		Event:     *event,
		Effects:   arena.SplitEffects(*effects),
		Runs:      *runs,
		Timeout:   *timeout,
		Warriors:  splitNames(*warriors),
		CrewNames: splitNames(*crew),
		Poll:      *poll,
		Settle:    *settle,
		PlaceWait: *placeWait,
		Record: func(r arena.BossRecord) error {
			if err := arena.AppendBossRecord(path, r); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(r)
		},
		Out: os.Stderr,
	}
	// An interrupt lets the run finish its cleanup instead of leaving a
	// buffed boss and loaded areas behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	recs, err := b.Run(ctx)
	var busy *arena.NotAloneError
	switch {
	case errors.As(err, &busy):
		verb := "refused"
		if len(recs) > 0 && recs[len(recs)-1].Outcome == arena.BossAborted {
			verb = "aborted"
		}
		fmt.Fprintln(os.Stderr, "arenadirector: "+verb+": "+err.Error())
		os.Exit(2)
	case err != nil:
		fail("arenadirector: " + err.Error())
	}
}

func defaultLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "arena-runs.jsonl"), nil
}

func printSummary(w, warn io.Writer, path string) error {
	recs, skipped, err := arena.ReadBossRecords(path)
	if err != nil {
		return err
	}
	if skipped > 0 {
		fmt.Fprintf(warn, "arenadirector: skipped %d malformed line(s) in %s\n", skipped, path)
	}
	return json.NewEncoder(w).Encode(arena.SummarizeBoss(recs))
}

func loadBook(path string) (*arena.WaveBook, error) {
	if path == "" {
		return arena.DefaultWaves()
	}
	return arena.LoadWaveFile(path)
}

// applyRallyOverrides mirrors the run flags onto the book. The -1 default
// means the operator did not pass the flag, so the book's value stands; an
// explicit 0 disables rallies.
func applyRallyOverrides(book *arena.WaveBook, after, every time.Duration) {
	if after >= 0 {
		book.RallyAfter = &arena.Duration{Duration: after}
	}
	if every >= 0 {
		book.RallyEvery = &arena.Duration{Duration: every}
	}
}

func newConsole(lab, compose string) (arena.Console, error) {
	if target, ok := strings.CutPrefix(lab, "k8s:"); ok {
		namespace, pod, ok := strings.Cut(target, "/")
		if !ok || namespace == "" || pod == "" {
			return nil, fmt.Errorf("%q is not k8s:<namespace>/<pod>", lab)
		}
		return arena.NewKubectlConsole(namespace, pod), nil
	}
	return arena.NewDockerConsole(compose, lab), nil
}

func requireLab(lab string) {
	if lab == "" {
		fail("arenadirector: --lab is required")
	}
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

func writeJSON(v any) {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		fail("arenadirector: " + err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
