package icons

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// A long body with a neck hung off its front, tipped forward by a quarter
// turn, and a head on the neck that is not turned of itself. Each box's
// texture is a rectangle of its own, so a layer says which box it is.
const tippedModel = `{"format_version": "1.12.0", "minecraft:geometry": [
 {"description": {"identifier": "geometry.tipped", "texture_width": 64, "texture_height": 64},
  "bones": [
   {"name": "body", "pivot": [0, 0, 0], "cubes": [{"origin": [-2, 0, -4], "size": [4, 4, 8], "uv": [0, 0]}]},
   {"name": "neck", "parent": "body", "pivot": [0, 4, -4], "rotation": [90, 0, 0], "cubes": [{"origin": [-1, 4, -5], "size": [2, 6, 2], "uv": [0, 16]}]},
   {"name": "head", "parent": "neck", "pivot": [0, 10, -4], "cubes": [{"origin": [-1, 10, -5], "size": [2, 2, 2], "uv": [0, 32]}]},
   {"name": "wing", "parent": "body", "pivot": [0, 4, 0], "rotation": [0, 40, 0], "cubes": [{"origin": [2, 2, -2], "size": [6, 1, 4], "uv": [32, 0]}]},
   {"name": "feather", "parent": "wing", "cubes": [{"origin": [8, 2, -2], "size": [2, 1, 2], "uv": [32, 16]}]},
   {"name": "fin", "parent": "body", "cubes": [{"origin": [0, 4, 0], "size": [0, 2, 3], "uv": [48, 0]}]},
   {"name": "lying", "parent": "body", "pivot": [0, 0, 4], "bind_pose_rotation": [90, 0, 0], "cubes": [{"origin": [-1, 0, 4], "size": [2, 4, 1], "uv": [48, 16]}]},
   {"name": "lyingChild", "parent": "lying", "cubes": [{"origin": [-1, 0, 6], "size": [2, 1, 1], "uv": [48, 32]}]}
  ]}
]}`

func sideParts(t *testing.T) map[string][]sidePart {
	t.Helper()
	m := modelsOf(t, tippedModel)["geometry.tipped"]
	out := map[string][]sidePart{}
	for _, p := range m.sideOf(faceWest, nil, nil, nil) {
		out[p.bone] = append(out[p.bone], p)
	}
	return out
}

func near2(a, b [2]float64) bool { return math.Hypot(a[0]-b[0], a[1]-b[1]) < 1e-9 }

func TestABoneTurnedAboutItsFlankIsDrawnTurnedWithAllThatHangsOffIt(t *testing.T) {
	parts := sideParts(t)
	body, neck, head := parts["body"], parts["neck"], parts["head"]
	if len(body) != 1 || len(neck) != 1 || len(head) != 1 {
		t.Fatalf("parts = %+v", parts)
	}
	// Seen with its front to the left and y downwards: the neck, which
	// stood up from the front of the body, is tipped forward to lie ahead
	// of it, a quarter turn anticlockwise about its pivot, and the head
	// goes with it, further forward again.
	at := func(p sidePart) [2]float64 { return [2]float64{p.cx - body[0].cx, p.cy - body[0].cy} }
	if !near2(at(neck[0]), [2]float64{-7, -2}) || math.Abs(neck[0].angle+90) > 1e-9 {
		t.Errorf("the neck is at %v turned %v, want -7,-2 turned -90", at(neck[0]), neck[0].angle)
	}
	if !near2(at(head[0]), [2]float64{-11, -2}) || math.Abs(head[0].angle+90) > 1e-9 {
		t.Errorf("the head is at %v turned %v, want -11,-2 turned -90 with the neck", at(head[0]), head[0].angle)
	}
}

func TestABoneTurnedOutOfTheViewIsLeftOutWithWhatHangsOffIt(t *testing.T) {
	parts := sideParts(t)
	if len(parts["wing"]) > 0 || len(parts["feather"]) > 0 {
		t.Errorf("a wing turned 40 degrees about y, and the feather on it, were drawn: %+v %+v", parts["wing"], parts["feather"])
	}
}

func TestABindPoseTurnsTheBonesOwnBoxesAndNothingElse(t *testing.T) {
	parts := sideParts(t)
	lying, child := parts["lying"], parts["lyingChild"]
	if len(lying) != 1 || len(child) != 1 {
		t.Fatalf("lying = %+v, child = %+v", lying, child)
	}
	if math.Abs(lying[0].angle+90) > 1e-9 {
		t.Errorf("the posed box is turned %v, want -90", lying[0].angle)
	}
	// The child is placed as the file places it: the older models give
	// what hangs off a posed bone where it ends up.
	if child[0].angle != 0 || !near2([2]float64{child[0].cx, child[0].cy}, [2]float64{6.5, -0.5}) {
		t.Errorf("the child of a posed bone is at %v,%v turned %v", child[0].cx, child[0].cy, child[0].angle)
	}
}

func TestASheetIsDrawnFromWhicheverSideTheTextureFills(t *testing.T) {
	fin := sideParts(t)["fin"]
	if len(fin) != 2 || fin[0].src == fin[1].src || fin[0].src.Flip == fin[1].src.Flip {
		t.Fatalf("a fin with no thickness gave %+v, want both its faces, the far one turned", fin)
	}
	if fin[0].cx != fin[1].cx || fin[0].cy != fin[1].cy || fin[0].w != fin[1].w {
		t.Errorf("the two faces of a sheet are not in one place: %+v", fin)
	}
}

func TestAProfileIsMadeOnlyFromTheBonesItNames(t *testing.T) {
	m := modelsOf(t, tippedModel)["geometry.tipped"]
	for _, p := range m.sideOf(faceWest, []string{"neck"}, nil, nil) {
		if p.bone != "neck" && p.bone != "head" {
			t.Errorf("%s was drawn in a profile of the neck", p.bone)
		}
	}
	for _, p := range m.sideOf(faceWest, nil, []string{"neck"}, nil) {
		if p.bone == "neck" || p.bone == "head" {
			t.Errorf("%s was drawn though the neck is skipped", p.bone)
		}
	}
	for _, p := range m.sideOf(faceWest, nil, nil, func(b string) bool { return b == "body" }) {
		t.Errorf("%s was drawn though the body it hangs off is hidden", p.bone)
	}
}

func TestATurnedLayerTakesEachPixelFromWhereItLiesOnTheTexture(t *testing.T) {
	l := Layer{Texture: "textures/entity/t", Src: [4]int{0, 0, 4, 2}, Span: [2]float64{4, 2}, Centre: [2]float64{1, 2}, Angle: 90, Skin: true}
	b := freeBounds(l)
	l.Dst = [4]int{b.Min.X, b.Min.Y, b.Dx(), b.Dy()}
	r := Recipe{W: 2, H: 4, Main: [4]int{0, 0, 2, 4}, Layers: []Layer{l}}
	if err := r.check(); err != nil {
		t.Fatal(err)
	}
	// Composed and not rendered: a texture four pixels a side is too
	// alike in colour to pass for a face.
	img, err := compose(r.Layers, r.W, r.H, 1, map[string]*image.NRGBA{"textures/entity/t": painted(4, 4)}, l.Texture, false)
	if err != nil {
		t.Fatal(err)
	}
	// A quarter turn clockwise: the texture's top row runs down the
	// picture's right-hand column.
	for at, want := range map[[2]int][2]int{{1, 0}: {0, 0}, {0, 0}: {0, 1}, {1, 3}: {3, 0}, {0, 3}: {3, 1}} {
		if got := from(img.NRGBAAt(at[0], at[1])); got != want {
			t.Errorf("pixel %v came from %v of the texture, want %v", at, got, want)
		}
	}
}

func TestALayerSetByItsMiddleIsHeldToItsPicture(t *testing.T) {
	good := Layer{Texture: "textures/entity/t", Src: [4]int{0, 0, 4, 2}, Span: [2]float64{4, 2}, Centre: [2]float64{2, 1}, Dst: [4]int{0, 0, 4, 2}}
	if err := checkLayers([]Layer{good}, 4, 2); err != nil {
		t.Fatalf("a layer that fills its picture was refused: %v", err)
	}
	for name, l := range map[string]Layer{
		"its bounds misstated":   func() Layer { l := good; l.Dst = [4]int{0, 0, 3, 2}; return l }(),
		"outside the picture":    func() Layer { l := good; l.Centre = [2]float64{3, 1}; l.Dst = [4]int{1, 0, 4, 2}; return l }(),
		"turned with no middle":  {Texture: "textures/entity/t", Src: [4]int{0, 0, 4, 2}, Dst: [4]int{0, 0, 4, 2}, Angle: 30},
		"of no size":             func() Layer { l := good; l.Span = [2]float64{-4, 2}; return l }(),
		"a number out of range":  func() Layer { l := good; l.Angle = math.NaN(); return l }(),
		"tinted by no colour":    func() Layer { l := good; l.Tint = "orange"; return l }(),
		"turned past all reason": func() Layer { l := good; l.Angle = 1e6; return l }(),
	} {
		if err := checkLayers([]Layer{l}, 4, 2); err == nil {
			t.Errorf("a layer %s was accepted", name)
		}
	}
}

func TestATintMultipliesTheTexture(t *testing.T) {
	if got := multiplied(color.NRGBA{200, 100, 50, 255}, "#800000"); got != (color.NRGBA{100, 0, 0, 255}) {
		t.Errorf("tinted = %v", got)
	}
	if got := multiplied(color.NRGBA{200, 100, 50, 7}, ""); got != (color.NRGBA{200, 100, 50, 7}) {
		t.Errorf("no tint changed the colour to %v", got)
	}
}

func TestAProfileThatIsAStripIsNoPictureUnlessTheMobIsOne(t *testing.T) {
	models := modelsOf(t, `{"format_version": "1.12.0", "minecraft:geometry": [
 {"description": {"identifier": "geometry.eel", "texture_width": 64, "texture_height": 32},
  "bones": [{"name": "body", "cubes": [{"origin": [-1, 0, -10], "size": [2, 2, 20], "uv": [0, 0]}]}]}]}`)
	lib := library{models: models}
	look := appearance{Textures: map[string]string{"default": "textures/entity/eel"}, Geometry: map[string]string{"default": "geometry.eel"}}
	if _, err := lib.face(look, override{profile: true, textures: []string{"default"}}); err == nil {
		t.Error("a profile 20 by 2 was made")
	}
	r, err := lib.face(look, override{profile: true, strip: true, textures: []string{"default"}})
	if err != nil || r.W != 20 || r.H != 2 {
		t.Errorf("a mob that is a strip gave %+v %v", r, err)
	}
}
