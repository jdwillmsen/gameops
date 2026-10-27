package trainer

import "github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"

// tacticList and setTacticList are the only places the trainer touches
// Profile.Tactics, so a change in how a profile stores its tactics for a
// family is a change to these two functions.

// tacticList is the ordered tactics a profile uses against a family; empty
// means the engine's own fight.
func tacticList(pr tactics.Profile, f tactics.Family) []string {
	return pr.TacticsFor(f)
}

// setTacticList sets the tactics for one family without writing through to
// the map it was copied from, since profiles share Base's map until changed.
func setTacticList(pr *tactics.Profile, f tactics.Family, list []string) {
	next := make(map[tactics.Family][]string, len(pr.Tactics)+1)
	for k, v := range pr.Tactics {
		next[k] = v
	}
	if len(list) == 0 {
		delete(next, f)
	} else {
		next[f] = append([]string(nil), list...)
	}
	if len(next) == 0 {
		next = nil
	}
	pr.Tactics = next
}
