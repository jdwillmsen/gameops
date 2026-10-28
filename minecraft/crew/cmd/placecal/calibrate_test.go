package main

import "testing"

func TestByConstructionNamesTheSecondHalfKeys(t *testing.T) {
	got := byConstruction(map[string]any{"upper_block_bit": false, "open_bit": false, "head_piece_bit": false})
	if len(got) != 2 || got[0] != "head_piece_bit" || got[1] != "upper_block_bit" {
		t.Errorf("got %v", got)
	}
}
