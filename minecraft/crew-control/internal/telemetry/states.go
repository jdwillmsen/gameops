package telemetry

// What a unit is doing, as a fleet-level reading. The five are exclusive and
// together cover every unit, so counting them always adds up to the roster.
const (
	StateWorking = "working"
	StateIdle    = "idle"
	StateStuck   = "stuck"
	StateDead    = "dead"
	StateOffline = "offline"
)

// UnitState is one unit's reading at the end of a poll.
type UnitState struct {
	ID    string
	State string
	// Health and Hunger are nil while the unit is offline or has not reported
	// them, so an absent reading is never exported as zero.
	Health, Hunger *float32
}

// stateOf orders the readings by what an operator needs to act on first: a
// unit that is offline says nothing else, and a dead one is not working even
// if its last task is still listed.
func stateOf(s Status, stuck bool) string {
	switch {
	case !s.Online:
		return StateOffline
	case s.Dead:
		return StateDead
	case stuck && s.Task != nil:
		return StateStuck
	case s.Task != nil:
		return StateWorking
	default:
		return StateIdle
	}
}

func (p *Poller) statesLocked() []UnitState {
	out := make([]UnitState, len(p.units))
	for i, u := range p.units {
		st := p.statuses[i]
		out[i] = UnitState{ID: u.ID, State: stateOf(st, p.tracks[i].stuck), Health: st.Health, Hunger: st.Hunger}
	}
	return out
}
