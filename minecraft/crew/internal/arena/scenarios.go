package arena

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand"
	"os"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"go.yaml.in/yaml/v3"
)

//go:embed scenarios.yaml
var defaultScenariosYAML []byte

// Pass conditions a scenario may be graded against.
const (
	// PassClear wants every summoned mob down before the timeout.
	PassClear = "clear"
	// PassSurvive wants the squad still standing when the timeout fires.
	PassSurvive = "survive"
)

// Ring bounds keep a scenario spawnable on any real arena floor and keep the
// per-mob marker claim (radius 4) from reaching into a neighbour's cell:
// twelve mobs on the smallest ring still sit over three blocks apart.
const (
	minRing = 6
	maxRing = 24
	// maxScenarioMobs matches the wave book's live cap, so one scenario is
	// always a single spawn batch and never throttled mid-fight.
	maxScenarioMobs = 12
)

// sideAngles point at the four compass sides; Bedrock's north is -Z.
var sideAngles = map[string]float64{
	"north": -math.Pi / 2, "south": math.Pi / 2, "east": 0, "west": math.Pi,
}

// scenarioFamilies maps the family a scenario may be tagged with to the mobs
// its scenarios may contain, so a book entry cannot claim to train one
// family while fielding another's mobs. The default family admits any mob:
// mixed opposition is exactly what the fallback profile faces. A phantom
// fights like a flying zombie — press, hit, back off — so it rides with the
// zombie set. The player family is absent entirely: Bedrock cannot summon a
// player, and duels are how a player opponent is trained.
var scenarioFamilies = map[tactics.Family]map[string]bool{
	tactics.FamilyDefault: nil,
	tactics.FamilyZombie: {
		"zombie": true, "husk": true, "drowned": true,
		"zombie_villager": true, "phantom": true,
	},
	tactics.FamilySkeleton: {"skeleton": true, "stray": true, "wither_skeleton": true},
	tactics.FamilyCreeper:  {"creeper": true},
	tactics.FamilySpider:   {"spider": true, "cave_spider": true},
	tactics.FamilyBlaze:    {"blaze": true},
	tactics.FamilyEnderman: {"enderman": true},
	tactics.FamilyWitch:    {"witch": true},
	tactics.FamilyIllager: {
		"pillager": true, "vindicator": true,
		"evocation_illager": true, "vex": true,
	},
	tactics.FamilyBrute: {"piglin_brute": true, "hoglin": true, "zombified_piglin": true},
	// The boss family admits exactly the mobs the arena treats as bosses.
	tactics.FamilyBoss: bossMobs,
}

// Scenario is one short training fight: a small mob set from one opponent
// family, spawned on a ring, graded by its own timeout and pass condition.
type Scenario struct {
	Name    string         `yaml:"name"`
	Family  tactics.Family `yaml:"family"`
	Tier    int            `yaml:"tier"`
	Timeout Duration       `yaml:"timeout"`
	Pass    string         `yaml:"pass"`
	Spawn   SpawnPattern   `yaml:"spawn"`
	Mobs    []ScenarioMob  `yaml:"mobs"`
}

// SpawnPattern places a scenario's mobs on a ring around the arena centre.
type SpawnPattern struct {
	Ring int    `yaml:"ring"`
	Side string `yaml:"side"`
}

// ScenarioMob is one mob type repeated Count times.
type ScenarioMob struct {
	Mob     string `yaml:"mob"`
	Count   int    `yaml:"count"`
	Name    string `yaml:"name"`
	Variant string `yaml:"variant"`
}

// ScenarioBook is the scenario file plus the wave book's tier recipes its
// entries expand with; the recipes stay in waves.yaml so there is one source
// of truth for what a tier equips.
type ScenarioBook struct {
	Scenarios []Scenario `yaml:"scenarios"`

	tiers map[int]Tier
}

// DefaultScenarios decodes the shipped scenario book with the shipped tier
// recipes.
func DefaultScenarios() (*ScenarioBook, error) {
	waves, err := DefaultWaves()
	if err != nil {
		return nil, err
	}
	return LoadScenarios(bytes.NewReader(defaultScenariosYAML), waves.Tiers)
}

// LoadScenarioFile reads a scenario book from disk.
func LoadScenarioFile(path string, tiers map[int]Tier) (*ScenarioBook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadScenarios(f, tiers)
}

// LoadScenarios decodes and validates a scenario book against the tier
// recipes it will expand with. Validation is strict, like the wave book's:
// a bad entry must fail at load, not as a console error mid-training.
func LoadScenarios(r io.Reader, tiers map[int]Tier) (*ScenarioBook, error) {
	var b ScenarioBook
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("parse scenarios: %w", err)
	}
	if err := b.validate(tiers); err != nil {
		return nil, err
	}
	b.tiers = maps.Clone(tiers)
	return &b, nil
}

func (b *ScenarioBook) validate(tiers map[int]Tier) error {
	if len(b.Scenarios) == 0 {
		return fmt.Errorf("no scenarios defined")
	}
	names := map[string]bool{}
	for i := range b.Scenarios {
		s := &b.Scenarios[i]
		if err := validateScenario(s, tiers); err != nil {
			return err
		}
		if names[s.Name] {
			return fmt.Errorf("scenario name %q repeats", s.Name)
		}
		names[s.Name] = true
	}
	return nil
}

// validateScenario checks one entry, whether it came from a file or was
// handed to Expand built by hand.
func validateScenario(s *Scenario, tiers map[int]Tier) error {
	if s.Name == "" {
		return fmt.Errorf("scenario has no name")
	}
	if s.Family == tactics.FamilyPlayer {
		return fmt.Errorf("scenario %q: a player opponent is fought in a duel, not summoned", s.Name)
	}
	allowed, ok := scenarioFamilies[s.Family]
	if !ok {
		return fmt.Errorf("scenario %q: unknown family %q", s.Name, s.Family)
	}
	if s.Tier < 1 || s.Tier > 4 {
		return fmt.Errorf("scenario %q: tier %d is outside 1..4", s.Name, s.Tier)
	}
	if _, ok := tiers[s.Tier]; !ok {
		return fmt.Errorf("scenario %q: tier %d has no recipe", s.Name, s.Tier)
	}
	if s.Timeout.Duration <= 0 {
		return fmt.Errorf("scenario %q: timeout must be positive", s.Name)
	}
	if s.Pass != PassClear && s.Pass != PassSurvive {
		return fmt.Errorf("scenario %q: pass %q is neither %q nor %q", s.Name, s.Pass, PassClear, PassSurvive)
	}
	if _, ok := sideAngles[s.Spawn.Side]; !ok {
		return fmt.Errorf("scenario %q: spawn side %q is not north, south, east or west", s.Name, s.Spawn.Side)
	}
	if s.Spawn.Ring < minRing {
		return fmt.Errorf("scenario %q: spawn ring %d is under %d blocks", s.Name, s.Spawn.Ring, minRing)
	}
	if s.Spawn.Ring > maxRing {
		return fmt.Errorf("scenario %q: spawn ring %d is over %d blocks", s.Name, s.Spawn.Ring, maxRing)
	}
	if len(s.Mobs) == 0 {
		return fmt.Errorf("scenario %q has no mobs", s.Name)
	}
	for j, m := range s.Mobs {
		if m.Mob == "" {
			return fmt.Errorf("scenario %q mob %d has no mob type", s.Name, j+1)
		}
		if !validIdentifier(m.Mob) {
			return fmt.Errorf("scenario %q mob %q is not a lowercase identifier", s.Name, m.Mob)
		}
		if m.Mob == "player" || m.Mob == "item" {
			return fmt.Errorf("scenario %q mob %q is not a summonable scenario mob", s.Name, m.Mob)
		}
		if m.Count < 1 {
			return fmt.Errorf("scenario %q mob %q count must be positive", s.Name, m.Mob)
		}
		if allowed != nil && !allowed[m.Mob] {
			return fmt.Errorf("scenario %q: mob %q does not belong to family %q", s.Name, m.Mob, s.Family)
		}
		if strings.ContainsAny(m.Name, "\"\n") {
			return fmt.Errorf("scenario %q mob %q name has a quote or newline", s.Name, m.Mob)
		}
		if m.Variant != "" {
			if !strings.HasPrefix(m.Variant, "minecraft:") {
				return fmt.Errorf("scenario %q mob %q variant %q must start with minecraft:", s.Name, m.Mob, m.Variant)
			}
			if !validIdentifier(m.Variant) {
				return fmt.Errorf("scenario %q mob %q variant %q is not a lowercase identifier", s.Name, m.Mob, m.Variant)
			}
		}
	}
	if n := s.total(); n > maxScenarioMobs {
		return fmt.Errorf("scenario %q spawns %d mobs, at most %d", s.Name, n, maxScenarioMobs)
	}
	return nil
}

// Scenario returns the named scenario.
func (b *ScenarioBook) Scenario(name string) (Scenario, bool) {
	for _, s := range b.Scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

// total counts the mobs the scenario summons.
func (s Scenario) total() int {
	n := 0
	for _, m := range s.Mobs {
		n += m.Count
	}
	return n
}

// Expand turns a scenario into the concrete console commands that summon its
// mobs on the arena floor and apply the tier recipe to each, in a fixed
// order. The seed rotates the whole ring within a sixth of a turn: the same
// seed always produces the same commands, so a fight is repeatable, while
// different seeds vary where the opposition appears without drifting away
// from the side the scenario names.
func (b *ScenarioBook) Expand(l Layout, s Scenario, seed int64) ([]string, error) {
	if b.tiers == nil {
		return nil, fmt.Errorf("scenario %q: the book carries no tier recipes", s.Name)
	}
	if err := validateScenario(&s, b.tiers); err != nil {
		return nil, err
	}
	if l.FloorRadius <= 0 {
		return nil, fmt.Errorf("scenario %q: the layout has no fighting floor", s.Name)
	}
	if s.Spawn.Ring > l.FloorRadius-2 {
		return nil, fmt.Errorf("scenario %q: ring %d leaves the floor of radius %d", s.Name, s.Spawn.Ring, l.FloorRadius)
	}
	if l.GroundY <= 0 {
		return nil, fmt.Errorf("scenario %q: the layout has no ground height", s.Name)
	}
	recipe := b.tiers[s.Tier]
	rng := rand.New(rand.NewSource(seed))
	base := sideAngles[s.Spawn.Side] + (rng.Float64()-0.5)*(math.Pi/3)
	var cmds []string
	slot := 0
	for _, m := range s.Mobs {
		for range m.Count {
			angle := base + 2*math.Pi*float64(slot)/float64(s.total())
			x := float64(l.Center.X) + float64(s.Spawn.Ring)*math.Cos(angle)
			z := float64(l.Center.Z) + float64(s.Spawn.Ring)*math.Sin(angle)
			sp := spawn{
				Type: m.Mob, Name: m.Name, Variant: m.Variant, Tier: s.Tier,
				// A boss-family scenario's headliner is watched through the
				// boss tag, the same way a boss wave's is.
				Boss:  s.Family == tactics.FamilyBoss && isBossMob(m.Mob),
				Point: Point{X: int(math.Round(x)), Y: l.GroundY + 1, Z: int(math.Round(z))},
			}
			cmds = append(cmds, sp.commands(recipe)...)
			slot++
		}
	}
	return cmds, nil
}
