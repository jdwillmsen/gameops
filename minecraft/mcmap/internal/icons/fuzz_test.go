package icons

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"testing"
	"time"
)

// The fuzz targets below run their seeds with every go test, and can be
// run for longer by hand:
//
//	go test -run '^$' -fuzz FuzzModels -fuzztime 1m ./minecraft/mcmap/internal/icons/
//
// Every seed is written here; none is a file of the game's.

// FuzzModels reads whatever it is given as a geometry file and, for every
// model it yields, resolves what it inherits and looks for its face from
// each side. Nothing it reads may make it fail, and a face it finds must
// pass the recipe's own check.
func FuzzModels(f *testing.F) {
	f.Add([]byte(olderModels))
	f.Add([]byte(newerModels))
	f.Add([]byte(`{"geometry.a:geometry.a": {"bones": [{"name": "head"}]}}`))
	f.Add([]byte(`{"geometry.t": {"bones": [{"name": "head", "mirror": true, "cubes": [{"origin": [-3.3, 4, -3.5], "size": [2, 2, 2], "uv": {"north": {"uv": [16, 16], "uv_size": [-16, 16]}}, "inflate": 0.6, "rotation": [50, 0, 0]}]}]}}`))
	f.Add([]byte(`{"minecraft:geometry": [{"description": {"identifier": "geometry.x", "texture_width": 1e9}, "bones": 3}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		models, err := parseModels(raw)
		if err != nil {
			return
		}
		byID := map[string]model{}
		for _, m := range models {
			byID[m.id] = m
		}
		for id := range byID {
			m, err := resolved(byID, id)
			if err != nil {
				continue
			}
			for _, face := range []string{faceNorth, faceSouth, faceEast, faceWest, faceUp, "nowhere"} {
				r, err := faceRecipe([]drawn{{model: m, textures: []string{"textures/entity/x"}}}, view{bones: defaultBones, cube: -1, face: face})
				if err != nil {
					continue
				}
				if err := r.check(); err != nil {
					t.Fatalf("a face that was planned does not pass its own check: %v", err)
				}
			}
		}
	})
}

// FuzzExpression reads whatever it is given as an expression of a
// controller with a few arrays, one of which names itself. It must come
// to an answer or an error, in bounded work.
func FuzzExpression(f *testing.F) {
	for _, seed := range []string{
		"query.is_baby ? Geometry.baby : Array.geos[query.is_sheared]",
		"query.property('minecraft:climate_variant') == 'warm' ? Texture.warm : Texture.mild",
		"Array.skins[-1e308*1e308]", "Array.ring[0]", "((((", "1/0", "!-!-1.5f", "Array.skins[Array.skins[Array.skins[0]]]", "a ? b", "'", "",
	} {
		f.Add(seed)
	}
	const file = `{"render_controllers": {"controller.render.f": {"arrays": {
		"textures": {"Array.skins": ["Texture.a", "query.x ? Texture.b : Array.skins[0]"], "Array.ring": ["Array.ring[0] + Array.loop[0]"], "Array.loop": ["Array.ring[0]"]},
		"geometries": {"Array.geos": ["Geometry.default", "Geometry.sheared"]}}}}}`
	f.Fuzz(func(t *testing.T, expression string) {
		controllers, err := parseControllers(t.Context(), []byte(file))
		if err != nil {
			t.Fatal(err)
		}
		c := controllers["controller.render.f"]
		started := time.Now()
		_, _ = c.pick(expression)
		_ = holds(expression)
		if spent := maxFileSteps - c.work.file; spent > maxExpressionSteps+1 {
			t.Fatalf("%d steps spent on one expression", spent)
		}
		if took := time.Since(started); took > 2*time.Second {
			t.Fatalf("one expression took %s", took)
		}
	})
}

// FuzzRecipe reads whatever it is given as a recipe, as one is read back
// from the index on the volume, and makes its picture from synthetic
// textures. One that passes the check must render or be refused, and what
// it renders must be within the size a picture may be.
func FuzzRecipe(f *testing.F) {
	seeds := []Recipe{
		{W: 8, H: 8, Main: [4]int{0, 0, 8, 8}, Layers: []Layer{{Texture: "textures/entity/a", Units: [2]int{64, 32}, Src: [4]int{8, 8, 8, 8}, Dst: [4]int{0, 0, 8, 8}, Skin: true}}},
		{W: 32, H: 32, Main: [4]int{0, 0, 32, 32}, Sparse: true, Layers: []Layer{{Texture: "textures/entity/a", Src: [4]int{0, 0, 16, 16}, Dst: [4]int{0, 0, 32, 32}, Flip: true, Turn: 3}}},
		{Block: bed("textures/entity/a")},
		{Flat: "textures/entity/b", Else: "textures/entity/a"},
	}
	for _, seed := range seeds {
		raw, _ := json.Marshal(seed)
		f.Add(raw)
	}
	f.Add([]byte(`{"w": 48, "h": 3, "m": [0, 0, 3, 3], "l": [{"t": "textures/entity/a.tga", "u": [1, 1024], "s": [0, 1000, 1, 1024], "d": [47, 0, 1, 3], "r": 9}]}`))
	textures := map[string]*image.NRGBA{"textures/entity/a": painted(64, 64), "textures/entity/b": painted(16, 16), "textures/entity/a.tga": painted(128, 128)}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var r Recipe
		if json.Unmarshal(raw, &r) != nil || r.check() != nil {
			return
		}
		img, err := r.render(textures)
		if err != nil {
			return
		}
		if limit := maxFaceUnits * maxTextureScale * 2; img.Rect.Dx() > limit || img.Rect.Dy() > limit || img.Rect.Empty() {
			t.Fatalf("a recipe that passed its check made a picture of %v", img.Rect)
		}
	})
}

// FuzzTextures reads whatever it is given as a texture, PNG and TGA. A
// file may declare any size in a few bytes, and none may be decoded, or
// even allotted, past the limit.
func FuzzTextures(f *testing.F) {
	var small bytes.Buffer
	_ = png.Encode(&small, painted(4, 4))
	f.Add(small.Bytes())
	f.Add(tga(2, 2, 2, 0, []byte{3, 2, 1, 255, 6, 5, 4, 255, 9, 8, 7, 255, 12, 11, 10, 0}))
	f.Add(tga(2, 2, 10, 0x20, []byte{0x82, 3, 2, 1, 255, 0x00, 6, 5, 4, 255}))
	f.Add(tga(60000, 60000, 10, 0, []byte{0xff, 1, 2, 3, 4}))
	f.Add([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\xff\xff\x00\x00\xff\xff\x08\x06\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		for _, path := range []string{"textures/entity/x", "textures/entity/x.tga"} {
			img, err := decodeTexture(path, raw)
			if err != nil {
				continue
			}
			if img.Rect.Dx() > maxModelTextureSide || img.Rect.Dy() > maxModelTextureSide || img.Rect.Empty() {
				t.Fatalf("decoded a texture of %v", img.Rect)
			}
		}
		if _, err := Clean(raw, 1, maxIconSide, false); err == nil {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width > maxIconSide || cfg.Height > maxIconSide {
				t.Fatalf("cleaned a picture outside its bounds")
			}
		}
	})
}
