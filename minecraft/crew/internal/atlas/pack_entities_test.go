package atlas

import (
	"os"
	"path/filepath"
	"testing"
)

// The real wildlife pack is the fixture: it is what the lab ships.
const wildlifePack = "../../../../packs/lab-mods-wildlife/resource_pack"

func buildWildlife(t *testing.T) EntityTable {
	t.Helper()
	if _, err := os.Stat(filepath.Join(wildlifePack, "manifest.json")); err != nil {
		t.Skip("wildlife pack is not in this checkout")
	}
	png, tbl, skipped, err := BuildPackEntities(wildlifePack)
	if err != nil || len(skipped) != 0 || len(png) == 0 {
		t.Fatalf("build: err=%v skipped=%v png=%d", err, skipped, len(png))
	}
	return tbl
}

func TestWildlifePackCompilesEverySpecies(t *testing.T) {
	tbl := buildWildlife(t)
	for _, name := range []string{"tiger", "cheetah", "lion", "black_leopard", "brown_bear", "hyena"} {
		m, ok := tbl.Models["gameops:"+name]
		if !ok {
			t.Errorf("gameops:%s has no model", name)
			continue
		}
		cubes := 0
		for _, b := range m.Default[0].Bones {
			cubes += len(b.Cubes)
		}
		if cubes == 0 || m.Height <= 0 {
			t.Errorf("%s: %d cubes, height %v", name, cubes, m.Height)
		}
		if m.Anim == nil || len(m.Anim.Run) != 4 {
			t.Errorf("%s: animations = %+v, want walk, idle, sit, attack", name, m.Anim)
		}
	}
}

func TestWildlifeVariantsPickTexturesAndHideTheFemaleMane(t *testing.T) {
	tbl := buildWildlife(t)
	for _, name := range []string{"gameops:tiger", "gameops:lion", "gameops:hyena"} {
		if n := len(tbl.Models[name].Variants); n != 2 {
			t.Errorf("%s has %d variants, want 2", name, n)
		}
	}
	if n := len(tbl.Models["gameops:cheetah"].Variants); n != 0 {
		t.Errorf("cheetah has %d variants, want none", n)
	}
	cubesOf := func(l []Layer, bone string) int {
		for _, b := range l[0].Bones {
			if b.Name == bone {
				return len(b.Cubes)
			}
		}
		return -1
	}
	lion := tbl.Models["gameops:lion"].Variants
	if cubesOf(lion[0], "mane") == 0 || cubesOf(lion[0], "mane_chest") == 0 {
		t.Error("the male (variant 0) has no mane")
	}
	if cubesOf(lion[1], "mane") != 0 || cubesOf(lion[1], "mane_chest") != 0 {
		t.Error("the female (variant 1) kept its mane")
	}
	if cubesOf(lion[1], "head") == 0 {
		t.Error("hiding the mane hid the head")
	}
	tiger := tbl.Models["gameops:tiger"].Variants
	rect := func(l []Layer) [4]float32 { return l[0].Bones[0].Cubes[0].UV[0] }
	if rect(tiger[0]) == rect(tiger[1]) {
		t.Error("tiger variants share one texture rect")
	}
}

func TestWildlifeAnimationsKeepTheirExpressions(t *testing.T) {
	a := buildWildlife(t).Models["gameops:tiger"].Anim
	if a.Run[2].Name != "sit" || a.Run[2].When != "query.is_sitting" || a.Run[3].When != "variable.attack_time > 0" {
		t.Errorf("run = %+v", a.Run)
	}
	leg := a.Defs["walk"].Bones["front_left_leg"]
	if leg.Rot == nil || leg.Rot[0] != "math.cos(query.modified_distance_moved * 24.0) * 40.0 * query.modified_move_speed" {
		t.Errorf("walk leg = %+v", leg)
	}
	if p := a.Defs["attack"].Bones["head"].Pos; p == nil || p[2] != "-3.0 * math.sin(variable.attack_time * 180.0)" {
		t.Errorf("attack head position = %v", p)
	}
}

func TestPackEntitiesRefuseUnsafeExpressions(t *testing.T) {
	if exprOK("math.cos(query.life_time*3) + 1") != true || exprOK("`rm`") || exprOK("a;b") {
		t.Error("exprOK misjudged")
	}
	if got := vec3Expr([]byte(`[0, "math.sin(1)", "x;y"]`)); got != nil {
		t.Errorf("an unsafe part was accepted: %v", got)
	}
}

func TestPartVisibilityConditions(t *testing.T) {
	rc := renderController{visible: []visibility{{"*", "true"}, {"mane*", "query.variant == 0"}}}
	cases := []struct {
		bone    string
		variant int
		want    bool
	}{{"mane", 0, true}, {"mane", 1, false}, {"mane_chest", 1, false}, {"head", 1, true}}
	for _, c := range cases {
		if got := rc.shows(c.bone, c.variant); got != c.want {
			t.Errorf("%s variant %d: %v", c.bone, c.variant, got)
		}
	}
}

func TestVanillaTableStillHasNoPackFields(t *testing.T) {
	m := EntityModel{Default: []Layer{{}}}
	if sets := m.layerSets(); len(sets) != 3 {
		t.Errorf("layer sets = %d", len(sets))
	}
}
