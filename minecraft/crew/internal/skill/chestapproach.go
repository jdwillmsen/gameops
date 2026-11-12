package skill

import (
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

const (
	// chestProbes bounds how many standing cells one attempt plans a path to
	// before concluding the chest cannot be walked to right now.
	chestProbes = 8
	// chestRetries is how many times a visit waits and looks again when no
	// standing cell near the chest can be walked to. A chunk beside the chest
	// may simply not have streamed in yet.
	chestRetries = 5
	// chestRetryTicks is the wait between those attempts.
	chestRetryTicks = 40
)

type approachResult int

const (
	// approachWalk: nav leads to a cell that can be walked to.
	approachWalk approachResult = iota
	// approachWait: nothing can be walked to yet; try again after a pause.
	approachWait
	// approachNone: nothing near the chest can be stood on at all.
	approachNone
	// approachGiveUp: the retries are spent.
	approachGiveUp
)

// chestApproach picks where a unit stands to open a chest. The standable cell
// nearest the unit may sit behind a wall or a bed, with the chest between, so
// it plans a path to candidates in turn and takes the first that has one; when
// none does it waits for terrain to load and tries again, a bounded number of
// times.
type chestApproach struct {
	failed  map[protocol.BlockPos]bool
	cell    protocol.BlockPos
	retries int
	waited  int
	// lastErr is why the last candidate could not be walked to.
	lastErr error
}

// standCandidates lists the standable cells within reach of the chest, the
// nearest the unit first.
func standCandidates(world ChestWorld, chest protocol.BlockPos, p act.Pose) []protocol.BlockPos {
	type scored struct {
		c protocol.BlockPos
		d float32
	}
	var cs []scored
	const span = 3
	for dx := int32(-span); dx <= span; dx++ {
		for dy := int32(-span); dy <= span; dy++ {
			for dz := int32(-span); dz <= span; dz++ {
				c := protocol.BlockPos{chest.X() + dx, chest.Y() + dy, chest.Z() + dz}
				if c == chest {
					continue
				}
				if centre(chest).Sub(standEye(c)).Len() > chestRange-arriveSlack || !path.Standable(world, c) {
					continue
				}
				cs = append(cs, scored{c, centre(c).Sub(p.Feet).LenSqr()})
			}
		}
	}
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].d < cs[j-1].d; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
	out := make([]protocol.BlockPos, len(cs))
	for i, s := range cs {
		out[i] = s.c
	}
	return out
}

// pick chooses the standing cell for one attempt. A cell an earlier walk
// failed on is not offered again until the next retry.
func (c *chestApproach) pick(world ChestWorld, chest protocol.BlockPos, p act.Pose) (*navigator, approachResult) {
	if c.waited > 0 {
		return nil, approachWait
	}
	cands := standCandidates(world, chest, p)
	if len(cands) == 0 {
		return nil, approachNone
	}
	probed := 0
	for _, cell := range cands {
		if c.failed[cell] {
			continue
		}
		if probed++; probed > chestProbes {
			break
		}
		if _, err := path.FindFromPos(world, p.Feet.X(), p.Feet.Y(), p.Feet.Z(), cell, 0); err != nil {
			c.lastErr = err
			continue
		}
		c.cell = cell
		return newNavigator(world, cell, arriveSlack), approachWalk
	}
	if c.retries >= chestRetries {
		return nil, approachGiveUp
	}
	return nil, approachWait
}

// pause counts one idle tick of the wait between attempts. When the wait ends
// the next attempt starts with a clean slate: what failed before may be
// walkable now the terrain has loaded.
func (c *chestApproach) pause() {
	if c.waited++; c.waited >= chestRetryTicks {
		c.waited, c.failed = 0, nil
		c.retries++
	}
}

// walkFailed rules out the cell a walk could not finish and says whether the
// visit may look for another; it may not once the retries are spent.
func (c *chestApproach) walkFailed(err error) bool {
	if c.failed == nil {
		c.failed = map[protocol.BlockPos]bool{}
	}
	c.failed[c.cell] = true
	c.lastErr = err
	return c.retries < chestRetries
}

// err is the failure of a visit that ran out of ways to reach the chest.
func (c *chestApproach) err() error {
	cause := c.lastErr
	if cause == nil {
		cause = errors.New("no path")
	}
	return fmt.Errorf("getting to the chest: %w", cause)
}
