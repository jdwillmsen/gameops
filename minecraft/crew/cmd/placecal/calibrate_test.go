package main

import "testing"

func TestByConstructionNamesTheSecondHalfKeys(t *testing.T) {
	got := byConstruction("wooden_door", map[string]any{"upper_block_bit": false, "open_bit": false, "head_piece_bit": false})
	if len(got) != 2 || got[0] != "head_piece_bit" || got[1] != "upper_block_bit" {
		t.Errorf("got %v", got)
	}
}

func TestByConstructionTreatsAPlateSignalAsStandingOnIt(t *testing.T) {
	if got := byConstruction("stone_pressure_plate", map[string]any{"redstone_signal": 0}); len(got) != 1 {
		t.Errorf("plate = %v", got)
	}
	if got := byConstruction("redstone_wire", map[string]any{"redstone_signal": 0}); len(got) != 0 {
		t.Errorf("wire = %v", got)
	}
}

func TestVocabularyHasAKeyOnAnyState(t *testing.T) {
	v := vocabulary{"standing_sign": {{"ground_sign_direction": int32(0)}}, "stone": {{}}}
	if !v.has("standing_sign", sixteenWay) || v.has("stone", sixteenWay) || v.has("nothing", sixteenWay) {
		t.Error("has")
	}
}

func TestSoilItemNamesThePlantsThatNeedOne(t *testing.T) {
	if !soilItem("cactus") || !soilItem("wheat_seeds") || soilItem("stone") || soilItem("apple") {
		t.Error("soilItem")
	}
}
