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
	// World is the unit's roster world. Dim is its dimension's closed name,
	// empty while offline or before it knows where it stands.
	World, Dim string
	// Health and Hunger are nil while the unit is offline or has not reported
	// them, so an absent reading is never exported as zero.
	Health, Hunger *float32
	// ToolDurability is the percent of wear left on the held item, nil when
	// offline, nothing is held, or the held item cannot wear out.
	ToolDurability *int
	// ToolItem names the held item behind ToolDurability, so a consumer can
	// tell a repair from a swap to another tool.
	ToolItem string
	// Fight is the warrior's running fight totals, nil while offline and for
	// a unit that reports none, so a gap is never read as a reset to zero.
	Fight *FightTotals
	// Corrections is the running count of server movement corrections, nil
	// while offline and for a unit that does not report it.
	Corrections *uint64
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
		out[i] = UnitState{ID: u.ID, World: u.World, Dim: p.positions[i].DimName, State: stateOf(st, p.tracks[i].stuck), Health: st.Health, Hunger: st.Hunger, ToolDurability: p.tracks[i].held, ToolItem: p.tracks[i].heldItem, Fight: p.tracks[i].fightPrev, Corrections: p.tracks[i].corrections}
	}
	return out
}
