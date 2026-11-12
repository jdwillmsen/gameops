package skill

import (
	"context"
	"fmt"
	"sort"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

// Require ends at once, done if the unit holds every item in the amount asked
// and failed naming the first it lacks. It sits in front of a long chain so a
// goal that cannot be met stops before the unit sets out, not after.
type Require struct {
	self func() belief.Snapshot
	want map[string]int
}

// NewRequire holds the unit to carrying at least want[item] of each item.
func NewRequire(self func() belief.Snapshot, want map[string]int) Skill {
	return &Require{self: self, want: want}
}

func (r *Require) Name() string { return "require" }

func (r *Require) Ready(s belief.Snapshot) error { return r.check(s) }

// check names the first missing item, in sorted order so the answer is the
// same on every run.
func (r *Require) check(s belief.Snapshot) error {
	names := make([]string, 0, len(r.want))
	for n := range r.want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if count(s, n) < r.want[n] {
			return fmt.Errorf("no %s in inventory", n)
		}
	}
	return nil
}

func (r *Require) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := r.check(r.self()); err != nil {
		return Failed, err
	}
	return Done, nil
}
