package arena

import (
	"strings"
	"testing"
	"time"
)

func TestWarriorChecksNeedAFloor(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	f := &FakeConsole{Respond: s.Exec}
	d := &Director{
		Console: f, Layout: testLayout(), Warriors: []string{"Vanguard"},
		PlaceSquad: true, WarriorMisses: 3, Out: &strings.Builder{}, Now: clock.Now,
	}
	states := map[string]*warriorState{"Vanguard": {up: true, live: true}}
	var result Result
	check := func() {
		t.Helper()
		if _, err := d.checkWarriors(states, &result); err != nil {
			t.Fatalf("checkWarriors: %v", err)
		}
	}
	check()
	s.place("Vanguard", 14, 65, -760)
	check()
	clock.Sleep(6 * time.Second)
	s.place("Vanguard", -20, 65, -760)
	check()
	if result.WarriorDeaths != 0 {
		t.Errorf("deaths without a floor = %d, want none from normal movement", result.WarriorDeaths)
	}
	if got := f.Count(`tp "Vanguard"`); got != 0 {
		t.Errorf("warrior teleports without a floor = %d, want none:\n%s", got, strings.Join(f.Cmds, "\n"))
	}
}

func TestSquadPlacementIsSkippedWithoutAFloor(t *testing.T) {
	clock := newFakeClock()
	s := newSim()
	s.warriorUp = map[string]bool{"Vanguard": true}
	s.place("Vanguard", 0, 65, -760)
	f := &FakeConsole{Respond: s.Exec}
	book := testBook(wave("One", "zombie", 1, 1, 1))
	d := newDirector(f, book, clock)
	d.Warriors = []string{"Vanguard"}
	d.PlaceSquad = true
	var log strings.Builder
	d.Out = &log
	if _, err := d.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.Count(`tp "`); got != 0 {
		t.Errorf("placements without a floor = %d, want none", got)
	}
	if got := strings.Count(log.String(), "skipping squad placement"); got != 1 {
		t.Errorf("skip log lines = %d, want 1:\n%s", got, log.String())
	}
}
