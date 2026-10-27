package atlas

import "testing"

func TestWaterAlphaFallsBackToTheDefaultAndKnowsBothSpellings(t *testing.T) {
	tbl := Table{WaterAlphas: map[string]float64{"default": 0.5, "minecraft:swamp": 0.8}}
	if got := tbl.WaterAlpha("swamp"); got != 0.8 {
		t.Errorf("swamp = %v, want the prefixed key's 0.8", got)
	}
	if got := tbl.WaterAlpha("plains"); got != 0.5 {
		t.Errorf("plains = %v, want the default 0.5", got)
	}
	if got := (Table{}).WaterAlpha("plains"); got != DefaultWaterAlpha {
		t.Errorf("empty table = %v, want %v", got, DefaultWaterAlpha)
	}
}
