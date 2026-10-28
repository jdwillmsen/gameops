package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// minFamily is the smallest group a majority is read from; two blocks with
// different answers say nothing about which is right.
const minFamily = 3

// gap is a block whose reachable states differ from those of its family.
type gap struct {
	block, item string
	have, want  int
	missing     []string // state keys the family reaches and this block does not
	extra       []string // state keys only this block reaches
}

func (g gap) String() string {
	s := fmt.Sprintf("%s: %d reachable states, family has %d", g.block, g.have, g.want)
	if len(g.missing) > 0 {
		s += "; missing " + sample3(g.missing)
	}
	if len(g.extra) > 0 {
		s += "; extra " + sample3(g.extra)
	}
	return s
}

func sample3(keys []string) string {
	if len(keys) <= 3 {
		return strings.Join(keys, " | ")
	}
	return fmt.Sprintf("%s | ... (%d in all)", strings.Join(keys[:3], " | "), len(keys))
}

// familyKey groups blocks that should behave alike: the same state property
// names (reachable or not) and the same last word of the name, so every
// stair, slab, door or trapdoor lands together and a `facing_direction`
// block of another kind does not.
func familyKey(name string, b *place.Block) string {
	names := map[string]bool{}
	for _, e := range b.States {
		for k := range e.State {
			names[k] = true
		}
	}
	for _, st := range b.Unreachable {
		for k := range st {
			names[k] = true
		}
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	last := name
	if i := strings.LastIndexByte(name, '_'); i >= 0 {
		last = name[i+1:]
	}
	return last + "|" + strings.Join(keys, ",")
}

func reachableKeys(b *place.Block) map[string]bool {
	out := map[string]bool{}
	for _, e := range b.States {
		out[place.Key(e.State, nil)] = true
	}
	return out
}

func setSig(s map[string]bool) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

// gapCheck compares every block with the majority of its family and lists
// the ones whose reachable states differ. A block whose family has fewer
// than minFamily members, or no clear majority, is not judged.
func gapCheck(t *place.Table) []gap {
	groups := map[string][]string{}
	for name, b := range t.Blocks {
		if len(b.States) == 0 {
			continue
		}
		k := familyKey(name, b)
		groups[k] = append(groups[k], name)
	}
	var out []gap
	for _, names := range groups {
		if len(names) < minFamily {
			continue
		}
		sort.Strings(names)
		votes := map[string]int{}
		rep := map[string]map[string]bool{}
		for _, n := range names {
			r := reachableKeys(t.Blocks[n])
			sig := setSig(r)
			votes[sig]++
			rep[sig] = r
		}
		best, bestN, tie := "", 0, false
		for sig, n := range votes {
			switch {
			case n > bestN:
				best, bestN, tie = sig, n, false
			case n == bestN:
				tie = true
			}
		}
		// A family splits legitimately (stairs in water, say); only a clear
		// majority makes the minority suspect.
		if tie || bestN*2 <= len(names) {
			continue
		}
		want := rep[best]
		for _, n := range names {
			have := reachableKeys(t.Blocks[n])
			if setSig(have) == best {
				continue
			}
			g := gap{block: n, item: t.Blocks[n].Item, have: len(have), want: len(want)}
			for k := range want {
				if !have[k] {
					g.missing = append(g.missing, k)
				}
			}
			for k := range have {
				if !want[k] {
					g.extra = append(g.extra, k)
				}
			}
			sort.Strings(g.missing)
			sort.Strings(g.extra)
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].block < out[j].block })
	return out
}

// gapItems are the creative items to run again for the gaps, without the
// namespace, and without duplicates.
func gapItems(gaps []gap) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range gaps {
		n := strings.TrimPrefix(g.item, "minecraft:")
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
