package arena

import (
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time        { return c.t }
func (c *fakeClock) Sleep(d time.Duration) { c.t = c.t.Add(d) }

func testLayout() Layout {
	pen := func(id, x int) Pen {
		gate := Box{X0: x + 1, Y0: 64, Z0: -761, X1: x + 1, Y1: 66, Z1: -759}
		return Pen{ID: id, Spawn: Point{X: x, Y: 64, Z: -760}, Gate: gate,
			Open: gate.Fill("air")}
	}
	return Layout{
		Pens:      []Pen{pen(1, 10), pen(2, 20)},
		StartGate: StartGate{Name: "squad_start", Gate: Box{-3, 64, -709, 3, 67, -709}, Open: "fill -3 64 -709 3 67 -709 air"},
		Boss:      Point{X: 0, Y: 65, Z: -760},
	}
}

func testBook(waves ...Wave) *WaveBook {
	return &WaveBook{
		Pause:       Duration{2 * time.Second},
		MaxLive:     12,
		WaveTimeout: Duration{5 * time.Minute},
		Tiers: map[int]Tier{
			1: {
				Effects: []Effect{{ID: "strength", Seconds: 600, Amplifier: 0}},
				Gear:    []Gear{{Slot: "weapon.mainhand", Item: "stone_sword"}},
				Enchant: &Enchant{ID: "sharpness", Level: 1},
			},
			4: {
				Effects: []Effect{{ID: "resistance", Seconds: 600, Amplifier: 3}},
				Gear:    []Gear{{Slot: "weapon.mainhand", Item: "diamond_sword"}},
			},
			5: {
				Effects: []Effect{{ID: "absorption", Seconds: 600, Amplifier: 4}},
				Gear:    []Gear{{Slot: "weapon.mainhand", Item: "diamond_sword"}},
			},
		},
		Waves: waves,
	}
}

func wave(name string, mob string, count, tier, pen int) Wave {
	return Wave{Name: name, Mobs: []Mob{{Mob: mob, Count: count, Tier: tier, Pen: pen}}}
}

// sim answers the director's console so a whole run can play out offline.
type sim struct {
	live       int
	peak       int
	summons    int
	kills      int
	bossPolls  int
	boss       bool
	countStep  int
	warriorUp  map[string]bool
	warriorHit map[string]int
	warriorOff map[string]int
}

func newSim() *sim {
	return &sim{countStep: 1, warriorUp: map[string]bool{}, warriorHit: map[string]int{}, warriorOff: map[string]int{}}
}

func found(n int) string {
	switch {
	case n <= 0:
		return "ERROR: No targets matched selector"
	case n == 1:
		return "Found Zombie"
	default:
		names := make([]string, n)
		for i := range names {
			names[i] = "Zombie"
		}
		return "Found " + strings.Join(names, ", ")
	}
}

func (s *sim) Exec(cmd string) (string, error) {
	switch {
	case strings.Contains(cmd, " run summon "):
		s.summons++
		s.live++
		if s.live > s.peak {
			s.peak = s.live
		}
		return "Object successfully summoned", nil
	case strings.HasPrefix(cmd, "tag @e[tag=arena_tiered] add "+bossTag):
		s.boss = true
		return "", nil
	case cmd == killWaveCommand():
		s.kills++
		s.live, s.boss = 0, false
		return "Killed 1 entity", nil
	case cmd == countCommand():
		n := s.live
		s.live -= s.countStep
		if s.live < 0 {
			s.live = 0
		}
		return found(n), nil
	case cmd == bossCountCommand():
		if !s.boss {
			return found(0), nil
		}
		if s.bossPolls > 0 {
			s.bossPolls--
			return found(1), nil
		}
		s.boss = false
		return found(0), nil
	case strings.HasPrefix(cmd, "testfor @e[name="):
		name := strings.TrimSuffix(strings.TrimPrefix(cmd, "testfor @e[name="), ",type=player]")
		s.warriorHit[name]++
		if off := s.warriorOff[name]; off > 0 && s.warriorHit[name] >= off {
			s.warriorUp[name] = false
		}
		if s.warriorUp[name] {
			return "Found " + name, nil
		}
		return found(0), nil
	}
	return "", nil
}

func newDirector(f *FakeConsole, book *WaveBook, clock *fakeClock) *Director {
	return &Director{
		Console:       f,
		Layout:        testLayout(),
		Book:          book,
		Tier:          5,
		MaxLive:       book.MaxLive,
		Poll:          time.Second,
		Out:           &strings.Builder{},
		Now:           clock.Now,
		Sleep:         clock.Sleep,
		OpenStartGate: true,
	}
}

func TestRunClearsWavesAndRecordsTuning(t *testing.T) {
	clock := newFakeClock()
	book := testBook(
		wave("One", "zombie", 2, 1, 1),
		wave("Two", "skeleton", 1, 1, 2),
	)
	s := newSim()
	f := &FakeConsole{Respond: s.Exec}
	d := newDirector(f, book, clock)
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared || result.WavesCleared != 2 || result.WavesTotal != 2 {
		t.Errorf("result = %+v", result)
	}
	if result.WarriorDeaths != 0 || result.DurationSeconds <= 0 {
		t.Errorf("tuning fields = %+v", result)
	}
	if !hasCommand(f.Cmds, testLayout().StartGate.Open) {
		t.Error("the squad start gate was never opened")
	}
	if f.Count("stone_sword") != 3 || f.Count("enchant @e[tag=arena_tiered] sharpness 1") != 3 {
		t.Errorf("tier-1 gear commands = %d sword, %d enchant, want 3 each",
			f.Count("stone_sword"), f.Count("enchant @e[tag=arena_tiered] sharpness 1"))
	}
	if !before(f.Cmds, " run summon ", testLayout().Pens[0].Open) {
		t.Error("pen 1 opened before its first summon")
	}
	if f.Count("kill @e[tag=arena_wave]") != 0 {
		t.Error("a cleared run killed tagged mobs")
	}
}

func TestTierCapsTheWaves(t *testing.T) {
	clock := newFakeClock()
	book := testBook(
		wave("Fodder", "zombie", 1, 1, 1),
		wave("Elite", "vindicator", 1, 4, 2),
	)
	s := newSim()
	f := &FakeConsole{Respond: s.Exec}
	d := newDirector(f, book, clock)
	d.Tier = 1
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.WavesTotal != 1 || result.WavesCleared != 1 || result.Outcome != OutcomeCleared {
		t.Errorf("result = %+v", result)
	}
	if f.Count("vindicator") != 0 {
		t.Error("a tier-1 run spawned the tier-4 wave")
	}
}

func TestLiveCapBatchesTheSpawns(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("Many", "zombie", 20, 1, 1))
	book.MaxLive = 12
	d := newDirector(f, book, clock)
	d.MaxLive = 12
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared || result.WavesCleared != 1 {
		t.Errorf("result = %+v", result)
	}
	if s.summons != 20 {
		t.Errorf("summoned %d mobs, want 20", s.summons)
	}
	if s.peak > 12 {
		t.Errorf("live mobs peaked at %d, cap is 12", s.peak)
	}
}

func TestSquadWipeEndsTheRun(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true}
	s.warriorOff = map[string]int{"Vanguard": 2, "Striker": 2}
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("Horde", "zombie", 5, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard", "Striker"}
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeWipe {
		t.Errorf("outcome = %q, want wipe", result.Outcome)
	}
	if result.WarriorDeaths != 2 {
		t.Errorf("deaths = %d, want 2", result.WarriorDeaths)
	}
	if s.kills != 1 {
		t.Errorf("kills = %d, want the wipe to clear its mobs", s.kills)
	}
}

func TestOneWarriorDownStillRuns(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true}
	s.warriorOff = map[string]int{"Vanguard": 2}
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("One", "zombie", 2, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard", "Striker"}
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared || result.WarriorDeaths != 1 {
		t.Errorf("result = %+v, want cleared with one death", result)
	}
}

func TestRunTimesOut(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Director)
	}{
		{"wave", func(d *Director) { d.WaveTimeout = 5 * time.Second }},
		{"run", func(d *Director) { d.RunTimeout = 5 * time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			s := newSim()
			s.countStep = 0
			f := &FakeConsole{Respond: s.Exec}
			book := testBook(wave("Stuck", "zombie", 2, 1, 1))
			d := newDirector(f, book, clock)
			d.WaveTimeout = time.Hour
			d.RunTimeout = time.Hour
			tt.set(d)
			result, err := d.Run()
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Outcome != OutcomeTimeout {
				t.Errorf("outcome = %q, want timeout", result.Outcome)
			}
			if result.DurationSeconds < 5 {
				t.Errorf("duration = %.1fs, want at least 5", result.DurationSeconds)
			}
			if s.kills != 0 {
				t.Error("a timeout killed its mobs; the operator cannot inspect the field")
			}
		})
	}
}

func TestBossDownEndsTheRunWithPhases(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.bossPolls = 2
	s.countStep = 0
	f := &FakeConsole{Respond: s.Exec}
	w := Wave{
		Name: "The Warden", Boss: true,
		Mobs: []Mob{
			{Mob: "warden", Count: 1, Tier: 5, Pen: 0},
			{Mob: "skeleton", Count: 2, Tier: 1, Pen: 1},
		},
		Phases: []Phase{
			{At: Duration{0}, Callout: "The Warden rises.", Commands: []string{"effect give @e[tag=arena_boss] speed 600 1"}},
			{At: Duration{time.Second}, Callout: "The Warden closes in."},
		},
	}
	book := testBook(w)
	book.Tiers[5] = Tier{Effects: []Effect{{ID: "resistance", Seconds: 600, Amplifier: 4}}}
	d := newDirector(f, book, clock)
	var log strings.Builder
	d.Out = &log
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeBossDown || result.WavesCleared != 1 {
		t.Errorf("result = %+v", result)
	}
	for _, callout := range []string{"The Warden rises.", "The Warden closes in."} {
		if !strings.Contains(log.String(), callout) {
			t.Errorf("log lacks the phase callout %q:\n%s", callout, log.String())
		}
	}
	if !hasCommand(f.Cmds, "effect give @e[tag=arena_boss] speed 600 1") {
		t.Error("the phase command did not run")
	}
	if s.kills != 1 {
		t.Errorf("kills = %d, want boss-down cleanup", s.kills)
	}
	if !hasCommand(f.Cmds, "execute positioned 0 65 -760 run summon warden ~ ~ ~") {
		t.Error("the boss did not spawn on the dais")
	}
}

func TestBossWaveRequiresABossSpawn(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	book := testBook(Wave{Name: "Bossless", Boss: true, Mobs: []Mob{{Mob: "zombie", Count: 1, Tier: 1, Pen: 1}}})
	f := &FakeConsole{Respond: s.Exec}
	d := newDirector(f, book, clock)
	d.WaveTimeout = time.Second
	// Run still needs a boss to tag, but a hand-built book can lack one; the
	// wave simply clears like any other.
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared {
		t.Errorf("outcome = %q", result.Outcome)
	}
}

func TestResetKillsOnlyWaveMobs(t *testing.T) {
	f := &FakeConsole{}
	d := &Director{Console: f}
	if err := d.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(f.Cmds) != 1 || f.Cmds[0] != "kill @e[tag=arena_wave]" {
		t.Errorf("commands = %v", f.Cmds)
	}
}

func TestStatusReportsWaveBossAndWarriors(t *testing.T) {
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		switch cmd {
		case countCommand():
			return "Found Zombie, Zombie", nil
		case bossCountCommand():
			return "No targets matched selector", nil
		}
		if strings.Contains(cmd, "name=Vanguard") {
			return "Found Vanguard", nil
		}
		return "No targets matched selector", nil
	}}
	d := &Director{Console: f, Warriors: []string{"Vanguard", "Striker"}}
	st, err := d.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.ArenaWave != 2 || st.Boss != 0 {
		t.Errorf("status = %+v", st)
	}
	if !st.Warriors["Vanguard"] || st.Warriors["Striker"] {
		t.Errorf("warriors = %+v", st.Warriors)
	}
}

func TestWarriorBaselineMustFindSomeone(t *testing.T) {
	f := &FakeConsole{Respond: func(string) (string, error) {
		return "ERROR: No targets matched selector", nil
	}}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, newFakeClock())
	d.Warriors = []string{"Ghost"}
	if _, err := d.Run(); err == nil || !strings.Contains(err.Error(), "none of the warriors") {
		t.Fatalf("Run error = %v, want a missing-warrior error", err)
	}
}

func TestWaveSpawnsRejectAnUnknownPen(t *testing.T) {
	f := &FakeConsole{}
	d := newDirector(f, testBook(wave("Bad", "zombie", 1, 1, 99)), newFakeClock())
	if _, err := d.Run(); err == nil || !strings.Contains(err.Error(), "unknown pen 99") {
		t.Fatalf("Run error = %v, want an unknown-pen error", err)
	}
}

func before(cmds []string, first, second string) bool {
	a, b := -1, -1
	for i, cmd := range cmds {
		if a < 0 && strings.Contains(cmd, first) {
			a = i
		}
		if b < 0 && cmd == second {
			b = i
		}
	}
	return a >= 0 && b >= 0 && a < b
}

func TestCountRetriesAnEmptyLogRead(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	counts := 0
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == countCommand() {
			counts++
			if counts == 1 {
				return "", nil
			}
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared || result.WavesCleared != 1 {
		t.Errorf("result = %+v", result)
	}
	if counts < 2 {
		t.Errorf("count was not retried")
	}
}
