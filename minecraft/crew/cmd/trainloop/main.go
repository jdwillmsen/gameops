// Command trainloop is the capped overnight orchestrator: it runs the
// trainer's loop (as a library, not a subprocess) for a rotation of opponent
// families, evaluates fights through the trainbridge command, prints a
// trainreport summary after every family and at the end, and never promotes
// anything — the champion store files them.
//
// usage: trainloop overnight --families zombie,skeleton --scenarios <book>
//
//	--store <dir> --control-dir <dir> --bridge <path> --lab <lab>
//	--layout <file> [--compose <file>] --squad-url <crew-control>
//	--squad <lab-squad> [--squad-b <lab-squad>]
//	--max-hours <d> --max-rounds 8 --gens-per-round 20
//	--fights-per-gen 8 --max-total-fights 2000 --max-consecutive-failures 5
//	[--stop-file <path>] [--crew-names names] [--warriors names]
//	[--warrior-kits kits] [--fight-timeout 10m] [--seed N] [--tactics]
//	[--state <path>] [--log <path>] [--evaluator-args "..."]
//
// All six caps are mandatory with no default: startup refuses any value at
// or below zero, so the overnight run cannot start unbounded. The rotation
// cycles the families until a cap trips. Every cap and the switch are checked
// between rounds; the training switch is missing or off, or the stop file
// exists, and the campaign stops with exit 3. A campaign that stops on the
// consecutive-failure cap exits 1. A restart with the same flags resumes from
// the state file it wrote after the last round, so the spent budget is never
// reset: the caps are paid from what is left. Wake it deliberately with a
// stop file, `tacticsctl training off`, or SIGINT.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
)

const defaultCompose = "minecraft/crew/lab/compose.yaml"

const usage = `usage: trainloop overnight --families zombie,skeleton --scenarios <book> --store <dir>` + "\n" +
	`          --bridge <trainbridge path> --lab <lab> --layout <file> --squad-url <url> --squad <lab-squad>` + "\n" +
	`          --max-hours <d> --max-rounds N --gens-per-round N --fights-per-gen N` + "\n" +
	`          --max-total-fights N --max-consecutive-failures N` + "\n" +
	`        [--compose <file>] [--squad-b <lab-squad>] [--stop-file <path>] [--control-dir <dir>]` + "\n" +
	`        [--crew-names names] [--warriors names] [--warrior-kits kits] [--fight-timeout d]` + "\n" +
	`        [--seed N] [--tactics=true] [--state <path>] [--log <path>] [--evaluator-args "..."]`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := realMain(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func realMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || args[0] != "overnight" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	l, sec := parse(args[1:], stderr)
	if sec {
		return 2
	}
	// The console and its readiness check live behind a variable so tests
	// can put a fake lab behind the command, like arenadirector's.
	console, err := newConsole(l.Lab, l.Compose)
	if err != nil {
		fmt.Fprintln(stderr, "trainloop:", err)
		return 2
	}
	l.Console = console
	return l.Run(ctx, stdout)
}

func parse(args []string, stderr io.Writer) (*Loop, bool) {
	fs := flag.NewFlagSet("overnight", flag.ContinueOnError)
	fs.SetOutput(stderr)
	l := &Loop{Logf: func(format string, a ...any) { fmt.Fprintf(stderr, "trainloop: "+format+"\n", a...) }}

	families := fs.String("families", "", "comma-separated opponent families to train in rotation")
	scenarios := fs.String("scenarios", "", "arena scenario book YAML, or a directory holding exactly one")
	store := fs.String("store", "", "champion store directory (state and fight log default beside it)")

	maxHours := fs.Duration("max-hours", 0, "whole-campaign wall clock; required, no default")
	maxRounds := fs.Int("max-rounds", 0, "family runs; required, no default")
	gens := fs.Int("gens-per-round", 0, "trainer generations per family run; required, no default")
	fightsPerGen := fs.Int("fights-per-gen", 0, "mirrored fights per candidate per generation; required, at least 2")
	maxFights := fs.Int("max-total-fights", 0, "fights the whole campaign may spend; required, no default")
	maxFailures := fs.Int("max-consecutive-failures", 0, "stop a run after this many consecutive evaluator failures; required")

	bridge := fs.String("bridge", "trainbridge", "path of the trainbridge command the evaluator runs")
	lab := fs.String("lab", "", "crew lab compose service or k8s:<namespace>/<pod>")
	compose := fs.String("compose", defaultCompose, "compose file for a docker lab")
	layout := fs.String("layout", "", "layout JSON from labcourses arena-layout")
	squadURL := fs.String("squad-url", "", "crew-control base URL; hands profiles to the lab squad's route")
	squad := fs.String("squad", "", "lab squad id the units fight in, lab- prefixed")
	squadB := fs.String("squad-b", "", "lab squad id of a duel's --team-b units")
	warriors := fs.String("warriors", "Rusty,Slate,Tinder", "comma-separated warriors for mob scenarios")
	warriorKits := fs.String("warrior-kits", "", "comma-separated catalog kits, one per --warriors name")
	crew := fs.String("crew-names", strings.Join(arena.DefaultCrewNames, ","), "players allowed online while the campaign runs")
	fightTimeout := fs.Duration("fight-timeout", 10*time.Minute, "hard limit for one evaluator fight")

	controlDir := fs.String("control-dir", tacticsctl.DefaultDir(), "tacticsctl state directory holding the training switch; the campaign stops unless it says on")
	stopFile := fs.String("stop-file", "", "stop between rounds while this file exists (default <store>/KILL)")
	seed := fs.Int64("seed", 1, "random seed; every round continues it, so the same seed reproduces a fake run")
	learn := fs.Bool("tactics", true, "also learn which registered tactics to use against the family")
	statePath := fs.String("state", "", "campaign state file (default <store>/overnight-state.json)")
	logPath := fs.String("log", "", "scorecard log for the trainreport summaries (default <store>/fights.jsonl)")
	evaluatorArgs := fs.String("evaluator-args", "", "extra whitespace-joined trainbridge arguments; any --lab, --squad or --squad-b in them must be the crew lab")

	if err := fs.Parse(args); err != nil {
		return l, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "trainloop: unexpected argument %q\n", fs.Arg(0))
		return l, true
	}

	l.Families = splitFamilies(*families)
	l.Scenarios, l.Store = *scenarios, *store
	l.MaxHours, l.MaxRounds = *maxHours, *maxRounds
	l.GensPerRound, l.FightsPerGen = *gens, *fightsPerGen
	l.MaxTotalFights, l.MaxConsecutiveFailures = *maxFights, *maxFailures
	l.Bridge = *bridge
	l.Lab, l.Compose, l.Layout = *lab, *compose, *layout
	l.SquadURL, l.Squad, l.SquadB = *squadURL, *squad, *squadB
	l.Warriors, l.WarriorKits, l.CrewNames = *warriors, *warriorKits, *crew
	l.FightTimeout = *fightTimeout
	l.ControlDir, l.StopFile = *controlDir, *stopFile
	l.Seed, l.LearnTactics = *seed, *learn
	l.StatePath, l.FightsLog = *statePath, *logPath
	// Args are split on whitespace, like the trainer splits its cmd evaluator.
	l.EvaluatorArgs = strings.Fields(*evaluatorArgs)
	return l, false
}

// splitFamilies keeps the order and trims the spaces, and drops nothing: an
// empty entry is a malformed invocation the rotation must not paper over.
func splitFamilies(list string) []tactics.Family {
	var out []tactics.Family
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, tactics.Family(name))
	}
	return out
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

// splitNames keeps a comma list trimmed and drops empty entries.
func splitNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
