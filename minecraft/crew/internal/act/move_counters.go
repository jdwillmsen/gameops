package act

import "sync/atomic"

// MoveCounters count what a live check needs to tell a sprint model that
// matches the server from one it fights: the ticks sent while sprinting and
// the corrections the server answered with. They only rise and are shared by
// every actuator of a unit, so a reconnect does not reset them.
type MoveCounters struct {
	sprint      atomic.Uint64
	corrections atomic.Uint64
}

// SprintTicks is the inputs sent while the model was sprinting.
func (c *MoveCounters) SprintTicks() uint64 { return c.sprint.Load() }

// Corrections is the server corrections that overruled the predicted pose.
func (c *MoveCounters) Corrections() uint64 { return c.corrections.Load() }

// CountMovement makes the actuator add to c. Nil stops counting.
func (a *Actuator) CountMovement(c *MoveCounters) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.moves = c
}
