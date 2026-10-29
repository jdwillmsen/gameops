package main

import (
	"bytes"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

func TestBuildBlocksKeepsTheCheapestRecipePerStateAndListsTheRest(t *testing.T) {
	vocab := vocabulary{"oak_stairs": {
		{"facing": int32(0), "upside_down_bit": false},
		{"facing": int32(1), "upside_down_bit": false},
		{"facing": int32(0), "upside_down_bit": true},
	}}
	ir := newItemResult("oak_stairs")
	ir.add(click{Face: fNorth, Hit: hMid}, placed("oak_stairs", "facing", int32(0), "upside_down_bit", false))
	ir.add(click{Face: fTop, Hit: hMid}, placed("oak_stairs", "facing", int32(0), "upside_down_bit", false))
	ir.add(click{Face: fTop, Hit: hMid, Yaw: 90}, placed("oak_stairs", "facing", int32(1), "upside_down_bit", false))
	blocks, sum := buildBlocks([]*itemResult{ir}, vocab)
	b := blocks["oak_stairs"]
	if b == nil || len(b.States) != 2 {
		t.Fatalf("block = %+v", b)
	}
	if got, ok := (&place.Table{Blocks: blocks}).Lookup("oak_stairs", map[string]any{"facing": 0}); !ok || got.Face != "top" {
		t.Errorf("facing 0 recipe = %+v %v", got, ok)
	}
	if len(b.Unreachable) != 1 || b.Unreachable[0]["upside_down_bit"] != true {
		t.Errorf("unreachable = %v", b.Unreachable)
	}
	if sum.blocks != 1 || sum.reachable != 2 || sum.unreachable != 1 {
		t.Errorf("summary = %+v", sum)
	}
}

func TestBuildBlocksFoldsDerivedKeysOutOfStates(t *testing.T) {
	vocab := vocabulary{"wall": {
		{"post": true, "east": "none"}, {"post": true, "east": "short"}, {"post": false, "east": "none"},
	}}
	ir := newItemResult("wall")
	ir.add(click{Face: fTop, Hit: hMid}, placed("wall", "post", true, "east", "none"))
	ir.add(click{Face: fTop, Hit: hXm}, placed("wall", "post", true, "east", "short"))
	ir.derived["wall"] = map[string]bool{"east": true}
	blocks, _ := buildBlocks([]*itemResult{ir}, vocab)
	b := blocks["wall"]
	if len(b.States) != 1 || len(b.States[0].State) != 1 {
		t.Fatalf("states = %+v", b.States)
	}
	if len(b.Derived) != 1 || b.Derived[0] != "east" {
		t.Errorf("derived = %v", b.Derived)
	}
	if len(b.Unreachable) != 1 || b.Unreachable[0]["post"] != false {
		t.Errorf("unreachable = %v", b.Unreachable)
	}
}

func TestBuildBlocksReportsUnplaceableItems(t *testing.T) {
	ir := newItemResult("diamond_sword")
	ir.unplaced = true
	_, sum := buildBlocks([]*itemResult{ir}, vocabulary{})
	if len(sum.unplaceable) != 1 || sum.unplaceable[0] != "diamond_sword" {
		t.Errorf("summary = %+v", sum)
	}
}

func TestDiffKeys(t *testing.T) {
	d := diffKeys(map[string]any{"a": 1, "b": "x"}, map[string]any{"a": int32(1), "b": "y", "c": true})
	if len(d) != 2 || !d["b"] || !d["c"] {
		t.Errorf("diff = %v", d)
	}
}

func TestMergeTablesKeepsEarlierBlocksAndSettlesTheCoveredItems(t *testing.T) {
	base := &place.Table{
		Header: place.Header{ServerVersion: "1", NotCovered: []string{"a: not reached", "b: not reached", "c: not reached"}, Partial: true, Unplaceable: []string{"x"}},
		Blocks: map[string]*place.Block{"old": {Item: "minecraft:old"}, "same": {Item: "minecraft:stale"}},
	}
	later := &place.Table{
		Header: place.Header{ServerVersion: "1", NotCovered: []string{"c: no empty hotbar slot"}, Unplaceable: []string{"x", "y"}},
		Blocks: map[string]*place.Block{"same": {Item: "minecraft:fresh"}, "new": {Item: "minecraft:new"}},
	}
	got, err := mergeTables(base, later, map[string]bool{"b": true, "c": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 3 || got.Blocks["same"].Item != "minecraft:fresh" {
		t.Errorf("blocks = %v", got.Blocks)
	}
	if len(got.Header.NotCovered) != 2 || got.Header.NotCovered[0] != "a: not reached" || got.Header.NotCovered[1] != "c: no empty hotbar slot" || !got.Header.Partial {
		t.Errorf("not covered = %v partial %v", got.Header.NotCovered, got.Header.Partial)
	}
	if len(got.Header.Unplaceable) != 2 {
		t.Errorf("unplaceable = %v", got.Header.Unplaceable)
	}
}

func TestBuildBlocksIsDeterministicAcrossEqualCostTies(t *testing.T) {
	vocab := vocabulary{"oak_sign": {{"dir": int32(0)}, {"dir": int32(1)}}}
	mk := func() []*itemResult {
		ir := newItemResult("oak_sign")
		// Yaw 90 and 270 cost the same; only the order rule may pick.
		for _, y := range []int{270, 90, 180, 0} {
			ir.add(click{Face: fTop, Hit: hMid, Yaw: y}, placed("oak_sign", "dir", int32(y/180)))
		}
		return []*itemResult{ir}
	}
	var first []byte
	for i := 0; i < 60; i++ {
		blocks, _ := buildBlocks(mk(), vocab)
		var buf bytes.Buffer
		if err := (&place.Table{Header: place.Header{Format: place.FormatVersion}, Blocks: blocks}).Write(&buf); err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = buf.Bytes()
		} else if !bytes.Equal(first, buf.Bytes()) {
			t.Fatalf("build %d differs from the first", i)
		}
	}
}

func TestBuildBlocksKeepsTruncatedSweepsAsNotSeen(t *testing.T) {
	vocab := vocabulary{"hanging": {{"d": int32(0)}, {"d": int32(1)}, {"d": int32(2)}}}
	ir := newItemResult("hanging")
	ir.truncated = true
	ir.add(click{Face: fTop, Hit: hMid}, placed("hanging", "d", int32(0)))
	blocks, sum := buildBlocks([]*itemResult{ir}, vocab)
	b := blocks["hanging"]
	if len(b.Unreachable) != 0 || len(b.NotSeen) != 2 || !b.Truncated || sum.notSeen != 2 {
		t.Errorf("block = %+v summary %+v", b, sum)
	}
}

func TestBuildBlocksKeepsADerivedKeyTheLookStillChanges(t *testing.T) {
	vocab := vocabulary{"door": {{"hinge": true}, {"hinge": false}}}
	ir := newItemResult("door")
	ir.add(click{Face: fTop, Hit: hMid}, placed("door", "hinge", false))
	ir.add(click{Face: fTop, Hit: hMid, Yaw: 90}, placed("door", "hinge", true))
	ir.derived["door"] = map[string]bool{"hinge": true}
	b := mustBlock(t, buildFrom(ir, vocab), "door")
	if len(b.Derived) != 0 || len(b.Mixed) != 1 || len(b.States) != 2 {
		t.Errorf("block = %+v", b)
	}
}

func buildFrom(ir *itemResult, vocab vocabulary) map[string]*place.Block {
	blocks, _ := buildBlocks([]*itemResult{ir}, vocab)
	return blocks
}

func mustBlock(t *testing.T, m map[string]*place.Block, name string) *place.Block {
	t.Helper()
	if m[name] == nil {
		t.Fatalf("no block %s", name)
	}
	return m[name]
}

func TestMergeTablesMergesStatesAndChecksTheMatrix(t *testing.T) {
	st := func(v int) place.Entry {
		return place.Entry{State: map[string]any{"d": v}, Recipe: place.Recipe{Face: "top", Hit: [3]float32{0.5, 1, 0.5}}}
	}
	m := place.Matrix{Faces: []string{"top"}, Yaws: []int{0}}
	base := &place.Table{Header: place.Header{ServerVersion: "1", Matrix: m, Unmapped: 2}}
	base.Blocks = map[string]*place.Block{"b": {Item: "minecraft:b", States: []place.Entry{st(0)}}}
	later := &place.Table{Header: place.Header{ServerVersion: "1", Matrix: m, Unmapped: 1}, Blocks: map[string]*place.Block{"b": {Item: "minecraft:b", States: []place.Entry{st(1)}}}}
	got, err := mergeTables(base, later, nil, vocabulary{"b": {{"d": 0}, {"d": 1}, {"d": 2}}})
	if err != nil {
		t.Fatal(err)
	}
	b := got.Blocks["b"]
	if len(b.States) != 2 || len(b.Unreachable) != 1 || got.Header.Unmapped != 2 {
		t.Errorf("block %+v unmapped %d", b, got.Header.Unmapped)
	}
	other := &place.Table{Header: place.Header{ServerVersion: "1", Matrix: place.Matrix{Faces: []string{"top"}, Yaws: []int{0, 90}}}}
	if _, err := mergeTables(&place.Table{Header: place.Header{ServerVersion: "1", Matrix: m}}, other, nil, nil); err == nil {
		t.Error("matrix mismatch was accepted")
	}
	// A table decoded from {"blocks":null} must not panic.
	if _, err := mergeTables(&place.Table{Header: place.Header{ServerVersion: "1"}}, later, nil, nil); err != nil {
		t.Error(err)
	}
}

func TestMergeTablesDropsAnItemTheLaterRunPlaced(t *testing.T) {
	base := &place.Table{Header: place.Header{ServerVersion: "1", Unplaceable: []string{"cactus", "apple"}}}
	later := &place.Table{Header: place.Header{ServerVersion: "1"}, Blocks: map[string]*place.Block{"cactus": {Item: "minecraft:cactus"}}}
	got, err := mergeTables(base, later, map[string]bool{"cactus": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Header.Unplaceable) != 1 || got.Header.Unplaceable[0] != "apple" {
		t.Errorf("unplaceable = %v", got.Header.Unplaceable)
	}
}
