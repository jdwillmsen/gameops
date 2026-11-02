package telemetry

import (
	"fmt"
	"time"
)

const (
	// threatHigh is how many hostile mobs close by make a unit "under
	// attack". The units cap their count at maxThreatCount.
	threatHigh     = 3
	maxThreatCount = 9
	// threatClear is the count at or under which the state clears. It is
	// below threatHigh so a mob wandering in and out of range at the
	// boundary is one episode, not a stream of events.
	threatClear = 1
)

// Threat is the counts of hostile mobs and players a unit reported near it.
// It is counts only: the unit never sends names, types or positions.
type Threat struct {
	Hostiles int
	Players  int
}

// observeThreat reports threat_high once as the hostile count reaches
// threatHigh, and re-arms when it falls to threatClear. An unreported count
// changes nothing, so a unit that is not sure where it is neither raises nor
// clears the state.
func (t *track) observeThreat(th *Threat, now time.Time) []Event {
	if th == nil {
		return nil
	}
	switch {
	case !t.underAttack && th.Hostiles >= threatHigh:
		t.underAttack = true
		return []Event{{
			At: now, Kind: "threat_high", Level: "warn",
			Text:  fmt.Sprintf("%d hostile mobs nearby", th.Hostiles),
			Attrs: map[string]any{"hostiles": th.Hostiles, "players": th.Players},
		}}
	case t.underAttack && th.Hostiles <= threatClear:
		t.underAttack = false
	}
	return nil
}
