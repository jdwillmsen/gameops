package main

import (
	"runtime/debug"
	"sort"
	"strings"

	_ "github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/world"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// vocabulary is every state the block registry the unit decodes with knows,
// by block name without namespace. It is the same registry that turns an
// UpdateBlock hash into a name and states, so a state read back is always a
// member of it. A block the server added after the registry was built has
// no entry; its hash reads as unmapped and the run counts it.
type vocabulary map[string][]map[string]any

func loadVocabulary() (v vocabulary, states int, source string) {
	world.DefaultBlockRegistry.Finalize()
	reg := world.DefaultBlockRegistry
	v = vocabulary{}
	for i := 0; i < reg.BlockCount(); i++ {
		name, props, ok := reg.RuntimeIDToState(uint32(i))
		if !ok {
			continue
		}
		name = strings.TrimPrefix(name, "minecraft:")
		v[name] = append(v[name], props)
		states++
	}
	source = "dragonfly block registry"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/df-mc/dragonfly" {
				source += " " + d.Version
			}
		}
	}
	return v, states, source
}

// hasStates says the block has more than one state, so its placement may
// depend on the click.
func (v vocabulary) hasStates(name string) bool { return len(v[name]) > 1 }

// project lists the block's distinct states with the skipped keys left out,
// ordered by key.
func (v vocabulary) project(name string, skip map[string]bool) []map[string]any {
	seen := map[string]bool{}
	var out []map[string]any
	for _, st := range v[name] {
		k := place.Key(st, skip)
		if seen[k] {
			continue
		}
		seen[k] = true
		cp := make(map[string]any, len(st))
		for kk, vv := range st {
			if !skip[kk] {
				cp[kk] = vv
			}
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return place.Key(out[i], nil) < place.Key(out[j], nil) })
	return out
}
