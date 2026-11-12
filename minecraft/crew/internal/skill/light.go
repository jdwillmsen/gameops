package skill

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// torchBelow is the block light under which a cell gets a torch. Hostile mobs
// spawn in the dark, and a torch lights a cell to 8 or better out to 6 blocks,
// so a tunnel lit to this level has a torch about every dozen blocks.
const torchBelow = 8

// lightSources are the blocks the unit can see that give light, with the level
// they give. The server sends block names, not light values, so the level of a
// cell is modelled from these.
var lightSources = map[string]int{
	"torch": 14, "soul_torch": 10, "redstone_torch": 7,
	"lava": 15, "flowing_lava": 15, "lantern": 15, "soul_lantern": 10,
	"glowstone": 15, "sea_lantern": 15, "shroomlight": 15, "froglight": 15,
	"campfire": 15, "end_rod": 14, "magma": 3, "lit_furnace": 13,
}

// maxLight bounds the flood: no source gives more than this.
const maxLight = 15

var sixWays = [6][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}

// blockLight models the block light of a cell. Light leaves a source and loses
// one level for each step through open cells; solid blocks stop it. It is a
// model of what the server computes, not a read of it, so unknown cells count
// as solid and it errs dark: a unit would sooner place a torch too many than
// walk a tunnel it only thinks is lit.
func blockLight(w path.Blocks, c protocol.BlockPos) int {
	type node struct {
		pos  protocol.BlockPos
		dist int
	}
	best := 0
	seen := map[protocol.BlockPos]bool{c: true}
	queue := []node{{c, 0}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		name, known := w.Block(n.pos)
		if !known {
			continue
		}
		if level, ok := lightSources[name]; ok && level-n.dist > best {
			best = level - n.dist
		}
		// A source more than this far cannot reach: stop before the flood.
		if n.dist+1 >= maxLight || (n.pos != c && !path.Passable(name)) {
			continue
		}
		for _, d := range sixWays {
			next := protocol.BlockPos{n.pos.X() + d[0], n.pos.Y() + d[1], n.pos.Z() + d[2]}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, node{next, n.dist + 1})
			}
		}
	}
	return best
}
