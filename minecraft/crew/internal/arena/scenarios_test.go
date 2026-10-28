package arena

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// tacticsFamilies is every tactics.Family constant. The loader's known set
// must match it (minus player), so a family added to tactics without a
// matching scenario-book change fails these tests.
var tacticsFamilies = []tactics.Family{
	tactics.FamilyDefault, tactics.FamilyZombie, tactics.FamilySkeleton,
	tactics.FamilyCreeper, tactics.FamilySpider, tactics.FamilyBlaze,
	tactics.FamilyEnderman, tactics.FamilyWitch, tactics.FamilyIllager,
	tactics.FamilyBrute, tactics.FamilyBoss, tactics.FamilyPlayer,
}

var testTiers = map[int]Tier{1: {}, 2: {}, 3: {}, 4: {}}

const minimalScenarios = `scenarios:
  - name: one
    family: zombie
    tier: 1
    timeout: 1m
    pass: clear
    spawn: {ring: 6, side: north}
    mobs:
      - {mob: zombie, count: 2}
`

const duplicateScenario = `  - name: one
    family: zombie
    tier: 2
    timeout: 1m
    pass: clear
    spawn: {ring: 6, side: south}
    mobs:
      - {mob: husk, count: 1}
`

func TestScenarioFamiliesMatchTactics(t *testing.T) {
	for _, f := range tacticsFamilies {
		_, ok := scenarioFamilies[f]
		if f == tactics.FamilyPlayer {
			if ok {
				t.Errorf("family %q is trainable with summoned mobs", f)
			}
			continue
		}
		if !ok {
			t.Errorf("tactics family %q is not a scenario family", f)
		}
	}
	for f := range scenarioFamilies {
		if !slices.Contains(tacticsFamilies, f) {
			t.Errorf("scenario family %q is not a tactics family", f)
		}
	}
}

func TestDefaultScenariosValidateAndTrainEveryTier(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatalf("DefaultScenarios: %v", err)
	}
	if len(book.Scenarios) == 0 {
		t.Fatal("the shipped book is empty")
	}
	tiers := map[int]bool{}
	mixed := false
	for _, s := range book.Scenarios {
		tiers[s.Tier] = true
		if s.Family == tactics.FamilyDefault {
			mixed = true
		}
		if s.Timeout.Duration <= 0 {
			t.Errorf("scenario %q has no timeout", s.Name)
		}
		if n := s.total(); n < 1 || n > maxScenarioMobs {
			t.Errorf("scenario %q spawns %d mobs", s.Name, n)
		}
	}
	for tier := 1; tier <= 4; tier++ {
		if !tiers[tier] {
			t.Errorf("no scenario trains tier %d", tier)
		}
	}
	if !mixed {
		t.Error("no mixed-pack scenario trains the fallback profile")
	}
}

// A scenario trains the family the warrior will fight its headliner as, or
// the trained profile would never apply to the mob it was trained against.
func TestScenarioFamilyIsTheFamilyOfItsHeadlinerMob(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range book.Scenarios {
		if s.Family == tactics.FamilyDefault {
			continue
		}
		for _, m := range s.Mobs {
			if got := tactics.FamilyOfType(m.Mob); got != s.Family {
				t.Errorf("scenario %q trains family %q but mob %q is fought as %q", s.Name, s.Family, m.Mob, got)
			}
		}
	}
	s, _ := book.Scenario("ravager-charge")
	if s.Family != tactics.FamilyBrute {
		t.Errorf("ravager-charge family = %q, want brute", s.Family)
	}
}

func TestScenarioFamilyMobSetsAgreeWithTheWarriorsFamilyTable(t *testing.T) {
	for fam, mobs := range scenarioFamilies {
		for mob := range mobs {
			if got := tactics.FamilyOfType(mob); got != fam {
				t.Errorf("family %q admits %q, which the warrior fights as %q", fam, mob, got)
			}
		}
	}
}

func TestDefaultScenariosCoverEveryFamily(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatalf("DefaultScenarios: %v", err)
	}
	covered := map[tactics.Family]bool{}
	for _, s := range book.Scenarios {
		covered[s.Family] = true
	}
	for _, f := range tacticsFamilies {
		if f == tactics.FamilyDefault || f == tactics.FamilyPlayer {
			continue
		}
		if !covered[f] {
			t.Errorf("no scenario trains family %q", f)
		}
	}
}

func TestLoadScenariosValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		tiers   map[int]Tier
		wantErr string
	}{
		{"valid", minimalScenarios, testTiers, ""},
		{"default family admits any mob",
			strings.Replace(strings.Replace(minimalScenarios, "family: zombie", "family: default", 1),
				"mob: zombie", "mob: blaze", 1), testTiers, ""},
		{"no scenarios", "scenarios: []\n", testTiers, "no scenarios defined"},
		{"empty name", strings.Replace(minimalScenarios, "name: one", `name: ""`, 1), testTiers, "has no name"},
		{"repeated name", minimalScenarios + duplicateScenario, testTiers, "repeats"},
		{"unknown family", strings.Replace(minimalScenarios, "family: zombie", "family: dragon", 1), testTiers, "unknown family"},
		{"player family", strings.Replace(minimalScenarios, "family: zombie", "family: player", 1), testTiers, "duel"},
		{"tier zero", strings.Replace(minimalScenarios, "tier: 1", "tier: 0", 1), testTiers, "outside 1..4"},
		{"tier five", strings.Replace(minimalScenarios, "tier: 1", "tier: 5", 1), testTiers, "outside 1..4"},
		{"tier without a recipe", minimalScenarios, map[int]Tier{}, "has no recipe"},
		{"zero timeout", strings.Replace(minimalScenarios, "timeout: 1m", "timeout: 0s", 1), testTiers, "timeout must be positive"},
		{"bad duration", strings.Replace(minimalScenarios, "timeout: 1m", "timeout: soon", 1), testTiers, "invalid duration"},
		{"bad pass", strings.Replace(minimalScenarios, "pass: clear", "pass: flee", 1), testTiers, `neither "clear" nor "survive"`},
		{"bad side", strings.Replace(minimalScenarios, "side: north", "side: up", 1), testTiers, "not north, south, east or west"},
		{"ring too small", strings.Replace(minimalScenarios, "ring: 6", "ring: 5", 1), testTiers, "under 6 blocks"},
		{"ring too far", strings.Replace(minimalScenarios, "ring: 6", "ring: 25", 1), testTiers, "over 24 blocks"},
		{"no mobs", strings.Replace(minimalScenarios, "    mobs:\n      - {mob: zombie, count: 2}\n", "", 1), testTiers, "has no mobs"},
		{"count zero", strings.Replace(minimalScenarios, "count: 2", "count: 0", 1), testTiers, "count must be positive"},
		{"too many mobs", strings.Replace(minimalScenarios, "count: 2", "count: 13", 1), testTiers, "at most 12"},
		{"player mob", strings.Replace(minimalScenarios, "mob: zombie", "mob: player", 1), testTiers, "not a summonable scenario mob"},
		{"item mob", strings.Replace(minimalScenarios, "mob: zombie", "mob: item", 1), testTiers, "not a summonable scenario mob"},
		{"uppercase mob", strings.Replace(minimalScenarios, "mob: zombie", "mob: Zombie", 1), testTiers, "lowercase identifier"},
		{"mob outside its family", strings.Replace(minimalScenarios, "mob: zombie", "mob: skeleton", 1), testTiers, "does not belong to family"},
		{"variant without the namespace",
			strings.Replace(minimalScenarios, "count: 2}", "count: 2, variant: become_drowned}", 1), testTiers, "must start with minecraft:"},
		{"name with a quote",
			strings.Replace(minimalScenarios, "count: 2}", `count: 2, name: 'ba"d'}`, 1), testTiers, "quote, backslash or control"},
		{"name with a backslash",
			strings.Replace(minimalScenarios, "count: 2}", `count: 2, name: 'a\b'}`, 1), testTiers, "quote, backslash or control"},
		{"name with a selector tail",
			strings.Replace(minimalScenarios, "count: 2}", `count: 2, name: "x\u0007y"}`, 1), testTiers, "quote, backslash or control"},
		{"unknown field", strings.Replace(minimalScenarios, "  - name: one", "  - name: one\n    bogus: true", 1), testTiers, "field bogus not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadScenarios(strings.NewReader(tt.yaml), tt.tiers)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadScenarios: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadScenarios error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func colosseumLayout() Layout {
	return Layout{Center: Point{X: -300, Y: 64, Z: -758}, GroundY: 63, FloorRadius: 42}
}

func TestDefaultScenariosExpand(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	l := colosseumLayout()
	for _, s := range book.Scenarios {
		cmds, err := book.Expand(l, s, 1)
		if err != nil {
			t.Fatalf("scenario %q: %v", s.Name, err)
		}
		if len(cmds) == 0 {
			t.Errorf("scenario %q expanded to nothing", s.Name)
		}
	}
}

func TestExpandIsDeterministicPerSeed(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	l := colosseumLayout()
	for _, s := range book.Scenarios {
		first, err := book.Expand(l, s, 42)
		if err != nil {
			t.Fatalf("scenario %q: %v", s.Name, err)
		}
		again, err := book.Expand(l, s, 42)
		if err != nil {
			t.Fatalf("scenario %q: %v", s.Name, err)
		}
		if !slices.Equal(first, again) {
			t.Errorf("scenario %q expanded differently on the same seed", s.Name)
		}
	}
	s, ok := book.Scenario("pillager-patrol")
	if !ok {
		t.Fatal("pillager-patrol missing")
	}
	seen := map[string]bool{}
	for seed := int64(1); seed <= 10; seed++ {
		cmds, err := book.Expand(l, s, seed)
		if err != nil {
			t.Fatal(err)
		}
		seen[strings.Join(cmds, "\n")] = true
	}
	if len(seen) < 2 {
		t.Errorf("seeds 1..10 produced %d distinct expansions, want variety", len(seen))
	}
}

func TestExpandCommands(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	l := colosseumLayout()
	s, ok := book.Scenario("four-zombies")
	if !ok {
		t.Fatal("four-zombies missing")
	}
	cmds, err := book.Expand(l, s, 7)
	if err != nil {
		t.Fatal(err)
	}
	summons := 0
	for _, c := range cmds {
		f := strings.Fields(c)
		if len(f) < 8 || f[0] != "execute" || f[1] != "positioned" || f[6] != "summon" {
			continue
		}
		summons++
		x, _ := strconv.Atoi(f[2])
		y, _ := strconv.Atoi(f[3])
		z, _ := strconv.Atoi(f[4])
		dist := math.Hypot(float64(x-l.Center.X), float64(z-l.Center.Z))
		if dist < float64(s.Spawn.Ring)-1 || dist > float64(s.Spawn.Ring)+1 {
			t.Errorf("summon at %d %d sits %.1f blocks out, want ring %d", x, z, dist, s.Spawn.Ring)
		}
		if y != l.GroundY+1 {
			t.Errorf("summon height %d, want %d", y, l.GroundY+1)
		}
		if f[7] != "zombie" {
			t.Errorf("summoned %s, want a zombie", f[7])
		}
	}
	if summons != 4 {
		t.Errorf("%d zombie summons, want 4", summons)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "effect @e[tag=arena_tiered] strength 600 0") {
		t.Error("the tier 1 strength recipe was not applied")
	}
	if !strings.Contains(joined, "replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 stone_sword 1") {
		t.Error("the tier 1 mainhand gear was not applied")
	}
	if !strings.Contains(joined, "enchant @e[tag=arena_tiered] sharpness 1") {
		t.Error("the tier 1 enchant was not applied")
	}
}

func TestExpandNamesTheBossAndTagsIt(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := book.Scenario("ravager-charge")
	if !ok {
		t.Fatal("ravager-charge missing")
	}
	cmds, err := book.Expand(colosseumLayout(), s, 3)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, `summon ravager "Breaker" ~ ~ ~`) {
		t.Errorf("the named summon is missing or malformed:\n%s", joined)
	}
	if !strings.Contains(joined, "tag @e[tag=arena_tiered] add arena_boss") {
		t.Error("the scenario boss never got the boss tag")
	}
	if !strings.Contains(joined, "effect @e[tag=arena_tiered] health_boost 600 7") {
		t.Error("the tier 4 recipe was not applied")
	}
}

func TestExpandRejectsBadInput(t *testing.T) {
	book, err := DefaultScenarios()
	if err != nil {
		t.Fatal(err)
	}
	floor := Layout{Center: Point{X: 0, Y: 64, Z: 0}, GroundY: 63, FloorRadius: 42}
	far, ok := book.Scenario("skeleton-line")
	if !ok {
		t.Fatal("skeleton-line missing")
	}
	small := floor
	small.FloorRadius = 7
	if _, err := book.Expand(small, far, 1); err == nil || !strings.Contains(err.Error(), "leaves the floor") {
		t.Errorf("ring 14 on a floor of radius 7: err = %v, want a floor error", err)
	}
	noGround := floor
	noGround.GroundY = 0
	if _, err := book.Expand(noGround, far, 1); err == nil || !strings.Contains(err.Error(), "ground height") {
		t.Errorf("no ground height: err = %v, want a ground error", err)
	}
	noFloor := floor
	noFloor.FloorRadius = 0
	if _, err := book.Expand(noFloor, far, 1); err == nil || !strings.Contains(err.Error(), "no fighting floor") {
		t.Errorf("no floor: err = %v, want a floor error", err)
	}
	handBuilt := Scenario{
		Name: "hand", Family: "dragon", Tier: 1, Timeout: Duration{time.Minute},
		Pass: PassClear, Spawn: SpawnPattern{Ring: 8, Side: "north"},
		Mobs: []ScenarioMob{{Mob: "zombie", Count: 1}},
	}
	if _, err := book.Expand(floor, handBuilt, 1); err == nil || !strings.Contains(err.Error(), "unknown family") {
		t.Errorf("hand-built unknown family: err = %v, want a family error", err)
	}
	badSide := handBuilt
	badSide.Family = tactics.FamilyZombie
	badSide.Spawn.Side = "up"
	if _, err := book.Expand(floor, badSide, 1); err == nil || !strings.Contains(err.Error(), "not north, south, east or west") {
		t.Errorf("hand-built bad side: err = %v, want a side error", err)
	}
	empty := &ScenarioBook{}
	if _, err := empty.Expand(floor, handBuilt, 1); err == nil || !strings.Contains(err.Error(), "no tier recipes") {
		t.Errorf("book without recipes: err = %v, want a recipe error", err)
	}
}
