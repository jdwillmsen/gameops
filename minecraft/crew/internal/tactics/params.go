// Package tactics holds the warrior tuning surface and the scorecard of a
// scored fight. It is data only, like squadmsg: the combat engine reads a
// Profile, the trainer proposes and scores Profiles, and neither imports the
// other.
package tactics

import (
	"encoding/json"
	"fmt"
	"math"
)

// Version is bumped when a field changes meaning, so a stored champion is not
// loaded as if it meant something else.
const Version = 1

// Params are the numbers a warrior's fight reads. Distances are blocks and
// durations are ticks of 50 ms. Defaults() reproduces the constants the
// engine shipped with, so a unit with no profile fights exactly as before.
type Params struct {
	FocusRange      float64 `json:"focusRange"`
	HealBelow       float64 `json:"healBelow"`
	HealSafe        float64 `json:"healSafe"`
	PressRange      float64 `json:"pressRange"`
	RegroupWithin   float64 `json:"regroupWithin"`
	KiteBackoff     float64 `json:"kiteBackoff"`
	ShotEvery       float64 `json:"shotEvery"`
	ArrowNear       float64 `json:"arrowNear"`
	LineWidth       float64 `json:"lineWidth"`
	OrbitMin        float64 `json:"orbitMin"`
	CritWindow      float64 `json:"critWindow"`
	MeleeShieldTick float64 `json:"meleeShieldTicks"`
}

// Bound is the closed range a parameter may take. The trainer can only
// propose inside it; Clamp enforces it on anything loaded from disk.
type Bound struct{ Min, Max float64 }

// Defaults are the shipped engine constants.
func Defaults() Params {
	return Params{
		FocusRange: 12, HealBelow: 14, HealSafe: 3, PressRange: 1.6,
		RegroupWithin: 3, KiteBackoff: 5, ShotEvery: 30, ArrowNear: 3,
		LineWidth: 1.2, OrbitMin: 1.5, CritWindow: 12, MeleeShieldTick: 12,
	}
}

// Bounds are the safe ranges, chosen so no value can make a warrior inert or
// suicidal (a zero ShotEvery would spam, a huge HealSafe would never heal).
func Bounds() map[string]Bound {
	return map[string]Bound{
		"focusRange":       {6, 20},
		"healBelow":        {6, 18},
		"healSafe":         {1.5, 8},
		"pressRange":       {1.2, 3},
		"regroupWithin":    {1.5, 8},
		"kiteBackoff":      {2.5, 10},
		"shotEvery":        {25, 60},
		"arrowNear":        {1.5, 6},
		"lineWidth":        {0.8, 2.5},
		"orbitMin":         {1.2, 3},
		"critWindow":       {6, 20},
		"meleeShieldTicks": {4, 30},
	}
}

func (p *Params) fields() map[string]*float64 {
	return map[string]*float64{
		"focusRange": &p.FocusRange, "healBelow": &p.HealBelow, "healSafe": &p.HealSafe,
		"pressRange": &p.PressRange, "regroupWithin": &p.RegroupWithin,
		"kiteBackoff": &p.KiteBackoff, "shotEvery": &p.ShotEvery, "arrowNear": &p.ArrowNear,
		"lineWidth": &p.LineWidth, "orbitMin": &p.OrbitMin, "critWindow": &p.CritWindow,
		"meleeShieldTicks": &p.MeleeShieldTick,
	}
}

// Get returns the named parameter.
func (p Params) Get(name string) (float64, bool) {
	f, ok := p.fields()[name]
	if !ok {
		return 0, false
	}
	return *f, true
}

// Set writes the named parameter, clamped to its bound. NaN and infinities are
// refused and leave the previous value: clamping would turn an Inf into a
// bound the proposer never chose, and NaN compares false against every bound.
func (p *Params) Set(name string, v float64) bool {
	f, ok := p.fields()[name]
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	b := Bounds()[name]
	*f = math.Min(b.Max, math.Max(b.Min, v))
	return true
}

// Names lists the tunable parameters in a stable order.
func Names() []string {
	return []string{"focusRange", "healBelow", "healSafe", "pressRange", "regroupWithin",
		"kiteBackoff", "shotEvery", "arrowNear", "lineWidth", "orbitMin", "critWindow", "meleeShieldTicks"}
}

// Clamp forces every field into its bound and replaces NaN with the default.
func (p Params) Clamp() Params {
	d := Defaults()
	for _, n := range Names() {
		v, _ := p.Get(n)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v, _ = d.Get(n)
		}
		p.Set(n, v)
	}
	return p
}

// Family names a class of opponent a strategy is learned for. "default" is
// the fallback; "player" is another bot or human fighting as a player.
type Family string

const (
	FamilyDefault  Family = "default"
	FamilyZombie   Family = "zombie"
	FamilySkeleton Family = "skeleton"
	FamilyCreeper  Family = "creeper"
	FamilySpider   Family = "spider"
	FamilyBlaze    Family = "blaze"
	FamilyEnderman Family = "enderman"
	FamilyWitch    Family = "witch"
	FamilyIllager  Family = "illager"
	FamilyBrute    Family = "brute"
	FamilyBoss     Family = "boss"
	FamilyPlayer   Family = "player"
	// FamilyAir is what flies out of a ground fighter's reach.
	FamilyAir Family = "air"
)

// Profile is a versioned set of Params: a default plus per-family overrides.
// Evidence is the trainer-store-relative path of the scorecards that justified
// the profile; it is hashed with the rest, so an approval covers it.
type Profile struct {
	Version   int               `json:"version"`
	Name      string            `json:"name"`
	Parent    string            `json:"parent,omitempty"`
	Evidence  string            `json:"evidence,omitempty"`
	Default   Params            `json:"default"`
	Overrides map[Family]Params `json:"overrides,omitempty"`
	// Tactics names, in order, the registered tactic variants tried against a
	// family: the first that takes the tick fights it, and the rest are the
	// fall-through. A family absent here, or mapped to nothing, runs the
	// engine's own fight, so a profile that sets nothing fights exactly as
	// shipped.
	Tactics map[Family][]string `json:"tactics,omitempty"`
}

// NewProfile returns the shipped behaviour.
func NewProfile(name string) Profile {
	return Profile{Version: Version, Name: name, Default: Defaults()}
}

// For returns the params used against a family, falling back to the default.
func (pr Profile) For(f Family) Params {
	if p, ok := pr.Overrides[f]; ok {
		return p
	}
	return pr.Default
}

// TacticsFor is the ordered tactic variants named for a family, nil for none.
func (pr Profile) TacticsFor(f Family) []string {
	return pr.Tactics[f]
}

// Validate rejects a profile of the wrong version and clamps every set.
func (pr Profile) Validate() (Profile, error) {
	if pr.Version != Version {
		return pr, fmt.Errorf("tactics: profile %q is version %d, engine reads %d", pr.Name, pr.Version, Version)
	}
	pr.Default = pr.Default.Clamp()
	for f, p := range pr.Overrides {
		pr.Overrides[f] = p.Clamp()
	}
	return pr, nil
}

// UnmarshalJSON starts from the shipped values so a field the document does
// not name keeps its default instead of decoding to zero and being clamped to
// its floor. Each override is read on top of the profile's own default, so an
// override that names one knob changes only that knob. Tactics accepts the
// older one-name string for a family as well as the list.
func (pr *Profile) UnmarshalJSON(b []byte) error {
	var raw struct {
		Version   int                        `json:"version"`
		Name      string                     `json:"name"`
		Parent    string                     `json:"parent"`
		Evidence  string                     `json:"evidence"`
		Default   json.RawMessage            `json:"default"`
		Overrides map[Family]json.RawMessage `json:"overrides"`
		Tactics   map[Family]tacticNames     `json:"tactics"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := Profile{Version: raw.Version, Name: raw.Name, Parent: raw.Parent, Evidence: raw.Evidence, Default: Defaults()}
	if len(raw.Default) > 0 {
		if err := json.Unmarshal(raw.Default, &out.Default); err != nil {
			return fmt.Errorf("tactics: default: %w", err)
		}
	}
	if len(raw.Overrides) > 0 {
		out.Overrides = make(map[Family]Params, len(raw.Overrides))
		for f, msg := range raw.Overrides {
			o := out.Default
			if len(msg) > 0 {
				if err := json.Unmarshal(msg, &o); err != nil {
					return fmt.Errorf("tactics: override %q: %w", f, err)
				}
			}
			out.Overrides[f] = o
		}
	}
	if len(raw.Tactics) > 0 {
		out.Tactics = make(map[Family][]string, len(raw.Tactics))
		for f, names := range raw.Tactics {
			out.Tactics[f] = names
		}
	}
	*pr = out
	return nil
}

// tacticNames reads a family's tactics as a list or as the single name an
// earlier profile format stored; an empty name means none.
type tacticNames []string

func (t *tacticNames) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*t = nil
		} else {
			*t = tacticNames{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("tactics: a family's tactics must be a name or a list of names: %w", err)
	}
	*t = many
	return nil
}

// Parse decodes and validates a profile.
func Parse(b []byte) (Profile, error) {
	var pr Profile
	if err := json.Unmarshal(b, &pr); err != nil {
		return pr, err
	}
	return pr.Validate()
}
