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
	// Duelists are the units a duel may clear and teleport; it is separate
	// from Crew, who may merely be online.
	Duelists []string
	TeamA    []string
	TeamB    []string
	DuelKits []string

	// Damage, when set, fills damage taken on mob scorecards.
	Damage arena.DamageSource

	// DryRun writes the candidate to the sink and stops: it never fights and
	// never scores, since the file sink has no consumer.
	DryRun bool

	// ApplyWait is how long the units get to take a pushed profile before it
	// is read back a second time. A unit fetches on a poll, and one failed
	// fetch holds it on its old profile for far longer than that, so the
	// wait is for the squad route's answer to settle, not a promise.
	ApplyWait time.Duration

	// FightTimeout is a hard wall around the whole fight, set-up included.
	// A fight it cuts short scores as aborted.
	FightTimeout time.Duration
	// Deadline, when set, is when the trainer will send this process SIGTERM.
	// The fight ends cleanupReserve before it, so the profile is withdrawn and
	// the arena cleared while the process still has its full time to do so.
	Deadline     time.Time
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

	// wall is FightTimeout after the Deadline clamp; set by Run.
	wall time.Duration
}

// cleanupReserve is how long before the trainer's deadline a fight must be
// over: release is bounded at 10 s and the arena cleanup runs through the
// console after it.
const cleanupReserve = 15 * time.Second

// fightWall is the longest the fight may run: the configured timeout, cut so
// it ends cleanupReserve (or, with less than twice that left, half the
// remaining time) before the trainer's deadline.
func (b *bridge) fightWall() time.Duration {
	wall := b.FightTimeout
	if b.Deadline.IsZero() {
		return wall
	}
	remaining := b.Deadline.Sub(b.now())
	room := remaining - min(cleanupReserve, remaining/2)
	if wall <= 0 || room < wall {
		wall = room
	}
	// A deadline already past still yields a positive wall, so the context
	// expires at once and the run scores as aborted instead of running free.
	return max(wall, time.Nanosecond)
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
	if b.DryRun {
		return nil, b.dryRun(ctx, prof, req)
	}
	if err := classify(arena.CheckAlone(b.Console, b.Crew)); err != nil {
		return nil, err
	}
	if b.wall = b.fightWall(); b.wall > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.wall)
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

// dryRun shows what a run would hand the units and refuses to score: the
// file sink is not read by any unit, so a fight against it would measure the
// shipped behaviour as the candidate.
func (b *bridge) dryRun(ctx context.Context, prof tactics.Profile, req request) error {
	units := slices.Concat(b.Warriors, b.TeamA, b.TeamB)
	slices.Sort(units)
	units = slices.Compact(units)
	if err := b.Sink.Push(ctx, units, prof); err != nil {
		return err
	}
	b.logf("dry run: profile %q written for %s; nothing was fought", prof.Name, strings.Join(units, ","))
	return refusal{errors.New("dry run: nothing was fought and no scorecards exist")}
}

// wait sleeps through the injected clock, or for real, and ends early with
// the context.
func (b *bridge) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	if b.Sleep != nil {
		b.Sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// handoff is one profile and the units that must be served exactly it.
type handoff struct {
	units   []string
	profile tactics.Profile
}

// confirm reads every handoff back from the sink, waits for the units to
// settle on it and reads again; a fight is scored only when both reads show
// the squad serving exactly the pushed profile.
func (b *bridge) confirm(ctx context.Context, hs []handoff) error {
	check := func() error {
		for _, h := range hs {
			if err := b.Sink.Confirm(ctx, h.units, h.profile); err != nil {
				return fmt.Errorf("profile not confirmed: %w", err)
			}
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if err := b.wait(ctx, b.ApplyWait); err != nil {
		return err
	}
	return check()
}

// lock holds the lab squads of the units for the whole fight.
func (b *bridge) lock(ctx context.Context, units []string) (func(), error) {
	squads, err := b.Sink.LabSquads(units)
	if err != nil {
		return nil, refusal{err}
	}
	if len(squads) == 0 {
		return func() {}, nil
	}
	return lockSquads(ctx, b.ControlDir, squads)
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
	rec := &arena.Recorder{Meta: arena.FightMeta{
		RunID:    fmt.Sprintf("%s-%d-%s", sc.Name, ts.Seed, ts.Side),
		Scenario: sc.Name, Family: sc.Family, Opponent: mobSummary(sc),
		Profile: prof.Name, Side: ts.Side, Seed: ts.Seed, Warriors: b.Warriors,
		Spawned: sc.MobCount(),
	}, Damage: b.Damage, Logf: b.logf}
	unlock, err := b.lock(ctx, b.Warriors)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Registered before the push: a PUT whose answer was lost may still have
	// landed, and the withdrawal must cover it.
	defer b.release(b.Warriors)
	if err := b.Sink.Push(ctx, b.Warriors, prof); err != nil {
		return nil, err
	}
	started := b.now()
	if err := b.confirm(ctx, []handoff{{b.Warriors, prof}}); err != nil {
		b.logf("fight %s not started: %v", sc.Name, err)
		return []tactics.Scorecard{rec.Finish(tactics.OutcomeAborted, b.now().Sub(started).Seconds())}, classify(err)
	}
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
	unlock, err := b.lock(ctx, all)
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer b.release(all)
	if err := b.Sink.Push(ctx, b.TeamA, cand); err != nil {
		return nil, err
	}
	if err := b.Sink.Push(ctx, b.TeamB, opp); err != nil {
		return nil, err
	}
	started := b.now()
	aborted := func() tactics.Scorecard {
		return tactics.Scorecard{
			TS: started.UTC().Format(time.RFC3339), RunID: "duel-" + fmt.Sprint(ts.Seed), Scenario: tactics.ScenarioDuel,
			Family: tactics.FamilyPlayer, Opponent: opp.Name, Profile: cand.Name, Side: ts.Side, Seed: ts.Seed,
			Outcome: tactics.OutcomeAborted, Seconds: b.now().Sub(started).Seconds(), Deaths: map[string]int{},
		}
	}
	if err := b.confirm(ctx, []handoff{{b.TeamA, cand}, {b.TeamB, opp}}); err != nil {
		b.logf("duel not started: %v", err)
		return []tactics.Scorecard{aborted()}, classify(err)
	}

	got := &collector{}
	d := &arena.Duel{
		Console: b.Console, Layout: b.Layout, Red: red, Blue: blue,
		RedProfile: redProfile, BlueProfile: blueProfile, Kits: b.DuelKits,
		Rounds: 1, RoundTimeout: b.RoundTimeout, MaxWall: min(max(b.wall, time.Second), arena.MaxDuelWall),
		CrewNames: b.Crew, Duelists: b.Duelists, Policy: &arena.ConsolePolicy{Console: b.Console}, Sink: got,
		RunID:   fmt.Sprintf("duel-%d-%s", ts.Seed, ts.Side),
		Spacing: b.Spacing, Poll: b.Poll, Settle: b.Settle, KitGap: b.KitGap, Countdown: b.Countdown,
		Halted: b.stopped, Out: b.Out, Now: b.Now, Sleep: b.Sleep,
	}
	_, err = d.Run(ctx)
	cards := got.cards
	if err != nil {
		b.logf("duel ended early: %v", err)
	}
	if len(cards) == 0 {
		if err == nil {
			err = errors.New("duel produced no scorecards")
		}
		cards = []tactics.Scorecard{aborted()}
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
