package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

// blockAcc gathers one block's results across the items that produced it.
type blockAcc struct {
	item      string
	states    map[string]*seenState
	derived   map[string]bool
	inferred  bool
	truncated bool
}

type seenState struct {
	props map[string]any
	c     click
	cost  float64
	item  string
	soil  string
}

// before is a total order on seen clicks: cheaper first, then by the click's
// text, then by item, so equal-cost ties never depend on map order.
func (s *seenState) before(o *seenState) bool {
	if s.cost != o.cost {
		return s.cost < o.cost
	}
	if ks, ko := s.c.key(), o.c.key(); ks != ko {
		return ks < ko
	}
	return s.item < o.item
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
		if ir.unmapped {
			sum.unmappedItems = append(sum.unmappedItems, ir.item)
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
			a.truncated = a.truncated || ir.truncated
			for k, s := range states {
				cand := &seenState{props: ir.props[block][k], c: s.c, cost: s.cost, item: ir.item}
				if s.c.Lane {
					cand.soil = ir.soil
				}
				if cur := a.states[k]; cur == nil || cand.before(cur) {
					a.states[k] = cand
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
		// A derived key that still took several values when only the look
		// changed (same face, same hit, same neighbours) is decided partly by
		// the click: it stays a state the matcher compares.
		mixed := mixedKeys(a)
		skip := map[string]bool{}
		for k := range a.derived {
			if !mixed[k] {
				skip[k] = true
			}
		}
		b := &place.Block{Item: a.item, Inferred: a.inferred, Truncated: a.truncated}
		for k := range skip {
			b.Derived = append(b.Derived, k)
		}
		sort.Strings(b.Derived)
		for k := range mixed {
			b.Mixed = append(b.Mixed, k)
		}
		sort.Strings(b.Mixed)
		byKey := map[string]place.Entry{}
		best := map[string]*seenState{}
		for _, s := range a.states {
			pk := place.Key(s.props, skip)
			if cur := best[pk]; cur != nil && !s.before(cur) {
				continue
			}
			state := map[string]any{}
			for kk, vv := range s.props {
				if !skip[kk] {
					state[kk] = vv
				}
			}
			e := place.Entry{State: state, Recipe: s.c.recipeOn(s.soil)}
			if "minecraft:"+s.item != a.item {
				e.Item = "minecraft:" + s.item
			}
			byKey[pk] = e
			best[pk] = s
		}
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.States = append(b.States, byKey[k])
		}
		fillUnreached(b, block, vocab)
		out[block] = b
		sum.blocks++
		sum.reachable += len(b.States)
		sum.unreachable += len(b.Unreachable)
		sum.notSeen += len(b.NotSeen)
		if len(b.Derived) > 0 {
			sum.derived++
		}
		if b.Inferred {
			sum.inferred++
		}
	}
	sort.Strings(sum.unplaceable)
	sort.Strings(sum.truncated)
	sort.Strings(sum.unmappedItems)
	return out, sum
}

// mixedKeys names the derived keys that took more than one value across
// clicks that differ only in look and sneak.
func mixedKeys(a *blockAcc) map[string]bool {
	out := map[string]bool{}
	for k := range a.derived {
		vals := map[string]bool{}
		for _, s := range a.states {
			if s.c.Face == fTop && !s.c.Lane && s.c.Hit == hMid {
				if v, ok := s.props[k]; ok {
					vals[place.Key(map[string]any{"v": v}, nil)] = true
				}
			}
		}
		if len(vals) > 1 {
			out[k] = true
		}
	}
	return out
}

// fillUnreached lists the vocabulary states no click gave: as unreachable
// when the whole sweep ran, as not seen when it was cut short.
func fillUnreached(b *place.Block, block string, vocab vocabulary) {
	skip := map[string]bool{}
	for _, k := range b.Derived {
		skip[k] = true
	}
	reached := map[string]bool{}
	for _, e := range b.States {
		reached[place.Key(e.State, nil)] = true
	}
	b.Unreachable, b.NotSeen = nil, nil
	for _, st := range vocab.project(block, skip) {
		if reached[place.Key(st, nil)] {
			continue
		}
		if b.Truncated {
			b.NotSeen = append(b.NotSeen, st)
		} else {
			b.Unreachable = append(b.Unreachable, st)
		}
	}
}

// summary is the coverage numbers of a table.
type summary struct {
	blocks, reachable, unreachable, derived, inferred, notSeen int
	unplaceable, truncated, unmappedItems                      []string
}

// mergeInto folds a later run into an earlier table; see mergeTables.
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
	vocab, _, _ := loadVocabulary()
	return mergeTables(base, later, asked, vocab)
}

// sameMatrix says two headers describe the same click matrix, so what the
// two runs call unreachable means the same.
func sameMatrix(a, b place.Matrix) bool {
	eq := func(x, y any) bool { return fmt.Sprint(x) == fmt.Sprint(y) }
	return eq(a.Faces, b.Faces) && eq(a.Yaws, b.Yaws) && eq(a.Pitches, b.Pitches) && eq(a.Sneak, b.Sneak) && eq(a.Hits, b.Hits)
}

// mergeTables folds a later run into an earlier table. A block both have is
// merged state by state (the cheaper recipe wins a tie on state), as long as
// both ran with the same derived keys; otherwise the later run's block
// replaces it, because the two key spaces differ. The earlier header stays,
// with the items the later run was asked for dropped from its not-covered
// list and the ones it missed again put back with its reason. Unreachable and
// not-seen states are recomputed from the vocabulary for every block.
func mergeTables(base, later *place.Table, asked map[string]bool, vocab vocabulary) (*place.Table, error) {
	if base.Header.ServerVersion != later.Header.ServerVersion {
		return nil, fmt.Errorf("the earlier table is for server %s, this run for %s", base.Header.ServerVersion, later.Header.ServerVersion)
	}
	if len(later.Header.Matrix.Faces) > 0 && len(base.Header.Matrix.Faces) > 0 && !sameMatrix(base.Header.Matrix, later.Header.Matrix) {
		return nil, errors.New("the earlier table was built with a different click matrix (faces, yaws, pitches, sneak or hits); merging would mix two meanings of unreachable")
	}
	if base.Blocks == nil {
		base.Blocks = map[string]*place.Block{}
	}
	for name, lb := range later.Blocks {
		bb := base.Blocks[name]
		if bb == nil || lb == nil || strings.Join(bb.Derived, ",") != strings.Join(lb.Derived, ",") {
			base.Blocks[name] = lb
			continue
		}
		base.Blocks[name] = mergeBlock(bb, lb)
	}
	for name, b := range base.Blocks {
		if b != nil && vocab != nil {
			fillUnreached(b, name, vocab)
		}
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
	// An item the later run was asked for and placed is no longer unplaceable.
	var stillUnplaced []string
	for _, n := range base.Header.Unplaceable {
		if !asked[n] {
			stillUnplaced = append(stillUnplaced, n)
		}
	}
	base.Header.Unplaceable = union(stillUnplaced, later.Header.Unplaceable)
	base.Header.UnmappedItems = union(base.Header.UnmappedItems, later.Header.UnmappedItems)
	// The count is of distinct hashes a run saw; the runs overlap, so the
	// larger one is the better floor, not their sum.
	if later.Header.Unmapped > base.Header.Unmapped {
		base.Header.Unmapped = later.Header.Unmapped
	}
	return base, nil
}

func mergeBlock(a, b *place.Block) *place.Block {
	out := *b
	byKey := map[string]place.Entry{}
	for _, e := range a.States {
		if e.Item == "" && a.Item != b.Item {
			e.Item = a.Item
		}
		byKey[place.Key(e.State, nil)] = e
	}
	for _, e := range b.States {
		k := place.Key(e.State, nil)
		if cur, ok := byKey[k]; !ok || e.Recipe.Cost() < cur.Recipe.Cost() {
			byKey[k] = e
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out.States = nil
	for _, k := range keys {
		out.States = append(out.States, byKey[k])
	}
	out.Inferred = a.Inferred && b.Inferred
	out.Truncated = a.Truncated && b.Truncated
	out.Mixed = union(a.Mixed, b.Mixed)
	return &out
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string(nil), a...), b...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
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
