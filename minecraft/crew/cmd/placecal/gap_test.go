package main

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/place"
)

func stairBlock(item string, facings ...int) *place.Block {
	b := &place.Block{Item: "minecraft:" + item}
	for _, f := range facings {
		b.States = append(b.States, place.Entry{State: map[string]any{"weirdo_direction": f, "upside_down_bit": false}})
	}
	return b
}

func TestGapCheckFlagsTheMinorityOfAFamily(t *testing.T) {
	tab := &place.Table{Blocks: map[string]*place.Block{
		"oak_stairs":     stairBlock("oak_stairs", 0, 1, 2, 3),
		"birch_stairs":   stairBlock("birch_stairs", 0, 1, 2, 3),
		"granite_stairs": stairBlock("granite_stairs", 0, 1, 2),
		"stone_stairs":   stairBlock("stone_stairs", 0, 1, 2, 3),
		"oak_slab":       {Item: "minecraft:oak_slab", States: []place.Entry{{State: map[string]any{"minecraft:vertical_half": "top"}}}},
	}}
	gaps := gapCheck(tab)
	if len(gaps) != 1 || gaps[0].block != "granite_stairs" || gaps[0].have != 3 || gaps[0].want != 4 || len(gaps[0].missing) != 1 {
		t.Fatalf("gaps = %+v", gaps)
	}
	if got := gapItems(gaps); len(got) != 1 || got[0] != "granite_stairs" {
		t.Errorf("items = %v", got)
	}
}

func TestGapCheckLeavesSmallAndSplitFamiliesAlone(t *testing.T) {
	two := &place.Table{Blocks: map[string]*place.Block{
		"a_stairs": stairBlock("a_stairs", 0, 1), "b_stairs": stairBlock("b_stairs", 0),
	}}
	if g := gapCheck(two); len(g) != 0 {
		t.Errorf("two blocks judged: %v", g)
	}
	split := &place.Table{Blocks: map[string]*place.Block{
		"a_stairs": stairBlock("a_stairs", 0, 1), "b_stairs": stairBlock("b_stairs", 0), "c_stairs": stairBlock("c_stairs", 0, 1, 2),
	}}
	if g := gapCheck(split); len(g) != 0 {
		t.Errorf("a three-way split was judged: %v", g)
	}
}
