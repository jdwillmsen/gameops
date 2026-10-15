package layers

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

func TestLevelsOf_EveryLevelIsACutItsDimensionHas(t *testing.T) {
	nether := []NetherCut{{Y: 100, Label: "Top"}, {Y: 66, Label: "Mid"}, {Y: 34, Label: "Low"}}
	for _, d := range chunks.Dimensions {
		levels := LevelsOf(d, nether)
		if len(levels) == 0 {
			t.Errorf("%s has no levels", d.Name())
		}
		seen := map[string]bool{}
		for _, l := range levels {
			spec := SpecOf(d, l, Floor, 0)
			if err := spec.Valid(); err != nil {
				t.Errorf("%s level %s: %v", d.Name(), l.ID, err)
			}
			if seen[l.ID] {
				t.Errorf("%s has level %s twice", d.Name(), l.ID)
			}
			seen[l.ID] = true
		}
	}
	if got := LevelsOf(chunks.Dimension(9), nether); got != nil {
		t.Errorf("a dimension there is not has %v", got)
	}
	if got := LevelsOf(chunks.Nether, nil); len(got) != 0 {
		t.Errorf("a nether with no cuts configured has %v", got)
	}
	// What is handed out is the caller's.
	LevelsOf(chunks.Overworld, nil)[0].Y = 5
	if LevelsOf(chunks.Overworld, nil)[0].Y != 62 {
		t.Error("the overworld's levels were changed through a copy")
	}
}

func TestSpecOf_TakesTheLevelsBandOrAnotherOrAllBelow(t *testing.T) {
	ow := LevelsOf(chunks.Overworld, nil)[3]
	nether := LevelsOf(chunks.Nether, []NetherCut{{Y: 66, Label: "Mid"}})[0]
	for _, c := range []struct {
		name  string
		d     chunks.Dimension
		level Level
		band  int
		want  int
	}{
		{"the overworld's own", chunks.Overworld, ow, 0, 16},
		{"another band", chunks.Overworld, ow, 64, 64},
		{"all of it", chunks.Overworld, ow, AllBelow, 65},
		{"the nether's, which is all", chunks.Nether, nether, 0, 67},
		{"the end's, which is all", chunks.End, LevelsOf(chunks.End, nil)[0], 0, 256},
	} {
		got := SpecOf(c.d, c.level, Cut, c.band)
		if got.Thickness != c.want || got.Cut != c.level.Y || got.Dim != c.d || got.Style != Cut {
			t.Errorf("%s: %+v, want a band of %d", c.name, got, c.want)
		}
	}
}
