// Command arenadirector drives the Colosseum wave book over a lab server's
// console. It is lab-only: mobs are buffed and geared with cheats, and the
// squad is never attacked by the director itself.
//
// usage: arenadirector run --lab <lab> --layout <file> [options]
//
//	arenadirector reset --lab <lab> [--crew-names names]
//	arenadirector status --lab <lab> [--warriors names]
//	arenadirector boss --lab <lab> --layout <file> --mob <id> [options]
//	arenadirector boss --summary [--log <path>]
//	arenadirector duel --lab <lab> --layout <file> --red A,B --blue C,D [options]
//
// Every command that touches the server (run, boss, reset, duel) takes only
// the crew lab: --lab and --compose pass arena.ValidateLabConsole, and each of
// them except status first checks that no player outside --crew-names is
// online.
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
//
// run and boss take --scorecards <path> to also append one tactics scorecard
// per fight (the trainer's input) without changing their own output. The
// Bedrock console reports no damage, so scorecards carry zero damage taken
// unless --status-url gives the units' state endpoint, with {name} for the
// warrior, and the fight's difference of totals is read from there.
//
// duel fights two equal sides of crew warriors against each other on the
// lab's Colosseum floor for --rounds rounds, swapping the spawn lines every
// round, and appends one tactics scorecard per side per round to --scores. It
// prints one JSON line per round, exits 2 on a non-crew player like boss does,
// and accepts only the crew lab as target. The rounds, per-round time and
// wall-clock time are all capped. A duel clears inventories and teleports, so
// only the units in --duelists-allow (default Rusty,Slate,Tinder, plus the
// crew-lab names in --duelists-roster) may duel, whoever --crew-names lets
// stay online. A duelist that leaves the world aborts the round instead of
// losing it.
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
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
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
	case "duel":
		duel(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: arenadirector run --lab <lab> --layout <file> [--tier N] [--waves <file>] [--warriors names] [--crew-names names] [--rally-after d] [--rally-every d] [--place-squad=false] [--scorecards <path> [--profile name] [--family f] [--run-id id] [--side s] [--seed n] [--status-url url]]\n"+
		"       arenadirector reset --lab <lab> [--crew-names names]\n"+
		"       arenadirector status --lab <lab> [--warriors names]\n"+
		"       arenadirector boss --lab <lab> --layout <file> --mob <id> [--name W] [--effects \"cmd1,cmd2\"] [--runs N] [--timeout 5m] [--warriors names] [--log <path>] [--trace <path>] [--summary] [--scorecards <path> [--profile name] [--run-id id] [--side s] [--seed n] [--status-url url]]\n"+
		"       arenadirector duel --lab <lab> --layout <file> --red A,B --blue C,D [--kits vanguard/t5,ranger/t5] [--rounds N] [--timeout 2m] [--max-wall 30m] [--red-profile P] [--blue-profile P] [--crew-names names] [--duelists-allow names] [--duelists-roster <file>] [--scores <path>] [--stop-file <path>]")
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
	family := fs.String("family", string(tactics.FamilyDefault), "opponent family recorded on the scorecard")
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during a run")
	sf := addScoreFlags(fs)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *rallyAfter < -1 {
		fail("arenadirector: --rally-after must be -1 or non-negative")
	}
	if *rallyEvery < -1 {
		fail("arenadirector: --rally-every must be -1 or non-negative")
	}
	requireLab(*t.lab, *t.compose)
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
	requireAlone(console, *crew)

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
	rec := sf.recorder(tactics.ScenarioWave, tactics.Family(*family), fmt.Sprintf("gauntlet-t%d", *tier), d.Warriors, time.Now())
	if rec != nil {
		d.Observer = rec
	}
	result, err := d.Run()
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	if rec != nil {
		writeScorecard(os.Stderr, *sf.path, runScorecard(rec, result))
	}
	writeJSON(result)
}

func reset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	t := common(fs)
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online during a reset")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	requireLab(*t.lab, *t.compose)
	console, err := newConsole(*t.lab, *t.compose)
	if err != nil {
		fail("arenadirector: " + err.Error())
	}
	requireAlone(console, *crew)
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
	requireLab(*t.lab, *t.compose)
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
	tracePath := fs.String("trace", "", "append a JSON line per poll with the boss and warrior positions to this file")
	poll := fs.Duration("poll", 3*time.Second, "delay between squad and boss checks")
	settle := fs.Duration("settle", 25*time.Second, "wait for the arena chunks to load")
	placeWait := fs.Duration("place-wait", 3*time.Second, "wait after placing the squad")
	sf := addScoreFlags(fs)
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
	requireLab(*t.lab, *t.compose)
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

	var trace io.Writer
	var traceFile *os.File
	if *tracePath != "" {
		if samePath(*tracePath, path) {
			fail("arenadirector: --trace must not be the run log")
		}
		f, err := os.OpenFile(*tracePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fail("arenadirector: " + err.Error())
		}
		traceFile, trace = f, f
	}

	b := &arena.BossTest{
		Console:   console,
		Layout:    layout,
		Trace:     trace,
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
		Out:       os.Stderr,
	}
	rec := sf.recorder(tactics.ScenarioBoss, tactics.FamilyBoss, *mob, b.Warriors, time.Now())
	baseID := ""
	if rec != nil {
		baseID = rec.Meta.RunID
		rec.Meta.Spawned = 1
		b.Observer = rec
	}
	b.Record = func(r arena.BossRecord) error {
		if err := arena.AppendBossRecord(path, r); err != nil {
			return err
		}
		if rec != nil {
			writeScorecard(os.Stderr, *sf.path, bossScorecard(rec, baseID, *runs, r))
		}
		return json.NewEncoder(os.Stdout).Encode(r)
	}
	// An interrupt lets the run finish its cleanup instead of leaving a
	// buffed boss and loaded areas behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	recs, err := b.Run(ctx)
	// Closed before the exits below, which skip deferred calls; a failed
	// close can lose buffered trace lines, so it is reported.
	if traceFile != nil {
		if cerr := traceFile.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "arenadirector: trace: "+cerr.Error())
		}
	}
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

// samePath reports whether two paths name the same file, including through a
// link, and falls back to comparing absolute paths when one does not exist.
func samePath(a, b string) bool {
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			return os.SameFile(ai, bi)
		}
	}
	aa, errA := filepath.Abs(a)
	ba, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == ba
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

// newConsole is a variable so tests can put a fake lab behind a command.
var newConsole = realConsole

func realConsole(lab, compose string) (arena.Console, error) {
	if target, ok := strings.CutPrefix(lab, "k8s:"); ok {
		namespace, pod, ok := strings.Cut(target, "/")
		if !ok || namespace == "" || pod == "" {
			return nil, fmt.Errorf("%q is not k8s:<namespace>/<pod>", lab)
		}
		return arena.NewKubectlConsole(namespace, pod), nil
	}
	return arena.NewDockerConsole(compose, lab), nil
}

// requireLab refuses anything but the crew lab, both the service or pod
// named by --lab and the compose file a docker lab runs from.
func requireLab(lab, compose string) {
	if err := arena.ValidateLabConsole(lab, compose); err != nil {
		fail("arenadirector: " + err.Error())
	}
}

// requireAlone is the pre-flight of a command that changes the lab: it exits
// 2 before touching anything when a player outside the crew is online, the
// same refusal duel and boss give.
func requireAlone(console arena.Console, crew string) {
	err := arena.CheckAlone(console, splitNames(crew))
	var busy *arena.NotAloneError
	switch {
	case err == nil:
	case errors.As(err, &busy):
		fmt.Fprintln(os.Stderr, "arenadirector: refused: "+err.Error())
		exit(2)
	default:
		fail("arenadirector: " + err.Error())
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

// exit is os.Exit, replaceable so tests can see the code a command ends with.
var exit = os.Exit

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	exit(1)
}
