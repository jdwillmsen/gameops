package arena

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func bossLayout() Layout {
	l := testLayout()
	l.Center = Point{X: 0, Y: 64, Z: -760}
	l.GroundY = 64
	l.FloorRadius = 45
	return l
}

// bossSim answers the console for a boss fight: the boss is found for the
// first aliveFor testfor replies and gone afterwards, and a warrior can be
// scripted down for a stretch of polls.
type bossSim struct {
	list       string
	aliveFor   int
	bossPolls  int
	summon     string
	downFrom   map[string]int
	downUntil  map[string]int
	warriorPos map[string]TargetPosition
	respawnPos map[string]TargetPosition
	polls      map[string]int
	tpReply    string
	missReply  string
	summoned   bool
}

func newBossSim() *bossSim {
	return &bossSim{
		list:       "There are 3/10 players online:\nRusty, Slate, Tinder",
		aliveFor:   2,
		downFrom:   map[string]int{},
		downUntil:  map[string]int{},
		warriorPos: map[string]TargetPosition{},
		respawnPos: map[string]TargetPosition{},
		polls:      map[string]int{},
		missReply:  "[2026-10-23 12:00:00:000 ERROR] No targets matched the selector",
	}
}

func (s *bossSim) Exec(cmd string) (string, error) {
	switch {
	case cmd == "list":
		return s.list, nil
	case strings.HasPrefix(cmd, "summon "):
		s.summoned = true
		return s.summon, nil
	case strings.HasPrefix(cmd, "tp "):
		return s.tpReply, nil
	case strings.HasPrefix(cmd, "testfor @e[name=") && strings.HasSuffix(cmd, "type=!player]"):
		s.bossPolls++
		if s.bossPolls <= s.aliveFor {
			return "Found W", nil
		}
		return s.missReply, nil
	case strings.HasPrefix(cmd, "testfor @e[name=") && strings.HasSuffix(cmd, "type=player]"):
		name := warriorName(cmd)
		s.polls[name]++
		if from := s.downFrom[name]; from > 0 && s.polls[name] >= from && s.polls[name] < s.downUntil[name] {
			return s.missReply, nil
		}
		if s.downFrom[name] > 0 && s.downUntil[name] == 0 && s.polls[name] >= s.downFrom[name] {
			return s.missReply, nil
		}
		return "Found " + name, nil
	case strings.HasPrefix(cmd, "querytarget @a[name="):
		name := queryName(cmd)
		p, ok := s.warriorPos[name]
		if !ok {
			p = TargetPosition{X: 0, Y: 65, Z: -785}
		}
		if after, ok := s.respawnPos[name]; ok && s.polls[name] >= s.downUntil[name] {
			p = after
		}
		return targetJSON(p), nil
	}
	return "", nil
}

func newBossTest(f *FakeConsole, clock *fakeClock) (*BossTest, *[]BossRecord) {
	var recs []BossRecord
	return &BossTest{
		Console:   f,
		Layout:    bossLayout(),
		Mob:       "warden",
		Name:      "W",
		Runs:      1,
		Timeout:   5 * time.Minute,
		Warriors:  []string{"Rusty", "Slate", "Tinder"},
		CrewNames: DefaultCrewNames,
		Poll:      5 * time.Second,
		Settle:    25 * time.Second,
		Record:    func(r BossRecord) error { recs = append(recs, r); return nil },
		Now:       clock.Now,
		Sleep:     clock.Sleep,
	}, &recs
}

func TestBossRefusesWhenAHumanIsOnline(t *testing.T) {
	s := newBossSim()
	s.list = "There are 2/10 players online:\nRusty, Steve99"
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	_, err := b.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) || !reflect.DeepEqual(busy.Names, []string{"Steve99"}) {
		t.Fatalf("Run error = %v, want a refusal naming Steve99", err)
	}
	if f.Count("summon") != 0 || f.Count("tickingarea") != 0 || f.Count("tp ") != 0 || len(*recs) != 0 {
		t.Errorf("a refused test touched the server:\n%s", strings.Join(f.Cmds, "\n"))
	}
}

func TestBossIgnoresAnErrorLineAfterTheHeader(t *testing.T) {
	s := newBossSim()
	s.list = "There are 0/10 players online:\n[2026-10-23 12:00:00:000 ERROR] No targets matched the selector"
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	var busy *NotAloneError
	if _, err := b.Run(context.Background()); errors.As(err, &busy) {
		t.Fatalf("an ERROR line read as a player: %v", err)
	}
}

func TestBossKillIsRecordedFromTheTestforReply(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) != 1 || len(*recs) != 1 {
		t.Fatalf("records = %d returned, %d recorded; want 1 each", len(got), len(*recs))
	}
	r := got[0]
	if r.Outcome != BossKilled || r.Boss != "warden" || r.Run != 1 || r.Seconds < 10 || r.Seconds > 20 {
		t.Errorf("record = %+v, want a kill of the warden in about three polls", r)
	}
	if !reflect.DeepEqual(r.Deaths, map[string]int{"Rusty": 0, "Slate": 0, "Tinder": 0, "total": 0}) {
		t.Errorf("deaths = %v", r.Deaths)
	}
	if r.Effects == nil || len(r.Effects) != 0 {
		t.Errorf("effects = %#v, want an empty list", r.Effects)
	}
}

func TestBossSummonPutsTheNameBeforeTheCoordinates(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCommand(f.Cmds, `summon warden "W" 0 65 -775`) {
		t.Errorf("no summon 10 blocks past the squad start:\n%s", strings.Join(f.Cmds, "\n"))
	}
	for _, place := range []string{`tp "Rusty" 0 65 -785`, `tp "Slate" 3 65 -785`, `tp "Tinder" -3 65 -785`} {
		if !hasCommand(f.Cmds, place) {
			t.Errorf("squad was not placed with %q", place)
		}
	}
}

func TestBossFightsTheEnderDragonLikeAnyOtherBoss(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	b.Mob = "ender_dragon"
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCommand(f.Cmds, `summon ender_dragon "W" 0 65 -775`) {
		t.Errorf("no named dragon summoned:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if len(*recs) != 1 || (*recs)[0].Boss != "ender_dragon" || (*recs)[0].Outcome != BossKilled {
		t.Errorf("records = %#v, want one killed ender_dragon run", *recs)
	}
}

func TestBossLoadsTheLayoutAreasAndAlwaysRemovesThem(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	add := "tickingarea add -16 0 -800 31 0 -696 crew-arena-run-0"
	if !before(f.Cmds, add, `summon warden "W" 0 65 -775`) {
		t.Errorf("the area was not loaded before the summon:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if f.Count("tickingarea remove crew-arena-run-0") != 1 {
		t.Errorf("area removals = %d, want 1", f.Count("tickingarea remove crew-arena-run-0"))
	}
	if last := f.Cmds[len(f.Cmds)-1]; last != "tickingarea remove crew-arena-run-0" {
		t.Errorf("last command = %q, want the area removal after the boss kill", last)
	}
	if f.Count(`kill @e[name="W",type=!player]`) < 2 {
		t.Error("the boss was not killed before the run and again after it")
	}
}

func TestBossRunsEffectsAfterTheSummon(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	b.Effects = SplitEffects(`effect @e[name=W,type=warden] weakness 999 4, effect @e[name=W] slowness 999 1`)
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !before(f.Cmds, `summon warden`, `effect @e[name=W,type=warden] weakness 999 4`) ||
		!hasCommand(f.Cmds, `effect @e[name=W] slowness 999 1`) {
		t.Errorf("effects not run after the summon:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if (*recs)[0].Effects[1] != `effect @e[name=W] slowness 999 1` {
		t.Errorf("recorded effects = %q", (*recs)[0].Effects)
	}
}

func TestSplitEffectsKeepsCommasInsideSelectors(t *testing.T) {
	got := SplitEffects(`effect @e[name=W,type=warden] weakness 9 4,  , kill @e[name=X]`)
	want := []string{`effect @e[name=W,type=warden] weakness 9 4`, `kill @e[name=X]`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitEffects = %q, want %q", got, want)
	}
	if got := SplitEffects(""); len(got) != 0 {
		t.Errorf("SplitEffects of nothing = %q", got)
	}
}

func TestBossDownNeedsANoTargetsReplyWithoutTheName(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		down  bool
	}{
		{"no targets", "[2026-10-23 12:00:00:000 ERROR] No targets matched the selector", true},
		{"found", "Found W", false},
		{"found with a stray miss line", "Found W\nNo targets matched the selector", false},
		{"silence", "", false},
		{"unrelated reply", "Teleported Rusty", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bossDown(tt.reply, "W"); got != tt.down {
				t.Errorf("bossDown(%q) = %v, want %v", tt.reply, got, tt.down)
			}
		})
	}
}

func TestBossIsNotKilledByATeleportMiss(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	s.tpReply = "[2026-10-23 12:00:00:000 ERROR] No targets matched the selector"
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Timeout = 30 * time.Second
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got[0].Outcome != BossTimeout {
		t.Errorf("outcome = %q, want timeout; a tp miss is not a kill", got[0].Outcome)
	}
}

func TestBossOneStaleMissIsNotAKill(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	f := &FakeConsole{}
	f.Respond = func(cmd string) (string, error) {
		out, err := s.Exec(cmd)
		if strings.HasSuffix(cmd, "type=!player]") && strings.HasPrefix(cmd, "testfor") && s.bossPolls == 3 {
			// A late line from another command, read as this reply.
			return s.missReply, err
		}
		return out, err
	}
	b, _ := newBossTest(f, newFakeClock())
	b.Timeout = 30 * time.Second
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got[0].Outcome != BossTimeout {
		t.Errorf("outcome = %q, want timeout; one stray miss is not a kill", got[0].Outcome)
	}
}

func TestBossKillIsConfirmedByASecondTestfor(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	clock := newFakeClock()
	b, _ := newBossTest(f, clock)
	var sleeps []time.Duration
	b.Sleep = func(d time.Duration) { sleeps = append(sleeps, d); clock.Sleep(d) }
	got, err := b.Run(context.Background())
	if err != nil || got[0].Outcome != BossKilled {
		t.Fatalf("Run = %+v, %v; want a kill", got, err)
	}
	if s.bossPolls != s.aliveFor+2 {
		t.Errorf("boss polled %d times, want %d: two misses in a row", s.bossPolls, s.aliveFor+2)
	}
	if !slices.Contains(sleeps, bossConfirmWait) {
		t.Errorf("sleeps = %v, want a %v wait before the confirming testfor", sleeps, bossConfirmWait)
	}
}

func TestBossTimesOutAndStillCleansUp(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Timeout = time.Minute
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got[0].Outcome != BossTimeout || got[0].Seconds < 60 || got[0].Seconds > 70 {
		t.Errorf("record = %+v, want a timeout at about 60s", got[0])
	}
	if f.Count("tickingarea remove") != 1 || f.Count(`kill @e[name="W",type=!player]`) < 2 {
		t.Error("a timed-out run left the boss or its area behind")
	}
}

func TestBossReplacesARespawnedWarrior(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 4
	s.downFrom["Rusty"], s.downUntil["Rusty"] = 3, 4
	s.respawnPos["Rusty"] = TargetPosition{X: 0, Y: 80, Z: 0}
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := f.Count(`tp "Rusty" 0 65 -785`); n < 2 {
		t.Errorf("Rusty was placed %d times, want the respawn returned to its cell", n)
	}
	if got[0].Deaths["Rusty"] != 1 || got[0].Deaths["total"] != 1 {
		t.Errorf("deaths = %v, want one Rusty death", got[0].Deaths)
	}
}

func TestBossEndsOnAWipeAfterThreeDownPolls(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	for _, n := range []string{"Rusty", "Slate", "Tinder"} {
		s.downFrom[n] = 3
	}
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r := got[0]
	if r.Outcome != BossWipe || r.Deaths["total"] != 3 || r.Deaths["Slate"] != 1 {
		t.Errorf("record = %+v, want a wipe with each warrior down once", r)
	}
	if s.bossPolls > 8 {
		t.Errorf("boss polled %d times; the wipe should end the run after three down polls", s.bossPolls)
	}
	if f.Count("tickingarea remove") != 1 {
		t.Error("a wiped run left its area behind")
	}
}

func TestBossOneDownPollIsNotAWipe(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 4
	for _, n := range []string{"Rusty", "Slate", "Tinder"} {
		s.downFrom[n], s.downUntil[n] = 3, 4
	}
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	got, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got[0].Outcome != BossKilled {
		t.Errorf("outcome = %q, want a single down poll to pass", got[0].Outcome)
	}
}

func TestBossSummonFailureIsAnErrorWithCleanup(t *testing.T) {
	s := newBossSim()
	s.summon = "Unable to summon object"
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	_, err := b.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Unable to summon") {
		t.Fatalf("Run error = %v, want the failed summon", err)
	}
	if len(*recs) != 0 {
		t.Errorf("a failed summon was recorded: %+v", *recs)
	}
	if f.Count("tickingarea remove") != 1 || f.Count(`kill @e[name="W",type=!player]`) < 2 {
		t.Error("a failed run left the boss or its area behind")
	}
}

func TestBossThatNeverAppearsIsAnErrorNotAKill(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 0
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	_, err := b.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "never found") || len(*recs) != 0 {
		t.Fatalf("Run = %v with %d records, want an error and no record", err, len(*recs))
	}
}

func TestBossRepeatsRunsAndRechecksThePlayerList(t *testing.T) {
	s := newBossSim()
	runs := 0
	f := &FakeConsole{}
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "summon ") {
			runs++
			s.bossPolls = 0
		}
		return s.Exec(cmd)
	}
	b, recs := newBossTest(f, newFakeClock())
	b.Runs = 3
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*recs) != 3 || (*recs)[0].Run != 1 || (*recs)[2].Run != 3 {
		t.Fatalf("records = %+v, want runs 1..3", *recs)
	}
	if f.Count("list") < 4 || f.Count("tickingarea add") != 1 {
		t.Errorf("list x%d, ticking add x%d; want a list before loading and per run, and one area load", f.Count("list"), f.Count("tickingarea add"))
	}
}

func TestBossStopsBetweenRunsWhenAHumanJoins(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{}
	f.Respond = func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "summon ") {
			s.list = "There are 2/10 players online:\nRusty, Steve99"
			s.bossPolls = 0
		}
		return s.Exec(cmd)
	}
	b, recs := newBossTest(f, newFakeClock())
	b.Runs = 3
	got, err := b.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) {
		t.Fatalf("Run error = %v, want a refusal before run 2", err)
	}
	if len(got) != 1 || len(*recs) != 1 {
		t.Errorf("kept %d records, want the finished first run only", len(got))
	}
	if f.Count("tickingarea remove") != 1 {
		t.Error("the refusal left the area loaded")
	}
}

func TestBossRechecksPlayersAfterTheSettleWait(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	clock := newFakeClock()
	b, recs := newBossTest(f, clock)
	b.Sleep = func(d time.Duration) {
		if d == b.Settle {
			s.list = "There are 2/10 players online:\nRusty, Steve99"
		}
		clock.Sleep(d)
	}
	_, err := b.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) {
		t.Fatalf("Run error = %v, want a refusal after the settle wait", err)
	}
	if f.Count("summon") != 0 || len(*recs) != 0 {
		t.Errorf("a human who joined while the arena loaded still got a fight:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if f.Count("tickingarea remove") != 1 {
		t.Error("the refusal left the area loaded")
	}
}

func TestBossAbortsWhenAHumanJoinsMidFight(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	f := &FakeConsole{}
	clock := newFakeClock()
	var joined time.Time
	f.Respond = func(cmd string) (string, error) {
		if s.bossPolls == 3 && joined.IsZero() {
			s.list = "There are 4/10 players online:\nRusty, Slate, Tinder, Steve99"
			joined = clock.Now()
		}
		return s.Exec(cmd)
	}
	b, recs := newBossTest(f, clock)
	b.Runs = 2
	got, err := b.Run(context.Background())
	var busy *NotAloneError
	if !errors.As(err, &busy) || !reflect.DeepEqual(busy.Names, []string{"Steve99"}) {
		t.Fatalf("Run error = %v, want an abort naming Steve99", err)
	}
	if len(got) != 1 || len(*recs) != 1 || got[0].Outcome != BossAborted {
		t.Fatalf("records = %+v, want the one aborted run logged", *recs)
	}
	if waited := clock.Now().Sub(joined); waited > 25*time.Second {
		t.Errorf("abort came %v after the join, want within one player check", waited)
	}
	if f.Count("summon") != 1 {
		t.Errorf("summons = %d, want no second run", f.Count("summon"))
	}
	// The three untags sit between the kill and the area removal.
	tail := f.Cmds[len(f.Cmds)-5:]
	if tail[0] != `kill @e[name="W",type=!player]` || tail[4] != "tickingarea remove crew-arena-run-0" {
		t.Errorf("last commands = %q, want the boss kill, the untags, then the area removal", tail)
	}
}

func TestBossCancelledRunCleansUp(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	f := &FakeConsole{Respond: s.Exec}
	b, recs := newBossTest(f, newFakeClock())
	ctx, cancel := context.WithCancel(context.Background())
	b.Sleep = func(d time.Duration) {
		if f.Count("summon") > 0 {
			cancel()
		}
	}
	_, err := b.Run(ctx)
	if !errors.Is(err, context.Canceled) || len(*recs) != 0 {
		t.Fatalf("Run = %v with %d records, want cancellation and no record", err, len(*recs))
	}
	if f.Count("tickingarea remove") != 1 || f.Count(`kill @e[name="W",type=!player]`) < 2 {
		t.Error("a cancelled run left the boss or its area behind")
	}
}

func TestBossCancelDuringTheSettleWaitStartsNoFight(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	clock := newFakeClock()
	b, recs := newBossTest(f, clock)
	ctx, cancel := context.WithCancel(context.Background())
	b.Sleep = func(d time.Duration) {
		if d == b.Settle {
			cancel()
		}
		clock.Sleep(d)
	}
	_, err := b.Run(ctx)
	if !errors.Is(err, context.Canceled) || len(*recs) != 0 {
		t.Fatalf("Run = %v with %d records, want cancellation and no record", err, len(*recs))
	}
	if f.Count("summon") != 0 || f.Count("tp ") != 0 {
		t.Errorf("a cancelled test still started a fight:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if f.Count("tickingarea remove") != 1 {
		t.Error("the cancelled test left the area loaded")
	}
}

func TestBossCancelInterruptsTheSettleWait(t *testing.T) {
	f := &FakeConsole{Respond: newBossSim().Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Now, b.Sleep = nil, nil
	b.Settle = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := b.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want the context's", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("Run took %v; the settle wait ignored the context", waited)
	}
}

func TestBossRecordFailureStopsTheTest(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Runs = 2
	b.Record = func(BossRecord) error { return fmt.Errorf("disk full") }
	if _, err := b.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Run error = %v, want the log failure", err)
	}
}

func TestBossEffectsMayOnlyTargetTheBoss(t *testing.T) {
	allowed := []string{
		`effect @e[name=W] slowness 999 1`,
		`effect @e[name=W,type=warden] weakness 999 4`,
		`effect @e[name="W",type=warden] weakness 999 4`,
		`effect @e[name=W, type=warden] clear`,
		`damage @e[name=W] 5`,
		`event entity @e[name=W] minecraft:start_sniffing`,
	}
	refused := []string{
		`stop`,
		`op Steve99`,
		`kill @a`,
		`kill @e[name=W]`,
		`effect @a weakness 999 4`,
		`effect @p weakness 999 4`,
		`effect @s weakness 999 4`,
		`effect @r weakness 999 4`,
		`effect @e weakness 999 4`,
		`effect @e[type=warden] weakness 999 4`,
		`effect @e[name=WW] weakness 999 4`,
		`effect @e[type=warden,name=W] weakness 999 4`,
		`effect Steve99 weakness 999 4`,
		`effect @e[name=W]`,
		`execute as @e[name=W] run effect @a weakness 999 4`,
		`damage @e[name=W] 5 entity_attack entity @a`,
		`event entity @a minecraft:start_sniffing`,
		`event @e[name=W] minecraft:start_sniffing`,
		`tp @e[name=W] 0 64 0`,
	}
	for _, cmd := range allowed {
		f := &FakeConsole{Respond: newBossSim().Exec}
		b, _ := newBossTest(f, newFakeClock())
		b.Effects = []string{cmd}
		if _, err := b.Run(context.Background()); err != nil {
			t.Errorf("%q refused: %v", cmd, err)
		}
	}
	for _, cmd := range refused {
		f := &FakeConsole{Respond: newBossSim().Exec}
		b, _ := newBossTest(f, newFakeClock())
		b.Effects = []string{cmd}
		if _, err := b.Run(context.Background()); err == nil || len(f.Cmds) != 0 {
			t.Errorf("%q accepted: err %v, %d commands sent", cmd, err, len(f.Cmds))
		}
	}
}

func TestBossValidatesItsInputs(t *testing.T) {
	for name, mutate := range map[string]func(*BossTest){
		"no warriors":  func(b *BossTest) { b.Warriors = nil },
		"quoted name":  func(b *BossTest) { b.Name = `W"` },
		"odd mob":      func(b *BossTest) { b.Mob = "warden ~ ~ ~; kill @a" },
		"empty mob":    func(b *BossTest) { b.Mob = "" },
		"no timeout":   func(b *BossTest) { b.Timeout = 0 },
		"empty effect": func(b *BossTest) { b.Effects = []string{" "} },
		"crew name":    func(b *BossTest) { b.Name = "Probe" },
	} {
		t.Run(name, func(t *testing.T) {
			f := &FakeConsole{Respond: newBossSim().Exec}
			b, _ := newBossTest(f, newFakeClock())
			mutate(b)
			if _, err := b.Run(context.Background()); err == nil {
				t.Error("Run accepted bad input")
			}
			if len(f.Cmds) != 0 {
				t.Errorf("bad input still sent %d commands", len(f.Cmds))
			}
		})
	}
}

func TestBossSummonWithAnEventPutsTheNameLast(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Mob = "gameops:ground_dragon"
	b.Event = "gameops:hp_500"
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasCommand(f.Cmds, `summon gameops:ground_dragon 0 65 -775 0 0 gameops:hp_500 "W"`) {
		t.Errorf("no event summon with the name after the rotations:\n%s", strings.Join(f.Cmds, "\n"))
	}
}

func TestBossRejectsAnEventThatIsNotAnId(t *testing.T) {
	f := &FakeConsole{}
	b, _ := newBossTest(f, newFakeClock())
	b.Event = "x; kill @a"
	if _, err := b.Run(context.Background()); err == nil {
		t.Fatal("a malformed spawn event was accepted")
	}
}

func TestBossTagsTheSquadForPackBossesAndUntagsOnExit(t *testing.T) {
	s := newBossSim()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, w := range []string{"Rusty", "Slate", "Tinder"} {
		if !hasCommand(f.Cmds, fmt.Sprintf("tag %q add arena_warrior", w)) {
			t.Errorf("%s was not tagged", w)
		}
		if !hasCommand(f.Cmds, fmt.Sprintf("tag %q remove arena_warrior", w)) {
			t.Errorf("%s was not untagged on exit", w)
		}
	}
}
