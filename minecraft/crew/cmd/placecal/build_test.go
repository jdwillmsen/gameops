package main

import (
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
	ir.add(click{Face: fTop, Hit: hMid, Yaw: 90}, placed("wall", "post", true, "east", "short"))
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
	got := mergeTables(base, later, map[string]bool{"b": true, "c": true})
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
