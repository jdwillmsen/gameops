package atlas

import (
	"bytes"
	"image/png"
	"testing"
)

func TestBuildEntitiesPacksWornArmourTextures(t *testing.T) {
	root := fixturePack(t)
	writePNG(t, root, "textures/models/armor/iron_1.png", 64, 32)
	writePNG(t, root, "textures/models/armor/iron_2.png", 64, 32)
	writePNG(t, root, "textures/models/armor/iron_baby.png", 32, 32)
	writePNG(t, root, "textures/models/armor/elytra.png", 64, 32)
	sheet, tbl, err := buildEntities(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Armor) != 2 {
		t.Fatalf("armor = %v, want only the _1 and _2 layouts", tbl.Armor)
	}
	img, err := png.Decode(bytes.NewReader(sheet))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"iron_1", "iron_2"} {
		r, ok := tbl.Armor[key]
		if !ok || r[2]-r[0] != 64 || r[3]-r[1] != 32 {
			t.Fatalf("%s = %v", key, r)
		}
		if int(r[2]) > img.Bounds().Dx() || int(r[3]) > img.Bounds().Dy() {
			t.Fatalf("%s lies outside the sheet: %v", key, r)
		}
		if _, _, _, a := img.At(int(r[0]), int(r[1])).RGBA(); a == 0 {
			t.Fatalf("%s is blank on the sheet", key)
		}
	}
}
