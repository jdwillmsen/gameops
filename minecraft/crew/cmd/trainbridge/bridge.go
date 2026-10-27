package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tacticsctl"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/trainer"
)

// request is what the trainer's cmd evaluator writes on stdin.
type request struct {
	Profile  tactics.Profile  `json:"profile"`
	Scenario trainer.Scenario `json:"scenario"`
}

// refusal marks a run that must not start or go on: training is off, the
// target is not the lab, or a player outside the crew is online. It exits 2.
type refusal struct{ err error }

func (r refusal) Error() string { return r.err.Error() }
func (r refusal) Unwrap() error { return r.err }

// bridge fights one scenario with one candidate Profile on the crew lab and
// returns the scorecards. Everything it touches is injected so the tests run
// offline against the arena's fakes.
type bridge struct {
	ControlDir string
	Lab        string
	StopFile   string

	Console arena.Console
	Layout  arena.Layout
	Book    *arena.ScenarioBook
	Sink    ProfileSink

	// Opponent is the Profile the other team fights with in a duel.
	Opponent tactics.Profile

	Warriors    []string
	WarriorKits []string
	Crew        []string
	TeamA       []string
	TeamB       []string
	DuelKits    []string

	// Damage, when set, fills damage taken on mob scorecards.
	Damage arena.DamageSource

	// FightTimeout is a hard wall around the whole fight, set-up included.
	// A fight it cuts short scores as aborted.
	FightTimeout time.Duration
	RoundTimeout time.Duration
	Poll         time.Duration
	Settle       time.Duration
	PlaceWait    time.Duration
	Countdown    time.Duration
	KitGap       time.Duration
	Spacing      int
	// Emit is "all" for every card a duel produces or "candidate" for the
	// candidate's alone.
	Emit string

	Out   io.Writer
	Now   func() time.Time
	Sleep func(time.Duration)
}

func (b *bridge) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *bridge) logf(format string, args ...any) {
	if b.Out != nil {
		fmt.Fprintf(b.Out, "trainbridge: "+format+"\n", args...)
	}
}

// guard is checked before anything touches the server.
func (b *bridge) guard() error {
	if err := tacticsctl.LabOnly(b.Lab); err != nil {
		return refusal{err}
	}
	if err := arena.ValidateLabTarget(b.Lab); err != nil {
		return refusal{err}
	}
	if !tacticsctl.TrainingEnabled(b.ControlDir) {
		return refusal{errors.New("training is disabled (kill switch)")}
	}
	return nil
}

// stopped is the live kill switch: the stop file, or training turned off
// while the fight runs.
func (b *bridge) stopped() bool {
	if b.StopFile != "" {
		if _, err := os.Stat(b.StopFile); err == nil {
			return true
		}
	}
	return !tacticsctl.TrainingEnabled(b.ControlDir)
}

// Run returns the scorecards of one fight. A fight the wall clock, the stop
// file or the kill switch ended returns aborted cards and no error, since
// that is a measured nothing, not a fault. Real faults return the aborted
// cards and the error.
func (b *bridge) Run(ctx context.Context, req request) ([]tactics.Scorecard, error) {
	if err := b.guard(); err != nil {
		return nil, err
	}
	prof, err := req.Profile.Validate()
	if err != nil {
		return nil, refusal{err}
	}
	// Checked before the profile is pushed, so a refused run changes nothing.
	if err := classify(arena.CheckAlone(b.Console, b.Crew)); err != nil {
		return nil, err
	}
	if b.FightTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.FightTimeout)
		defer cancel()
	}
	switch req.Scenario.Kind {
	case tactics.ScenarioMob:
		return b.mob(ctx, prof, req.Scenario)
	case tactics.ScenarioDuel:
		return b.duel(ctx, prof, req.Scenario)
	}
	return nil, refusal{fmt.Errorf("scenario kind %q is not supported; use mob or duel", req.Scenario.Kind)}
}

// expected reports whether an error is the fight being stopped on purpose.
func expected(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, arena.ErrFightHalted) || errors.Is(err, arena.ErrDuelHalted) ||
		errors.Is(err, arena.ErrDuelWallClock)
}

// classify turns a fight error into the error the caller sees.
func classify(err error) error {
	var busy *arena.NotAloneError
	switch {
	case err == nil, expected(err):
		return nil
	case errors.As(err, &busy):
		return refusal{err}
	}
	return err
}

// release puts the units back on the default Profile. It outlives the fight's
// context, which a timeout may already have ended.
func (b *bridge) release(units []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Sink.Clear(ctx, units); err != nil {
		b.logf("clearing profiles: %v", err)
	}
}

func oppositeSide(side string) string {
	switch side {
	case "north":
		return "south"
	case "south":
		return "north"
	case "east":
		return "west"
	}
	return "east"
}

func mobSummary(s arena.Scenario) string {
	parts := make([]string, 0, len(s.Mobs))
	for _, m := range s.Mobs {
		parts = append(parts, fmt.Sprintf("%s:%d", m.Mob, m.Count))
	}
	return strings.Join(parts, ",")
}

func (b *bridge) mob(ctx context.Context, prof tactics.Profile, ts trainer.Scenario) ([]tactics.Scorecard, error) {
	sc, ok := b.Book.Scenario(ts.Name)
	if !ok {
		return nil, refusal{fmt.Errorf("scenario %q is not in the book", ts.Name)}
	}
	if ts.Side == "b" {
		// The b side of a mirrored pair meets the mobs from the far side.
		sc.Spawn.Side = oppositeSide(sc.Spawn.Side)
	}
	if err := b.Sink.Push(ctx, b.Warriors, prof); err != nil {
		return nil, err
	}
	defer b.release(b.Warriors)

	rec := &arena.Recorder{Meta: arena.FightMeta{
		RunID:    fmt.Sprintf("%s-%d-%s", sc.Name, ts.Seed, ts.Side),
		Scenario: sc.Name, Family: sc.Family, Opponent: mobSummary(sc),
		Profile: prof.Name, Side: ts.Side, Seed: ts.Seed, Warriors: b.Warriors,
	}, Damage: b.Damage, Logf: b.logf}
	started := b.now()
	fight := &arena.ScenarioFight{
		Console: b.Console, Layout: b.Layout, Book: b.Book, Scenario: sc, Seed: ts.Seed,
		Warriors: b.Warriors, WarriorKits: b.WarriorKits, CrewNames: b.Crew,
		Poll: b.Poll, Settle: b.Settle, PlaceWait: b.PlaceWait,
		Observer: rec, Halted: b.stopped, Out: b.Out, Now: b.Now, Sleep: b.Sleep,
	}
	res, err := fight.Run(ctx)
	if err != nil {
		b.logf("fight %s ended without a result: %v", sc.Name, err)
		return []tactics.Scorecard{rec.Finish(tactics.OutcomeAborted, b.now().Sub(started).Seconds())}, classify(err)
	}
	return []tactics.Scorecard{rec.Finish(res.Outcome, res.Seconds)}, nil
}

// collector keeps the cards a duel hands its sink.
type collector struct{ cards []tactics.Scorecard }

func (c *collector) Record(s tactics.Scorecard) error {
	c.cards = append(c.cards, s)
	return nil
}

func (b *bridge) duel(ctx context.Context, cand tactics.Profile, ts trainer.Scenario) ([]tactics.Scorecard, error) {
	opp := b.Opponent
	if opp.Version == 0 {
		opp = tactics.NewProfile("default")
	}
	if opp.Name == cand.Name {
		// Two cards with one profile name could not be told apart.
		opp.Name += "-opponent"
	}
	red, blue, redProfile, blueProfile := b.TeamA, b.TeamB, cand.Name, opp.Name
	if ts.Side == "b" {
		red, blue, redProfile, blueProfile = b.TeamB, b.TeamA, opp.Name, cand.Name
	}
	all := append(slices.Clone(b.TeamA), b.TeamB...)
	defer b.release(all)
	if err := b.Sink.Push(ctx, b.TeamA, cand); err != nil {
		return nil, err
	}
	if err := b.Sink.Push(ctx, b.TeamB, opp); err != nil {
		return nil, err
	}

	got := &collector{}
	d := &arena.Duel{
		Console: b.Console, Layout: b.Layout, Red: red, Blue: blue,
		RedProfile: redProfile, BlueProfile: blueProfile, Kits: b.DuelKits,
		Rounds: 1, RoundTimeout: b.RoundTimeout, MaxWall: min(max(b.FightTimeout, time.Second), arena.MaxDuelWall),
		CrewNames: b.Crew, Policy: &arena.ConsolePolicy{Console: b.Console}, Sink: got,
		RunID:   fmt.Sprintf("duel-%d-%s", ts.Seed, ts.Side),
		Spacing: b.Spacing, Poll: b.Poll, Settle: b.Settle, KitGap: b.KitGap, Countdown: b.Countdown,
		Halted: b.stopped, Out: b.Out, Now: b.Now, Sleep: b.Sleep,
	}
	started := b.now()
	_, err := d.Run(ctx)
	cards := got.cards
	if err != nil {
		b.logf("duel ended early: %v", err)
	}
	if len(cards) == 0 {
		if err == nil {
			err = errors.New("duel produced no scorecards")
		}
		cards = []tactics.Scorecard{{
			TS: started.UTC().Format(time.RFC3339), RunID: "duel-" + fmt.Sprint(ts.Seed), Scenario: tactics.ScenarioDuel,
			Family: tactics.FamilyPlayer, Opponent: opp.Name, Profile: cand.Name, Side: ts.Side,
			Outcome: tactics.OutcomeAborted, Seconds: b.now().Sub(started).Seconds(), Deaths: map[string]int{},
		}}
	}
	for i := range cards {
		cards[i].Seed = ts.Seed
	}
	// The candidate's card leads, and with Emit candidate it is alone, because
	// the trainer averages every measured card it is given.
	slices.SortStableFunc(cards, func(x, y tactics.Scorecard) int {
		return boolRank(y.Profile == cand.Name) - boolRank(x.Profile == cand.Name)
	})
	if b.Emit == "candidate" {
		cards = slices.DeleteFunc(cards, func(c tactics.Scorecard) bool { return c.Profile != cand.Name })
	}
	return cards, classify(err)
}

func boolRank(v bool) int {
	if v {
		return 1
	}
	return 0
}
