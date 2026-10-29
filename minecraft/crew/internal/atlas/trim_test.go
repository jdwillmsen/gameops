package atlas

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestWantedTrimAndShieldTextures(t *testing.T) {
	for p, want := range map[string]bool{
		"resource_pack/textures/trims/coast.png":                      true,
		"resource_pack/textures/trims/coast_leggings.png":             true,
		"resource_pack/textures/trims/coast.texture_set.json":         false,
		"resource_pack/textures/trims/coast_mers.tga":                 false,
		"resource_pack/textures/trims/color_palettes/quartz.png":      true,
		"resource_pack/textures/trims/color_palettes/quartz_mers.tga": false,
		"resource_pack/textures/trims/color_palettes/sub/quartz.png":  false,
		"resource_pack/textures/trims/../x.png":                       false,
		"resource_pack/textures/entity/shield.png":                    true,
		"resource_pack/textures/entity/shield_mers.tga":               false,
		"behavior_pack/textures/trims/coast.png":                      false,
	} {
		if got := wanted(p); got != want {
			t.Errorf("wanted(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestBuildEntitiesPacksTrimsPalettesAndShield(t *testing.T) {
	root := fixturePack(t)
	writePNG(t, root, "textures/trims/coast.png", 64, 32)
	writePNG(t, root, "textures/trims/coast_leggings.png", 64, 32)
	writePNG(t, root, "textures/trims/odd.png", 16, 16)
	pal := image.NewNRGBA(image.Rect(0, 0, 8, 1))
	for x := range 8 {
		pal.SetNRGBA(x, 0, color.NRGBA{R: uint8(x * 10), G: 5, B: 6, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, pal); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "textures/trims/color_palettes/quartz.png", b.String())
	writePNG(t, root, "textures/trims/color_palettes/wide.png", 16, 1)
	writeFile(t, root, "models/entity/shield.geo.json", `{"minecraft:geometry":[{"description":{"identifier":"geometry.shield","texture_width":64,"texture_height":64},
	  "bones":[{"name":"shield","pivot":[1,15.5,3],"cubes":[{"origin":[0,25,0],"size":[2,6,6],"uv":[26,0]},{"origin":[-5,17,-1],"size":[12,22,1],"uv":[0,0]}]}]}]}`)
	writePNG(t, root, "textures/entity/shield.png", 64, 64)
	_, tbl, err := buildEntities(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Trims) != 2 || tbl.Trims["coast"] == [4]float32{} || tbl.Trims["coast_leggings"] == [4]float32{} {
		t.Fatalf("trims = %v, want the two 64x32 pictures only", tbl.Trims)
	}
	if p := tbl.TrimPalettes["quartz"]; len(p) != 8 || p[3] != [3]uint8{30, 5, 6} || len(tbl.TrimPalettes) != 1 {
		t.Fatalf("palettes = %v", tbl.TrimPalettes)
	}
	m, ok := tbl.Models[ShieldModel]
	if !ok || len(m.Default) != 1 || len(m.Default[0].Bones) == 0 {
		t.Fatalf("shield model = %+v", m)
	}
}
