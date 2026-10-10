package structures

import (
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Info is what is true of a kind of structure whether or not this world
// holds one: what a page needs in order to list it before any is found.
type Info struct {
	Kind Kind `json:"kind"`
	// Dimensions is where the game generates the kind, by name. A kind is
	// listed only in a dimension it can be in.
	Dimensions []string `json:"dimensions"`
	// Asked is set for a kind that is off until the viewer turns it on and
	// is left out of a search until then: one that gives away a goal, or
	// loot, before anybody has been there.
	Asked bool `json:"asked,omitempty"`
	// Quiet is set for a kind that can be at a site the world holds no
	// sign of, so that a finished site with none is not the world saying
	// there is none: a village the game has not run yet.
	Quiet bool `json:"quiet,omitempty"`

	in []chunks.Dimension
}

func info(kind Kind, asked, quiet bool, in ...chunks.Dimension) Info {
	out := Info{Kind: kind, Asked: asked, Quiet: quiet, in: in}
	for _, d := range in {
		out.Dimensions = append(out.Dimensions, d.Name())
	}
	return out
}

// Catalog is every kind the map can show, in the order they are listed.
var Catalog = []Info{
	info(Fortress, false, false, chunks.Nether),
	info(Monument, false, false, chunks.Overworld),
	info(Outpost, false, false, chunks.Overworld),
	info(Village, false, true, chunks.Overworld),
	info(WitchHut, false, false, chunks.Overworld),
	// Where the stronghold is, is the one thing on this map a player sets
	// out to find for themselves.
	info(Stronghold, true, false, chunks.Overworld),
	info(TrialChamber, false, false, chunks.Overworld),
}

// Kinds in the order they are listed.
var Kinds = func() []Kind {
	out := make([]Kind, len(Catalog))
	for i, k := range Catalog {
		out[i] = k.Kind
	}
	return out
}()

// InfoOf is what is true of a kind, if the map knows it.
func InfoOf(kind Kind) (Info, bool) {
	i := slices.IndexFunc(Catalog, func(k Info) bool { return k.Kind == kind })
	if i < 0 {
		return Info{}, false
	}
	return Catalog[i], true
}

// In reports whether the game generates the kind in a dimension.
func (k Info) In(d chunks.Dimension) bool { return slices.Contains(k.in, d) }
