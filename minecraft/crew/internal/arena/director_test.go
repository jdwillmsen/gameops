package arena

import (
	"fmt"
	"math"
	"strconv"
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
		Center:    Point{X: 0, Y: 65, Z: -760},
		Areas:     []Area{{X0: -16, Z0: -800, X1: 31, Z1: -696}},
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
		RallyAfter:  &Duration{0},
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
	warriorPos map[string]TargetPosition
}

func newSim() *sim {
	return &sim{
		countStep:  1,
		warriorUp:  map[string]bool{},
		warriorHit: map[string]int{},
		warriorOff: map[string]int{},
		warriorPos: map[string]TargetPosition{},
	}
}

// place puts a warrior at a queried position; a name without one answers the
// querytarget command with an empty log, as a missed read does.
func (s *sim) place(name string, x, y, z float64) {
	s.warriorPos[name] = TargetPosition{X: x, Y: y, Z: z}
}

func targetJSON(p TargetPosition) string {
	return fmt.Sprintf(`[{"position":{"x":%g,"y":%g,"z":%g}}]`, p.X, p.Y, p.Z)
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
	case strings.HasPrefix(cmd, "querytarget @a[name="):
		p, ok := s.warriorPos[queryName(cmd)]
		if !ok {
			return "", nil
		}
		return targetJSON(p), nil
	case strings.HasPrefix(cmd, "testfor @e[name="):
		name := warriorName(cmd)
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
	if f.Count("kill @e[tag=arena_wave]") != 1 {
		t.Errorf("wave kills = %d, want one cleanup sweep", f.Count("kill @e[tag=arena_wave]"))
	}
	if f.Count("tickingarea remove crew-arena-run-0") != 1 {
		t.Error("a cleared run left its ticking area behind")
	}
}

func TestRunLoadsTheLayoutAreasBeforeSpawning(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	if _, err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	add := "tickingarea add -16 0 -800 31 0 -696 crew-arena-run-0"
	if !hasCommand(f.Cmds, add) {
		t.Fatalf("run did not load its arena area:\n%s", strings.Join(f.Cmds, "\n"))
	}
	if !before(f.Cmds, add, "execute positioned 10 64 -760 run summon zombie ~ ~ ~") {
		t.Error("the arena area was loaded after the first summon")
	}
	if f.Count("tickingarea remove crew-arena-run-0") != 1 {
		t.Errorf("area removals = %d, want 1", f.Count("tickingarea remove crew-arena-run-0"))
	}
}

func TestSummonFailureAbortsAsAnError(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.Contains(cmd, " run summon ") {
			return "Unable to summon object", nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	result, err := d.Run()
	if err == nil || !strings.Contains(err.Error(), "Unable to summon") {
		t.Fatalf("Run error = %v, want a summon failure", err)
	}
	if result.Outcome != OutcomeError {
		t.Errorf("outcome = %q, want %q", result.Outcome, OutcomeError)
	}
	if result.WavesCleared != 0 {
		t.Errorf("waves cleared = %d, want 0", result.WavesCleared)
	}
	if s.kills != 1 {
		t.Errorf("kills = %d, want an error to clean up its mobs", s.kills)
	}
	pen := testLayout().Pens[0]
	if f.Count(penCloseCommand(pen)) != 3 {
		t.Error("an errored run left its pen open")
	}
	if f.Count("tickingarea remove crew-arena-run-0") != 1 {
		t.Error("an errored run left its ticking area behind")
	}
}

func TestEmptyBatchAfterSummonAbortsAsAnError(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if cmd == countCommand() {
			return found(0), nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	result, err := d.Run()
	if err == nil || !strings.Contains(err.Error(), "counted none") {
		t.Fatalf("Run error = %v, want an empty-batch error", err)
	}
	if result.Outcome != OutcomeError {
		t.Errorf("outcome = %q, want %q", result.Outcome, OutcomeError)
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

func warriorName(cmd string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(cmd, "testfor @e[name="), ",type=player]")
	return strings.Trim(name, `"`)
}

func TestRespawnGapDoesNotCountADeath(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	calls := 0
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=") {
			calls++
			if calls >= 2 && calls <= 3 {
				return found(0), nil
			}
			return "Found Vanguard", nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 4, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard"}
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeCleared || result.WarriorDeaths != 0 {
		t.Errorf("result = %+v, want cleared with no death across the respawn gap", result)
	}
}

func TestRepeatDeathsAreCounted(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	calls := 0
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=") {
			if warriorName(cmd) != "Vanguard" {
				return "Found Second", nil
			}
			calls++
			if (calls >= 2 && calls <= 4) || (calls >= 6 && calls <= 8) {
				return found(0), nil
			}
			return "Found Vanguard", nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 2, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard", "Second"}
	d.WaveTimeout = 10 * time.Second
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeTimeout {
		t.Errorf("outcome = %q, want timeout", result.Outcome)
	}
	if result.WarriorDeaths != 2 {
		t.Errorf("deaths = %d, want both deaths counted", result.WarriorDeaths)
	}
}

func TestWipeNeedsEveryWarriorDownAtOnce(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	calls := map[string]int{}
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=") {
			name := warriorName(cmd)
			calls[name]++
			switch name {
			case "Vanguard":
				if calls[name] >= 2 && calls[name] <= 4 {
					return found(0), nil
				}
			case "Striker":
				if calls[name] >= 7 && calls[name] <= 9 {
					return found(0), nil
				}
			}
			return "Found " + name, nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 2, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard", "Striker"}
	d.WaveTimeout = 10 * time.Second
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Outcome != OutcomeTimeout {
		t.Errorf("outcome = %q, want timeout; a wipe needs both down together", result.Outcome)
	}
	if result.WarriorDeaths != 2 {
		t.Errorf("deaths = %d, want each warrior's death once", result.WarriorDeaths)
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
			if s.kills != 1 {
				t.Errorf("kills = %d, want a timeout to clean up its mobs", s.kills)
			}
			pen := testLayout().Pens[0]
			if !afterAll(f.Cmds, pen.Open, penCloseCommand(pen)) {
				t.Error("a timeout left its pen open")
			}
			if f.Count("tickingarea remove crew-arena-run-0") != 1 {
				t.Error("a timeout left its ticking area behind")
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
		if strings.Contains(cmd, `name="Vanguard"`) {
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

func afterAll(cmds []string, first, second string) bool {
	a, b := -1, -1
	for i, cmd := range cmds {
		if cmd == first {
			a = i
		}
		if cmd == second {
			b = i
		}
	}
	return a >= 0 && b >= 0 && b > a
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

// rallySim keeps a wave alive and records when the director teleports it.
func rallySim(t *testing.T, d *Director, clock *fakeClock, s *sim) (*FakeConsole, []time.Duration, Result) {
	t.Helper()
	start := clock.Now()
	var at []time.Duration
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.Contains(cmd, " run tp ") {
			at = append(at, clock.Now().Sub(start))
		}
		return s.Exec(cmd)
	}}
	d.Console = f
	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return f, at, result
}

func rallyBook(w Wave) *WaveBook {
	book := testBook(w)
	book.RallyAfter = &Duration{20 * time.Second}
	book.RallyEvery = &Duration{25 * time.Second}
	return book
}

func TestRallyStartsAfterRallyAfterAndRepeats(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 60 * time.Second
	var log strings.Builder
	d.Out = &log

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 2 {
		t.Fatalf("rallies at %v, want two (20s and 45s)", at)
	}
	if at[0] != 20*time.Second || at[1] != 45*time.Second {
		t.Errorf("rally times = %v, want 20s then 45s", at)
	}
	if got := result.Rallies; got != 2 {
		t.Errorf("result rallies = %d, want 2", got)
	}
	if !strings.Contains(log.String(), "arena: rally 2 mobs") {
		t.Errorf("log lacks the rally line:\n%s", log.String())
	}
	want := []string{
		`execute as @e[tag=arena_wave] at @a[name="Vanguard"] run tp @s ~8 ~ ~0`,
		`execute as @e[tag=arena_wave] at @a[name="Vanguard"] run tp @s ~-8 ~ ~0`,
	}
	if got := tpCommands(t, f.Cmds); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rally commands = %v, want %v", got, want)
	}
}

func TestRallyNotIssuedAfterTheWaveClears(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true}
	book := rallyBook(wave("Quick", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{20 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 0 {
		t.Errorf("rallies at %v, want none after the wave cleared", at)
	}
	if got := result.Rallies; got != 0 {
		t.Errorf("result rallies = %d, want 0", got)
	}
	if n := len(tpCommands(t, f.Cmds)); n != 0 {
		t.Errorf("teleports = %d, want 0", n)
	}
}

func TestRallySkippedWhenNoWarriorIsAlive(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.warriorOff = map[string]int{"Vanguard": 2}
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{5 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WarriorMisses = 100
	d.WaveTimeout = 15 * time.Second

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 0 {
		t.Errorf("rallies at %v, want none with the squad down", at)
	}
	if result.Rallies != 0 || len(tpCommands(t, f.Cmds)) != 0 {
		t.Errorf("rallies = %d, teleports = %d, want none", result.Rallies, len(tpCommands(t, f.Cmds)))
	}
}

func TestRallyCanBeDisabled(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{0}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 10 * time.Second

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 0 || result.Rallies != 0 || len(tpCommands(t, f.Cmds)) != 0 {
		t.Errorf("rallies = %v (%d), teleports = %d, want none", at, result.Rallies, len(tpCommands(t, f.Cmds)))
	}
}

func TestRallyRunRequiresWarriors(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	f := &FakeConsole{Respond: s.Exec}
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = nil

	_, err := d.Run()
	if err == nil || !strings.Contains(err.Error(), "warrior") {
		t.Fatalf("Run error = %v, want a missing-warrior error", err)
	}
	if len(f.Cmds) != 0 {
		t.Errorf("a rejected run ran %d commands, want none before spawning:\n%s",
			len(f.Cmds), strings.Join(f.Cmds, "\n"))
	}
}

func TestBossWaveOnlyRalliesWhenTheBookSaysSo(t *testing.T) {
	tests := []struct {
		name  string
		rally bool
		want  int
	}{
		{"default boss", false, 0},
		{"boss rally", true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			s := newSim()
			s.countStep = 0
			s.bossPolls = 1000
			s.warriorUp = map[string]bool{"Vanguard": true}
			s.place("Vanguard", 0, 65, -760)
			w := Wave{
				Name: "Boss", Boss: true, Rally: tt.rally,
				Mobs: []Mob{{Mob: "warden", Count: 1, Tier: 5, Pen: 0}},
			}
			book := rallyBook(w)
			book.RallyAfter = &Duration{5 * time.Second}
			d := newDirector(nil, book, clock)
			d.Warriors = []string{"Vanguard"}
			d.WaveTimeout = 10 * time.Second

			f, at, result := rallySim(t, d, clock, s)
			if len(at) != tt.want {
				t.Errorf("rallies at %v, want %d", at, tt.want)
			}
			if result.Rallies != tt.want {
				t.Errorf("result rallies = %d, want %d", result.Rallies, tt.want)
			}
			if got := len(tpCommands(t, f.Cmds)); got != tt.want {
				t.Errorf("teleports = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestRallyFallbackStaysInsideTheFloor covers an anchor too close to the edge
// for any fixed offset: the survivors go towards the centre instead, clamped
// so the teleport cannot land in the stands.
func TestRallyFallbackStaysInsideTheFloor(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 3, 65, -760)
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{5 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 10 * time.Second
	d.Layout.FloorRadius = 4

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 1 || result.Rallies != 1 {
		t.Fatalf("rallies at %v (%d), want the fallback to fire once", at, result.Rallies)
	}
	want := "execute as @e[tag=arena_wave] run tp @s -2 65 -760"
	if got := tpCommands(t, f.Cmds); len(got) != 1 || got[0] != want {
		t.Errorf("fallback teleport = %v, want %q", got, want)
	}
}

func TestRallyDestinationStaysInsideTheFloor(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 40, 65, -760)
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{5 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 10 * time.Second
	d.Layout.FloorRadius = 45

	f, at, result := rallySim(t, d, clock, s)
	if len(at) == 0 || result.Rallies != len(at) {
		t.Fatalf("rallies at %v (%d), want at least one", at, result.Rallies)
	}
	for _, cmd := range tpCommands(t, f.Cmds) {
		x, z := tpDestination(t, cmd, 40, -760)
		if dist := math.Hypot(x, z+760); dist > 43 {
			t.Errorf("destination %.1f,%.1f is %.1f from the centre, want at most 43: %q", x, z, dist, cmd)
		}
	}
	if got := tpCommands(t, f.Cmds)[0]; got != `execute as @e[tag=arena_wave] at @a[name="Vanguard"] run tp @s ~-8 ~ ~0` {
		t.Errorf("east-edge rally = %q, want the first offset that fits", got)
	}
}

func TestRallyCentreWarriorUsesTheOffsets(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{5 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 10 * time.Second
	d.Layout.FloorRadius = 45

	f, at, result := rallySim(t, d, clock, s)
	if len(at) != 1 || result.Rallies != 1 {
		t.Fatalf("rallies at %v (%d), want one", at, result.Rallies)
	}
	want := `execute as @e[tag=arena_wave] at @a[name="Vanguard"] run tp @s ~8 ~ ~0`
	if got := tpCommands(t, f.Cmds); len(got) != 1 || got[0] != want {
		t.Errorf("centre rally = %v, want %q", got, want)
	}
}

func TestRallyUnreadablePositionSkips(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.countStep = 0
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	book := rallyBook(wave("Horde", "zombie", 2, 1, 1))
	book.RallyAfter = &Duration{5 * time.Second}
	d := newDirector(nil, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.WaveTimeout = 10 * time.Second
	d.Layout.FloorRadius = 45
	var log strings.Builder
	d.Out = &log
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "querytarget ") {
			return "", nil
		}
		return s.Exec(cmd)
	}}
	d.Console = f

	result, err := d.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Rallies != 0 {
		t.Errorf("result rallies = %d, want 0 when the anchor cannot be read", result.Rallies)
	}
	if got := tpCommands(t, f.Cmds); len(got) != 0 {
		t.Errorf("teleports = %v, want none without an anchor position", got)
	}
	if f.Count(`querytarget @a[name="Vanguard"]`) == 0 {
		t.Error("the director never queried the anchor position")
	}
	if !strings.Contains(log.String(), "rally skipped") {
		t.Errorf("log lacks the skipped-rally line:\n%s", log.String())
	}
}

// tpDestination resolves a rally teleport against the anchor's position so a
// test can check where the mobs actually land.
func tpDestination(t *testing.T, cmd string, anchorX, anchorZ float64) (float64, float64) {
	t.Helper()
	_, rest, ok := strings.Cut(cmd, " tp @s ")
	if !ok {
		t.Fatalf("no tp target in %q", cmd)
	}
	fields := strings.Fields(rest)
	if len(fields) != 3 {
		t.Fatalf("tp target = %q", rest)
	}
	coord := func(s string, base float64) float64 {
		if s == "~" {
			return base
		}
		if rel, ok := strings.CutPrefix(s, "~"); ok {
			v, err := strconv.ParseFloat(rel, 64)
			if err != nil {
				t.Fatalf("coordinate %q: %v", s, err)
			}
			return base + v
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("coordinate %q: %v", s, err)
		}
		return v
	}
	return coord(fields[0], anchorX), coord(fields[2], anchorZ)
}

func queryName(cmd string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(cmd, "querytarget @a[name="), "]")
	return strings.Trim(name, `"`)
}

// tpCommands returns every teleport and fails the test if one moves anything
// but the wave tag; a rally must never name another entity selector.
func tpCommands(t *testing.T, cmds []string) []string {
	t.Helper()
	var out []string
	for _, cmd := range cmds {
		if !strings.Contains(cmd, " run tp ") {
			continue
		}
		if strings.Count(cmd, "@e[") != 1 || !strings.Contains(cmd, "@e[tag=arena_wave]") {
			t.Errorf("rally teleports more than the wave tag: %q", cmd)
		}
		out = append(out, cmd)
	}
	return out
}

// TestSquadPlacedOnTheFloorBeforeWaveOne pins the three start cells: spread
// either side of the centre line, well short of the pens and the gate.
func TestSquadPlacedOnTheFloorBeforeWaveOne(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true, "Ranger": true}
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	d.Layout.GroundY = 64
	d.Warriors = []string{"Vanguard", "Striker", "Ranger"}
	d.PlaceSquad = true
	if _, err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		`tp "Vanguard" 0 65 -785`,
		`tp "Striker" 3 65 -785`,
		`tp "Ranger" -3 65 -785`,
	}
	for _, cmd := range want {
		if !hasCommand(f.Cmds, cmd) {
			t.Errorf("run did not place the squad: missing %q in\n%s", cmd, strings.Join(f.Cmds, "\n"))
		}
		summon := "execute positioned 10 64 -760 run summon zombie ~ ~ ~"
		if !before(f.Cmds, "tickingarea add", cmd) || !before(f.Cmds, cmd, summon) {
			t.Errorf("%q did not run between loading the area and the first summon", cmd)
		}
	}
}

func TestRespawnedWarriorReturnsToItsStartCell(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true}
	vanguardCalls := 0
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=") && warriorName(cmd) == "Vanguard" {
			vanguardCalls++
			if vanguardCalls == 2 {
				return found(0), nil
			}
			return "Found Vanguard", nil
		}
		return s.Exec(cmd)
	}}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	d.Layout.GroundY = 64
	d.Warriors = []string{"Vanguard", "Striker"}
	d.PlaceSquad = true
	if _, err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := `tp "Vanguard" 0 65 -785`
	if got := f.Count(want); got != 2 {
		t.Errorf("Vanguard placements = %d, want start plus respawn:\n%s", got, strings.Join(f.Cmds, "\n"))
	}
	if got := f.Count(`tp "Striker" 3 65 -785`); got != 1 {
		t.Errorf("Striker placements = %d, want only the start placement", got)
	}
}

func TestFarWarriorReturnsToItsStartCell(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	f := &FakeConsole{Respond: s.Exec}
	d := &Director{
		Console: f, Layout: testLayout(), Warriors: []string{"Vanguard"},
		PlaceSquad: true, WarriorMisses: 3, Out: &strings.Builder{}, Now: clock.Now,
	}
	d.Layout.GroundY = 64
	d.Layout.FloorRadius = 45
	states := map[string]*warriorState{"Vanguard": {up: true, live: true}}
	var result Result
	if _, err := d.checkWarriors(states, &result); err != nil {
		t.Fatalf("checkWarriors: %v", err)
	}
	if got := f.Count(`tp "Vanguard"`); got != 0 {
		t.Fatalf("placed a warrior already on the floor %d times", got)
	}
	s.place("Vanguard", 0, 65, -400)
	if _, err := d.checkWarriors(states, &result); err != nil {
		t.Fatalf("checkWarriors: %v", err)
	}
	if got := f.Count(`tp "Vanguard" 0 65 -785`); got != 1 {
		t.Errorf("stray warrior placements = %d, want 1:\n%s", got, strings.Join(f.Cmds, "\n"))
	}
}

func TestPlacedWarriorIsNotReplacedAgainWithinFiveSeconds(t *testing.T) {
	clock := newFakeClock()
	f := &FakeConsole{Respond: func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "testfor @e[name=") {
			return found(1), nil
		}
		return "", nil
	}}
	d := &Director{
		Console: f, Layout: testLayout(), Warriors: []string{"Vanguard"},
		PlaceSquad: true, WarriorMisses: 3, Out: &strings.Builder{}, Now: clock.Now,
	}
	d.Layout.GroundY = 64
	d.Layout.FloorRadius = 45
	states := map[string]*warriorState{"Vanguard": {up: true, live: true}}
	var result Result
	cycle := func() {
		up := false
		f.Respond = func(cmd string) (string, error) {
			if strings.HasPrefix(cmd, "testfor @e[name=") {
				if up {
					return found(1), nil
				}
				return found(0), nil
			}
			return "", nil
		}
		if _, err := d.checkWarriors(states, &result); err != nil {
			t.Fatalf("down poll: %v", err)
		}
		up = true
		if _, err := d.checkWarriors(states, &result); err != nil {
			t.Fatalf("up poll: %v", err)
		}
	}
	cycle()
	placed := f.Count(`tp "Vanguard"`)
	if placed != 1 {
		t.Fatalf("placements after the first respawn = %d, want 1", placed)
	}
	for range 4 {
		clock.Sleep(time.Second)
		cycle()
	}
	if got := f.Count(`tp "Vanguard"`); got != placed {
		t.Errorf("placements within five seconds = %d, want %d", got, placed)
	}
	clock.Sleep(time.Second)
	cycle()
	if got := f.Count(`tp "Vanguard"`); got != placed+1 {
		t.Errorf("placements after five seconds = %d, want %d", got, placed+1)
	}
}

func TestPlaceSquadDisabledLeavesWarriorsAlone(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true, "Ranger": true}
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard", "Striker", "Ranger"}
	var log strings.Builder
	d.Out = &log
	if _, err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.Count(`tp "`); got != 0 {
		t.Errorf("placements = %d, want none when --place-squad=false:\n%s", got, strings.Join(f.Cmds, "\n"))
	}
	if strings.Contains(log.String(), "arena: placed") {
		t.Errorf("log claims a placement with the feature off:\n%s", log.String())
	}
}
