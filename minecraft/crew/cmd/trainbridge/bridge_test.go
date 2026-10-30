package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/statusapi"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
)

type clock struct{ t time.Time }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time        { return c.t }
func (c *clock) Sleep(d time.Duration) { c.t = c.t.Add(d) }

// fakeSink records the profiles it was handed and what it released.
type fakeSink struct {
	mu      sync.Mutex
	pushed  map[string]string
	cleared [][]string
	// clearCtxErr is the context error Clear saw, to show a withdrawal does
	// not run on the fight's finished context.
	clearCtxErr error
	err         error
	// lostPut makes Push land and then fail, the way a lost PUT response does.
	lostPut bool
	squads  []string
	// confirmErr fails every Confirm; confirmed records each call's profile name.
	confirmErr error
	confirmed  []string
}

func (f *fakeSink) LabSquads([]string) ([]string, error) { return f.squads, nil }

func (f *fakeSink) Confirm(_ context.Context, _ []string, p tactics.Profile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmed = append(f.confirmed, p.Name)
	return f.confirmErr
}

func (f *fakeSink) Push(_ context.Context, units []string, p tactics.Profile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil && !f.lostPut {
		return f.err
	}
	if f.pushed == nil {
		f.pushed = map[string]string{}
	}
	for _, u := range units {
		f.pushed[u] = p.Name
	}
	return f.err
}

func (f *fakeSink) Clear(ctx context.Context, units []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCtxErr = ctx.Err()
	f.cleared = append(f.cleared, units)
	return nil
}

func testLayout() arena.Layout {
	return arena.Layout{
		Center: arena.Point{X: 0, Y: 65, Z: -760}, GroundY: 64, FloorRadius: 30,
		Areas: []arena.Area{{X0: -48, Z0: -800, X1: 47, Z1: -700}},
	}
}

// sim answers the lab console. Mobs appear when the first summon arrives and
// are all gone mobsGoneAfter later; a negative value keeps them forever.
// Duelists named in dieAfter respawn at the world spawn that long after the go
// tag, or read as missing from the world when gone is set.
type sim struct {
	clk           *clock
	mobs          int
	mobsGoneAfter time.Duration
	spawnedAt     time.Time
	spawned       bool
	wipeAfter     time.Duration
	dieAfter      map[string]time.Duration
	gone          bool
	goAt          time.Time
	armed         bool
	cmds          []string
}

func (s *sim) mobsLive() int {
	if !s.spawned || (s.mobsGoneAfter >= 0 && s.clk.Now().Sub(s.spawnedAt) >= s.mobsGoneAfter) {
		return 0
	}
	return s.mobs
}

func (s *sim) wiped() bool {
	return s.wipeAfter > 0 && s.spawned && s.clk.Now().Sub(s.spawnedAt) >= s.wipeAfter
}

func (s *sim) down(name string) bool {
	after, ok := s.dieAfter[name]
	return ok && s.armed && s.clk.Now().Sub(s.goAt) >= after
}

func (s *sim) Exec(cmd string) (string, error) {
	s.cmds = append(s.cmds, cmd)
	switch {
	case cmd == "list":
		return "There are 4/10 players online:\nRusty, Slate, Tinder, Agate", nil
	case strings.Contains(cmd, " run summon ") && !s.spawned:
		s.spawned, s.spawnedAt = true, s.clk.Now()
	case strings.HasPrefix(cmd, "tag ") && strings.Contains(cmd, " add duel_go"):
		s.armed, s.goAt = true, s.clk.Now()
	case strings.HasPrefix(cmd, "tag ") && strings.Contains(cmd, " remove duel_go"):
		s.armed = false
	case strings.HasPrefix(cmd, "testfor @e[tag=arena_wave"):
		return found(s.mobsLive(), "Zombie"), nil
	case strings.HasPrefix(cmd, "testfor @e[name="):
		name := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(cmd, "testfor @e[name="), ",type=player]"), `"`)
		if s.wiped() || (s.down(name) && s.gone) {
			return "ERROR: No targets matched selector", nil
		}
		return "Found " + name, nil
	case strings.HasPrefix(cmd, "querytarget @a[name="):
		name := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(cmd, "querytarget @a[name="), "]"), `"`)
		if s.down(name) {
			return `[{"position":{"x":0,"y":64,"z":-400}}]`, nil
		}
		return `[{"position":{"x":0,"y":65,"z":-760}}]`, nil
	case strings.HasPrefix(cmd, "querytarget"):
		return `[{"position":{"x":0,"y":65,"z":-760}}]`, nil
	}
	return "", nil
}

func found(n int, name string) string {
	if n <= 0 {
		return "ERROR: No targets matched selector"
	}
	names := make([]string, n)
	for i := range names {
		names[i] = name
	}
	return "Found " + strings.Join(names, ", ")
}

type rig struct {
	b    *bridge
	sim  *sim
	sink *fakeSink
	clk  *clock
	out  *bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctl := t.TempDir()
	if err := os.WriteFile(filepath.Join(ctl, tacticsctl.TrainingFile), []byte("on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	book, err := arena.DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	s := &sim{clk: clk, mobs: 4, mobsGoneAfter: 20 * time.Second}
	sink := &fakeSink{}
	out := &bytes.Buffer{}
	return &rig{
		sim: s, sink: sink, clk: clk, out: out,
		b: &bridge{
			ControlDir: ctl, Lab: "crew-lab",
			Console: &arena.FakeConsole{Respond: s.Exec}, Layout: testLayout(), Book: book, Sink: sink,
			Warriors: []string{"Rusty", "Slate", "Tinder"}, Crew: arena.DefaultCrewNames,
			TeamA: []string{"Rusty", "Slate"}, TeamB: []string{"Tinder", "Agate"},
			Duelists:     []string{"Rusty", "Slate", "Tinder", "Agate"},
			FightTimeout: time.Hour, RoundTimeout: time.Minute, Poll: 5 * time.Second,
			Settle: 25 * time.Second, ApplyWait: 6 * time.Second, PlaceWait: 3 * time.Second, Countdown: 5 * time.Second,
			KitGap: 3 * time.Second, Spacing: 12, Emit: "all",
			Out: out, Now: clk.Now, Sleep: clk.Sleep,
		},
	}
}

func candidate(name string) tactics.Profile {
	p := tactics.NewProfile(name)
	p.Default.KiteBackoff = 7
	return p
}

func mobRequest(side string) request {
	return request{
		Profile:  candidate("cand-1"),
		Scenario: trainer.Scenario{Name: "four-zombies", Kind: tactics.ScenarioMob, Family: tactics.FamilyZombie, Seed: 42, Side: side},
	}
}

func duelRequest(side string) request {
	return request{
		Profile:  candidate("cand-1"),
		Scenario: trainer.Scenario{Name: "duel", Kind: tactics.ScenarioDuel, Family: tactics.FamilyPlayer, Seed: 7, Side: side},
	}
}

func TestMobScenarioScores(t *testing.T) {
	r := newRig(t)
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, r.out)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.Outcome != tactics.OutcomeWin || c.Kills != 4 || c.TotalDeaths() != 0 {
		t.Errorf("card = %+v, want a win with 4 kills and no deaths", c)
	}
	if c.Profile != "cand-1" || c.Scenario != "four-zombies" || c.Family != tactics.FamilyZombie || c.Side != "a" || c.Seed != 42 {
		t.Errorf("card identity = %+v", c)
	}
	if c.Seconds <= 0 || c.Score() <= 0 {
		t.Errorf("seconds %v score %v, want a positive score", c.Seconds, c.Score())
	}
	for _, w := range r.b.Warriors {
		if r.sink.pushed[w] != "cand-1" {
			t.Errorf("%s got profile %q, want cand-1", w, r.sink.pushed[w])
		}
	}
	if len(r.sink.cleared) != 1 || !slices.Equal(r.sink.cleared[0], r.b.Warriors) {
		t.Errorf("cleared = %v, want one release of the warriors", r.sink.cleared)
	}
	if n := (&arena.FakeConsole{Cmds: r.sim.cmds}).Count("kill @e[tag=arena_wave]"); n == 0 {
		t.Error("the scenario's mobs were not killed on the way out")
	}
}

// growing reports 5 more damage per warrior at every reading.
type growing struct{ reads int }

func (g *growing) Taken(_ context.Context, units []string) (map[string]float64, error) {
	g.reads++
	out := map[string]float64{}
	for _, u := range units {
		out[u] = float64(5 * g.reads)
	}
	return out, nil
}

func TestMobScorecardCarriesTheSquadsDamageTaken(t *testing.T) {
	r := newRig(t)
	r.b.Damage = &growing{}
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, r.out)
	}
	if want := 5.0 * float64(len(r.b.Warriors)); cards[0].DamageTaken != want {
		t.Errorf("DamageTaken = %v, want %v", cards[0].DamageTaken, want)
	}

	plain := newRig(t)
	cards, _ = plain.b.Run(context.Background(), mobRequest("a"))
	if cards[0].DamageTaken != 0 {
		t.Errorf("DamageTaken = %v without a source, want 0", cards[0].DamageTaken)
	}
}

func TestMobSideBMeetsTheMobsFromTheFarSide(t *testing.T) {
	spawnX := func(side string) string {
		r := newRig(t)
		if _, err := r.b.Run(context.Background(), mobRequest(side)); err != nil {
			t.Fatal(err)
		}
		for _, c := range r.sim.cmds {
			if strings.Contains(c, " run summon ") {
				return c
			}
		}
		t.Fatal("no summon")
		return ""
	}
	if a, b := spawnX("a"), spawnX("b"); a == b {
		t.Errorf("side b summoned from the same place as side a: %s", a)
	}
}

func TestMobWipeIsALoss(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.sim.wipeAfter = 15 * time.Second
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cards[0].Outcome != tactics.OutcomeLoss || cards[0].TotalDeaths() != 3 {
		t.Errorf("card = %+v, want a loss with 3 deaths", cards[0])
	}
}

func TestScenarioTimeoutIsATimeoutCard(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cards[0].Outcome != tactics.OutcomeTimeout {
		t.Errorf("outcome = %q, want the scenario's own timeout", cards[0].Outcome)
	}
}

func TestDuelEmitsTwoCardsCandidateFirst(t *testing.T) {
	r := newRig(t)
	r.sim.dieAfter = map[string]time.Duration{"Tinder": 20 * time.Second, "Agate": 30 * time.Second}
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, r.out)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	cand, opp := cards[0], cards[1]
	if cand.Profile != "cand-1" || opp.Profile != "default" {
		t.Errorf("profiles = %q, %q; want candidate first and the shipped opponent", cand.Profile, opp.Profile)
	}
	if cand.Outcome != tactics.OutcomeWin || opp.Outcome != tactics.OutcomeLoss {
		t.Errorf("outcomes = %q, %q; want win, loss", cand.Outcome, opp.Outcome)
	}
	if cand.Side != "red" || cand.Family != tactics.FamilyPlayer || cand.Seed != 7 || opp.Seed != 7 {
		t.Errorf("candidate card = %+v", cand)
	}
	if r.sink.pushed["Rusty"] != "cand-1" || r.sink.pushed["Agate"] != "default" {
		t.Errorf("pushed = %v, want team A on the candidate and team B on the opponent", r.sink.pushed)
	}
	if len(r.sink.cleared) != 1 || len(r.sink.cleared[0]) != 4 {
		t.Errorf("cleared = %v, want one release of all four duelists", r.sink.cleared)
	}
}

func TestDuelSideBStartsTheCandidateOnBlue(t *testing.T) {
	r := newRig(t)
	r.sim.dieAfter = map[string]time.Duration{"Rusty": 20 * time.Second, "Slate": 25 * time.Second}
	cards, err := r.b.Run(context.Background(), duelRequest("b"))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, r.out)
	}
	if cards[0].Profile != "cand-1" || cards[0].Side != "blue" || cards[0].Outcome != tactics.OutcomeLoss {
		t.Errorf("candidate card = %+v, want a loss on blue", cards[0])
	}
}

func TestDuelEmitCandidateOnly(t *testing.T) {
	r := newRig(t)
	r.b.Emit = "candidate"
	r.sim.dieAfter = map[string]time.Duration{"Tinder": 20 * time.Second, "Agate": 30 * time.Second}
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Profile != "cand-1" {
		t.Errorf("cards = %+v, want the candidate's alone", cards)
	}
}

func TestDuelOpponentNamedLikeTheCandidateStaysDistinct(t *testing.T) {
	r := newRig(t)
	r.b.Opponent = candidate("cand-1")
	r.sim.dieAfter = map[string]time.Duration{"Tinder": 20 * time.Second, "Agate": 30 * time.Second}
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if err != nil {
		t.Fatal(err)
	}
	if cards[0].Profile == cards[1].Profile {
		t.Errorf("both cards carry profile %q", cards[0].Profile)
	}
}

func TestDisabledTrainingRefuses(t *testing.T) {
	for _, state := range []string{"off\n", ""} {
		r := newRig(t)
		path := filepath.Join(r.b.ControlDir, tacticsctl.TrainingFile)
		if state == "" {
			os.Remove(path)
		} else if err := os.WriteFile(path, []byte(state), 0o644); err != nil {
			t.Fatal(err)
		}
		cards, err := r.b.Run(context.Background(), mobRequest("a"))
		var ref refusal
		if !errors.As(err, &ref) || len(cards) != 0 {
			t.Fatalf("state %q: cards %v err %v, want a refusal and no cards", state, cards, err)
		}
		if len(r.sim.cmds) != 0 || len(r.sink.pushed) != 0 {
			t.Errorf("state %q: refused run still touched the lab: %v %v", state, r.sim.cmds, r.sink.pushed)
		}
	}
}

func TestNonLabTargetRefuses(t *testing.T) {
	for _, lab := range []string{"fwb-minecraft", "crew-prd", "bedrock", "fwb-lab", "k8s:minecraft/fwb-0"} {
		r := newRig(t)
		r.b.Lab = lab
		cards, err := r.b.Run(context.Background(), mobRequest("a"))
		var ref refusal
		if !errors.As(err, &ref) || len(cards) != 0 {
			t.Errorf("lab %q: cards %v err %v, want a refusal and no cards", lab, cards, err)
		}
		if len(r.sim.cmds) != 0 || len(r.sink.pushed) != 0 {
			t.Errorf("lab %q: refused run still touched the target", lab)
		}
	}
}

func TestUnsupportedKindAndUnknownScenarioRefuse(t *testing.T) {
	r := newRig(t)
	req := mobRequest("a")
	req.Scenario.Kind = tactics.ScenarioBoss
	if _, err := r.b.Run(context.Background(), req); !errors.As(err, new(refusal)) {
		t.Errorf("boss kind: err = %v, want a refusal", err)
	}
	req = mobRequest("a")
	req.Scenario.Name = "no-such-scenario"
	if _, err := r.b.Run(context.Background(), req); !errors.As(err, new(refusal)) {
		t.Errorf("unknown scenario: err = %v, want a refusal", err)
	}
	for _, c := range r.sim.cmds {
		if c != "list" {
			t.Errorf("a refused request sent %q to the console", c)
		}
	}
	if len(r.sink.pushed) != 0 {
		t.Errorf("a refused request pushed a profile: %v", r.sink.pushed)
	}
}

func TestPerFightTimeoutProducesAbortedCard(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.b.FightTimeout = 40 * time.Millisecond
	// Real time passes in the loop so the hard wall can fire; the scenario's
	// own 90 s timeout never does.
	r.b.Now = time.Now
	r.b.Sleep = func(time.Duration) { time.Sleep(2 * time.Millisecond) }
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v, a timed-out fight is not a fault", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Fatalf("cards = %+v, want one aborted card", cards)
	}
	if cards[0].Score() != 0 {
		t.Errorf("aborted score = %v, want 0", cards[0].Score())
	}
	if len(r.sink.cleared) != 1 {
		t.Errorf("the profile was not withdrawn after the timeout: %v", r.sink.cleared)
	}
	if n := (&arena.FakeConsole{Cmds: r.sim.cmds}).Count("kill @e[tag=arena_wave]"); n < 2 {
		t.Errorf("mobs killed %d times, want a clean-up after the timeout", n)
	}
}

func TestDuelTimeoutProducesAbortedCards(t *testing.T) {
	r := newRig(t)
	r.b.FightTimeout = 60 * time.Millisecond
	r.b.Now = time.Now
	r.b.Sleep = func(time.Duration) { time.Sleep(3 * time.Millisecond) }
	r.b.Settle, r.b.Countdown = 0, 0
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cards) == 0 {
		t.Fatal("no cards")
	}
	for _, c := range cards {
		if c.Outcome != tactics.OutcomeAborted {
			t.Errorf("card = %+v, want aborted", c)
		}
	}
}

func TestStopFileEndsTheFightAborted(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.b.StopFile = filepath.Join(t.TempDir(), "STOP")
	r.b.Sleep = func(d time.Duration) {
		r.clk.Sleep(d)
		if r.sim.spawned {
			os.WriteFile(r.b.StopFile, nil, 0o644)
		}
	}
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Errorf("cards = %+v, want one aborted card", cards)
	}
}

func TestKillSwitchFlippedMidFightAborts(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.b.Sleep = func(d time.Duration) {
		r.clk.Sleep(d)
		if r.sim.spawned {
			os.WriteFile(filepath.Join(r.b.ControlDir, tacticsctl.TrainingFile), []byte("off"), 0o644)
		}
	}
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Errorf("cards = %+v, want one aborted card", cards)
	}
}

func TestNonCrewPlayerRefusesAndStillAbortsCleanly(t *testing.T) {
	r := newRig(t)
	r.b.Console = &arena.FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == "list" {
			return "There are 4/10 players online:\nRusty, Slate, Tinder, Steve99", nil
		}
		return r.sim.Exec(cmd)
	}}
	_, err := r.b.Run(context.Background(), mobRequest("a"))
	if !errors.As(err, new(refusal)) {
		t.Errorf("err = %v, want a refusal", err)
	}
	if len(r.sink.pushed) != 0 {
		t.Errorf("profile pushed to units while a stranger was online: %v", r.sink.pushed)
	}
}

func TestProfileOutOfVersionRefuses(t *testing.T) {
	r := newRig(t)
	req := mobRequest("a")
	req.Profile.Version = 99
	if _, err := r.b.Run(context.Background(), req); !errors.As(err, new(refusal)) {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestProfilePushedIsClamped(t *testing.T) {
	r := newRig(t)
	req := mobRequest("a")
	req.Profile.Default.KiteBackoff = 1000
	var got tactics.Profile
	r.b.Sink = pushRecorder{fakeSink: r.sink, got: &got}
	if _, err := r.b.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if max := tactics.Bounds()["kiteBackoff"].Max; got.Default.KiteBackoff != max {
		t.Errorf("pushed kiteBackoff = %v, want it clamped to %v", got.Default.KiteBackoff, max)
	}
}

type pushRecorder struct {
	*fakeSink
	got *tactics.Profile
}

func (p pushRecorder) Push(ctx context.Context, units []string, pr tactics.Profile) error {
	*p.got = pr
	return p.fakeSink.Push(ctx, units, pr)
}

func TestSinkFailureIsAFaultNotAScore(t *testing.T) {
	r := newRig(t)
	r.sink.err = fmt.Errorf("disk full")
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err == nil || errors.As(err, new(refusal)) || len(cards) != 0 {
		t.Errorf("cards %v err %v, want a plain fault and no cards", cards, err)
	}
}

func TestPushIsWithdrawnEvenWhenItsAnswerWasLost(t *testing.T) {
	for name, req := range map[string]request{"mob": mobRequest("a"), "duel": duelRequest("a")} {
		r := newRig(t)
		r.sink.err, r.sink.lostPut = fmt.Errorf("connection reset"), true
		if _, err := r.b.Run(context.Background(), req); err == nil {
			t.Fatalf("%s: a failed push was not a fault", name)
		}
		if len(r.sink.pushed) == 0 {
			t.Fatalf("%s: the stub did not record the landed push", name)
		}
		if len(r.sink.cleared) == 0 {
			t.Errorf("%s: the profile that may have landed was never withdrawn", name)
		}
	}
}

func TestProfileIsReadBackBeforeAndAfterTheApplyWait(t *testing.T) {
	r := newRig(t)
	started := r.clk.Now()
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil || len(cards) != 1 || cards[0].Outcome == tactics.OutcomeAborted {
		t.Fatalf("cards %+v err %v", cards, err)
	}
	if want := []string{"cand-1", "cand-1"}; !slices.Equal(r.sink.confirmed, want) {
		t.Errorf("confirmed %v, want two reads %v", r.sink.confirmed, want)
	}
	if r.clk.Now().Sub(started) < r.b.ApplyWait {
		t.Error("the second read did not wait for the units to settle")
	}
}

func TestUnconfirmedProfileIsNeverScored(t *testing.T) {
	for name, req := range map[string]request{"mob": mobRequest("a"), "duel": duelRequest("a")} {
		r := newRig(t)
		r.sink.confirmErr = fmt.Errorf("squad serves profile %q", "default")
		cards, err := r.b.Run(context.Background(), req)
		if err == nil || errors.As(err, new(refusal)) {
			t.Errorf("%s: err = %v, want a plain fault", name, err)
		}
		if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
			t.Errorf("%s: cards = %+v, want one aborted card", name, cards)
		}
		if r.sim.spawned {
			t.Errorf("%s: the fight started on an unconfirmed profile", name)
		}
		if len(r.sink.cleared) == 0 {
			t.Errorf("%s: the units were not released", name)
		}
	}
}

func TestDuelConfirmsBothTeamsProfiles(t *testing.T) {
	r := newRig(t)
	if _, err := r.b.Run(context.Background(), duelRequest("a")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"cand-1", "default", "cand-1", "default"}; !slices.Equal(r.sink.confirmed, want) {
		t.Errorf("confirmed %v, want %v", r.sink.confirmed, want)
	}
}

func TestRunsSerialiseOnEachLabSquad(t *testing.T) {
	r := newRig(t)
	r.sink.squads = []string{"lab-bravo", "lab-alpha"}
	other, err := lockSquads(context.Background(), r.b.ControlDir, []string{"lab-bravo"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err = r.b.Run(ctx, mobRequest("a"))
	if err == nil || !strings.Contains(err.Error(), "lab-bravo") || errors.As(err, new(refusal)) {
		t.Fatalf("err = %v, want a fault naming the held squad", err)
	}
	if len(r.sink.pushed) != 0 || r.sim.spawned {
		t.Error("a run that never got the squad pushed a profile or fought")
	}

	other()
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil || len(cards) != 1 {
		t.Fatalf("after the holder left: cards %v err %v", cards, err)
	}
	for _, sq := range r.sink.squads {
		if _, err := os.Stat(filepath.Join(r.b.ControlDir, "squad-"+sq+".lock")); err != nil {
			t.Errorf("no lock file for %s: %v", sq, err)
		}
	}
}

func TestSquadLockWaitsForTheHolder(t *testing.T) {
	dir := t.TempDir()
	held, err := lockSquads(context.Background(), dir, []string{"lab-alpha"})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		release, err := lockSquads(context.Background(), dir, []string{"lab-alpha", "lab-alpha"})
		if err == nil {
			release()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("second run got the squad while it was held: %v", err)
	case <-time.After(2 * lockPoll):
	}
	held()
	if err := <-got; err != nil {
		t.Errorf("second run after release: %v", err)
	}
}

func TestSquadLockRefusesAnIDThatIsNotALabName(t *testing.T) {
	for _, sq := range []string{"lab-/../fwb", "alpha", "../lab-a", ""} {
		if _, err := lockSquads(context.Background(), t.TempDir(), []string{sq}); err == nil {
			t.Errorf("squad %q was locked", sq)
		}
	}
}

func TestDryRunWritesTheCandidateAndRefusesToScore(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.b.Sink, r.b.DryRun = FileSink{Dir: dir}, true
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if !errors.As(err, new(refusal)) || len(cards) != 0 {
		t.Fatalf("cards %v err %v, want a refusal and no cards", cards, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Rusty.json")); err != nil {
		t.Errorf("the dry run wrote no profile: %v", err)
	}
	if len(r.sim.cmds) != 0 {
		t.Errorf("a dry run touched the lab: %v", r.sim.cmds)
	}
}

func TestDuelistWhoLeftTheWorldIsAnAbortedRoundNotALoss(t *testing.T) {
	r := newRig(t)
	r.sim.gone = true
	r.sim.dieAfter = map[string]time.Duration{"Rusty": 10 * time.Second}
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if !errors.Is(err, arena.ErrDuelistGone) || errors.As(err, new(refusal)) {
		t.Fatalf("err = %v, want a plain ErrDuelistGone fault", err)
	}
	if len(cards) == 0 {
		t.Fatal("no cards")
	}
	for _, c := range cards {
		if c.Outcome != tactics.OutcomeAborted {
			t.Errorf("card = %+v, want aborted: a disconnect is no loss", c)
		}
	}
}

func TestDuelOnlyPutsAllowListedUnitsThrough(t *testing.T) {
	r := newRig(t)
	r.b.Duelists = []string{"Rusty", "Slate", "Tinder"}
	_, err := r.b.Run(context.Background(), duelRequest("a"))
	if err == nil || !strings.Contains(err.Error(), "Agate") {
		t.Fatalf("err = %v, want Agate refused though crew may be online", err)
	}
	for _, c := range r.sim.cmds {
		if strings.HasPrefix(c, "clear ") || strings.HasPrefix(c, "tp ") {
			t.Errorf("the server was touched: %s", c)
		}
	}
}

func TestFightWallEndsBeforeTheTrainerDeadline(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		timeout  time.Duration
		deadline time.Duration
		want     time.Duration
	}{
		{"no deadline keeps the timeout", 10 * time.Minute, 0, 10 * time.Minute},
		{"a deadline beyond the timeout leaves it", 10 * time.Second, 5 * time.Minute, 10 * time.Second},
		{"a nearer deadline wins, minus the reserve", 10 * time.Minute, 5 * time.Minute, 5*time.Minute - cleanupReserve},
		{"under twice the reserve left, half is kept for cleanup", 10 * time.Minute, 20 * time.Second, 10 * time.Second},
		{"a deadline already past expires at once", 10 * time.Minute, -time.Minute, time.Nanosecond},
	}
	for _, tc := range cases {
		b := &bridge{FightTimeout: tc.timeout, Now: func() time.Time { return now }}
		if tc.deadline != 0 {
			b.Deadline = now.Add(tc.deadline)
		}
		if got := b.fightWall(); got != tc.want {
			t.Errorf("%s: wall = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPastTrainerDeadlineAbortsAndStillCleansUp(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.b.Now = time.Now
	r.b.Sleep = func(time.Duration) { time.Sleep(time.Millisecond) }
	r.b.Deadline = time.Now().Add(-time.Second)
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v, a deadline cut is not a fault", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Fatalf("cards = %+v, want one aborted card", cards)
	}
	if len(r.sink.cleared) != 1 {
		t.Errorf("the profile was not withdrawn: %v", r.sink.cleared)
	}
}

func TestSIGTERMStyleCancellationStillWithdrawsOnAFreshContext(t *testing.T) {
	r := newRig(t)
	r.sim.mobsGoneAfter = -1
	r.b.Now = time.Now
	ctx, cancel := context.WithCancel(context.Background())
	r.b.Sleep = func(time.Duration) {
		if r.sim.spawned {
			cancel()
		}
		time.Sleep(time.Millisecond)
	}
	cards, err := r.b.Run(ctx, mobRequest("a"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Fatalf("cards = %+v, want one aborted card", cards)
	}
	if len(r.sink.cleared) != 1 || r.sink.clearCtxErr != nil {
		t.Errorf("withdrawal after the cancel: cleared %v, ctx err %v", r.sink.cleared, r.sink.clearCtxErr)
	}
	if n := (&arena.FakeConsole{Cmds: r.sim.cmds}).Count("kill @e[tag=arena_wave]"); n < 2 {
		t.Errorf("mobs killed %d times, want a clean-up after the cancel", n)
	}
}

func TestDeadlineFromEnv(t *testing.T) {
	if d, err := deadlineFromEnv(""); err != nil || !d.IsZero() {
		t.Errorf("empty = %v, %v", d, err)
	}
	want := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if d, err := deadlineFromEnv("2026-10-28T12:00:00Z"); err != nil || !d.Equal(want) {
		t.Errorf("valid = %v, %v", d, err)
	}
	if _, err := deadlineFromEnv("soon"); err == nil {
		t.Error("a malformed deadline was ignored")
	}
}

// fakeUnits stands in for the units' state endpoints. A unit follows the
// profile the sink was last pushed once lag polls have gone by; a unit in
// stuck keeps reporting the named profile, and one in down is unreachable.
type fakeUnits struct {
	sink  *fakeSink
	hash  func(name string) string
	lag   int
	stuck map[string]arena.UnitTactics
	down  map[string]error
	polls map[string]int
}

func (f *fakeUnits) Tactics(_ context.Context, unit string) (arena.UnitTactics, error) {
	if f.polls == nil {
		f.polls = map[string]int{}
	}
	f.polls[unit]++
	if err := f.down[unit]; err != nil {
		return arena.UnitTactics{}, err
	}
	if st, ok := f.stuck[unit]; ok {
		return st, nil
	}
	if f.polls[unit] <= f.lag {
		return arena.UnitTactics{Profile: "default", Hash: f.hash("default")}, nil
	}
	name := f.sink.pushed[unit]
	return arena.UnitTactics{Profile: name, Hash: f.hash(name)}, nil
}

// unitRig adds fake units to the rig. Candidates are built by candidate() and
// the opponent is the shipped default, so those two names cover every push.
func unitRig(t *testing.T) (*rig, *fakeUnits) {
	t.Helper()
	r := newRig(t)
	hashes := map[string]string{}
	for _, p := range []tactics.Profile{candidate("cand-1"), tactics.NewProfile("default")} {
		h, err := tactics.Hash(p)
		if err != nil {
			t.Fatal(err)
		}
		hashes[p.Name] = h
	}
	u := &fakeUnits{sink: r.sink, hash: func(n string) string { return hashes[n] }}
	r.b.Units = u
	r.b.ApplyWait = 20 * time.Second
	r.b.Poll = 3 * time.Second
	return r, u
}

func TestMobFightsOnceEveryUnitReportsThePushedHash(t *testing.T) {
	r, u := unitRig(t)
	u.lag = 2
	cards, err := r.b.Run(context.Background(), mobRequest("a"))
	if err != nil || len(cards) != 1 || cards[0].Outcome == tactics.OutcomeAborted {
		t.Fatalf("cards %+v err %v\n%s", cards, err, r.out)
	}
	for _, w := range r.b.Warriors {
		if u.polls[w] != 3 {
			t.Errorf("%s polled %d times, want 3 (two stale, one match)", w, u.polls[w])
		}
	}
	if want := []string{"cand-1", "cand-1"}; !slices.Equal(r.sink.confirmed, want) {
		t.Errorf("squad route confirmed %v, want a read before and after the unit check %v", r.sink.confirmed, want)
	}
}

func TestDuelChecksEachTeamAgainstItsOwnProfile(t *testing.T) {
	r, u := unitRig(t)
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if err != nil || len(cards) == 0 || cards[0].Outcome == tactics.OutcomeAborted {
		t.Fatalf("cards %+v err %v\n%s", cards, err, r.out)
	}
	for _, name := range append(slices.Clone(r.b.TeamA), r.b.TeamB...) {
		if u.polls[name] == 0 {
			t.Errorf("%s was never asked what it runs", name)
		}
	}
	// A team A unit reporting the opponent's profile must not pass.
	r2, u2 := unitRig(t)
	u2.stuck = map[string]arena.UnitTactics{"Rusty": {Profile: "default", Hash: u2.hash("default")}}
	cards, err = r2.b.Run(context.Background(), duelRequest("a"))
	if err == nil || len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted || !strings.Contains(err.Error(), "Rusty") {
		t.Fatalf("cards %+v err %v, want an aborted duel naming Rusty", cards, err)
	}
}

func TestUnitOnAnotherProfileAbortsTheFight(t *testing.T) {
	for name, req := range map[string]request{"mob": mobRequest("a"), "duel": duelRequest("a")} {
		r, u := unitRig(t)
		off := false
		u.stuck = map[string]arena.UnitTactics{"Slate": {Profile: "default", Hash: u.hash("default"), TrainingEnabled: &off}}
		started := r.clk.Now()
		cards, err := r.b.Run(context.Background(), req)
		if err == nil || errors.As(err, new(refusal)) {
			t.Errorf("%s: err = %v, want a plain fault (exit 1)", name, err)
		} else if !strings.Contains(err.Error(), "Slate") || !strings.Contains(err.Error(), "training off") {
			t.Errorf("%s: err = %v, want the unit and why", name, err)
		}
		if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
			t.Errorf("%s: cards = %+v, want one aborted card", name, cards)
		}
		if r.sim.spawned {
			t.Errorf("%s: the fight started on a profile the unit does not run", name)
		}
		if len(r.sink.cleared) == 0 {
			t.Errorf("%s: the units were not released", name)
		}
		if waited := r.clk.Now().Sub(started); waited < r.b.ApplyWait || waited > r.b.ApplyWait+r.b.Poll {
			t.Errorf("%s: waited %s, want about --apply-wait %s", name, waited, r.b.ApplyWait)
		}
		if len(r.sink.confirmed) == 0 || len(r.sink.confirmed) > 2 {
			t.Errorf("%s: squad route read %d times", name, len(r.sink.confirmed))
		}
	}
}

func TestOfflineUnitAbortsTheFight(t *testing.T) {
	for name, why := range map[string]error{
		"offline":   errors.New("connection refused"),
		"old build": arena.ErrNoTactics,
	} {
		r, u := unitRig(t)
		u.down = map[string]error{"Tinder": why}
		cards, err := r.b.Run(context.Background(), mobRequest("a"))
		if err == nil || errors.As(err, new(refusal)) || !strings.Contains(err.Error(), "Tinder") || !strings.Contains(err.Error(), why.Error()) {
			t.Errorf("%s: err = %v, want a fault naming Tinder and why", name, err)
		}
		if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted || r.sim.spawned {
			t.Errorf("%s: cards %+v spawned %v, want one aborted card and no fight", name, cards, r.sim.spawned)
		}
		if u.polls["Tinder"] < 2 {
			t.Errorf("%s: an offline unit was polled %d times, want retries", name, u.polls["Tinder"])
		}
	}
}

func TestUnitsAreNotAskedWhenTheSquadRouteDisagrees(t *testing.T) {
	r, u := unitRig(t)
	r.sink.confirmErr = errors.New("squad serves profile \"default\"")
	if _, err := r.b.Run(context.Background(), mobRequest("a")); err == nil {
		t.Fatal("want a fault")
	}
	if len(u.polls) != 0 {
		t.Errorf("units polled %v after the squad route already disagreed", u.polls)
	}
}

func TestUnitCheckEndsWithItsContext(t *testing.T) {
	r, u := unitRig(t)
	u.down = map[string]error{"Rusty": errors.New("down")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cards, _ := r.b.Run(ctx, mobRequest("a"))
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted || r.sim.spawned {
		t.Fatalf("cards %+v spawned %v, want an aborted card and no fight", cards, r.sim.spawned)
	}
	if len(r.sink.cleared) == 0 {
		t.Error("the units were not released")
	}
}

type unitTactics statusapi.Tactics

func (u unitTactics) Tactics() statusapi.Tactics { return statusapi.Tactics(u) }

type quiet struct{}

func (quiet) Snapshot() belief.Snapshot { return belief.Snapshot{} }

// The bridge reads exactly what the unit's real status handler writes.
func TestHTTPTacticsReadsWhatTheStatusAPIServes(t *testing.T) {
	want := candidate("cand-1")
	hash, err := tactics.Hash(want)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(statusapi.Handler(quiet{}, "dev", statusapi.Unit{}, nil,
		statusapi.WithTactics(unitTactics{Profile: want.Name, Hash: hash})))
	defer srv.Close()
	src, err := arena.NewHTTPTactics(srv.URL + "/{name}/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Tactics(context.Background(), "")
	if err != nil || got.Hash != hash || got.Profile != "cand-1" {
		t.Fatalf("tactics %+v err %v", got, err)
	}
}
