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
	"regexp"
)

// labPrefix marks a lab squad. No flag exists for a squad to carry one:
// squads are in-memory and rebuilt from hellos, so a mark kept in the
// store would vanish on a restart, and a mark claimed on the wire would
// let any unit mark its own squad trainable. The id is the one mark that
// survives both — it is what the operator configured the unit to join,
// it rides the rebuild, and a production squad is never named with it.
// The lab world is crew-lab; its squads are named the same way.
const labPrefix = "lab-"

// labSquadID is the whole shape of a lab squad id. A prefix check alone
// lets a path-valued route parameter such as "lab-/../fwb" through, and
// the id ends up in URLs and file names on the trainer's side.
var labSquadID = regexp.MustCompile(`^` + labPrefix + `[a-z0-9-]+$`)

// labSquad says the id marks the squad as one the lab runs, the only
// kind that may carry a tactics profile.
func labSquad(id string) bool { return len(id) <= maxLabel*2 && labSquadID.MatchString(id) }

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

// knownFamilies mirrors the tactics.Family constants: the only keys an
// override or a tactic list may carry. A family renamed or added in the
// contract must change here too; the crew module's route test walks the
// contract's families through this door to catch the drift.
var knownFamilies = map[string]bool{
	"default": true, "zombie": true, "skeleton": true, "creeper": true,
	"spider": true, "blaze": true, "enderman": true, "witch": true,
	"illager": true, "brute": true, "boss": true, "player": true,
}

// maxFamilyTactics bounds one family's ordered tactic list: the engine
// has a handful of variants per family, and a longer list only costs
// every unit a longer walk each tick.
const maxFamilyTactics = 8

// tacticName is the shape of a registered tactic: family/variant.
var tacticName = regexp.MustCompile(`^[a-z0-9][a-z0-9/_-]{0,63}$`)

// maxProfileText bounds the free-text lines a profile carries beyond its
// name: an evidence path and a tactic variant's name are one short line.
const maxProfileText = 256

// profileWire mirrors tactics.Profile: a version, a name, an optional
// parent for the lineage and the evidence path behind it, a default set of
// params, per-family overrides and per-family tactic variants (the family
// names are the map keys). The server stores and serves Evidence and
// Tactics untouched; the unit's load checks the names against its
// registry.
type profileWire struct {
	Version   int                      `json:"version"`
	Name      string                   `json:"name"`
	Parent    string                   `json:"parent,omitempty"`
	Evidence  string                   `json:"evidence,omitempty"`
	Default   profileParams            `json:"default"`
	Overrides map[string]profileParams `json:"overrides,omitempty"`
	Tactics   map[string][]string      `json:"tactics,omitempty"`
}

// valid is the half of the contract's validation this package can run
// without importing it: the right version, a name, parent and evidence
// that stay one readable line, known family keys, bounded tactic lists of
// well-formed names, and finite numbers, which JSON cannot quite promise
// on its own. The bounds clamp is the unit's, on load.
func (p profileWire) valid() bool {
	if p.Version != contractVersion ||
		!shortPrintable(p.Name, maxLabel) || !shortPrintable(p.Parent, maxLabel) ||
		!shortPrintable(p.Evidence, maxProfileText) {
		return false
	}
	if len(p.Overrides) > len(knownFamilies) || len(p.Tactics) > len(knownFamilies) {
		return false
	}
	for fam, names := range p.Tactics {
		if !knownFamilies[fam] || len(names) > maxFamilyTactics {
			return false
		}
		for _, n := range names {
			if !tacticName.MatchString(n) {
				return false
			}
		}
	}
	if !finiteParams(p.Default) {
		return false
	}
	for fam, o := range p.Overrides {
		if !knownFamilies[fam] || !finiteParams(o) {
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
