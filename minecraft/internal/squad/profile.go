package squad

// A squad's tactics profile is the tuning its units fight by, handed to
// them through the unit token the other write routes take. The shape
// mirrors the contract package minecraft/crew/internal/tactics, which
// this shared internal package cannot import (it is internal to crew);
// a field renamed there must be renamed here too, or a stored profile
// stops reading as one. The mirror is only the door: it checks the
// version and the shape, and the semantic half of the contract's
// validation — the bounds clamp — runs where the contract lives, on the
// unit that loads the profile.

import (
	"math"
	"strings"
)

// labPrefix marks a lab squad. No flag exists for a squad to carry one:
// squads are in-memory and rebuilt from hellos, so a mark kept in the
// store would vanish on a restart, and a mark claimed on the wire would
// let any unit mark its own squad trainable. The id is the one mark that
// survives both — it is what the operator configured the unit to join,
// it rides the rebuild, and a production squad is never named with it.
// The lab world is crew-lab; its squads are named the same way.
const labPrefix = "lab-"

// labSquad says the id marks the squad as one the lab runs, the only
// kind that may carry a tactics profile.
func labSquad(id string) bool { return strings.HasPrefix(id, labPrefix) }

// contractVersion mirrors tactics.Version: a profile the engine cannot
// read is refused at the door instead of stored for every unit to
// reject on load. Bump it with the contract.
const contractVersion = 1

// profileParams mirrors tactics.Params, field for field, tag for tag.
type profileParams struct {
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

// profileWire mirrors tactics.Profile: a version, a name, an optional
// parent for the lineage, a default set of params and per-family
// overrides (the family names are the map keys).
type profileWire struct {
	Version   int                      `json:"version"`
	Name      string                   `json:"name"`
	Parent    string                   `json:"parent,omitempty"`
	Default   profileParams            `json:"default"`
	Overrides map[string]profileParams `json:"overrides,omitempty"`
}

// valid is the half of the contract's validation this package can run
// without importing it: the right version, a name and parent that stay
// one readable line, and finite numbers, which JSON cannot quite promise
// on its own. The bounds clamp is the unit's, on load.
func (p profileWire) valid() bool {
	if p.Version != contractVersion ||
		!shortPrintable(p.Name, maxLabel) || !shortPrintable(p.Parent, maxLabel) {
		return false
	}
	if !finiteParams(p.Default) {
		return false
	}
	for _, o := range p.Overrides {
		if !finiteParams(o) {
			return false
		}
	}
	return true
}

// finiteParams rejects a set with a hole in it; the unit's clamp fills
// out-of-range values, but a NaN or infinity is no number at all.
func finiteParams(p profileParams) bool {
	for _, v := range []float64{p.FocusRange, p.HealBelow, p.HealSafe,
		p.PressRange, p.RegroupWithin, p.KiteBackoff, p.ShotEvery,
		p.ArrowNear, p.LineWidth, p.OrbitMin, p.CritWindow, p.MeleeShieldTick} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// SetProfile stores the squad's tactics profile, already checked at the
// door. ok is false for a squad the store does not hold or one not
// marked lab — the mark being the id, the store refuses what the route
// refuses, whoever calls it. Setting one bumps the squad's revision so
// a poll parked on the old one answers and carries the new profileSeq.
func (st *Store) SetProfile(id string, p profileWire) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	sq, ok := st.squads[id]
	if !ok || !labSquad(id) {
		return false
	}
	sq.profile = &p
	sq.profileSeq++
	sq.rev++
	return true
}

// Profile is the squad's current tactics profile. ok is false for a
// squad the store does not hold or one nobody handed a profile: its
// units fight on the shipped defaults.
func (st *Store) Profile(id string) (profileWire, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.prune(st.now())
	sq, ok := st.squads[id]
	if !ok || sq.profile == nil {
		return profileWire{}, false
	}
	return *sq.profile, true
}
