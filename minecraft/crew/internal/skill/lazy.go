package skill

import (
	"context"
	"errors"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// Lazy is a chain of skills that is worked out when the unit starts it, not
// when it is submitted. A task can wait behind others, or be submitted before
// the unit has spawned, and a chain priced from the inventory of the moment of
// submission credits items the unit no longer holds or misses ones it has
// since gained.
type Lazy struct {
	name  string
	self  func() belief.Snapshot
	build func(belief.Snapshot) ([]Skill, error)

	steps []Skill
	idx   int
	// ready is whether the current step has passed Ready; a step the unit
	// returns to after a fight is resumed instead.
	ready bool
}

// NewLazy defers build to the moment the chain starts. build gets the unit's
// snapshot then and returns the skills to run in order; an empty chain is an
// error, so build says why there is nothing to do.
func NewLazy(name string, self func() belief.Snapshot, build func(belief.Snapshot) ([]Skill, error)) *Lazy {
	return &Lazy{name: name, self: self, build: build}
}

// Expand works the chain out from a snapshot without starting it.
func (l *Lazy) Expand(s belief.Snapshot) ([]Skill, error) {
	steps, err := l.build(s)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, errors.New("nothing to do")
	}
	return steps, nil
}

// Name is the running step's, so the unit reports what it is doing.
func (l *Lazy) Name() string {
	if l.idx < len(l.steps) {
		return l.steps[l.idx].Name()
	}
	return l.name
}

// Ready builds the chain from the snapshot the task starts from. The runner
// calls it once, when the task begins, so the plan reflects what the unit
// holds then.
func (l *Lazy) Ready(s belief.Snapshot) error {
	steps, err := l.Expand(s)
	if err != nil {
		return err
	}
	l.steps, l.idx, l.ready = steps, 0, false
	return nil
}

func (l *Lazy) Step(ctx context.Context, a Actuator) (Status, error) {
	if l.steps == nil {
		if err := l.Ready(l.self()); err != nil {
			return Failed, err
		}
	}
	// A step that finishes without sending an input hands over inside the
	// same tick, so a Running result always carries exactly one input.
	for l.idx < len(l.steps) {
		sk := l.steps[l.idx]
		if !l.ready {
			if err := sk.Ready(l.self()); err != nil {
				return Failed, err
			}
			l.ready = true
		}
		st, err := sk.Step(ctx, a)
		if st != Done {
			return st, err
		}
		l.idx++
		l.ready = false
	}
	return Done, nil
}

// Stop abandons the running step.
func (l *Lazy) Stop(a Actuator) {
	if l.idx < len(l.steps) {
		if st, ok := l.steps[l.idx].(Stopper); ok {
			st.Stop(a)
		}
	}
}

// Resume picks the running step up again after a fight.
func (l *Lazy) Resume() {
	if l.idx >= len(l.steps) {
		return
	}
	if rs, ok := l.steps[l.idx].(Resumer); ok {
		rs.Resume()
		l.ready = true
		return
	}
	l.ready = false
}

// Seen is what the running step last saw of a chest.
func (l *Lazy) Seen() (ChestSeen, bool) {
	if l.idx < len(l.steps) {
		if sr, ok := l.steps[l.idx].(Seer); ok {
			return sr.Seen()
		}
	}
	return ChestSeen{}, false
}
