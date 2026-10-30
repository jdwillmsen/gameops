// The overnight orchestrator behind the trainloop command: it runs the
// trainer's loop for a rotation of opponent families until the hour, round,
// fight or failure budget runs out, with the trainbridge command evaluator
// and a trainreport summary after every family. It never promotes anything:
// the trainer store files champions, and only tacticsctl approval may take
// them further.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainreport"
)

// familyRE mirrors the trainer's rule for family names, so a bad name fails
// at startup instead of on fight one of a night.
var familyRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Loop is one overnight campaign. The CLI parses the operator's flags into
// it; Run drives it until the first cap trips or the operator pulls the
// switch.
type Loop struct {
	Families               []tactics.Family
	MaxHours               time.Duration
	MaxRounds              int
	GensPerRound           int
	FightsPerGen           int
	MaxTotalFights         int
	MaxConsecutiveFailures int
	Seed                   int64
	LearnTactics           bool
	// Scenarios is the arena scenario book: one YAML file, or a directory
	// holding exactly one.
	Scenarios     string
	ControlDir    string
	StopFile      string
	Store         string
	StatePath     string
	FightsLog     string
	Bridge        string
	FightTimeout  time.Duration
	Lab           string
	Compose       string
	Layout        string
	SquadURL      string
	Squad         string
	SquadB        string
	Warriors      string
	WarriorKits   string
	CrewNames     string
	EvaluatorArgs []string

	// Now is replaceable so tests can advance the campaign clock without
	// sleeping; every cap counts with it.
	Now func() time.Time
	// Logf reports progress; the CLI gives it a stderr printer.
	Logf func(format string, a ...any)
	// Console is the server console for the crew check; tests put a fake lab
	// behind it, the way arenadirector and trainbridge do.
	Console arena.Console
	// EvalOverride replaces the trainbridge evaluator in tests, which run
	// the trainer against a fake so the caps are provable offline.
	EvalOverride trainer.Evaluator
	// BetweenRounds is a test seam that runs after a finished round and
	// before the next one, where the switch and stop file are checked.
	BetweenRounds func()

	store *trainer.Store
	eval  trainer.Evaluator
	cards *json.Encoder
	// logWarn keeps a broken scorecard log at one warning, not one per fight.
	logWarn bool
}

// keepGoing is not an exit code: the round ended on its own per-round cap
// (its generation count) and the rotation moves to the next family.
const keepGoing = -1

// validate rejects a campaign the flags cannot honestly describe. Every cap
// is checked here, so there is no mode without limits.
func (l *Loop) validate() error {
	if len(l.Families) == 0 {
		return errors.New("--families is required: one or more comma-separated opponent families")
	}
	seen := map[tactics.Family]bool{}
	for _, f := range l.Families {
		if !familyRE.MatchString(string(f)) {
			return fmt.Errorf("--families: %q is not a safe family name", f)
		}
		if seen[f] {
			return fmt.Errorf("--families: %q repeats", f)
		}
		seen[f] = true
	}
	if l.MaxHours <= 0 {
		return errors.New("--max-hours is required: the campaign's whole wall-clock budget")
	}
	if l.MaxHours > 24*time.Hour {
		return errors.New("--max-hours must be at most 24h; an overnight run ends the day it starts")
	}
	if l.MaxRounds < 1 {
		return errors.New("--max-rounds is required: one family run of the rotation")
	}
	if l.GensPerRound < 1 {
		return errors.New("--gens-per-round is required: the trainer's generations per family run")
	}
	if l.MaxTotalFights < 1 {
		return errors.New("--max-total-fights is required: the fights the whole campaign may spend")
	}
	if l.MaxConsecutiveFailures < 1 {
		return errors.New("--max-consecutive-failures is required")
	}
	if l.FightsPerGen < 2 {
		// The trainer compares champion and challenger fight by fight; one
		// fight cannot be a paired test.
		return errors.New("--fights-per-gen must be at least 2 (a paired comparison needs two mirrored fights)")
	}
	if l.Scenarios == "" {
		return errors.New("--scenarios is required: the scenario book the fights come from")
	}
	if l.Store == "" {
		return errors.New("--store is required")
	}
	if l.ControlDir == "" {
		return errors.New("--control-dir is required: the tacticsctl directory with the training switch")
	}
	if l.StopFile == "" {
		l.StopFile = filepath.Join(l.Store, "KILL")
	}
	if l.StatePath == "" {
		l.StatePath = filepath.Join(l.Store, "overnight-state.json")
	}
	if l.FightsLog == "" {
		l.FightsLog = filepath.Join(l.Store, "fights.jsonl")
	}
	return nil
}

// prepare opens what the whole campaign shares and refuses the lab while
// anyone outside --crew-names is online, the same pre-flight arenadirector
// gives every command that changes the server.
func (l *Loop) prepare() error {
	if err := arena.ValidateLabConsole(l.Lab, l.Compose); err != nil {
		return err
	}
	// The deeper guard under the compose shape: the target must say crew-lab
	// or be a lab- squad id, whichever way the operator names it.
	if err := tacticsctl.LabOnly(l.Lab); err != nil {
		return fmt.Errorf("--lab: %w", err)
	}
	// The bridge drives the squads by id, and these are targets too.
	for _, s := range []string{l.Squad, l.SquadB} {
		if s == "" {
			continue
		}
		if err := tacticsctl.LabOnly(s); err != nil {
			return err
		}
	}
	if err := checkEvaluatorTargets(l.EvaluatorArgs); err != nil {
		return err
	}
	if l.Console != nil {
		if err := arena.CheckAlone(l.Console, splitNames(l.CrewNames)); err != nil {
			return err
		}
	}
	store, err := trainer.NewStore(l.Store)
	if err != nil {
		return err
	}
	l.store = store
	if l.Now == nil {
		l.Now = time.Now
	}
	if l.Logf == nil {
		l.Logf = func(string, ...any) {}
	}
	return nil
}

// labTargets are the --evaluator-args flags that aim fights at a server or a
// squad. They are re-checks before the first fight is spent, of the same
// rule the bridge applies to its own flags.
var labTargets = map[string]bool{"lab": true, "squad": true, "squad-b": true}

// checkEvaluatorTargets refuses extra evaluator arguments that aim at
// anything but the crew lab. Free-position flags and separate values are both
// accepted, as the trainer reads its own evaluator spec.
func checkEvaluatorTargets(args []string) error {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !labTargets[name] {
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return fmt.Errorf("--evaluator-args: flag %s has no value", a)
			}
			val = args[i+1]
		}
		if err := tacticsctl.LabOnly(val); err != nil {
			return fmt.Errorf("--evaluator-args: %w", err)
		}
	}
	return nil
}

// scenarioBook loads and validates the scenario book for the rotation: every
// family named on the command line must have a scenario, because a missing
// one could only show up when the bridge fails fight one.
func (l *Loop) scenarioBook() (*arena.ScenarioBook, string, error) {
	path, err := resolveBook(l.Scenarios)
	if err != nil {
		return nil, "", err
	}
	waves, err := arena.DefaultWaves()
	if err != nil {
		return nil, "", err
	}
	book, err := arena.LoadScenarioFile(path, waves.Tiers)
	if err != nil {
		return nil, "", err
	}
	for _, f := range l.Families {
		if scenarioOf(book, f) == "" {
			return nil, "", fmt.Errorf("scenario book %s has no scenario for family %q", path, f)
		}
	}
	return book, path, nil
}

// resolveBook points --scenarios at one book: a YAML file, or a directory
// that holds exactly one. More than one book is refused, since the bridge
// takes a single path.
func resolveBook(s string) (string, error) {
	info, err := os.Stat(s)
	if err != nil {
		return "", fmt.Errorf("--scenarios: %w", err)
	}
	if !info.IsDir() {
		return s, nil
	}
	entries, err := os.ReadDir(s)
	if err != nil {
		return "", err
	}
	var book string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}
		if book != "" {
			return "", fmt.Errorf("--scenarios: %s holds more than one scenario book; name the file to use", s)
		}
		book = filepath.Join(s, e.Name())
	}
	if book == "" {
		return "", fmt.Errorf("--scenarios: %s holds no scenario book (*.yaml or *.yml)", s)
	}
	return book, nil
}

// scenarioOf picks the family's scenario: the highest tier the book fields
// for it, so an overnight run trains against the hardest opposition it has.
func scenarioOf(b *arena.ScenarioBook, f tactics.Family) string {
	best, tier := "", -1
	for _, s := range b.Scenarios {
		if s.Family != f || s.Tier <= tier {
			continue
		}
		best, tier = s.Name, s.Tier
	}
	return best
}

// evaluatorCommand assembles the trainbridge argv the cmd evaluator runs for
// every fight. argv is split on whitespace, so none of the values may hold a
// space.
func (l *Loop) evaluatorCommand(book string) string {
	argv := []string{l.Bridge, "--lab", l.Lab, "--compose", l.Compose,
		"--layout", l.Layout, "--scenarios", book, "--warriors", l.Warriors,
		"--squad-url", l.SquadURL, "--squad", l.Squad}
	if l.SquadB != "" {
		argv = append(argv, "--squad-b", l.SquadB)
	}
	if l.WarriorKits != "" {
		argv = append(argv, "--warrior-kits", l.WarriorKits)
	}
	argv = append(argv, "--fight-timeout", l.FightTimeout.String())
	argv = append(argv, l.EvaluatorArgs...)
	return strings.Join(argv, " ")
}

// Run drives the campaign and returns its exit code: 0 finished within caps,
// 3 stopped by switch or stop file, 1 a failure or store error, 2 a
// malformed invocation.
func (l *Loop) Run(ctx context.Context, out io.Writer) int {
	if err := l.validate(); err != nil {
		l.Logf("%v", err)
		return 2
	}
	if err := l.prepare(); err != nil {
		l.Logf("%v", err)
		return 2
	}
	book, bookPath, err := l.scenarioBook()
	if err != nil {
		l.Logf("%v", err)
		return 2
	}
	// The evaluator watches the same switch and stop file as the campaign,
	// so a pulled switch ends a fight in progress and not only the next one.
	cmdEval := trainer.CmdEvaluator{
		Command: l.evaluatorCommand(bookPath),
		Timeout: l.FightTimeout,
		Env:     []string{"TACTICSCTL_DIR=" + l.ControlDir, "TRAINER_STOP_FILE=" + l.StopFile},
	}
	next := trainer.Evaluator(cmdEval)
	if l.EvalOverride != nil {
		next = l.EvalOverride
	}
	l.eval = loggedEval{next: next, log: l.logCards}

	st, had, err := loadState(l.StatePath)
	if err != nil {
		l.Logf("%v", err)
		return 2
	}
	if !had {
		st = State{Version: stateVersion, StartedAt: stamp(l.Now())}
		l.Logf("campaign for %s in %s, scenario book %s",
			strings.Join(names(l.Families), ", "), l.Store, bookPath)
	} else {
		l.Logf("resuming: %d round(s) done, %d fight(s), %s of the hour budget spent",
			len(st.Rounds), st.FightsUsed,
			(time.Duration(st.ConsumedMs) * time.Millisecond).Round(time.Second))
	}

	f, err := os.OpenFile(l.FightsLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.Logf("scorecard log: %v", err)
		return 1
	}
	defer func() {
		if err := f.Close(); err != nil {
			l.Logf("closing the scorecard log: %v", err)
		}
	}()
	l.cards = json.NewEncoder(f)

	code := l.loop(ctx, &st, book, out)
	if err := st.save(l.StatePath); err != nil {
		// A lost state file would make the next start over-spend the caps,
		// which the exit code should not hide.
		l.Logf("saving the state file: %v", err)
		return 1
	}
	l.report(out, "whole campaign over "+strings.Join(names(l.Families), ", "), nil)
	return code
}

// loop runs one family per round. Every cap is checked before the next round
// starts, so a family already in flight cannot overshoot one, and the
// training switch is asked before every family, that is between rounds.
func (l *Loop) loop(ctx context.Context, st *State, book *arena.ScenarioBook, out io.Writer) int {
	for {
		if !tacticsctl.TrainingEnabled(l.ControlDir) {
			l.Logf("stop: the training switch in %s is off or missing; turn it on with `tacticsctl training on`", l.ControlDir)
			return 3
		}
		if _, err := os.Stat(l.StopFile); err == nil {
			l.Logf("stop: the stop file %s exists", l.StopFile)
			return 3
		}
		if left := l.MaxHours - time.Duration(st.ConsumedMs)*time.Millisecond; left <= 0 {
			l.Logf("stop: the hour cap is spent (%s)", l.MaxHours)
			return 0
		}
		if roundsLeft := l.MaxRounds - len(st.Rounds); roundsLeft <= 0 {
			l.Logf("stop: the round cap is used up (%d rounds)", l.MaxRounds)
			return 0
		}
		if fightsLeft := l.MaxTotalFights - st.FightsUsed; fightsLeft <= 0 {
			l.Logf("stop: the fight cap is used up (%d fights)", l.MaxTotalFights)
			return 0
		}
		if err := ctx.Err(); err != nil {
			l.Logf("stop: %v; run the same command again to resume with the remaining budget", err)
			return 3
		}
		family := l.Families[len(st.Rounds)%len(l.Families)]
		if code := l.round(ctx, st, family, book, out); code != keepGoing {
			return code
		}
		// The state only records finished rounds, so it is written where a
		// restart would resume: between them.
		if err := st.save(l.StatePath); err != nil {
			l.Logf("saving the state file: %v", err)
			return 1
		}
		if l.BetweenRounds != nil {
			l.BetweenRounds()
		}
	}
}

// round runs the trainer for one family against the remaining budget and
// updates the campaign state with what was spent.
func (l *Loop) round(ctx context.Context, st *State, family tactics.Family, book *arena.ScenarioBook, out io.Writer) int {
	start := l.Now()
	res, err := trainer.Run(ctx, trainer.Config{
		Family: family, Scenario: scenarioOf(book, family), Kind: tactics.ScenarioMob,
		Store: l.store, Eval: l.eval, TacticOptions: tacticOptions(l.LearnTactics),
		MaxGenerations: l.GensPerRound,
		MaxFights:      l.MaxTotalFights - st.FightsUsed,
		Budget:         l.MaxHours - time.Duration(st.ConsumedMs)*time.Millisecond,
		MaxFailures:    l.MaxConsecutiveFailures,
		KillFile:       l.StopFile,
		ControlDir:     l.ControlDir,
		Seed:           l.Seed + int64(len(st.Rounds)),
		Evals:          l.FightsPerGen,
		RunID:          fmt.Sprintf("loop-%s-r%03d", family, len(st.Rounds)+1),
		Now:            l.Now,
		Logf:           func(format string, a ...any) { l.Logf(string(family)+": "+format, a...) },
	})
	spent := l.Now().Sub(start)
	st.ConsumedMs += spent.Milliseconds()
	if err != nil {
		l.Logf("trainer: %v", err)
		return 1
	}
	st.Rounds = append(st.Rounds, RoundState{
		Family: string(family), RunID: res.RunID, Scenario: scenarioOf(book, family),
		StopReason: res.StopReason, Generations: res.Generations, Fights: res.Fights,
		Promotions: res.Promotions, ElapsedMs: spent.Milliseconds(),
		Champion: res.Champion.Profile.Name,
	})
	st.FightsUsed += res.Fights
	l.Logf("%s %d: gens=%d fights=%d promotions=%d champion=%s stopped: %s after %s",
		family, len(st.Rounds), res.Generations, res.Fights, res.Promotions,
		res.Champion.Profile.Name, res.StopReason, spent.Round(time.Millisecond))

	fam := family
	l.report(out, fmt.Sprintf("family %s, after round %d", family, len(st.Rounds)), &fam)
	switch res.StopReason {
	case trainer.StopGenerations:
		return keepGoing
	case trainer.StopDisabled, trainer.StopKilled:
		return 3
	case trainer.StopFailures:
		l.Logf("%s: %d fights failed in a row; fix the lab or the bridge and start the campaign again", family, res.Failures)
		return 1
	case trainer.StopCancelled:
		l.Logf("interrupted; the state file keeps the budget spent so far")
		return 3
	}
	// StopBudget and StopFights: the trainer was given exactly the campaign's
	// remaining budget, so its stop is the campaign's own and it ends within
	// its caps.
	return 0
}

// tacticOptions wires tactic learning to the engine's registered variants.
func tacticOptions(learn bool) func(tactics.Family) []string {
	if !learn {
		return nil
	}
	return life.TacticNames
}

// report prints the trainreport summary of the fights fought so far. The log
// is read whole every time, so a family summary covers the campaign's history
// with that family, not only the finished round; a torn final line, from an
// evaluator still writing when the campaign stopped, is tolerated the same
// way the trainreport command tolerates one.
func (l *Loop) report(out io.Writer, title string, family *tactics.Family) {
	cards, torn, err := trainreport.Load(l.FightsLog)
	if err != nil {
		fmt.Fprintf(out, "== trainreport (%s): unavailable: %v\n", title, err)
		return
	}
	if family != nil {
		var kept []tactics.Scorecard
		for _, c := range cards {
			if c.Family == *family {
				kept = append(kept, c)
			}
		}
		cards = kept
	}
	rep := trainreport.Build(cards, trainreport.Options{})
	rep.TornLines = torn
	fmt.Fprintf(out, "== trainreport (%s) ==\n", title)
	trainreport.Render(out, rep, nil)
}

// loggedEval records every scorecard of the profile under test while the
// trainer scores fights; they are the input of the trainreport summaries. A
// duel evaluator may also print the opponent's cards; those pass through
// unlogged, since they score nobody the trainer is testing.
type loggedEval struct {
	next trainer.Evaluator
	log  func([]tactics.Scorecard)
}

func (e loggedEval) Evaluate(ctx context.Context, p tactics.Profile, s trainer.Scenario) ([]tactics.Scorecard, error) {
	cards, err := e.next.Evaluate(ctx, p, s)
	if err == nil {
		e.log(ownCards(cards, p.Name))
	}
	return cards, err
}

// ownCards keeps the cards naming the profile under test, the same set the
// trainer scores; it mirrors the trainer's own filter, which is not exported.
func ownCards(cards []tactics.Scorecard, profile string) []tactics.Scorecard {
	var kept []tactics.Scorecard
	for _, c := range cards {
		if c.Profile == profile {
			kept = append(kept, c)
		}
	}
	return kept
}

// logCards appends a fight's scorecards to the campaign log as JSON lines.
func (l *Loop) logCards(cards []tactics.Scorecard) {
	if len(cards) == 0 {
		return
	}
	for _, c := range cards {
		if err := l.cards.Encode(c); err != nil && !l.logWarn {
			l.logWarn = true
			l.Logf("scorecard log: %v; report summaries will be partial", err)
		}
	}
}

// names turns the rotation back into the comma list the logs read faster by.
func names(fs []tactics.Family) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f))
	}
	return out
}
