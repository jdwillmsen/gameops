package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainreport"
)

// bookYAML is a small scenario book: one scenario per family the tests train,
// shaped like the shipped one. Tier recipes still come from the wave book.
const bookYAML = `scenarios:
  - name: test-zombie-line
    family: zombie
    tier: 1
    timeout: 90s
    pass: clear
    spawn: {ring: 8, side: north}
    mobs:
      - {mob: zombie, count: 2}
  - name: test-skeleton-line
    family: skeleton
    tier: 1
    timeout: 90s
    pass: clear
    spawn: {ring: 8, side: east}
    mobs:
      - {mob: skeleton, count: 2}
`

// campaignClock is the injectable clock. Every fake fight advances it one
// step, so the hour cap tests pass in milliseconds of wall clock.
type campaignClock struct {
	mu   sync.Mutex
	t    time.Time
	step time.Duration
}

func (c *campaignClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *campaignClock) advance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(c.step)
}

// tickingFake wraps the trainer's fake evaluator with one tick of the
// campaign clock per fought fight.
type tickingFake struct {
	fake *trainer.Fake
	c    *campaignClock
}

func (e tickingFake) Evaluate(ctx context.Context, p tactics.Profile, s trainer.Scenario) ([]tactics.Scorecard, error) {
	cards, err := e.fake.Evaluate(ctx, p, s)
	e.c.advance()
	return cards, err
}

// failEval makes every fight fail, to drive the failure cap.
type failEval struct{}

func (failEval) Evaluate(context.Context, tactics.Profile, trainer.Scenario) ([]tactics.Scorecard, error) {
	return nil, fmt.Errorf("failEval: every fight fails")
}

// newCampaign builds a loop for one test with a small scenario book, a live
// control directory, and a clock whose step the test controls.
// newCampaign's log sink collects the loop's Logf lines, which is where the
// stop reasons and refusals are reported.
type campaignLog = bytes.Buffer

func newCampaign(t *testing.T, step time.Duration) (*Loop, *campaignClock, string, *campaignLog) {
	t.Helper()
	root := t.TempDir()
	bookPath := filepath.Join(root, "scenarios.yaml")
	if err := os.WriteFile(bookPath, []byte(bookYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &campaignClock{t: time.Date(2026, 10, 29, 22, 0, 0, 0, time.UTC), step: step}
	l := &Loop{
		Families:   []tactics.Family{"zombie", "skeleton"},
		Scenarios:  bookPath,
		Store:      filepath.Join(root, "store"),
		ControlDir: switchOn(t),
		MaxHours:   24 * time.Hour, MaxRounds: 50, GensPerRound: 1,
		FightsPerGen: 2, MaxTotalFights: 4000, MaxConsecutiveFailures: 4,
		LearnTactics: false, Seed: 1,
		Bridge: "trainbridge", Lab: "crew-lab",
		Compose: "minecraft/crew/lab/compose.yaml", Layout: "layout.json",
		SquadURL: "http://crew-control", Squad: "lab-alpha",
		Warriors: "Rusty,Slate,Tinder", CrewNames: strings.Join(arena.DefaultCrewNames, ","),
		FightTimeout: 10 * time.Minute,
	}
	logs := &campaignLog{}
	l.Now, l.Logf = c.Now, func(format string, a ...any) {
		fmt.Fprintf(logs, format+"\n", a...)
	}
	return l, c, filepath.Join(root, "store"), logs
}

// switchOn makes a control directory with the training switch on.
func switchOn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runCampaign drives the loop with an evaluator, returning the exit code and
// the summary text it printed.
func runCampaign(t *testing.T, l *Loop, eval trainer.Evaluator) (int, string) {
	t.Helper()
	var out bytes.Buffer
	l.EvalOverride = eval
	code := l.Run(context.Background(), &out)
	return code, out.String()
}

// freshFake is a fake evaluator bound to the campaign clock.
func freshFake(c *campaignClock) tickingFake {
	return tickingFake{fake: trainer.NewFake(tactics.FamilyZombie), c: c}
}

// readState loads the campaign state file for assertions.
func readState(t *testing.T, path string) State {
	t.Helper()
	st, had, err := loadState(path)
	if err != nil || !had {
		t.Fatalf("loadState(%s): had=%v err=%v", path, had, err)
	}
	return st
}

// TestRoundCapStopsTheRotation pins the per-round fight shape: one round of
// one generation is champion plus six children with two mirrored fights
// each, so two rounds spend 28 fights exactly and stop on the round cap.
func TestRoundCapStopsTheRotation(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 2
	code, out := runCampaign(t, l, freshFake(c))
	if code != 0 {
		t.Fatalf("exit %d, out:\n%s", code, out)
	}
	st := readState(t, l.StatePath)
	if len(st.Rounds) != 2 || st.FightsUsed != 28 {
		t.Fatalf("rounds %+v, fights %d; want 2 rounds and 28 fights", st.Rounds, st.FightsUsed)
	}
	// Rotation: the round after the last family restarts at the first.
	for i, want := range []string{"zombie", "skeleton"} {
		if st.Rounds[i].Family != want {
			t.Errorf("round %d family %s, want %s", i, st.Rounds[i].Family, want)
		}
	}
	// The trainer files its champion in the store; the campaign touches no
	// other store.
	if _, err := os.Stat(filepath.Join(l.Store, "zombie", "champion.json")); err != nil {
		t.Errorf("no champion filed: %v", err)
	}
	if !strings.Contains(out, "== trainreport (family zombie, after round 1)") {
		t.Errorf("no per-family summary:\n%s", out)
	}
	if !strings.Contains(out, "== trainreport (whole campaign") {
		t.Errorf("no final summary:\n%s", out)
	}
}

// TestHourCapStopsAtTheLimit consumes exactly the hour budget in one round,
// and a resume with none left runs no family again: the state file keeps the
// budget spent, so the caps are never reset.
func TestHourCapStopsAtTheLimitAndResumeKeepsBudget(t *testing.T) {
	l, c, _, logSink := newCampaign(t, 30*time.Second)
	l.MaxHours = 3 * time.Minute
	l.MaxRounds = 10
	if code, out := runCampaign(t, l, freshFake(c)); code != 0 {
		t.Fatalf("exit %d out:\n%s", code, out)
	}
	st := readState(t, l.StatePath)
	if n := len(st.Rounds); n != 1 || st.FightsUsed != 6 || st.ConsumedMs != 180_000 {
		t.Fatalf("one round of 6 fights and 3m spent; got %+v fights %d ms %d",
			st.Rounds, st.FightsUsed, st.ConsumedMs)
	}

	var out bytes.Buffer
	l.EvalOverride = freshFake(c)
	if code := l.Run(context.Background(), &out); code != 0 {
		t.Fatalf("resume exit %d out:\n%s", code, out.String())
	}
	after := readState(t, l.StatePath)
	if n := len(after.Rounds); n != 1 {
		t.Fatalf("resume ran %d extra rounds; out:\n%s", n-1, out.String())
	}
	if !strings.Contains(logSink.String(), "the hour cap is spent") {
		t.Errorf("resume does not name the spent cap:\n%s", out.String())
	}
}

// TestFightCapSpendsExactlyTheCap shows the trainer sharding its generations
// so the last one lands on the campaign's cap: 14 + 14 + 12 = 40 fights.
func TestFightCapSpendsExactlyTheCap(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxTotalFights = 40
	l.GensPerRound = 100
	code, out := runCampaign(t, l, freshFake(c))
	if code != 0 {
		t.Fatalf("exit %d out:\n%s", code, out)
	}
	st := readState(t, l.StatePath)
	if st.FightsUsed != 40 {
		t.Fatalf("fights %d, want exactly the 40 cap: %+v\n%s", st.FightsUsed, st.Rounds, out)
	}
	if last := st.Rounds[len(st.Rounds)-1]; last.StopReason != trainer.StopFights {
		t.Errorf("round stop reason %s, want %s", last.StopReason, trainer.StopFights)
	}
}

// TestConsecutiveFailureCapStopsTheCampaign stops the whole campaign with
// exit 1 once the evaluator fails in a row more than the cap allows.
func TestConsecutiveFailureCapStopsTheCampaign(t *testing.T) {
	l, _, _, _ := newCampaign(t, 0)
	l.MaxConsecutiveFailures = 2
	code, out := runCampaign(t, l, failEval{})
	if code != 1 {
		t.Fatalf("exit %d out:\n%s", code, out)
	}
	st := readState(t, l.StatePath)
	if len(st.Rounds) != 1 || st.Rounds[0].StopReason != trainer.StopFailures {
		t.Fatalf("rounds %+v", st.Rounds)
	}
	if st.Rounds[0].Fights != 2 {
		t.Errorf("the failure cap is 2 failed fights, the round has %d", st.Rounds[0].Fights)
	}
}

// TestTrainingSwitchOffStopsBetweenRounds stops with exit 3 and no further
// round when the switch is pulled between families.
func TestTrainingSwitchOffStopsBetweenRounds(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 3
	l.BetweenRounds = func() {
		if err := os.WriteFile(filepath.Join(l.ControlDir, tacticsctl.TrainingFile), []byte("off\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
	code, out := runCampaign(t, l, freshFake(c))
	if code != 3 {
		t.Fatalf("exit %d out:\n%s", code, out)
	}
	if st := readState(t, l.StatePath); len(st.Rounds) != 1 {
		t.Fatalf("rounds %+v, want the campaign to stop after round 1", st.Rounds)
	}
}

// TestStopFileStopsBetweenRounds is the same stop for the explicit stop file.
func TestStopFileStopsBetweenRounds(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 3
	l.BetweenRounds = func() {
		if err := os.WriteFile(l.StopFile, nil, 0o644); err != nil {
			t.Error(err)
		}
	}
	code, _ := runCampaign(t, l, freshFake(c))
	if code != 3 {
		t.Fatalf("exit %d, want 3 (stopped by stop file)", code)
	}
}

// TestMissingSwitchNeverStarts: a control directory without a training file
// stops before family one, because a wiped volume may never start a night.
func TestMissingSwitchNeverStarts(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	if err := os.Remove(filepath.Join(l.ControlDir, tacticsctl.TrainingFile)); err != nil {
		t.Fatal(err)
	}
	code, _ := runCampaign(t, l, freshFake(c))
	if code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if st, had, err := loadState(l.StatePath); err != nil || !had || len(st.Rounds) != 0 {
		t.Fatalf("state %+v had %v err %v; no round may have run", st, had, err)
	}
}

// TestResumeContinuesTheRotation: a one-round campaign resumes into the next
// family of the rotation, the spent round kept.
func TestResumeContinuesTheRotation(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 1
	runCampaign(t, l, freshFake(c))
	st := readState(t, l.StatePath)
	if st.Rounds[0].Family != "zombie" {
		t.Fatalf("first round %+v", st.Rounds)
	}

	var out bytes.Buffer
	l.EvalOverride = freshFake(c)
	l.MaxRounds = 2
	if code := l.Run(context.Background(), &out); code != 0 {
		t.Fatalf("resume exit %d out:\n%s", code, out.String())
	}
	after := readState(t, l.StatePath)
	if n := len(after.Rounds); n != 2 || after.Rounds[1].Family != "skeleton" {
		t.Fatalf("resumed rounds %+v out:\n%s", after.Rounds, out.String())
	}
	if after.Rounds[0].RunID != st.Rounds[0].RunID {
		t.Errorf("resume re-recorded the first round: %s vs %s",
			after.Rounds[0].RunID, st.Rounds[0].RunID)
	}
}

// TestTrainreportSummariesLogScorecards: the fight log holds one scorecard
// per fought fight, and the summaries are built from it.
func TestTrainreportSummariesLogScorecards(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 1
	code, out := runCampaign(t, l, freshFake(c))
	if code != 0 {
		t.Fatalf("exit %d out:\n%s", code, out)
	}
	cards, torn, err := trainreport.Load(l.FightsLog)
	if err != nil {
		t.Fatalf("fight log: %v", err)
	}
	if torn != 0 {
		t.Errorf("torn lines %d", torn)
	}
	if got := len(cards); got != 14 {
		t.Fatalf("log holds %d cards, want the 14 fights of the round", got)
	}
	if rep := trainreport.Build(cards, trainreport.Options{}); len(rep.Rows) == 0 {
		t.Error("report has no rows")
	}
	if !strings.Contains(out, "FAMILY") {
		t.Errorf("summary block lacks the table:\n%s", out)
	}
}

// TestUsageErrors pins every malformed invocation at exit 2 before the lab is
// touched. The caps have no defaults of unlimited, so a missing or zero cap
// is a usage error.
func TestUsageErrors(t *testing.T) {
	store := t.TempDir()
	for _, args := range [][]string{
		{},
		{"dance"},
		{"overnight"},
		{"overnight", "extra"},
		{"overnight", "--max-hours", "0s", "--max-rounds", "2", "--gens-per-round", "2",
			"--fights-per-gen", "2", "--max-total-fights", "100", "--max-consecutive-failures", "3",
			"--families", "zombie", "--scenarios", "book.yaml", "--store", store},
		{"overnight", "--max-rounds", "0"},
		{"overnight", "--gens-per-round", "0"},
		{"overnight", "--fights-per-gen", "1"},
		{"overnight", "--max-total-fights", "0"},
		{"overnight", "--max-consecutive-failures", "0"},
		{"overnight", "--max-hours", "30h"},
		{"overnight", "--max-hours", "0s"},
		{"overnight", "--families", "Bad/Family"},
		{"overnight", "--families", "zombie,zombie"},
	} {
		if _, code := fakeMain(context.Background(), args); code != 2 {
			t.Errorf("%s: exit %d, want 2", strings.Join(args, " "), code)
		}
	}
}

// TestLabRefusals: a target that is not the crew lab never reaches the
// console, the campaign flags or the evaluator arguments.
func TestLabRefusals(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	var out bytes.Buffer
	l.EvalOverride = freshFake(c)
	l.Lab = "minecraft-fwb"
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Errorf("lab %q: exit %d, want 2", l.Lab, code)
	}
	l.Lab = "crew-lab"
	l.Squad = "alpha"
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Errorf("squad %q: exit %d, want 2", l.Squad, code)
	}
	l.Squad = "lab-alpha"
	l.EvaluatorArgs = []string{"--status-url", "http://crew-control/units/{name}/v1/state", "--lab", "prd"}
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Errorf("evaluator arg --lab prd: exit %d, want 2", code)
	}
}

// TestNonCrewPlayerRefusesTheStart exercises the crew check through the same
// console helpers arenadirector and trainbridge use.
func TestNonCrewPlayerRefusesTheStart(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	var out bytes.Buffer
	l.EvalOverride = freshFake(c)
	l.Console = stubConsole{"There are 2/20 players online: Rusty, Steve99"}
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if st, had, _ := loadState(l.StatePath); had && len(st.Rounds) > 0 {
		t.Fatalf("rounds %+v; nothing may run while a stranger is online", st.Rounds)
	}
	l.Console = stubConsole{"There are 1/20 players online: Rusty"}
	if code := l.Run(context.Background(), &out); code != 0 {
		t.Fatalf("crew-only console: exit %d out:\n%s", code, out.String())
	}
}

// stubConsole answers every command with one canned line.
type stubConsole struct{ out string }

func (s stubConsole) Exec(string) (string, error) { return s.out, nil }

// TestBrokenStateIsRefused: a torn state file is refused, so a resume never
// resets the caps with a wrong notion of the spent budget.
func TestBrokenStateIsRefused(t *testing.T) {
	l, c, _, _ := newCampaign(t, 0)
	l.MaxRounds = 1
	runCampaign(t, l, freshFake(c))
	st := readState(t, l.StatePath)
	if st.Version != stateVersion || st.StartedAt == "" {
		t.Fatalf("state %+v", st)
	}
	if err := os.WriteFile(l.StatePath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Fatalf("broken state: exit %d, want 2: %s", code, out.String())
	}
}

// TestScenarioBookNeedsOneBookPerFamily: a book without the family's
// scenario is a start-time refusal, not a fight-one failure.
func TestScenarioBookNeedsOneBookPerFamily(t *testing.T) {
	l, _, _, logSink := newCampaign(t, 0)
	l.Families = []tactics.Family{"zombie", "creeper"}
	var out bytes.Buffer
	if code := l.Run(context.Background(), &out); code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, out.String())
	}
	if !strings.Contains(logSink.String(), "no scenario for family") {
		t.Errorf("unhelpful refusal:\n%s", out.String())
	}
}

// TestBookDirectoryResolvesOneBook: a directory scenario source works when it
// holds exactly one book, and is refused when it holds two.
func TestBookDirectoryResolvesOneBook(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "book.yaml")
	if err := os.WriteFile(one, []byte(bookYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := resolveBook(dir); err != nil || p != one {
		t.Fatalf("resolveBook(dir) = %q %v, want %q", p, err, one)
	}
	two := filepath.Join(dir, "other.yaml")
	if err := os.WriteFile(two, []byte(bookYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveBook(dir); err == nil {
		t.Fatal("two books were resolved, want a refusal")
	}
}

// fakeMain is realMain with a stubbed console, so a CLI invocation never
// reaches a docker or kubectl lab; the usage tests drive it.
func fakeMain(ctx context.Context, args []string) (string, int) {
	saved := newConsole
	newConsole = func(lab, compose string) (arena.Console, error) {
		return stubConsole{"There are 0/20 players online:"}, nil
	}
	defer func() { newConsole = saved }()
	var stdout, stderr bytes.Buffer
	code := realMain(ctx, nil2args(args), &stdout, &stderr)
	return stdout.String(), code
}

// nil2args keeps a nil arg slice honest for the no-command cases.
func nil2args(args []string) []string {
	if args == nil {
		return []string{}
	}
	return args
}
