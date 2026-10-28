package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

var hubPos = TargetPosition{X: 0, Y: 64, Z: -400}

// duelSim answers the console for a duel. A duelist goes down a scripted time
// after the start signal of its round; with respawn set (the default) it
// reappears at the world spawn, and without it the unit reads as missing.
type duelSim struct {
	clock   *fakeClock
	round   int
	armed   bool
	goAt    time.Time
	dieAt   map[int]map[string]time.Duration
	respawn bool
	// humanAfter makes a non-crew player show up that long after a start.
	humanAfter time.Duration
	list       string
}

func newDuelSim(clock *fakeClock) *duelSim {
	return &duelSim{
		clock: clock,
		// A real death shows as a respawn at the world spawn; a unit that
		// reads as missing has left the world.
		respawn: true,
		dieAt:   map[int]map[string]time.Duration{},
		list:    "There are 4/10 players online:\nRusty, Slate, Tinder, Agate",
	}
}

func (s *duelSim) die(round int, name string, after time.Duration) {
	if s.dieAt[round] == nil {
		s.dieAt[round] = map[string]time.Duration{}
	}
	s.dieAt[round][name] = after
}

func (s *duelSim) down(name string) bool {
	after, ok := s.dieAt[s.round][name]
	return s.armed && ok && s.clock.Now().Sub(s.goAt) >= after
}

func (s *duelSim) Exec(cmd string) (string, error) {
	switch {
	case cmd == "list":
		if s.humanAfter > 0 && s.armed && s.clock.Now().Sub(s.goAt) >= s.humanAfter {
			return "There are 5/10 players online:\nRusty, Slate, Tinder, Agate, Steve99", nil
		}
		return s.list, nil
	case strings.HasPrefix(cmd, "testfor @e[name="):
		n := warriorName(cmd)
		if s.down(n) && !s.respawn {
			return "ERROR: No targets matched selector", nil
		}
		return "Found " + n, nil
	case strings.HasPrefix(cmd, "querytarget @a[name="):
		if s.down(queryName(cmd)) && s.respawn {
			return targetJSON(hubPos), nil
		}
		return targetJSON(TargetPosition{X: 0, Y: 65, Z: -760}), nil
	}
	return "", nil
}

// hooks wires the sim to a policy: Go starts a round's clock, Disarm ends it.
func (s *duelSim) hooks(p *FakePolicy) {
	p.OnGo = func() { s.round++; s.armed = true; s.goAt = s.clock.Now() }
	p.OnDisarm = func() { s.armed = false }
}

type duelRig struct {
	d      *Duel
	f      *FakeConsole
	s      *duelSim
	policy *FakePolicy
	sink   *FakeSink
	clock  *fakeClock
	out    *strings.Builder
}

func newDuelRig() *duelRig {
	clock := newFakeClock()
	s := newDuelSim(clock)
	f := &FakeConsole{Respond: s.Exec}
	policy, sink := &FakePolicy{}, &FakeSink{}
	s.hooks(policy)
	out := &strings.Builder{}
	return &duelRig{
		f: f, s: s, policy: policy, sink: sink, clock: clock, out: out,
		d: &Duel{
			Console: f, Layout: bossLayout(),
			Red: []string{"Rusty", "Slate"}, Blue: []string{"Tinder", "Agate"},
			RedProfile: "alpha", BlueProfile: "beta",
			Kits:   []string{"vanguard/t5", "ranger/t5"},
			Rounds: 2, RoundTimeout: time.Minute, MaxWall: 30 * time.Minute,
			CrewNames: DefaultCrewNames, Duelists: []string{"Rusty", "Slate", "Tinder", "Agate"},
			Policy: policy, Sink: sink, RunID: "run-1",
			Spacing: 12, Poll: 5 * time.Second, Settle: 25 * time.Second,
			KitGap: 3 * time.Second, Countdown: 5 * time.Second,
			Out: out, Now: clock.Now, Sleep: clock.Sleep,
		},
	}
}

func (r *duelRig) run(t *testing.T) []DuelRound {
	t.Helper()
	rounds, err := r.d.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, r.out.String())
	}
	return rounds
}

func countPrefix(cmds []string, prefix string) int {
	n := 0
	for _, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestDuelSwapsSidesAndScoresEachSide(t *testing.T) {
	r := newDuelRig()
	r.s.die(1, "Tinder", 20*time.Second)
	r.s.die(1, "Agate", 30*time.Second)
	r.s.die(2, "Rusty", 15*time.Second)
	r.s.die(2, "Slate", 25*time.Second)
	rounds := r.run(t)

	if len(rounds) != 2 {
		t.Fatalf("rounds = %+v", rounds)
	}
	if rounds[0].Winner != SideRed || rounds[0].WinnerTeam != "red" || rounds[1].Winner != SideRed || rounds[1].WinnerTeam != "blue" {
		t.Errorf("winners = %+v, want the red spawn twice, red then blue as teams", rounds)
	}
	if !reflect.DeepEqual(rounds[1].RedSpawn, []string{"Tinder", "Agate"}) {
		t.Errorf("round 2 red spawn = %v, want the sides swapped", rounds[1].RedSpawn)
	}
	if len(r.sink.Cards) != 4 {
		t.Fatalf("cards = %d, want one per side per round", len(r.sink.Cards))
	}
	c := r.sink.Cards[0]
	if c.Side != SideRed || c.Profile != "alpha" || c.Opponent != "beta" || c.Outcome != tactics.OutcomeWin ||
		c.Kills != 2 || c.Scenario != tactics.ScenarioDuel || c.Family != tactics.FamilyPlayer || c.RunID != "run-1" {
		t.Errorf("red card = %+v", c)
	}
	if !reflect.DeepEqual(c.Deaths, map[string]int{"Rusty": 0, "Slate": 0}) {
		t.Errorf("red deaths = %v", c.Deaths)
	}
	b := r.sink.Cards[1]
	if b.Side != SideBlue || b.Profile != "beta" || b.Outcome != tactics.OutcomeLoss || b.Kills != 0 || b.TotalDeaths() != 2 {
		t.Errorf("blue card = %+v", b)
	}
	if b.Seconds < 30 {
		t.Errorf("blue seconds = %v, want the round to last until the last death", b.Seconds)
	}
	swapped := r.sink.Cards[2]
	if swapped.Side != SideRed || swapped.Profile != "beta" || swapped.Outcome != tactics.OutcomeWin {
		t.Errorf("round 2 red-spawn card = %+v, want beta on the red spawn", swapped)
	}
	if got := r.sink.Cards[2].Score(); got <= r.sink.Cards[3].Score() {
		t.Errorf("winner scores %v, loser %v", got, r.sink.Cards[3].Score())
	}
}

func TestDuelMirrorsSpawnPointsAndSwapsThem(t *testing.T) {
	r := newDuelRig()
	r.run(t)
	for _, want := range []string{
		`tp "Rusty" 0 65 -772 0 0`, `tp "Tinder" 0 65 -748 180 0`,
		`tp "Slate" 3 65 -772 0 0`, `tp "Agate" -3 65 -748 180 0`,
		// Round 2: the teams stand on each other's lines.
		`tp "Tinder" 0 65 -772 0 0`, `tp "Rusty" 0 65 -748 180 0`,
	} {
		if !hasCommand(r.f.Cmds, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(r.f.Cmds, "\n"))
		}
	}
	red, blue := r.d.spawnPoint(SideRed, 3), r.d.spawnPoint(SideBlue, 3)
	c := r.d.Layout.Center
	if red.X-c.X != c.X-blue.X || red.Z-c.Z != c.Z-blue.Z || red.Y != blue.Y {
		t.Errorf("red %+v and blue %+v are not mirrored about %+v", red, blue, c)
	}
}

func TestDuelGivesBothSidesTheSameKitBySlot(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.run(t)
	var kits []string
	for _, c := range r.f.Cmds {
		if strings.Contains(c, "function kits/") {
			kits = append(kits, c)
		}
	}
	want := []string{
		`execute as @a[name="Rusty"] at @s run function kits/vanguard/t5`,
		`execute as @a[name="Tinder"] at @s run function kits/vanguard/t5`,
		`execute as @a[name="Slate"] at @s run function kits/ranger/t5`,
		`execute as @a[name="Agate"] at @s run function kits/ranger/t5`,
	}
	if !reflect.DeepEqual(kits, want) {
		t.Errorf("kit commands = %v, want %v", kits, want)
	}
	if !before(r.f.Cmds, `clear "Agate"`, want[0]) {
		t.Error("a kit was handed out before every inventory was cleared")
	}
}

func TestDuelKitFollowsTheSlotAcrossTheSwap(t *testing.T) {
	r := newDuelRig()
	r.run(t)
	if n := r.f.Count(`@a[name="Tinder"] at @s run function kits/vanguard/t5`); n != 2 {
		t.Errorf("Tinder got its vanguard kit %d times in 2 rounds", n)
	}
}

func TestDuelRefusesRollingKitsAndMismatchedKitLists(t *testing.T) {
	for _, kits := range [][]string{{"vanguard/roll", "ranger/t5"}, {"vanguard/elite", "ranger/t5"}, {"vanguard/t5"}, {"random", "random"}} {
		r := newDuelRig()
		r.d.Kits = kits
		if _, err := r.d.Run(context.Background()); err == nil {
			t.Errorf("kits %v accepted", kits)
		}
		if len(r.f.Cmds) != 0 {
			t.Errorf("kits %v touched the server before failing: %v", kits, r.f.Cmds)
		}
	}
}

func TestDuelWithoutKitsLeavesInventoriesAlone(t *testing.T) {
	r := newDuelRig()
	r.d.Kits = nil
	r.d.Rounds = 1
	r.run(t)
	if countPrefix(r.f.Cmds, "clear ") != 0 || r.f.Count("function kits/") != 0 {
		t.Errorf("a kit-less duel cleared or kitted:\n%s", strings.Join(r.f.Cmds, "\n"))
	}
}

func TestDuelTargetPolicyOrder(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.s.die(1, "Tinder", 10*time.Second)
	r.s.die(1, "Agate", 20*time.Second)
	r.run(t)
	want := []string{"arm", "go", "out:Tinder", "out:Agate", "disarm"}
	if !reflect.DeepEqual(r.policy.Calls, want) {
		t.Errorf("policy calls = %v, want %v", r.policy.Calls, want)
	}
	if !reflect.DeepEqual(r.policy.Roster, Roster{Red: []string{"Rusty", "Slate"}, Blue: []string{"Tinder", "Agate"}}) {
		t.Errorf("roster = %+v", r.policy.Roster)
	}
}

func TestDuelStartSignalFollowsPlacementAndKits(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	var cmdsAtGo int
	prev := r.policy.OnGo
	r.policy.OnGo = func() { cmdsAtGo = len(r.f.Cmds); prev() }
	r.s.die(1, "Tinder", 10*time.Second)
	r.s.die(1, "Agate", 20*time.Second)
	r.run(t)
	for i, c := range r.f.Cmds {
		if i >= cmdsAtGo && (strings.HasPrefix(c, "tp ") || strings.Contains(c, "function kits/")) {
			t.Errorf("%q ran after the start signal", c)
		}
	}
}

func TestDuelTimeoutScoresBothSidesAsTimeouts(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	rounds := r.run(t)
	if rounds[0].Outcome != DuelTimeout || rounds[0].Winner != "" || rounds[0].Seconds < 60 {
		t.Errorf("round = %+v", rounds[0])
	}
	for _, c := range r.sink.Cards {
		if c.Outcome != tactics.OutcomeTimeout || c.Kills != 0 {
			t.Errorf("card = %+v", c)
		}
	}
}

func TestDuelMutualWipeIsADraw(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	for _, n := range []string{"Rusty", "Slate", "Tinder", "Agate"} {
		r.s.die(1, n, 20*time.Second)
	}
	rounds := r.run(t)
	if rounds[0].Outcome != DuelDraw || rounds[0].Winner != "" {
		t.Errorf("round = %+v", rounds[0])
	}
	if c := r.sink.Cards[0]; c.Outcome != tactics.OutcomeTimeout || c.Kills != 2 || c.TotalDeaths() != 2 {
		t.Errorf("draw card = %+v", c)
	}
}

func TestDuelAShortGapIsNotADeath(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	var tinderPolls int
	f := r.f
	prev := f.Respond
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=\"Tinder\"") {
			tinderPolls++
			if tinderPolls >= 5 && tinderPolls <= 6 {
				return "ERROR: No targets matched selector", nil
			}
		}
		return prev(cmd)
	}
	rounds := r.run(t)
	if rounds[0].Outcome != DuelTimeout || r.sink.Cards[1].TotalDeaths() != 0 {
		t.Errorf("a two-poll gap counted as a death: %+v %+v", rounds[0], r.sink.Cards[1])
	}
}

func TestDuelMissingDuelistAbortsTheRoundInsteadOfScoringALoss(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.s.respawn = false
	r.s.die(1, "Tinder", 10*time.Second)
	rounds, err := r.d.Run(context.Background())
	if !errors.Is(err, ErrDuelistGone) || !strings.Contains(err.Error(), "Tinder") {
		t.Fatalf("err = %v, want ErrDuelistGone naming the unit", err)
	}
	if len(rounds) != 1 || rounds[0].Outcome != DuelAborted || rounds[0].Winner != "" {
		t.Fatalf("rounds = %+v, want one aborted round", rounds)
	}
	if len(r.sink.Cards) != 2 {
		t.Fatalf("cards = %d, want one per side", len(r.sink.Cards))
	}
	for _, c := range r.sink.Cards {
		if c.Outcome != tactics.OutcomeAborted || c.TotalDeaths() != 0 {
			t.Errorf("card = %+v, want aborted with no death on it", c)
		}
	}
	if slices.Contains(r.policy.Out, "Tinder") {
		t.Error("the vanished duelist was reported as eliminated")
	}
}

func TestDuelOnlyAllowListedUnitsMayDuelWhateverIsOnline(t *testing.T) {
	r := newDuelRig()
	r.d.Duelists = []string{"Rusty", "Slate", "Tinder"}
	if _, err := r.d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "Agate") {
		t.Fatalf("err = %v, want Agate refused: crew may be online without being fair game", err)
	}
	if r.f.Count("tp ") != 0 || r.f.Count("clear ") != 0 {
		t.Error("the server was touched before the refusal")
	}
}

func TestDuelARespawnedDuelistStaysOut(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.s.respawn = true
	r.s.die(1, "Tinder", 10*time.Second)
	r.s.die(1, "Agate", 20*time.Second)
	rounds := r.run(t)
	if rounds[0].Outcome != DuelDecided || rounds[0].Winner != SideRed {
		t.Errorf("round = %+v, want red to win though the blue units stand at spawn again", rounds[0])
	}
}

func TestDuelRefusesWhenAHumanIsOnline(t *testing.T) {
	r := newDuelRig()
	r.s.list = "There are 5/10 players online:\nRusty, Slate, Tinder, Agate, Steve99"
	_, err := r.d.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) || !slices.Equal(busy.Names, []string{"Steve99"}) {
		t.Fatalf("err = %v, want a refusal naming Steve99", err)
	}
	if r.f.Count("tp ") != 0 || r.f.Count("tickingarea") != 0 || len(r.policy.Calls) != 0 || len(r.sink.Cards) != 0 {
		t.Errorf("a refused duel touched the server: %v", r.f.Cmds)
	}
}

func TestDuelRunsWithAListedHuman(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.s.list = "There are 5/10 players online:\nRusty, Slate, Tinder, Agate, Steve99"
	r.d.CrewNames = append(slices.Clone(DefaultCrewNames), "Steve99")
	r.run(t)
}

func TestDuelAbortsWhenAHumanJoinsMidRound(t *testing.T) {
	r := newDuelRig()
	r.s.humanAfter = 20 * time.Second
	rounds, err := r.d.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) {
		t.Fatalf("err = %v, want NotAloneError", err)
	}
	if len(rounds) != 1 || rounds[0].Outcome != DuelAborted {
		t.Fatalf("rounds = %+v", rounds)
	}
	for _, c := range r.sink.Cards {
		if c.Outcome != tactics.OutcomeAborted {
			t.Errorf("card = %+v, want aborted", c)
		}
	}
	if last := r.policy.Calls[len(r.policy.Calls)-1]; last != "disarm" {
		t.Errorf("policy calls = %v, want a disarm last", r.policy.Calls)
	}
	if r.f.Count("tickingarea remove") == 0 {
		t.Error("ticking areas left loaded")
	}
}

func TestDuelKillSwitchStopsTheRun(t *testing.T) {
	r := newDuelRig()
	r.d.Halted = func() bool { return r.s.armed && r.clock.Now().Sub(r.s.goAt) >= 10*time.Second }
	rounds, err := r.d.Run(context.Background())
	if !errors.Is(err, ErrDuelHalted) {
		t.Fatalf("err = %v", err)
	}
	if len(rounds) != 1 || rounds[0].Outcome != DuelAborted || r.policy.Calls[len(r.policy.Calls)-1] != "disarm" {
		t.Errorf("rounds %+v, calls %v", rounds, r.policy.Calls)
	}
}

func TestDuelWallClockCapEndsTheRun(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 10
	r.d.MaxWall = 100 * time.Second
	rounds, err := r.d.Run(context.Background())
	if !errors.Is(err, ErrDuelWallClock) {
		t.Fatalf("err = %v, want the wall-clock cap", err)
	}
	if len(rounds) != 1 || rounds[0].Outcome != DuelAborted {
		t.Errorf("rounds = %+v", rounds)
	}
}

func TestDuelWallClockCapStopsBetweenRounds(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 10
	for round := 1; round <= 10; round++ {
		r.s.die(round, "Tinder", 5*time.Second)
		r.s.die(round, "Agate", 5*time.Second)
		r.s.die(round, "Rusty", 5*time.Second)
		r.s.die(round, "Slate", 5*time.Second)
	}
	r.d.MaxWall = 90 * time.Second
	rounds, err := r.d.Run(context.Background())
	if !errors.Is(err, ErrDuelWallClock) || len(rounds) == 0 || len(rounds) >= 10 {
		t.Errorf("%d rounds, err %v: want the cap to cut a long run short", len(rounds), err)
	}
}

func TestDuelInterruptEndsTheRoundAndCleansUp(t *testing.T) {
	r := newDuelRig()
	ctx, cancel := context.WithCancel(context.Background())
	r.d.Sleep = func(d time.Duration) {
		r.clock.Sleep(d)
		if r.s.armed && r.clock.Now().Sub(r.s.goAt) >= 10*time.Second {
			cancel()
		}
	}
	rounds, err := r.d.Run(ctx)
	if !errors.Is(err, context.Canceled) || len(rounds) != 1 || rounds[0].Outcome != DuelAborted {
		t.Fatalf("rounds %+v, err %v", rounds, err)
	}
	if r.policy.Calls[len(r.policy.Calls)-1] != "disarm" || r.f.Count("tickingarea remove") == 0 {
		t.Error("an interrupt left the duel armed or the areas loaded")
	}
}

func TestDuelFailedArmMeasuresNothing(t *testing.T) {
	r := newDuelRig()
	r.policy.ArmErr = errors.New("no engine")
	rounds, err := r.d.Run(context.Background())
	if err == nil || len(rounds) != 0 || len(r.sink.Cards) != 0 {
		t.Fatalf("rounds %+v, cards %d, err %v", rounds, len(r.sink.Cards), err)
	}
	if !reflect.DeepEqual(r.policy.Calls, []string{"arm", "disarm"}) {
		t.Errorf("policy calls = %v, want no start signal", r.policy.Calls)
	}
}

func TestDuelMissingDuelistFailsBeforeTouchingAnyone(t *testing.T) {
	r := newDuelRig()
	prev := r.f.Respond
	r.f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, `testfor @e[name="Agate"`) {
			return "ERROR: No targets matched selector", nil
		}
		return prev(cmd)
	}
	if _, err := r.d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "Agate") {
		t.Fatalf("err = %v", err)
	}
	if countPrefix(r.f.Cmds, "tp ") != 0 || countPrefix(r.f.Cmds, "clear ") != 0 {
		t.Error("a duelist was moved or cleared though another was missing")
	}
}

func TestDuelSinkFailureIsReported(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.sink.Err = errors.New("disk full")
	r.s.die(1, "Tinder", 10*time.Second)
	r.s.die(1, "Agate", 10*time.Second)
	if _, err := r.d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v", err)
	}
}

func TestDuelValidation(t *testing.T) {
	cases := map[string]func(*Duel){
		"no rounds":         func(d *Duel) { d.Rounds = 0 },
		"too many rounds":   func(d *Duel) { d.Rounds = MaxDuelRounds + 1 },
		"timeout too short": func(d *Duel) { d.RoundTimeout = time.Second },
		"timeout too long":  func(d *Duel) { d.RoundTimeout = time.Hour },
		"no wall cap":       func(d *Duel) { d.MaxWall = 0 },
		"wall cap too long": func(d *Duel) { d.MaxWall = 3 * time.Hour },
		"unequal sides":     func(d *Duel) { d.Blue = d.Blue[:1]; d.Kits = nil },
		"empty sides":       func(d *Duel) { d.Red, d.Blue, d.Kits = nil, nil, nil },
		"six a side": func(d *Duel) {
			d.Red = []string{"Agate", "Basalt", "Cobble", "Dusty", "Ember", "Flint"}
			d.Blue = []string{"Gravel", "Hazel", "Ingot", "Jasper", "Kettle", "Loam"}
			d.Kits = nil
		},
		"a name twice":      func(d *Duel) { d.Blue = []string{"Rusty", "Agate"} },
		"not a crew unit":   func(d *Duel) { d.Blue = []string{"Steve99", "Agate"} },
		"crew, not allowed": func(d *Duel) { d.Blue = []string{"Basalt", "Agate"} },
		"no allow list":     func(d *Duel) { d.Duelists = nil },
		"a quote in name":   func(d *Duel) { d.Duelists = append(d.Duelists, `a"b`); d.Blue = []string{`a"b`, "Agate"} },
		"no policy":         func(d *Duel) { d.Policy = nil },
		"no sink":           func(d *Duel) { d.Sink = nil },
		"no console":        func(d *Duel) { d.Console = nil },
		"spacing too thin":  func(d *Duel) { d.Spacing = 2 },
		"spacing too wide":  func(d *Duel) { d.Spacing = 60 },
		"no floor":          func(d *Duel) { d.Layout.FloorRadius = 0 },
	}
	for name, mutate := range cases {
		r := newDuelRig()
		mutate(r.d)
		if _, err := r.d.Run(context.Background()); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if r.f.Count("tp ") != 0 {
			t.Errorf("%s: touched the server", name)
		}
	}
}

func TestValidateLabTarget(t *testing.T) {
	for _, ok := range []string{"lab", "crew-lab", "k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-0"} {
		if err := ValidateLabTarget(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "fwb", "FWB", "minecraft-fwb", "k8s:jdwillmsen-prd/minecraft-fwb-prd-bedrock-0",
		"k8s:fwb/crew-lab", "k8s:jdwillmsen-prd/some-other-pod", "k8s:ns", "k8s:/pod", "Lab Name", "lab;rm",
	} {
		if err := ValidateLabTarget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRosterEnemy(t *testing.T) {
	r := Roster{Red: []string{"A", "B"}, Blue: []string{"C"}}
	for _, c := range []struct {
		self, other string
		want        bool
	}{
		{"A", "C", true}, {"C", "B", true},
		{"A", "B", false}, {"A", "A", false},
		{"A", "Steve", false}, {"Steve", "A", false}, {"Dusty", "C", false},
	} {
		if got := r.Enemy(c.self, c.other); got != c.want {
			t.Errorf("Enemy(%s,%s) = %v, want %v", c.self, c.other, got, c.want)
		}
	}
}

func TestConsolePolicyTagsAndClears(t *testing.T) {
	f := &FakeConsole{}
	p := &ConsolePolicy{Console: f}
	if err := p.Arm(Roster{Red: []string{"Rusty"}, Blue: []string{"Tinder"}}); err != nil {
		t.Fatal(err)
	}
	if !hasCommand(f.Cmds, `tag "Rusty" add duel_red`) || !hasCommand(f.Cmds, `tag "Tinder" add duel_blue`) {
		t.Errorf("arm commands = %v", f.Cmds)
	}
	if !before(f.Cmds, `tag "Tinder" remove duel_red`, `tag "Rusty" add duel_red`) {
		t.Error("stale tags were not cleared before tagging")
	}
	if err := p.Go(); err != nil || !hasCommand(f.Cmds, `tag "Tinder" add duel_go`) {
		t.Errorf("go: %v %v", err, f.Cmds)
	}
	if err := p.Eliminated("Tinder"); err != nil || !hasCommand(f.Cmds, `tag "Tinder" add duel_out`) {
		t.Errorf("eliminated: %v %v", err, f.Cmds)
	}
	n := len(f.Cmds)
	if err := p.Disarm(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"duel_red", "duel_blue", "duel_go", "duel_out"} {
		if !hasCommand(f.Cmds[n:], fmt.Sprintf(`tag "Rusty" remove %s`, tag)) {
			t.Errorf("disarm left %s on Rusty", tag)
		}
	}
	n = len(f.Cmds)
	if err := p.Disarm(); err != nil || len(f.Cmds) != n {
		t.Error("a second disarm is not a no-op")
	}
}

func TestConsolePolicyRefusesAnInjectedName(t *testing.T) {
	f := &FakeConsole{}
	p := &ConsolePolicy{Console: f}
	if err := p.Arm(Roster{Red: []string{`x" run kill @a "`}}); err == nil {
		t.Error("a name with a quote was accepted")
	}
	if f.Count("kill") != 0 {
		t.Errorf("injected command reached the console: %v", f.Cmds)
	}
}

func TestJSONLSinkWritesOneCardPerLine(t *testing.T) {
	var buf bytes.Buffer
	s := JSONLSink{W: &buf}
	_ = s.Record(tactics.Scorecard{Side: "red", Outcome: tactics.OutcomeWin})
	_ = s.Record(tactics.Scorecard{Side: "blue", Outcome: tactics.OutcomeLoss})
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var c tactics.Scorecard
	if len(lines) != 2 || json.Unmarshal([]byte(lines[1]), &c) != nil || c.Side != "blue" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestDuelFitsTheColosseumLayout(t *testing.T) {
	layout, err := LoadLayout("../../lab/colosseum/layout.json")
	if err != nil {
		t.Skipf("no colosseum layout: %v", err)
	}
	r := newDuelRig()
	r.d.Layout = layout
	if err := r.d.validate(); err != nil {
		t.Fatalf("default spacing does not fit the colosseum: %v", err)
	}
	for slot := range r.d.Red {
		for _, p := range []Point{r.d.spawnPoint(SideRed, slot), r.d.spawnPoint(SideBlue, slot)} {
			if p.Y != layout.GroundY+1 {
				t.Errorf("spawn %+v is not on the floor (ground %d)", p, layout.GroundY)
			}
		}
	}
}

func TestValidateComposeFile(t *testing.T) {
	for _, ok := range []string{"minecraft/crew/lab/compose.yaml", "/home/u/worktrees/o/r/feat/x/minecraft/crew/lab/compose.yaml"} {
		if err := ValidateComposeFile(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "fwb/compose.yaml", "/srv/FWB-minecraft/docker-compose.yml", "compose-fwb.yaml"} {
		if err := ValidateComposeFile(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := ValidateLabConsole("crew-lab", "fwb/compose.yaml"); err == nil {
		t.Error("a compose lab on an FWB compose file was accepted")
	}
	if err := ValidateLabConsole("k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-0", ""); err != nil {
		t.Errorf("a k8s lab needs no compose file: %v", err)
	}
	if err := ValidateLabConsole("minecraft-fwb", "minecraft/crew/lab/compose.yaml"); err == nil {
		t.Error("an FWB lab was accepted")
	}
}
