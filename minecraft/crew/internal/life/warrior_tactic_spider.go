package life

// spiderTacticAllowed is the safety gate both spider variants pass before
// taking a tick. A meal, a shared retreat or regroup, a standing hold and an
// arrow in flight are the role's own decisions: a family tactic must not talk
// the unit out of healing, obeying an order, or raising its shield.
func spiderTacticAllowed(f *Fight) bool {
	w := f.Life.warrior
	if w == nil || f.Target.ID == 0 || w.healWant || w.healing {
		return false
	}
	switch f.Life.mode {
	case modeRetreat, modeStepBack:
		return false
	}
	switch w.order().Kind {
	case OrderHold, OrderRetreat, OrderRegroup:
		return false
	}
	return !f.Life.arrowIncoming(f.Pose)
}
