package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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

func soilWorker(con *fakeCon) *worker {
	return &worker{r: &calRun{con: &safeConsole{c: con}}, bay: bay{ox: 100, f: 64, oz: 100}}
}

func lastSoil(con *fakeCon) string {
	for i := len(con.cmds) - 1; i >= 0; i-- {
		if strings.HasPrefix(con.cmds[i], "setblock") {
			f := strings.Fields(con.cmds[i])
			return f[len(f)-1]
		}
	}
	return ""
}

func TestFindSoilResetsTheFloorWhenNoSoilWorks(t *testing.T) {
	con := &fakeCon{}
	ir := newItemResult("cactus")
	err := soilWorker(con).findSoil(context.Background(), ir, func() (bool, error) { return false, nil })
	if err != nil || ir.soil != "" {
		t.Fatalf("soil %q err %v", ir.soil, err)
	}
	if got := lastSoil(con); got != "dirt" {
		t.Errorf("floor left as %q", got)
	}
}

func TestFindSoilResetsTheFloorWhenATrialFails(t *testing.T) {
	con := &fakeCon{}
	ir := newItemResult("cactus")
	boom := errors.New("boom")
	err := soilWorker(con).findSoil(context.Background(), ir, func() (bool, error) { return false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if got := lastSoil(con); got != "dirt" {
		t.Errorf("floor left as %q", got)
	}
}

func TestFindSoilReportsAFailedReset(t *testing.T) {
	con := &fakeCon{reply: func(c string) (string, error) {
		if strings.HasSuffix(c, " dirt") {
			return "denied", errors.New("setblock failed")
		}
		return "", nil
	}}
	err := soilWorker(con).findSoil(context.Background(), newItemResult("cactus"), func() (bool, error) { return false, nil })
	if err == nil || !strings.Contains(err.Error(), "dirt") {
		t.Errorf("reset failure swallowed: %v", err)
	}
}

func TestFindSoilKeepsTheSoilThatWorked(t *testing.T) {
	con := &fakeCon{}
	ir := newItemResult("cactus")
	n := 0
	err := soilWorker(con).findSoil(context.Background(), ir, func() (bool, error) { n++; return n == 2, nil })
	if err != nil || ir.soil != altSoils[1] {
		t.Fatalf("soil %q err %v", ir.soil, err)
	}
	if got := lastSoil(con); got != altSoils[1] {
		t.Errorf("floor %q, want it left for the item's trials", got)
	}
}
