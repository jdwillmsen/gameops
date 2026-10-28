package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// blockAcc gathers one block's results across the items that produced it.
type blockAcc struct {
	item     string
	states   map[string]*seenState
	derived  map[string]bool
	inferred bool
	partial  bool
}

type seenState struct {
	props map[string]any
	c     click
	cost  float64
}

// buildBlocks folds item results into table blocks. Results are taken in item
// order, so the same results always give the same table.
func buildBlocks(results []*itemResult, vocab vocabulary) (map[string]*place.Block, summary) {
	sort.Slice(results, func(i, j int) bool { return results[i].item < results[j].item })
	accs := map[string]*blockAcc{}
	var sum summary
	for _, ir := range results {
		if ir.unplaced {
			sum.unplaceable = append(sum.unplaceable, ir.item)
		}
		if ir.truncated {
			sum.truncated = append(sum.truncated, ir.item)
		}
		for block, states := range ir.outcomes {
			a := accs[block]
			if a == nil {
				a = &blockAcc{item: "minecraft:" + ir.item, states: map[string]*seenState{}, derived: map[string]bool{}, inferred: true}
				accs[block] = a
			}
			if !ir.inferred[block] {
				a.inferred = false
			}
			for k, s := range states {
				if cur := a.states[k]; cur == nil || s.cost < cur.cost {
					a.states[k] = &seenState{props: ir.props[block][k], c: s.c, cost: s.cost}
				}
			}
		}
		for block, keys := range ir.derived {
			if a := accs[block]; a != nil {
				for k := range keys {
					a.derived[k] = true
				}
			}
		}
	}
	out := map[string]*place.Block{}
	for block, a := range accs {
		skip := a.derived
		b := &place.Block{Item: a.item, Inferred: a.inferred}
		for k := range skip {
			b.Derived = append(b.Derived, k)
		}
		sort.Strings(b.Derived)
		byKey := map[string]place.Entry{}
		costs := map[string]float64{}
		for _, s := range a.states {
			pk := place.Key(s.props, skip)
			if _, have := costs[pk]; have && costs[pk] <= s.cost {
				continue
			}
			state := map[string]any{}
			for kk, vv := range s.props {
				if !skip[kk] {
					state[kk] = vv
				}
			}
			byKey[pk] = place.Entry{State: state, Recipe: s.c.recipe()}
			costs[pk] = s.cost
		}
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.States = append(b.States, byKey[k])
		}
		for _, st := range vocab.project(block, skip) {
			if _, ok := byKey[place.Key(st, nil)]; !ok {
				b.Unreachable = append(b.Unreachable, st)
			}
		}
		out[block] = b
		sum.blocks++
		sum.reachable += len(b.States)
		sum.unreachable += len(b.Unreachable)
		if len(b.Derived) > 0 {
			sum.derived++
		}
		if b.Inferred {
			sum.inferred++
		}
	}
	sort.Strings(sum.unplaceable)
	sort.Strings(sum.truncated)
	return out, sum
}

// summary is the coverage numbers of a table.
type summary struct {
	blocks, reachable, unreachable, derived, inferred int
	unplaceable, truncated                            []string
}

// mergeInto folds a later run into an earlier table. The later run's blocks
// replace the earlier ones of the same name; the earlier header stays, with
// the items the later run was asked for dropped from its not-covered list and
// the ones the later run missed again put back with its reason.
func mergeInto(path string, later *place.Table, asked map[string]bool) (*place.Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	base, err := place.Read(f)
	if err != nil {
		return nil, err
	}
	if base.Header.ServerVersion != later.Header.ServerVersion {
		return nil, fmt.Errorf("%s is for server %s, this run for %s", path, base.Header.ServerVersion, later.Header.ServerVersion)
	}
	return mergeTables(base, later, asked), nil
}

func mergeTables(base, later *place.Table, asked map[string]bool) *place.Table {
	for name, b := range later.Blocks {
		base.Blocks[name] = b
	}
	var left []string
	for _, n := range base.Header.NotCovered {
		if asked[itemOf(n)] {
			continue
		}
		left = append(left, n)
	}
	// Items the later run was asked for and still missed stay uncovered with
	// the later run's reason.
	left = append(left, later.Header.NotCovered...)
	sort.Strings(left)
	base.Header.NotCovered = left
	base.Header.Partial = len(left) > 0
	seen := map[string]bool{}
	var unplaceable []string
	for _, n := range append(append([]string(nil), base.Header.Unplaceable...), later.Header.Unplaceable...) {
		if !seen[n] {
			seen[n] = true
			unplaceable = append(unplaceable, n)
		}
	}
	sort.Strings(unplaceable)
	base.Header.Unplaceable = unplaceable
	base.Header.Unmapped += later.Header.Unmapped
	return base
}

func itemOf(entry string) string {
	name, _, _ := strings.Cut(entry, ":")
	return name
}

// tableCounts is the coverage numbers of a whole table.
func tableCounts(t *place.Table) (blocks, reachable, unreachable, derived, inferred int) {
	for _, b := range t.Blocks {
		blocks++
		reachable += len(b.States)
		unreachable += len(b.Unreachable)
		if len(b.Derived) > 0 {
			derived++
		}
		if b.Inferred {
			inferred++
		}
	}
	return
}
