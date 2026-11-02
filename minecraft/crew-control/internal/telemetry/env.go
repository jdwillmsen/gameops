package telemetry

import "regexp"

// dayPhases is the closed list the public status and events may carry.
var dayPhases = map[string]bool{"dawn": true, "day": true, "dusk": true, "night": true}

// biomeShape is what a biome name from a unit must look like. Only the
// signed-in detail view shows it, and as text, so a shape check is enough.
var biomeShape = regexp.MustCompile(`^[a-z][a-z_]{0,39}$`)

// closedEnv drops whatever a unit's environment says that is outside the
// closed vocabulary: an unknown phase is no phase, an odd biome is other.
func closedEnv(e LiveEnv) LiveEnv {
	out := LiveEnv{Raining: e.Raining}
	if dayPhases[e.DayPhase] {
		out.DayPhase = e.DayPhase
	}
	switch {
	case e.Biome == "":
	case biomeShape.MatchString(e.Biome):
		out.Biome = e.Biome
	default:
		out.Biome = "other"
	}
	return out
}
