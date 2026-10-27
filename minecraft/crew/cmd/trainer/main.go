// Command trainer searches for better warrior tactics in a lab.
//
// usage: trainer run --family zombie --scenario <name> --store <dir>
//
//	[--kind mob|duel|boss|wave] [--gens 20] [--fights 2000] [--budget 2h]
//	[--max-failures 5] [--evaluator fake|cmd:<command>] [--kill-file <path>]
//	[--seed N] [--evals 8] [--mu 3] [--lambda 6] [--margin 0.5] [--alpha 0.05]
//	[--sigma 0.12] [--fight-timeout 5m] [--tactics=false]
//
// Every run is capped by --gens, --fights, --budget and --max-failures, and
// stops at once when the kill file exists (default <store>/KILL) or on
// SIGINT/SIGTERM. The cmd evaluator runs <command> once per fight with
// {"profile":...,"scenario":...} on stdin and reads one scorecard per stdout
// line. run prints one JSON result line on stdout and progress on stderr; it
// exits 1 when the run stopped on repeated evaluator failures. Unless
// --tactics=false the loop also learns which of the engine's registered
// tactics to use against the family, and in what order. Only one trainer may
// run per family and store; a second exits 2 at once.
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

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := realMain(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

const usage = "usage: trainer run --family <f> --scenario <name> --store <dir> [--gens N] [--fights N] [--budget d] [--evaluator fake|cmd:<command>] [--kill-file path]"

func realMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	return runCmd(ctx, args[1:], stdout, stderr)
}

func runCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	family := fs.String("family", "", "opponent family to train for (zombie, skeleton, ...)")
	scenario := fs.String("scenario", "", "scenario name the evaluator understands")
	kind := fs.String("kind", tactics.ScenarioMob, "scenario kind: mob, duel, boss or wave")
	storeDir := fs.String("store", "", "champion store directory")
	gens := fs.Int("gens", 20, "maximum generations")
	fights := fs.Int("fights", 2000, "maximum fights")
	budget := fs.Duration("budget", 2*time.Hour, "wall-clock budget")
	maxFail := fs.Int("max-failures", 5, "stop after this many consecutive evaluator failures")
	evaluator := fs.String("evaluator", "fake", "fake, or cmd:<command> printing scorecards as JSONL")
	killFile := fs.String("kill-file", "", "stop when this file exists (default <store>/KILL)")
	fightTimeout := fs.Duration("fight-timeout", 5*time.Minute, "per-fight limit for the cmd evaluator")
	seed := fs.Int64("seed", 1, "random seed; the same seed reproduces a fake run")
	evals := fs.Int("evals", 8, "mirrored fights per candidate per generation")
	mu := fs.Int("mu", 3, "parents kept per generation")
	lambda := fs.Int("lambda", 6, "challengers per generation")
	margin := fs.Float64("margin", 0.5, "score gain a challenger needs over the champion")
	alpha := fs.Float64("alpha", 0.05, "significance level of the paired test, per generation")
	sigma := fs.Float64("sigma", 0.12, "initial mutation size as a fraction of each parameter's range")
	learnTactics := fs.Bool("tactics", true, "also learn which registered tactics to use against the family")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "trainer:", err)
		return 2
	}
	if *family == "" || *scenario == "" || *storeDir == "" {
		return fail(errors.New("run needs --family, --scenario and --store"))
	}
	ev, err := newEvaluator(*evaluator, tactics.Family(*family), *fightTimeout)
	if err != nil {
		return fail(err)
	}
	store, err := trainer.NewStore(*storeDir)
	if err != nil {
		return fail(err)
	}
	if *killFile == "" {
		*killFile = filepath.Join(*storeDir, "KILL")
	}

	var tacticOptions func(tactics.Family) []string
	if *learnTactics {
		tacticOptions = life.TacticNames
	}
	res, err := trainer.Run(ctx, trainer.Config{
		TacticOptions: tacticOptions,
		Family:        tactics.Family(*family), Scenario: *scenario, Kind: *kind, Store: store, Eval: ev,
		MaxGenerations: *gens, MaxFights: *fights, Budget: *budget, MaxFailures: *maxFail,
		KillFile: *killFile, Seed: *seed, Evals: *evals, Mu: *mu, Lambda: *lambda,
		Margin: *margin, Alpha: *alpha, Sigma: *sigma,
		Logf: func(format string, a ...any) { fmt.Fprintf(stderr, "trainer: "+format+"\n", a...) },
	})
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(stdout).Encode(res); err != nil {
		return fail(err)
	}
	if res.StopReason == trainer.StopFailures {
		return 1
	}
	return 0
}

func newEvaluator(spec string, family tactics.Family, fightTimeout time.Duration) (trainer.Evaluator, error) {
	switch {
	case spec == "fake":
		return trainer.NewFake(family), nil
	case strings.HasPrefix(spec, "cmd:") && strings.TrimSpace(spec[4:]) != "":
		return trainer.CmdEvaluator{Command: spec[4:], Timeout: fightTimeout}, nil
	}
	return nil, fmt.Errorf("--evaluator must be fake or cmd:<command>, got %q", spec)
}
