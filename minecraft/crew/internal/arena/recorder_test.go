package arena

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

var recMeta = FightMeta{
	RunID: "r1", Scenario: tactics.ScenarioBoss, Family: tactics.FamilyBoss, Opponent: "warden",
	Profile: "champ-3", Side: "A", Seed: 9, Warriors: []string{"Rusty", "Slate", "Tinder"},
}

func TestRecorderBuildsAScorecardFromEvents(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		events func(r *Recorder)
		want   tactics.Scorecard
	}{
		{
			name:   "quiet win keeps every warrior at zero deaths",
			events: func(r *Recorder) { r.MobsKilled(1) },
			want:   tactics.Scorecard{Kills: 1, Deaths: map[string]int{"Rusty": 0, "Slate": 0, "Tinder": 0}},
		},
		{
			name: "deaths count per unit, repeats included",
			events: func(r *Recorder) {
				r.WarriorDied("Rusty")
				r.WarriorDied("Rusty")
				r.WarriorDied("Slate")
				r.MobsKilled(2)
				r.MobsKilled(0)
				r.MobsKilled(-4)
			},
			want: tactics.Scorecard{Kills: 2, Deaths: map[string]int{"Rusty": 2, "Slate": 1, "Tinder": 0}},
		},
		{
			name:   "a death of an unlisted unit is still kept",
			events: func(r *Recorder) { r.WarriorDied("Guest") },
			want:   tactics.Scorecard{Deaths: map[string]int{"Rusty": 0, "Slate": 0, "Tinder": 0, "Guest": 1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Recorder{Meta: recMeta}
			r.FightStart(at)
			tt.events(r)
			got := r.Finish(tactics.OutcomeWin, 31.5)
			tt.want.TS, tt.want.RunID, tt.want.Scenario = "2026-10-27T12:00:00Z", "r1", tactics.ScenarioBoss
			tt.want.Family, tt.want.Opponent, tt.want.Profile = tactics.FamilyBoss, "warden", "champ-3"
			tt.want.Side, tt.want.Seed, tt.want.Outcome, tt.want.Seconds = "A", 9, tactics.OutcomeWin, 31.5
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scorecard = %+v\nwant        %+v", got, tt.want)
			}
			if got.DamageDealt != 0 || got.DamageTaken != 0 {
				t.Error("no damage source is set, so damage must stay zero")
			}
		})
	}
}

func TestRecorderFightStartForgetsTheLastFight(t *testing.T) {
	r := &Recorder{Meta: recMeta}
	r.FightStart(time.Unix(0, 0))
	r.WarriorDied("Rusty")
	r.MobsKilled(5)
	r.FightStart(time.Unix(100, 0))
	got := r.Finish(tactics.OutcomeTimeout, 1)
	if got.Kills != 0 || got.TotalDeaths() != 0 {
		t.Errorf("scorecard = %+v, want a clean second fight", got)
	}
}

func TestScoreOutcomeMaps(t *testing.T) {
	boss := map[string]string{
		BossKilled: tactics.OutcomeWin, BossWipe: tactics.OutcomeLoss, BossTimeout: tactics.OutcomeTimeout,
		BossAborted: tactics.OutcomeAborted, "": tactics.OutcomeAborted,
	}
	for in, want := range boss {
		if got := BossScoreOutcome(in); got != want {
			t.Errorf("BossScoreOutcome(%q) = %q, want %q", in, got, want)
		}
	}
	run := map[string]string{
		OutcomeCleared: tactics.OutcomeWin, OutcomeBossDown: tactics.OutcomeWin, OutcomeWipe: tactics.OutcomeLoss,
		OutcomeTimeout: tactics.OutcomeTimeout, OutcomeError: tactics.OutcomeAborted, "": tactics.OutcomeAborted,
	}
	for in, want := range run {
		if got := RunScoreOutcome(in); got != want {
			t.Errorf("RunScoreOutcome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMobFamily(t *testing.T) {
	tests := map[string]tactics.Family{
		"zombie": tactics.FamilyZombie, "minecraft:husk": tactics.FamilyZombie,
		"stray": tactics.FamilySkeleton, "creeper": tactics.FamilyCreeper, "cave_spider": tactics.FamilySpider,
		"blaze": tactics.FamilyBlaze, "enderman": tactics.FamilyEnderman, "witch": tactics.FamilyWitch,
		"vindicator": tactics.FamilyIllager, "piglin_brute": tactics.FamilyBrute, "warden": tactics.FamilyBoss,
		"cow": tactics.FamilyDefault,
	}
	for mob, want := range tests {
		if got := MobFamily(mob); got != want {
			t.Errorf("MobFamily(%q) = %q, want %q", mob, got, want)
		}
	}
}

// legacyMobFamilies is what MobFamily answered before it shared the warrior's
// table. Every id it knew keeps its family, except the ravager: the warrior
// fights it as a brute, and the trainer has to train what is later applied.
var legacyMobFamilies = map[string]tactics.Family{
	"zombie": tactics.FamilyZombie, "husk": tactics.FamilyZombie, "drowned": tactics.FamilyZombie,
	"zombie_villager": tactics.FamilyZombie, "zombie_pigman": tactics.FamilyZombie, "zombified_piglin": tactics.FamilyZombie,
	"skeleton": tactics.FamilySkeleton, "stray": tactics.FamilySkeleton,
	"bogged": tactics.FamilySkeleton, "wither_skeleton": tactics.FamilySkeleton,
	"creeper": tactics.FamilyCreeper,
	"spider":  tactics.FamilySpider, "cave_spider": tactics.FamilySpider,
	"blaze":    tactics.FamilyBlaze,
	"enderman": tactics.FamilyEnderman,
	"witch":    tactics.FamilyWitch,
	"pillager": tactics.FamilyIllager, "vindicator": tactics.FamilyIllager,
	"evoker": tactics.FamilyIllager, "illusioner": tactics.FamilyIllager, "ravager": tactics.FamilyIllager,
	"piglin": tactics.FamilyBrute, "piglin_brute": tactics.FamilyBrute, "hoglin": tactics.FamilyBrute, "zoglin": tactics.FamilyBrute,
	"warden": tactics.FamilyBoss, "wither": tactics.FamilyBoss, "ender_dragon": tactics.FamilyBoss, "elder_guardian": tactics.FamilyBoss,
}

func TestMobFamilyKeepsEveryIdItKnewBeforeExceptTheRavager(t *testing.T) {
	for mob, was := range legacyMobFamilies {
		want := was
		if mob == "ravager" {
			want = tactics.FamilyBrute
		}
		for _, id := range []string{mob, "minecraft:" + mob} {
			if got := MobFamily(id); got != want {
				t.Errorf("MobFamily(%q) = %q, want %q", id, got, want)
			}
		}
	}
}

// bossFights runs a boss test against a scripted sim with a Recorder
// attached and returns the boss records with the scorecards they produced.
func bossFights(t *testing.T, s *bossSim, runs int) ([]BossRecord, []tactics.Scorecard) {
	t.Helper()
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Runs = runs
	rec := &Recorder{Meta: recMeta}
	b.Observer = rec
	var cards []tactics.Scorecard
	b.Record = func(r BossRecord) error {
		cards = append(cards, rec.Finish(BossScoreOutcome(r.Outcome), r.Seconds))
		return nil
	}
	recs, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return recs, cards
}

func TestBossFightsProduceScorecards(t *testing.T) {
	tests := []struct {
		name    string
		sim     func(*bossSim)
		outcome string
		kills   int
		deaths  map[string]int
		runs    int
	}{
		{
			name: "kill", sim: func(*bossSim) {}, outcome: tactics.OutcomeWin, kills: 1,
			deaths: map[string]int{"Rusty": 0, "Slate": 0, "Tinder": 0}, runs: 1,
		},
		{
			name: "kill after one warrior died and respawned",
			sim: func(s *bossSim) {
				s.aliveFor = 4
				s.downFrom["Rusty"], s.downUntil["Rusty"] = 3, 7
				s.respawnPos["Rusty"] = TargetPosition{X: 0, Y: 80, Z: 0}
			},
			outcome: tactics.OutcomeWin, kills: 1,
			deaths: map[string]int{"Rusty": 1, "Slate": 0, "Tinder": 0}, runs: 1,
		},
		{
			name: "wipe",
			sim: func(s *bossSim) {
				s.aliveFor = 1000
				for _, n := range []string{"Rusty", "Slate", "Tinder"} {
					s.downFrom[n] = 3
				}
			},
			outcome: tactics.OutcomeLoss, kills: 0,
			deaths: map[string]int{"Rusty": 1, "Slate": 1, "Tinder": 1}, runs: 1,
		},
		{
			name: "timeout", sim: func(s *bossSim) { s.aliveFor = 1000 }, outcome: tactics.OutcomeTimeout, kills: 0,
			deaths: map[string]int{"Rusty": 0, "Slate": 0, "Tinder": 0}, runs: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newBossSim()
			tt.sim(s)
			recs, cards := bossFights(t, s, tt.runs)
			if len(cards) != 1 || len(recs) != 1 {
				t.Fatalf("%d cards for %d records, want one each", len(cards), len(recs))
			}
			c := cards[0]
			if c.Outcome != tt.outcome || c.Kills != tt.kills || !reflect.DeepEqual(c.Deaths, tt.deaths) {
				t.Errorf("card = %+v, want %s, %d kills, deaths %v", c, tt.outcome, tt.kills, tt.deaths)
			}
			if c.Seconds != recs[0].Seconds || c.TS != recs[0].TS {
				t.Errorf("card timing %q %.1f does not follow the boss record %q %.1f", c.TS, c.Seconds, recs[0].TS, recs[0].Seconds)
			}
			if c.TotalDeaths() != recs[0].Deaths["total"] {
				t.Errorf("card deaths %d != boss record total %d", c.TotalDeaths(), recs[0].Deaths["total"])
			}
			if c.Family != tactics.FamilyBoss || c.Opponent != "warden" || c.Profile != "champ-3" || c.Seed != 9 || c.Side != "A" {
				t.Errorf("metadata lost: %+v", c)
			}
		})
	}
}

func TestBossRunsEachGetTheirOwnCounts(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 2
	s.downFrom["Rusty"], s.downUntil["Rusty"] = 2, 3
	f := &FakeConsole{Respond: s.Exec}
	b, _ := newBossTest(f, newFakeClock())
	b.Runs = 2
	rec := &Recorder{Meta: recMeta}
	b.Observer = rec
	var cards []tactics.Scorecard
	b.Record = func(r BossRecord) error {
		cards = append(cards, rec.Finish(BossScoreOutcome(r.Outcome), r.Seconds))
		if r.Run == 1 {
			s.bossPolls = 0
		}
		return nil
	}
	recs, err := b.Run(context.Background())
	if err != nil || len(recs) != 2 || len(cards) != 2 {
		t.Fatalf("recs %d cards %d err %v", len(recs), len(cards), err)
	}
	for i, c := range cards {
		if c.Kills != 1 || c.TotalDeaths() != recs[i].Deaths["total"] {
			t.Errorf("card %d = %+v, want one kill and the boss record's deaths %v", i, c, recs[i].Deaths)
		}
	}
}

func TestAbortedBossFightIsAnAbortedScorecard(t *testing.T) {
	s := newBossSim()
	s.aliveFor = 1000
	f := &FakeConsole{}
	f.Respond = func(cmd string) (string, error) {
		if s.bossPolls == 3 {
			s.list = "There are 4/10 players online:\nRusty, Slate, Tinder, Steve99"
		}
		return s.Exec(cmd)
	}
	b, _ := newBossTest(f, newFakeClock())
	rec := &Recorder{Meta: recMeta}
	b.Observer = rec
	var cards []tactics.Scorecard
	b.Record = func(r BossRecord) error {
		cards = append(cards, rec.Finish(BossScoreOutcome(r.Outcome), r.Seconds))
		return nil
	}
	_, _ = b.Run(context.Background())
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted || cards[0].Kills != 0 {
		t.Errorf("cards = %+v, want one aborted card", cards)
	}
}

func TestDirectorRunProducesScorecards(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s *sim, d *Director)
		outcome string
		kills   int
		deaths  map[string]int
	}{
		{
			name:    "cleared waves count every mob killed",
			setup:   func(s *sim, d *Director) {},
			outcome: tactics.OutcomeWin, kills: 3,
			deaths: map[string]int{"Vanguard": 0, "Striker": 0},
		},
		{
			name: "wipe is a loss with both warriors dead",
			setup: func(s *sim, d *Director) {
				s.countStep = 0
				s.warriorOff = map[string]int{"Vanguard": 2, "Striker": 2}
			},
			outcome: tactics.OutcomeLoss, kills: 0,
			deaths: map[string]int{"Vanguard": 1, "Striker": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newFakeClock()
			s := newSim()
			s.warriorUp = map[string]bool{"Vanguard": true, "Striker": true}
			f := &FakeConsole{Respond: s.Exec}
			book := testBook(wave("One", "zombie", 2, 1, 1), wave("Two", "skeleton", 1, 1, 2))
			d := newDirector(f, book, clock)
			d.Warriors = []string{"Vanguard", "Striker"}
			tt.setup(s, d)
			rec := &Recorder{Meta: FightMeta{RunID: "w1", Scenario: tactics.ScenarioWave, Family: tactics.FamilyDefault,
				Opponent: "gauntlet-t5", Profile: "default", Warriors: d.Warriors}}
			d.Observer = rec
			res, err := d.Run()
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			c := rec.Finish(RunScoreOutcome(res.Outcome), res.DurationSeconds)
			if c.Outcome != tt.outcome || c.Kills != tt.kills || !reflect.DeepEqual(c.Deaths, tt.deaths) {
				t.Errorf("card = %+v, want %s, %d kills, deaths %v (result %+v)", c, tt.outcome, tt.kills, tt.deaths, res)
			}
			if c.TotalDeaths() != res.WarriorDeaths {
				t.Errorf("card deaths %d != result %d", c.TotalDeaths(), res.WarriorDeaths)
			}
			if c.TS != "2026-10-23T12:00:00Z" {
				t.Errorf("TS = %q, want the run's start", c.TS)
			}
		})
	}
}

func TestRecorderCapsKillsAtTheSpawnedCount(t *testing.T) {
	meta := recMeta
	meta.Spawned = 3
	r := &Recorder{Meta: meta}
	r.FightStart(time.Now())
	r.MobsKilled(2)
	r.MobsKilled(4)
	if got := r.Finish(tactics.OutcomeWin, 10).Kills; got != 3 {
		t.Errorf("kills = %d, want the 3 mobs that were spawned", got)
	}

	r = &Recorder{Meta: recMeta}
	r.FightStart(time.Now())
	r.MobsKilled(9)
	if got := r.Finish(tactics.OutcomeWin, 10).Kills; got != 9 {
		t.Errorf("kills with an unknown spawn count = %d, want the inferred 9", got)
	}
}
