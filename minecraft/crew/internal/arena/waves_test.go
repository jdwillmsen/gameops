package arena

import (
	"strings"
	"testing"
	"time"
)

const minimalWaves = `pause: 2s
max_live: 6
wave_timeout: 1m
tiers:
  1: {}
  2: {}
  3: {}
  4: {}
  5: {}
waves:
  - name: One
    mobs:
      - {mob: zombie, count: 2, tier: 1, pen: 1}
`

func TestDefaultWavesShipFiveTiersAndTheGauntlet(t *testing.T) {
	book, err := DefaultWaves()
	if err != nil {
		t.Fatalf("DefaultWaves: %v", err)
	}
	if len(book.Tiers) != 5 {
		t.Errorf("%d tiers, want 5", len(book.Tiers))
	}
	for tier := 1; tier <= 5; tier++ {
		if _, ok := book.Tiers[tier]; !ok {
			t.Errorf("tier %d is missing", tier)
		}
	}
	if len(book.Waves) != 9 {
		t.Errorf("%d waves, want 9", len(book.Waves))
	}
	last := book.Waves[len(book.Waves)-1]
	if !last.Boss {
		t.Error("the last Gauntlet wave is not a boss wave")
	}
	if last.MaxTier() != 5 {
		t.Errorf("boss wave tier = %d, want 5", last.MaxTier())
	}
	if book.MaxLive != 12 {
		t.Errorf("max_live = %d, want 12", book.MaxLive)
	}
	for i := 1; i < len(book.Waves); i++ {
		if book.Waves[i].MaxTier() < book.Waves[i-1].MaxTier() {
			t.Errorf("wave %d (%s) drops below the previous tier", i+1, book.Waves[i].Name)
		}
	}
}

func TestLoadWavesValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"valid", minimalWaves, ""},
		{"missing tier", strings.Replace(minimalWaves, "  5: {}\n", "", 1), "tier 5 has no recipe"},
		{"zero count", strings.Replace(minimalWaves, "count: 2", "count: 0", 1), "count must be positive"},
		{"tier out of range", strings.Replace(minimalWaves, "tier: 1", "tier: 6", 1), "outside 1..5"},
		{"negative pen", strings.Replace(minimalWaves, "pen: 1", "pen: -1", 1), "pen must not be negative"},
		{"bad duration", strings.Replace(minimalWaves, "pause: 2s", "pause: soon", 1), "invalid duration"},
		{"unknown field", strings.Replace(minimalWaves, "  - name: One", "  - name: One\n    bogus: true", 1), "field bogus not found"},
		{"bad variant", strings.Replace(minimalWaves, "pen: 1}", "variant: become_drowned, pen: 1}", 1), "must start with minecraft:"},
		{"backwards phases", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 10s, callout: two}\n      - {at: 5s, callout: one}\n    mobs:\n", 1), "goes backwards"},
		{"boss without boss mob", strings.Replace(minimalWaves, "  - name: One", "  - name: One\n    boss: true", 1), "has no ravager"},
		{"player mob", strings.Replace(minimalWaves, "mob: zombie", "mob: player", 1), "not a wave mob"},
		{"item mob", strings.Replace(minimalWaves, "mob: zombie", "mob: item", 1), "not a wave mob"},
		{"uppercase mob", strings.Replace(minimalWaves, "mob: zombie", "mob: Zombie", 1), "lowercase identifier"},
		{"bad effect id", strings.Replace(minimalWaves, "  1: {}", "  1:\n    effects:\n      - {id: \"strength 1\", seconds: 600}", 1), "lowercase identifier"},
		{"bad gear item", strings.Replace(minimalWaves, "  1: {}", "  1:\n    gear:\n      - {slot: weapon.mainhand, item: \"stone sword\"}", 1), "lowercase identifier"},
		{"phase verb", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 1s, commands: [\"say hello\"]}\n    mobs:\n", 1), `verb "say"`},
		{"phase teleport", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 1s, commands: [\"tp @e[tag=arena_wave] @a\"]}\n    mobs:\n", 1), `verb "tp"`},
		{"phase selector outside the arena", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 1s, commands: [\"effect @e[type=zombie] speed 600 1\"]}\n    mobs:\n", 1), "tag=arena_"},
		{"phase effect give", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 1s, commands: [\"effect give @e[tag=arena_boss] speed 600 1\"]}\n    mobs:\n", 1), "no give form"},
		{"phase kill without an arena tag", strings.Replace(minimalWaves, "    mobs:\n", "    phases:\n      - {at: 1s, commands: [\"kill @e[type=zombie]\"]}\n    mobs:\n", 1), "tag=arena_"},
		{"negative rally_after", strings.Replace(minimalWaves, "pause: 2s\n", "pause: 2s\nrally_after: -1s\n", 1), "rally_after must not be negative"},
		{"negative rally_every", strings.Replace(minimalWaves, "pause: 2s\n", "pause: 2s\nrally_after: 5s\nrally_every: -1s\n", 1), "rally_every must not be negative"},
		{"zero rally_every with rallies on", strings.Replace(minimalWaves, "pause: 2s\n", "pause: 2s\nrally_after: 5s\nrally_every: 0s\n", 1), "rally_every must be positive"},
		{"bad slot", strings.Replace(minimalWaves, "  1: {}", "  1:\n    gear:\n      - {slot: hotbar, item: stone_sword}", 1), "gear slot"},
		{"bad amplifier", strings.Replace(minimalWaves, "  1: {}", "  1:\n    effects:\n      - {id: strength, seconds: 600, amplifier: 300}", 1), "amplifier 300"},
		{"bad sharpness", strings.Replace(minimalWaves, "  1: {}", "  1:\n    enchant: {id: sharpness, level: 9}", 1), "sharpness level 9"},
		{"no waves", strings.Replace(minimalWaves, "waves:\n  - name: One\n    mobs:\n      - {mob: zombie, count: 2, tier: 1, pen: 1}\n", "waves: []\n", 1), "no waves defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadWaves(strings.NewReader(tt.yaml))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadWaves: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadWaves error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestUpToCapsByOpposition(t *testing.T) {
	book, err := DefaultWaves()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ tier, waves int }{
		{0, 9}, {1, 2}, {2, 3}, {3, 5}, {4, 7}, {5, 9},
	}
	for _, tt := range tests {
		if got := len(book.UpTo(tt.tier)); got != tt.waves {
			t.Errorf("UpTo(%d) = %d waves, want %d", tt.tier, got, tt.waves)
		}
	}
}

func TestWavePauseCanBeExplicitlyZero(t *testing.T) {
	book, err := LoadWaves(strings.NewReader(strings.Replace(minimalWaves,
		"  - name: One", "  - name: One\n    pause: 0s", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if book.Waves[0].Pause == nil || book.Waves[0].Pause.Duration != 0 {
		t.Errorf("pause = %v, want an explicit zero", book.Waves[0].Pause)
	}
}

func TestWaveBookRallyDefaults(t *testing.T) {
	book, err := LoadWaves(strings.NewReader(minimalWaves))
	if err != nil {
		t.Fatal(err)
	}
	if book.RallyAfter == nil || book.RallyAfter.Duration != 20*time.Second {
		t.Errorf("rally_after = %v, want the 20s default", book.RallyAfter)
	}
	if book.RallyEvery == nil || book.RallyEvery.Duration != 25*time.Second {
		t.Errorf("rally_every = %v, want the 25s default", book.RallyEvery)
	}
}

func TestWaveBookRallyFields(t *testing.T) {
	doc := strings.Replace(minimalWaves, "pause: 2s\n", "pause: 2s\nrally_after: 0s\nrally_every: 9s\n", 1)
	doc = strings.Replace(doc, "  - name: One", "  - name: One\n    rally: true", 1)
	book, err := LoadWaves(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if book.RallyAfter.Duration != 0 {
		t.Errorf("rally_after = %v, want an explicit zero", book.RallyAfter.Duration)
	}
	if book.RallyEvery.Duration != 9*time.Second {
		t.Errorf("rally_every = %v, want 9s", book.RallyEvery.Duration)
	}
	if !book.Waves[0].Rally {
		t.Error("wave rally flag was not parsed")
	}
}

func TestWaveRallyRules(t *testing.T) {
	tests := []struct {
		name string
		wave Wave
		want bool
	}{
		{"ordinary waves rally", Wave{}, true},
		{"boss waves stay put", Wave{Boss: true}, false},
		{"boss waves rally on request", Wave{Boss: true, Rally: true}, true},
	}
	for _, tt := range tests {
		if got := tt.wave.rallies(); got != tt.want {
			t.Errorf("%s: rallies = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCustomMobValidation(t *testing.T) {
	const brute = "gameops:training_dummy_brute"
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"namespaced mob", strings.Replace(minimalWaves, "mob: zombie", "mob: "+brute, 1), ""},
		{"empty namespace", strings.Replace(minimalWaves, "mob: zombie", `mob: ":brute"`, 1), "namespace:name"},
		{"empty name", strings.Replace(minimalWaves, "mob: zombie", `mob: "gameops:"`, 1), "namespace:name"},
		{"two colons", strings.Replace(minimalWaves, "mob: zombie", `mob: "a:b:c"`, 1), "namespace:name"},
		{"custom variant", strings.Replace(strings.Replace(minimalWaves, "mob: zombie", "mob: "+brute, 1), "pen: 1}", "variant: minecraft:charged_creeper, pen: 1}", 1), "cannot take a spawn-event variant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadWaves(strings.NewReader(tt.yaml))
			if tt.wantErr == "" && err != nil {
				t.Fatalf("LoadWaves: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("LoadWaves error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCustomBruteWaveBookLoads(t *testing.T) {
	book, err := LoadWaveFile("../../lab/waves-custom-brute.yaml")
	if err != nil {
		t.Fatalf("LoadWaveFile: %v", err)
	}
	if len(book.Waves) != 1 || !customMob(book.Waves[0].Mobs[0].Mob) {
		t.Fatalf("unexpected waves: %+v", book.Waves)
	}
}
