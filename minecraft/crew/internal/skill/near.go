package skill

import (
	"context"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// Near walks until the unit's eye is within a distance of a block's centre.
// Unlike Navigate it works for a solid target, such as a crafting table the
// unit has just placed and must stand beside to use.
type Near struct {
	world  path.Blocks
	self   func() belief.Snapshot
	target protocol.BlockPos
	within float32
	nav    *navigator
}

func NewNear(world path.Blocks, self func() belief.Snapshot, target protocol.BlockPos, within float32) Skill {
	return &Near{world: world, self: self, target: target, within: within}
}

func (n *Near) Name() string { return "near" }

func (n *Near) Ready(s belief.Snapshot) error { return overworld(s) }

// Resume plans again from wherever the unit now stands.
func (n *Near) Resume() { n.nav = nil }

func (n *Near) Step(ctx context.Context, a Actuator) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Failed, err
	}
	if err := overworld(n.self()); err != nil {
		return Failed, err
	}
	pose := a.Pose()
	if centre(n.target).Sub(eyeOf(pose.Feet)).Len() <= n.within {
		return Done, nil
	}
	if n.nav == nil {
		stand, ok := standWithin(n.world, n.target, pose, 0, n.within)
		if !ok {
			return Failed, errors.New("no standing spot beside the target")
		}
		n.nav = newNavigator(n.world, stand, arriveSlack)
	}
	st, err := n.nav.step(a)
	switch st {
	case Done:
		n.nav = nil
		// Arriving at the stand cell is arriving, even if drift leaves the
		// eye a hair outside the distance.
		return Done, nil
	case Failed:
		return Failed, fmt.Errorf("getting beside the target: %w", err)
	}
	return Running, nil
}
