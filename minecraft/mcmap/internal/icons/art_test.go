package icons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// painted is a synthetic texture in which every pixel says where it is:
// its red is its column and its green its row, so a test can tell which
// pixel of the texture a pixel of a picture was taken from. Nothing in
// these tests is a real texture or a real model.
func painted(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 3), uint8(y * 3), 90, 255})
		}
	}
	return img
}

func asPNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// from is the pixel of a painted texture a colour was taken from.
func from(c color.NRGBA) [2]int { return [2]int{int(c.R) / 3, int(c.G) / 3} }

const (
	// The older layout: a key per model, one of which inherits.
	olderModels = `// a comment the real files carry
{"format_version": "1.8.0",
 "geometry.walker": {"texturewidth": 64, "textureheight": 32, "bones": [
   {"name": "body", "cubes": [{"origin": [-4, 12, -2], "size": [8, 12, 4], "uv": [16, 16]}]},
   {"name": "head", "cubes": [{"origin": [-4, 24, -4], "size": [8, 8, 8], "uv": [0, 0]}]},
   {"name": "hat", "parent": "head", "neverRender": true, "cubes": [{"origin": [-4, 24, -4], "size": [8, 8, 8], "uv": [32, 0], "inflate": 0.5}]}
 ]},
 "geometry.walker.dry:geometry.walker": {"bones": [
   {"name": "body", "cubes": [{"origin": [-4, 12, -2], "size": [8, 12, 4], "uv": [16, 0]}]},
   {"name": "head", "pivot": [0, 24, 0]}
 ]}
}`
	// The newer: a list, with the name in a description.
	newerModels = `{"format_version": "1.12.0", "minecraft:geometry": [
 {"description": {"identifier": "geometry.grazer", "texture_width": 64, "texture_height": 64},
  "bones": [
   {"name": "head", "cubes": [
     {"origin": [-4, 16, -14], "size": [8, 8, 6], "uv": [0, 0]},
     {"origin": [-3, 16, -15], "size": [6, 3, 1], "uv": [1, 33]},
     {"origin": [-20, 16, -14], "size": [2, 2, 2], "uv": [40, 40]}
   ]},
   {"name": "ear", "parent": "head", "rotation": [0, 0, 30], "cubes": [{"origin": [-5, 22, -12], "size": [1, 3, 1], "uv": [22, 0]}]}
  ]}
]}`
)

func modelsOf(t *testing.T, files ...string) map[string]model {
	t.Helper()
	out := map[string]model{}
	for _, file := range files {
		models, err := parseModels([]byte(file))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range models {
			out[m.id] = m
		}
	}
	return out
}

func TestBothLayoutsOfModelFileAreRead(t *testing.T) {
	models := modelsOf(t, olderModels, newerModels)
	for _, id := range []string{"geometry.walker", "geometry.walker.dry", "geometry.grazer"} {
		if _, ok := models[id]; !ok {
			t.Errorf("%s was not read: %v", id, slices.Sorted(func(yield func(string) bool) {
				for id := range models {
					if !yield(id) {
						return
					}
				}
			}))
		}
	}
	if m := models["geometry.walker"]; m.texW != 64 || m.texH != 32 || m.bone("head") < 0 {
		t.Errorf("the older layout read as %dx%d with head at %d", m.texW, m.texH, m.bone("head"))
	}
	if m := models["geometry.grazer"]; m.texW != 64 || m.texH != 64 || len(m.bones[m.bone("head")].Cubes) != 3 {
		t.Errorf("the newer layout read as %+v", m)
	}
}

func TestAModelInheritsWhatItDoesNotReplace(t *testing.T) {
	models := modelsOf(t, olderModels)
	if own := models["geometry.walker.dry"]; len(own.bones[own.bone("head")].Cubes) != 0 {
		t.Fatal("the child's own head has boxes; the test is not testing inheritance")
	}
	m, err := resolved(models, "geometry.walker.dry")
	if err != nil {
		t.Fatal(err)
	}
	head := m.bones[m.bone("head")]
	if len(head.Cubes) != 1 || head.Cubes[0].Size != [3]float64{8, 8, 8} {
		t.Errorf("a head that lists no boxes did not keep its parent's: %+v", head.Cubes)
	}
	var corner []float64
	_ = json.Unmarshal(m.bones[m.bone("body")].Cubes[0].UV, &corner)
	if !slices.Equal(corner, []float64{16, 0}) {
		t.Errorf("the child's own body was not the one used: uv %v", corner)
	}
	if m.texW != 64 || m.texH != 32 || m.bone("hat") < 0 {
		t.Errorf("the parent's texture size and other bones were not inherited: %dx%d", m.texW, m.texH)
	}
	// Without the parent there is no head at all, which is what a reader
	// that ignored inheritance would find.
	if _, _, err := models["geometry.walker.dry"].seen(view{bones: []string{"head"}, cube: -1}); err == nil {
		t.Error("the child alone has a face; inheritance is not what gave it one")
	}
	if _, main, err := m.seen(view{bones: []string{"head"}, cube: -1}); err != nil || main.w != 8 {
		t.Errorf("the head it inherits gives no face: %v", err)
	}

	loop := map[string]model{"geometry.a": {id: "geometry.a", parent: "geometry.b"}, "geometry.b": {id: "geometry.b", parent: "geometry.a"}}
	if _, err := resolved(loop, "geometry.a"); err == nil {
		t.Error("a model that inherits from itself was resolved")
	}
	if _, err := resolved(models, "geometry.absent"); err == nil {
		t.Error("a model nothing defines was resolved")
	}
}

func cubeOf(t *testing.T, raw string) cube {
	t.Helper()
	var c cube
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAFacesRectangleFollowsHowABoxUnfolds(t *testing.T) {
	boxed := cubeOf(t, `{"origin": [0,0,0], "size": [8, 10.9, 6], "uv": [3, 5]}`)
	for face, want := range map[string]texRect{
		faceNorth: {9, 11, 8, 10, false},
		faceSouth: {23, 11, 8, 10, false},
		faceEast:  {3, 11, 6, 10, false},
		faceWest:  {17, 11, 6, 10, false},
		faceUp:    {9, 5, 8, 6, false},
	} {
		if got, ok := faceRect(boxed, false, face); !ok || got != want {
			t.Errorf("%s of a box at 3,5 = %+v %v, want %+v", face, got, ok, want)
		}
	}
	// Mirrored: every face turned, and the two sides exchanged.
	if got, _ := faceRect(boxed, true, faceNorth); got != (texRect{9, 11, 8, 10, true}) {
		t.Errorf("mirrored front = %+v", got)
	}
	if got, _ := faceRect(boxed, true, faceEast); got != (texRect{17, 11, 6, 10, true}) {
		t.Errorf("mirrored side = %+v, want the other side's rectangle, turned", got)
	}
	// The box's own word overrides its bone's.
	if got, _ := faceRect(cubeOf(t, `{"size": [8, 8, 8], "uv": [0, 0], "mirror": false}`), true, faceNorth); got.Flip {
		t.Error("a box that says it is not mirrored was drawn mirrored for its bone")
	}
	if _, ok := faceRect(cubeOf(t, `{"size": [0.5, 8, 8], "uv": [0, 0]}`), false, faceNorth); ok {
		t.Error("a box under one unit wide has a front")
	}

	each := cubeOf(t, `{"size": [72, 72, 72], "uv": {"north": {"uv": [16, 16], "uv_size": [16, 16]}, "east": {"uv": [20, 4], "uv_size": [-4, 2]}, "up": {"uv": [8, 0]}}}`)
	if got, ok := faceRect(each, false, faceNorth); !ok || got != (texRect{16, 16, 16, 16, false}) {
		t.Errorf("a front given by itself = %+v %v", got, ok)
	}
	if got, ok := faceRect(each, false, faceEast); !ok || got != (texRect{16, 4, 4, 2, true}) {
		t.Errorf("a side given backwards = %+v %v, want it from its far edge, turned", got, ok)
	}
	if got, ok := faceRect(each, false, faceUp); !ok || got != (texRect{8, 0, 72, 72, false}) {
		t.Errorf("a top with no size = %+v %v, want the box's own", got, ok)
	}
	if _, ok := faceRect(each, false, faceSouth); ok {
		t.Error("a face the box does not list was drawn")
	}
}

func TestAModelOutsideTheBoundsIsRefused(t *testing.T) {
	head := func(cube string) string {
		return `{"geometry.x": {"bones": [{"name": "head", "cubes": [` + cube + `]}]}}`
	}
	if models, _ := parseModels([]byte(head(`{"origin": [0,0,0], "size": [8,8,8], "uv": [0,0]}`))); len(models) != 1 {
		t.Fatal("a plain model was refused; the cases below prove nothing")
	}
	many := strings.TrimSuffix(strings.Repeat(`{"origin": [0,0,0], "size": [1,1,1], "uv": [0,0]},`, maxBoneCubes+1), ",")
	for name, file := range map[string]string{
		"a box far from the origin":  head(`{"origin": [0, 1e9, 0], "size": [8,8,8], "uv": [0,0]}`),
		"a box of enormous size":     head(`{"origin": [0,0,0], "size": [8, 5000, 8], "uv": [0,0]}`),
		"a box of size below zero":   head(`{"origin": [0,0,0], "size": [-8, 8, 8], "uv": [0,0]}`),
		"too many boxes in a bone":   head(many),
		"a texture of enormous size": `{"geometry.x": {"texturewidth": 100000, "textureheight": 32, "bones": []}}`,
		"a bone with no usable name": `{"geometry.x": {"bones": [{"name": "../../etc", "cubes": []}]}}`,
		"a name that is no model's":  `{"geometry.x y z": {"bones": []}}`,
	} {
		if models, err := parseModels([]byte(file)); err == nil && len(models) != 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	deep := strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1)
	if _, err := parseModels([]byte(`{"geometry.x": ` + deep + `}`)); err == nil {
		t.Error("a file nested past the limit was parsed")
	}
	if !jsonWithin([]byte(`{"a": "[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[["}`), 2) {
		t.Error("brackets inside a string were counted as nesting")
	}
	if _, err := parseModels(make([]byte, maxModelBytes+1)); err == nil {
		t.Error("a file over the byte limit was parsed")
	}
}

// walker is the textures and the recipe of the older synthetic model's
// face.
func walker(t *testing.T) Recipe {
	t.Helper()
	m, err := resolved(modelsOf(t, olderModels), "geometry.walker")
	if err != nil {
		t.Fatal(err)
	}
	r, err := faceRecipe([]drawn{{model: m, textures: []string{"textures/entity/walker"}}}, view{bones: defaultBones, cube: -1})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAFaceIsTheFrontOfTheHeadPixelForPixel(t *testing.T) {
	r := walker(t)
	// The hat is a bone the model never draws, so the face is the head
	// alone: 8 by 8, from 8,8 of the texture.
	if r.W != 8 || r.H != 8 || len(r.Layers) != 1 || r.Layers[0].Src != [4]int{8, 8, 8, 8} {
		t.Fatalf("recipe = %+v", r)
	}
	img, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(64, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if img.Rect.Dx() != 8 || img.Rect.Dy() != 8 {
		t.Fatalf("picture is %v, want 8 by 8: one pixel to each of the texture's", img.Rect)
	}
	for _, at := range [][2]int{{0, 0}, {7, 0}, {3, 5}, {7, 7}} {
		if got := from(img.NRGBAAt(at[0], at[1])); got != [2]int{8 + at[0], 8 + at[1]} {
			t.Errorf("pixel %v came from %v of the texture", at, got)
		}
	}
	// A texture drawn at twice the resolution gives a picture twice the
	// size, each pixel still one of the texture's own.
	fine, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(128, 64)})
	if err != nil {
		t.Fatal(err)
	}
	if fine.Rect.Dx() != 16 || from(fine.NRGBAAt(15, 0)) != [2]int{31, 16} {
		t.Errorf("at twice the resolution: %v, corner from %v", fine.Rect, from(fine.NRGBAAt(15, 0)))
	}
}

func TestAMirroredHeadIsDrawnTurned(t *testing.T) {
	models := modelsOf(t, `{"geometry.m": {"texturewidth": 64, "textureheight": 32, "bones": [{"name": "head", "mirror": true, "cubes": [{"origin": [-4, 24, -4], "size": [8, 8, 8], "uv": [0, 0]}]}]}}`)
	r, err := faceRecipe([]drawn{{model: models["geometry.m"], textures: []string{"textures/entity/m"}}}, view{bones: defaultBones, cube: -1})
	if err != nil {
		t.Fatal(err)
	}
	img, err := r.render(map[string]*image.NRGBA{"textures/entity/m": painted(64, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if got := from(img.NRGBAAt(0, 0)); got != [2]int{15, 8} {
		t.Errorf("the left of a mirrored face came from %v, want the right of its rectangle", got)
	}
}

func TestWhatIsOnTheHeadIsDrawnOverItAndWhatIsFarOrTurnedIsNot(t *testing.T) {
	m := modelsOf(t, newerModels)["geometry.grazer"]
	r, err := faceRecipe([]drawn{{model: m, textures: []string{"textures/entity/grazer"}}}, view{bones: defaultBones, cube: -1})
	if err != nil {
		t.Fatal(err)
	}
	// The snout, which is nearer than the head; not the box far to one
	// side, nor the ear, which is turned out of square.
	if r.W != 8 || r.H != 8 || len(r.Layers) != 2 {
		t.Fatalf("recipe = %+v", r)
	}
	img, err := r.render(map[string]*image.NRGBA{"textures/entity/grazer": painted(64, 64)})
	if err != nil {
		t.Fatal(err)
	}
	// The snout's front is at 2,34 of the texture and sits a unit in from
	// the left along the bottom three rows.
	if got := from(img.NRGBAAt(1, 5)); got != [2]int{2, 34} {
		t.Errorf("where the snout is, the pixel came from %v", got)
	}
	if got := from(img.NRGBAAt(0, 5)); got != [2]int{6, 11} {
		t.Errorf("beside the snout, the pixel came from %v, want the head's own", got)
	}
}

func TestARectangleOutsideItsTextureIsRefused(t *testing.T) {
	r := walker(t)
	textures := map[string]*image.NRGBA{"textures/entity/walker": painted(64, 32)}
	if _, err := r.render(textures); err != nil {
		t.Fatalf("the plain recipe does not render: %v", err)
	}
	wide := r
	wide.Layers = []Layer{r.Layers[0]}
	wide.Layers[0].Src = [4]int{60, 8, 8, 8}
	if _, err := wide.render(textures); err == nil {
		t.Error("a rectangle running off the right of the texture was drawn")
	}
	tall := r
	tall.Layers = []Layer{r.Layers[0]}
	tall.Layers[0].Src = [4]int{8, 30, 8, 8}
	if _, err := tall.render(textures); err == nil {
		t.Error("a rectangle running off the bottom of the texture was drawn")
	}
	// A texture that is not the size its model was made for, in a way no
	// whole scale explains.
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(96, 40)}); err == nil {
		t.Error("a texture of the wrong shape was drawn from")
	}
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": painted(64*8, 32*8)}); err == nil {
		t.Error("a texture eight times the size was drawn from")
	}
	outside := r
	outside.Layers = []Layer{r.Layers[0]}
	outside.Layers[0].Dst = [4]int{4, 0, 8, 8}
	if outside.check() == nil {
		t.Error("a layer running off its picture passed the check")
	}
	elsewhere := r
	elsewhere.Layers = []Layer{r.Layers[0]}
	elsewhere.Layers[0].Texture = "textures/entity/../../secrets"
	if elsewhere.check() == nil {
		t.Error("a layer naming a path outside the textures passed the check")
	}
}

func TestABlankOrFlatFaceIsRefused(t *testing.T) {
	r := walker(t)
	flat := image.NewNRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(flat.Pix); i += 4 {
		copy(flat.Pix[i:], []byte{90, 140, 60, 255})
	}
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": flat}); !errors.Is(err, errImplausible) {
		t.Errorf("a face all of one colour: %v", err)
	}
	if _, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": image.NewNRGBA(image.Rect(0, 0, 64, 32))}); !errors.Is(err, errImplausible) {
		t.Errorf("a blank face: %v", err)
	}
	// A texture that says it is see-through but holds a face is drawn by a
	// material that does not care, and is drawn here.
	hidden := painted(64, 32)
	for i := 3; i < len(hidden.Pix); i += 4 {
		hidden.Pix[i] = 0
	}
	img, err := r.render(map[string]*image.NRGBA{"textures/entity/walker": hidden})
	if err != nil || img.NRGBAAt(0, 0).A != 255 {
		t.Errorf("a face in a texture that calls itself see-through: %v", err)
	}
	// A head too small to be a face is refused before any texture is read.
	tiny := modelsOf(t, `{"geometry.t": {"bones": [{"name": "head", "cubes": [{"origin": [0,0,0], "size": [2, 3, 2], "uv": [0, 0]}]}]}}`)
	if _, err := faceRecipe([]drawn{{model: tiny["geometry.t"], textures: []string{"textures/entity/t"}}}, view{bones: defaultBones, cube: -1}); err == nil {
		t.Error("a head two pixels wide was taken for a face")
	}
	none := modelsOf(t, `{"geometry.n": {"bones": [{"name": "leg0", "cubes": [{"origin": [0,0,0], "size": [8, 8, 8], "uv": [0, 0]}]}]}}`)
	if _, err := faceRecipe([]drawn{{model: none["geometry.n"], textures: []string{"textures/entity/n"}}}, view{bones: defaultBones, cube: -1}); err == nil {
		t.Error("a model with no head gave a face")
	}
}

const syntheticControllers = `{"format_version": "1.8.0", "render_controllers": {
 "controller.render.plain": {"geometry": "Geometry.default", "textures": ["Texture.default"]},
 "controller.render.varied": {
   "arrays": {"textures": {"Array.skins": ["Texture.first", "Texture.second"], "Array.young": ["Texture.baby_first"]},
              "geometries": {"array.shapes": ["Geometry.default", "Geometry.sheared"]}},
   "geometry": "query.is_baby ? Geometry.baby : Array.shapes[query.is_sheared]",
   "textures": ["query.is_tamed ? Texture.tame : (query.is_baby ? Array.young[query.variant] : Array.skins[query.variant])", "Texture.collar"]}
}}`

func TestAControllerIsAskedWhatAGrownDefaultMobWears(t *testing.T) {
	controllers, err := parseControllers([]byte(syntheticControllers))
	if err != nil {
		t.Fatal(err)
	}
	varied := controllers["controller.render.varied"]
	if name, ok := varied.pick(varied.Geometry); !ok || name != "default" {
		t.Errorf("model = %q %v, want the grown, unsheared one", name, ok)
	}
	if name, ok := varied.pick(varied.Textures[0]); !ok || name != "first" {
		t.Errorf("texture = %q %v, want the first of the grown skins", name, ok)
	}
	for expression, want := range map[string]string{
		"Texture.a": "a",
		"query.variant == 0 ? Texture.a : Texture.b":                                          "a",
		"query.variant != 0 ? Texture.a : Texture.b":                                          "b",
		"!query.is_baby && 1.0f > 0.5 ? Texture.a : Texture.b":                                "a",
		"(query.x || query.y) ? Texture.a : Texture.b":                                        "b",
		"query.property('minecraft:climate_variant') == 'warm' ? Texture.warm : Texture.mild": "mild",
		"Array.skins[-1]":                  "second",
		"Array.skins[3]":                   "second",
		"Array.skins[math.floor(1.5 * 0)]": "first",
	} {
		if name, ok := varied.pick(expression); !ok || name != want {
			t.Errorf("%s = %q %v, want %s", expression, name, ok, want)
		}
	}
	// A product that overflows is no index, and is refused, not used.
	huge := strings.Repeat("9", 300)
	for _, expression := range []string{"", "1 + 2", "Array.absent[0]", "Array.skins[" + huge + " * " + huge + "]", "Array.skins[0 / 0 - " + huge + " * " + huge + " + " + huge + " * " + huge + "]", "Texture.a ? ", "((((", strings.Repeat("(", maxExpressionDepth*2) + "Texture.a" + strings.Repeat(")", maxExpressionDepth*2), strings.Repeat("Texture.a || ", 200) + "Texture.a"} {
		if name, ok := varied.pick(expression); ok {
			t.Errorf("%.40q came to %q", expression, name)
		}
	}
	if !holds("query.variant == 0") || holds("query.death_ticks > 1.0") || holds("not an expression (") {
		t.Error("a condition on a default mob was read wrong")
	}
}

func TestAnOverrideIsAppliedInPlaceOfWhatTheModelWouldGive(t *testing.T) {
	// The model's head is a small box; the override names the body.
	models := modelsOf(t, `{"geometry.blob": {"texturewidth": 64, "textureheight": 32, "bones": [
		{"name": "head", "cubes": [{"origin": [-2, 8, -2], "size": [4, 4, 4], "uv": [0, 0]}]},
		{"name": "cube", "cubes": [{"origin": [-3, 1, -3], "size": [6, 6, 6], "uv": [0, 16]}]}]}}`)
	controllers, _ := parseControllers([]byte(syntheticControllers))
	lib := library{models: models, controllers: controllers}
	look := appearance{Textures: map[string]string{"default": "textures/entity/blob"}, Geometry: map[string]string{"default": "geometry.blob"},
		Controllers: []json.RawMessage{json.RawMessage(`"controller.render.plain"`)}}
	plainly, err := lib.face(look, override{})
	if err != nil || plainly.Layers[0].Src != [4]int{4, 4, 4, 4} {
		t.Fatalf("with no override the face is %+v %v, want the head's", plainly, err)
	}
	told, err := lib.face(look, override{bone: "cube"})
	if err != nil || told.Layers[0].Src != [4]int{6, 22, 6, 6} {
		t.Errorf("with the body named the face is %+v %v", told, err)
	}
	side, err := lib.face(look, override{bone: "cube", face: faceUp})
	if err != nil || side.Layers[0].Src != [4]int{6, 16, 6, 6} {
		t.Errorf("with the top named the face is %+v %v", side, err)
	}
	// And through the table itself: a slime's face is on its core, and a
	// parrot keeps its egg, whatever its model would have given.
	looks := map[string]mobLook{"slime": {appearance: look, egg: true}, "parrot": {appearance: look, egg: true}, "boat": {appearance: look}}
	recipes, rejected := plan(lib, looks, atlas{}, atlas{})
	if got := recipes["face/slime"]; len(got.Layers) == 0 || got.Layers[0].Src != [4]int{6, 22, 6, 6} {
		t.Errorf("the slime's override was not applied: %+v", got)
	}
	if _, made := recipes["face/parrot"]; made || !strings.HasPrefix(rejected["face/parrot"], "kept as its spawn egg") {
		t.Errorf("the parrot was not left as its egg: %q", rejected["face/parrot"])
	}
	if _, made := recipes["face/boat"]; made {
		t.Error("something with no spawn egg was given a face")
	}
	for kind, o := range overrides {
		if o.why == "" {
			t.Errorf("the override for %s does not say why", kind)
		}
	}
}

func TestABlockIsDrawnFromThreeSidesEachInItsOwnLight(t *testing.T) {
	solid := func(c color.NRGBA) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		for i := 0; i < len(img.Pix); i += 4 {
			copy(img.Pix[i:], []byte{c.R, c.G, c.B, c.A})
		}
		return img
	}
	top, left, right := color.NRGBA{250, 0, 0, 255}, color.NRGBA{0, 250, 0, 255}, color.NRGBA{0, 0, 250, 255}
	img := isometric(solid(top), solid(left), solid(right), 16, 16, 16)
	if img.Rect.Dx() != 32 || img.Rect.Dy() != 32 {
		t.Fatalf("a 16 pixel block came out %v, want 32 by 32", img.Rect)
	}
	for name, c := range map[string]struct {
		x, y int
		want color.NRGBA
	}{
		"the top":                {16, 8, color.NRGBA{245, 0, 0, 255}},
		"the top's left corner":  {0, 7, color.NRGBA{245, 0, 0, 255}},
		"the top's back corner":  {15, 0, color.NRGBA{245, 0, 0, 255}},
		"the left side":          {6, 20, color.NRGBA{0, 200, 0, 255}},
		"the left side's foot":   {15, 31, color.NRGBA{0, 200, 0, 255}},
		"the right side":         {26, 20, color.NRGBA{0, 0, 152, 255}},
		"the right side's foot":  {16, 31, color.NRGBA{0, 0, 152, 255}},
		"above the left corner":  {0, 0, color.NRGBA{}},
		"below the left corner":  {0, 31, color.NRGBA{}},
		"above the right corner": {31, 0, color.NRGBA{}},
		"below the right corner": {31, 31, color.NRGBA{}},
	} {
		if got := img.NRGBAAt(c.x, c.y); got != c.want {
			t.Errorf("%s, at %d,%d, is %v, want %v", name, c.x, c.y, got, c.want)
		}
	}
	// No colour is in it that is not one side's in its own light: nothing
	// was blended.
	seen := map[color.NRGBA]bool{}
	for y := range 32 {
		for x := range 32 {
			seen[img.NRGBAAt(x, y)] = true
		}
	}
	if len(seen) != 4 {
		t.Errorf("%d colours in a block of three flat sides, want those three and clear", len(seen))
	}
	// A side keeps which way up and which way round it is.
	marked := painted(16, 16)
	img = isometric(solid(top), marked, solid(right), 16, 16, 16)
	if got := img.NRGBAAt(0, 8); got != shade(marked.NRGBAAt(0, 0), shadeLeft) {
		t.Errorf("the left side's top left pixel is %v", got)
	}
	if got := img.NRGBAAt(15, 31); got != shade(marked.NRGBAAt(15, 15), shadeLeft) {
		t.Errorf("the left side's bottom right pixel is %v", got)
	}
	if (&Block{W: 16, H: 16, D: 64}).check() == nil || (&Block{W: 0, H: 16, D: 16}).check() == nil {
		t.Error("a block out of bounds passed the check")
	}
}

func tga(w, h int, kind, descriptor byte, data []byte) []byte {
	return append([]byte{0, 0, kind, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(w), byte(w >> 8), byte(h), byte(h >> 8), 32, descriptor}, data...)
}

func TestATGAIsReadPlainOrPackedAndHeldToBounds(t *testing.T) {
	// Two pixels wide and two tall, stored bottom row first, blue first.
	plain := tga(2, 2, 2, 0, []byte{3, 2, 1, 255, 6, 5, 4, 255, 9, 8, 7, 255, 12, 11, 10, 0})
	img, err := decodeTGA(plain, 16)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 1); got != (color.NRGBA{1, 2, 3, 255}) {
		t.Errorf("the first pixel stored is %v at the bottom left", got)
	}
	if got := img.NRGBAAt(1, 0); got != (color.NRGBA{10, 11, 12, 0}) {
		t.Errorf("the last pixel stored is %v at the top right", got)
	}
	// Packed: a run of three of one pixel, then one by itself, from the top.
	packed := tga(2, 2, 10, 0x20, []byte{0x82, 3, 2, 1, 255, 0x00, 6, 5, 4, 255})
	img, err = decodeTGA(packed, 16)
	if err != nil {
		t.Fatal(err)
	}
	if img.NRGBAAt(0, 0) != (color.NRGBA{1, 2, 3, 255}) || img.NRGBAAt(0, 1) != (color.NRGBA{1, 2, 3, 255}) || img.NRGBAAt(1, 1) != (color.NRGBA{4, 5, 6, 255}) {
		t.Errorf("a packed file read as %v", img.Pix)
	}
	for name, raw := range map[string][]byte{
		"a size past the limit, with no pixels behind it": tga(60000, 60000, 2, 0, nil),
		"pixels that stop short":                          tga(2, 2, 2, 0, []byte{1, 2, 3, 4}),
		"a run that overruns the picture":                 tga(2, 2, 10, 0, []byte{0xff, 3, 2, 1, 255}),
		"a kind this does not read":                       tga(2, 2, 1, 0, make([]byte, 16)),
		"nothing at all":                                  nil,
	} {
		if _, err := decodeTGA(raw, 16); err == nil {
			t.Errorf("%s was read", name)
		}
	}
}

// withMobs adds to the synthetic samples what a face is made from: two
// mobs with a model, a controller and a texture each, one of which is the
// mob a fortress is drawn as.
func withMobs(t *testing.T, s *samples) {
	t.Helper()
	def := func(kind, model, texture string) []byte {
		return fmt.Appendf(nil, `{"minecraft:client_entity":{"description":{"identifier":"minecraft:%s","min_engine_version":"1.8.0",
			"textures":{"default":%q},"geometry":{"default":%q},"render_controllers":["controller.render.plain"],"spawn_egg":{"texture":"spawn_egg_cow"}}}}`, kind, texture, model)
	}
	s.set("resource_pack/entity/blaze.entity.json", def("blaze", "geometry.walker", "textures/entity/blaze"))
	s.set("resource_pack/entity/grazer.entity.json", def("grazer", "geometry.grazer", "textures/entity/grazer"))
	s.set("resource_pack/models/entity/walker.geo.json", []byte(olderModels))
	s.set("resource_pack/models/entity/grazer.geo.json", []byte(newerModels))
	s.set("resource_pack/render_controllers/plain.render_controllers.json", []byte(syntheticControllers))
	s.set("resource_pack/textures/entity/blaze.png", asPNG(t, painted(64, 32)))
	s.set("resource_pack/textures/entity/grazer.png", asPNG(t, painted(64, 64)))
}

func TestFetchMakesAFaceForEachMobAndOneThatFailsCostsNoOther(t *testing.T) {
	s := newSamples(t)
	withMobs(t, s)
	// A third mob whose model is out of bounds, and a fourth whose texture
	// is not the one its model was made for.
	s.set("resource_pack/entity/broken.entity.json", []byte(`{"minecraft:client_entity":{"description":{"identifier":"minecraft:brokenmob","textures":{"default":"textures/entity/blaze"},"geometry":{"default":"geometry.huge"},"render_controllers":["controller.render.plain"],"spawn_egg":{"texture":"spawn_egg_cow"}}}}`))
	s.set("resource_pack/models/entity/huge.geo.json", []byte(`{"geometry.huge": {"bones": [{"name": "head", "cubes": [{"origin": [0, 1e12, 0], "size": [8,8,8], "uv": [0,0]}]}]}}`))
	s.set("resource_pack/entity/misfit.entity.json", []byte(`{"minecraft:client_entity":{"description":{"identifier":"minecraft:misfit","textures":{"default":"textures/entity/misfit"},"geometry":{"default":"geometry.walker"},"render_controllers":["controller.render.plain"],"spawn_egg":{"texture":"spawn_egg_cow"}}}}`))
	s.set("resource_pack/textures/entity/misfit.png", asPNG(t, painted(16, 16)))
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"face/blaze", "face/grazer"} {
		raw, ok := set.Pictures[key]
		if !ok {
			t.Fatalf("no %s: rejected %v, missing %v", key, set.Rejected, set.Missing)
		}
		if cfg, err := png.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 8 || cfg.Height != 8 {
			t.Errorf("%s is %dx%d %v", key, cfg.Width, cfg.Height, err)
		}
	}
	for _, key := range []string{"face/brokenmob", "face/misfit"} {
		if _, made := set.Pictures[key]; made || set.Rejected[key] == "" {
			t.Errorf("%s: made %v, rejected %q", key, made, set.Rejected[key])
		}
	}
	if len(set.Missing) != 0 {
		t.Errorf("missing = %v; a face that cannot be made is not one to ask for again", set.Missing)
	}
	// A fortress is drawn as the blaze's face, not as the item it was.
	if !bytes.Equal(set.Pictures["structure/fortress"], set.Pictures["face/blaze"]) {
		t.Error("the fortress is not drawn as its mob's face")
	}
	// And a structure whose mob has no face keeps its item.
	if colourOf(t, set.Pictures["structure/outpost"]) != yellow {
		t.Error("a structure with no face to use lost its item")
	}
	if len(set.Mobs) != 7 {
		t.Errorf("%d mob icons: the faces cost some of them", len(set.Mobs))
	}
}

func TestAFaceWhoseTextureCouldNotBeFetchedIsMadeLaterFromItsRecipeAlone(t *testing.T) {
	s := newSamples(t)
	withMobs(t, s)
	var up atomic.Bool
	var asked []string
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/textures/entity/grazer.png") && !up.Load() {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if up.Load() {
			s.mu.Lock()
			asked = append(asked, r.URL.Path)
			s.mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	})
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(set.Missing, "face/grazer") || !slices.Contains(set.Unreached, "face/grazer") {
		t.Fatalf("missing %v, unreached %v: the face is not marked to be asked for again", set.Missing, set.Unreached)
	}
	if _, ok := set.Pictures["face/blaze"]; !ok {
		t.Error("the other mob's face was lost with it")
	}
	if _, ok := set.Recipes["face/grazer"]; !ok {
		t.Fatal("no recipe was kept for the face that could not be made")
	}
	up.Store(true)
	got, err := s.source().Fill(t.Context(), set.Missing, set.Recipes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Pictures["face/grazer"]; !ok || len(got.Missing) != 0 {
		t.Errorf("after the source came back: pictures %v, missing %v", keys(got.Pictures), got.Missing)
	}
	// Its texture, and that of whatever else was not asked for once the
	// source was found to be out of reach; nothing but textures.
	if got.Replanned || !slices.ContainsFunc(asked, func(path string) bool { return strings.HasSuffix(path, "/textures/entity/grazer.png") }) ||
		slices.ContainsFunc(asked, func(path string) bool { return !strings.Contains(path, "/resource_pack/textures/") }) {
		t.Errorf("asked for %v, want textures and no listing, model or definition", asked)
	}
}

func TestWhenAModelCannotBeReadNoFaceIsMadeAndAllAreAskedForAgain(t *testing.T) {
	s := newSamples(t)
	withMobs(t, s)
	var up atomic.Bool
	inner := s.srv.Config.Handler
	s.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/models/") && !up.Load() {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		inner.ServeHTTP(w, r)
	})
	set, err := s.source().Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Mobs) != 5 || len(set.Pictures) != len(pictureKeys()) {
		t.Errorf("%d mob icons and %d pictures: the models cost what does not need them", len(set.Mobs), len(set.Pictures))
	}
	if !slices.Contains(set.Missing, artPlan) || !slices.Contains(set.Unreached, artPlan) || set.Recipes != nil {
		t.Fatalf("missing %v, unreached %v", set.Missing, set.Unreached)
	}
	up.Store(true)
	got, err := s.source().Fill(t.Context(), set.Missing, set.Recipes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Pictures["face/blaze"]; !ok || !got.Replanned || len(got.Missing) != 0 {
		t.Errorf("after the source came back: replanned %v, missing %v", got.Replanned, got.Missing)
	}
}

func TestAListingCutShortIsRefused(t *testing.T) {
	s := newSamples(t)
	s.truncated = true
	if _, err := s.source().Fetch(t.Context()); err == nil {
		t.Error("a listing that says it was cut short was taken as whole")
	}
}

// A volume written before any picture was made holds an index with no
// recipes and no revision. Everything it holds goes on being served, the
// pictures made here are asked for, and what was a structure's item is
// replaced by its mob's face when that arrives.
func TestAVolumeFromBeforeMadePicturesKeepsServingAndIsAddedTo(t *testing.T) {
	dir := t.TempDir()
	item := picture(t, 16, 16, yellow)
	first := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
		return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Entities: []string{"cow"},
			Pictures: map[string][]byte{"container/chest": picture(t, 16, 16, green), "structure/fortress": item}}, nil
	}}
	first.Run(t.Context())
	path := filepath.Join(first.home(), indexFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]any
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatal(err)
	}
	if idx["art"] == nil {
		t.Fatal("this version writes no revision; the test would prove nothing")
	}
	delete(idx, "art")
	delete(idx, "recipes")
	delete(idx, "rejected")
	raw, _ = json.Marshal(idx)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	face := asPNG(t, painted(8, 8))
	var asked [][]string
	held := make(chan struct{})
	m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t),
		Fill: func(_ context.Context, missing []string, recipes map[string]Recipe) (Set, error) {
			asked = append(asked, slices.Clone(missing))
			if len(asked) == 1 {
				close(held)
			}
			if len(recipes) != 0 {
				t.Errorf("recipes from another revision were handed on: %v", recipes)
			}
			return Set{Replanned: true, Recipes: map[string]Recipe{"face/cow": {Flat: "textures/items/x"}}, Rejected: map[string]string{"face/pig": "no head"},
				Pictures: map[string][]byte{"face/cow": face, "structure/fortress": face}}, nil
		}}
	m.Run(t.Context())
	<-held
	if len(asked) != 1 || !slices.Contains(asked[0], artPlan) {
		t.Fatalf("asked for %v, want the made pictures, once", asked)
	}
	if raw, ok := m.Picture("container/chest"); !ok || colourOf(t, raw) != green {
		t.Error("a picture the volume held was lost")
	}
	if raw, ok := m.Icon("cow"); !ok || colourOf(t, raw) != red {
		t.Error("a mob icon the volume held was lost")
	}
	if raw, _ := m.Picture("structure/fortress"); !bytes.Equal(raw, face) {
		t.Error("the structure's picture was not replaced by the one made for it")
	}
	if _, ok := m.Picture("face/cow"); !ok || m.Rejected()["face/pig"] == "" {
		t.Error("what was made, and why what was not was not, is not served")
	}
	again := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: neverFetch(t),
		Fill: func(_ context.Context, missing []string, _ map[string]Recipe) (Set, error) {
			t.Errorf("asked again for %v after it was made and kept", missing)
			return Set{}, nil
		}}
	again.Run(t.Context())
	if _, ok := again.Picture("face/cow"); !ok || again.Rejected()["face/pig"] == "" {
		t.Error("what was made was not kept on the volume")
	}
}

func TestAnIndexWithARecipeOutOfBoundsIsNotTrusted(t *testing.T) {
	for name, recipe := range map[string]Recipe{
		"a path outside the textures": {Flat: "textures/items/../../../etc/passwd"},
		"a face too large":            {W: 4000, H: 4000, Main: [4]int{0, 0, 8, 8}, Layers: []Layer{{Texture: "textures/entity/x", Src: [4]int{0, 0, 8, 8}, Dst: [4]int{0, 0, 8, 8}}}},
		"a layer off its picture":     {W: 8, H: 8, Main: [4]int{0, 0, 8, 8}, Layers: []Layer{{Texture: "textures/entity/x", Src: [4]int{0, 0, 8, 8}, Dst: [4]int{0, 0, 80, 8}}}},
		"a block too large":           {Block: &Block{W: 1000, H: 16, D: 16}},
	} {
		dir := t.TempDir()
		m := &Mobs{Dir: dir, Ref: testRef, Logger: quiet(), Fetch: func(context.Context) (Set, error) {
			return Set{Mobs: map[string][]byte{"cow": picture(t, 16, 16, red)}, Recipes: map[string]Recipe{"face/cow": recipe}}, nil
		}}
		m.Run(t.Context())
		if _, _, err := (&Mobs{Dir: dir, Ref: testRef}).load(); err == nil {
			t.Errorf("an index holding %s was read back as good", name)
		}
	}
}
