package arena

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

//go:embed waves.yaml
var defaultWaves []byte

// Duration is a YAML string such as "8s" or "5m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}

// WaveBook is the whole wave file: the shared pacing, the five tier recipes
// and the ordered waves.
type WaveBook struct {
	Pause       Duration     `yaml:"pause"`
	MaxLive     int          `yaml:"max_live"`
	WaveTimeout Duration     `yaml:"wave_timeout"`
	Tiers       map[int]Tier `yaml:"tiers"`
	Waves       []Wave       `yaml:"waves"`
}

// Tier is the console recipe applied to every mob spawned at that tier.
type Tier struct {
	Effects []Effect `yaml:"effects"`
	Gear    []Gear   `yaml:"gear"`
	Enchant *Enchant `yaml:"enchant"`
}

type Effect struct {
	ID        string `yaml:"id"`
	Seconds   int    `yaml:"seconds"`
	Amplifier int    `yaml:"amplifier"`
}

type Gear struct {
	Slot string `yaml:"slot"`
	Item string `yaml:"item"`
}

type Enchant struct {
	ID    string `yaml:"id"`
	Level int    `yaml:"level"`
}

// Wave is one group of mobs that must be cleared before the next opens.
type Wave struct {
	Name   string    `yaml:"name"`
	Pause  *Duration `yaml:"pause"`
	Boss   bool      `yaml:"boss"`
	Phases []Phase   `yaml:"phases"`
	Mobs   []Mob     `yaml:"mobs"`
}

type Mob struct {
	Mob     string `yaml:"mob"`
	Count   int    `yaml:"count"`
	Tier    int    `yaml:"tier"`
	Name    string `yaml:"name"`
	Pen     int    `yaml:"pen"`
	Variant string `yaml:"variant"`
}

// Phase is a timed boss beat. At is measured from the start of its wave.
type Phase struct {
	At       Duration `yaml:"at"`
	Callout  string   `yaml:"callout"`
	Commands []string `yaml:"commands"`
}

// bossMobs are the wave-ending entities; the spike verified all four summon,
// count and die through the console.
var bossMobs = map[string]bool{
	"ravager":    true,
	"iron_golem": true,
	"warden":     true,
	"wither":     true,
}

func isBossMob(mob string) bool { return bossMobs[mob] }

// MaxTier is the highest opposition tier in the wave.
func (w Wave) MaxTier() int {
	max := 0
	for _, m := range w.Mobs {
		if m.Tier > max {
			max = m.Tier
		}
	}
	return max
}

// UpTo keeps the waves whose opposition is within the tier cap. A boss wave
// is tier 5 by construction, so only a full run reaches it.
func (b *WaveBook) UpTo(tier int) []Wave {
	if tier <= 0 {
		return b.Waves
	}
	var out []Wave
	for _, w := range b.Waves {
		if w.MaxTier() <= tier {
			out = append(out, w)
		}
	}
	return out
}

// DefaultWaves decodes the shipped Gauntlet.
func DefaultWaves() (*WaveBook, error) {
	return LoadWaves(bytes.NewReader(defaultWaves))
}

// LoadWaveFile reads a wave book from disk.
func LoadWaveFile(path string) (*WaveBook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadWaves(f)
}

// LoadWaves decodes and validates a wave book. Validation is strict: a typo
// in a slot or effect would otherwise only show up as a console ERROR in the
// middle of a run.
func LoadWaves(r io.Reader) (*WaveBook, error) {
	var b WaveBook
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("parse waves: %w", err)
	}
	if err := b.normalise(); err != nil {
		return nil, err
	}
	return &b, nil
}

func (b *WaveBook) normalise() error {
	if b.MaxLive == 0 {
		b.MaxLive = 12
	}
	if b.Pause.Duration == 0 {
		b.Pause = Duration{5 * time.Second}
	}
	if b.WaveTimeout.Duration == 0 {
		b.WaveTimeout = Duration{5 * time.Minute}
	}
	if b.MaxLive < 1 {
		return fmt.Errorf("max_live must be positive, got %d", b.MaxLive)
	}
	if b.WaveTimeout.Duration < 0 {
		return fmt.Errorf("wave_timeout must not be negative")
	}
	if b.Pause.Duration < 0 {
		return fmt.Errorf("pause must not be negative")
	}
	if len(b.Waves) == 0 {
		return fmt.Errorf("no waves defined")
	}
	for t := 1; t <= 5; t++ {
		recipe, ok := b.Tiers[t]
		if !ok {
			return fmt.Errorf("tier %d has no recipe", t)
		}
		if err := validateTier(t, recipe); err != nil {
			return err
		}
	}
	names := map[string]bool{}
	for i := range b.Waves {
		w := &b.Waves[i]
		if w.Name == "" {
			return fmt.Errorf("wave %d has no name", i+1)
		}
		if names[w.Name] {
			return fmt.Errorf("wave name %q repeats", w.Name)
		}
		names[w.Name] = true
		if len(w.Mobs) == 0 {
			return fmt.Errorf("wave %q has no mobs", w.Name)
		}
		if w.Pause != nil && w.Pause.Duration < 0 {
			return fmt.Errorf("wave %q pause is negative", w.Name)
		}
		boss := false
		for j, m := range w.Mobs {
			if m.Mob == "" {
				return fmt.Errorf("wave %q mob %d has no mob type", w.Name, j+1)
			}
			if m.Count < 1 {
				return fmt.Errorf("wave %q mob %q count must be positive", w.Name, m.Mob)
			}
			if m.Tier < 1 || m.Tier > 5 {
				return fmt.Errorf("wave %q mob %q tier %d is outside 1..5", w.Name, m.Mob, m.Tier)
			}
			if _, ok := b.Tiers[m.Tier]; !ok {
				return fmt.Errorf("wave %q mob %q uses missing tier %d", w.Name, m.Mob, m.Tier)
			}
			if m.Pen < 0 {
				return fmt.Errorf("wave %q mob %q pen must not be negative", w.Name, m.Mob)
			}
			if strings.ContainsAny(m.Name, "\"\n") {
				return fmt.Errorf("wave %q mob name %q has a quote or newline", w.Name, m.Name)
			}
			if m.Variant != "" && !strings.HasPrefix(m.Variant, "minecraft:") {
				return fmt.Errorf("wave %q mob %q variant %q must start with minecraft:", w.Name, m.Mob, m.Variant)
			}
			if w.Boss && isBossMob(m.Mob) {
				boss = true
			}
		}
		if w.Boss && !boss {
			return fmt.Errorf("boss wave %q has no ravager, iron_golem, warden or wither", w.Name)
		}
		at := time.Duration(0)
		for j, p := range w.Phases {
			if p.At.Duration < at {
				return fmt.Errorf("wave %q phase %d goes backwards", w.Name, j+1)
			}
			at = p.At.Duration
			if p.Callout == "" && len(p.Commands) == 0 {
				return fmt.Errorf("wave %q phase %d has nothing to do", w.Name, j+1)
			}
		}
	}
	return nil
}

func validateTier(tier int, t Tier) error {
	for _, e := range t.Effects {
		if e.ID == "" {
			return fmt.Errorf("tier %d has an effect without an id", tier)
		}
		if e.Seconds < 1 || e.Seconds > 1000000 {
			return fmt.Errorf("tier %d effect %q duration %d is outside 1..1000000", tier, e.ID, e.Seconds)
		}
		if e.Amplifier < 0 || e.Amplifier > 255 {
			return fmt.Errorf("tier %d effect %q amplifier %d is outside 0..255", tier, e.ID, e.Amplifier)
		}
	}
	for _, g := range t.Gear {
		if !validSlot(g.Slot) {
			return fmt.Errorf("tier %d gear slot %q is not a mob slot", tier, g.Slot)
		}
		if g.Item == "" {
			return fmt.Errorf("tier %d gear slot %q has no item", tier, g.Slot)
		}
	}
	if t.Enchant != nil {
		if t.Enchant.ID == "" {
			return fmt.Errorf("tier %d enchant has no id", tier)
		}
		if t.Enchant.ID == "sharpness" && (t.Enchant.Level < 1 || t.Enchant.Level > 5) {
			return fmt.Errorf("tier %d sharpness level %d is outside 1..5", tier, t.Enchant.Level)
		}
		if t.Enchant.Level < 1 {
			return fmt.Errorf("tier %d enchant level must be positive", tier)
		}
	}
	return nil
}

// validSlot lists the slots the spike proved on mobs. hotbar and inventory
// fail with an unknown error, so they are rejected at load time.
func validSlot(slot string) bool {
	switch strings.TrimPrefix(slot, "slot.") {
	case "weapon.mainhand", "weapon.offhand", "armor.head", "armor.chest", "armor.legs", "armor.feet":
		return true
	}
	return false
}
