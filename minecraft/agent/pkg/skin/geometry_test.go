package skin

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
)

// modelFile is a synthetic geometry file of one model, built from the
// bones given as JSON.
func modelFile(name string, tw, th int, bones ...string) []byte {
	return []byte(fmt.Sprintf(`{"format_version":"1.14.0","minecraft:geometry":[{"description":{"identifier":%q,"texture_width":%d,"texture_height":%d},"bones":[%s]}]}`,
		name, tw, th, strings.Join(bones, ",")))
}

func boneOf(name, body string) string {
	if body == "" {
		return fmt.Sprintf(`{"name":%q}`, name)
	}
	return fmt.Sprintf(`{"name":%q,%s}`, name, body)
}

func boxCube(u, v int) string {
	return fmt.Sprintf(`{"origin":[-4,24,-4],"size":[8,8,8],"uv":[%d,%d]}`, u, v)
}

func cubes(c ...string) string { return `"cubes":[` + strings.Join(c, ",") + `]` }

const custom = "geometry.some_pack.fox"

func headOf(t *testing.T, raw []byte, name string, w, h int) (HeadUV, error) {
	t.Helper()
	g, err := ParseGeometry(raw)
	if err != nil {
		return HeadUV{}, err
	}
	return g.Head(name, w, h)
}

func solid(c rgba) color.NRGBA { return color.NRGBA{c.r, c.g, c.b, 255} }

// A model that keeps its head somewhere a classic skin does not: the face
// is found from the head bone's cube and the layer over it from the hat's.
func TestHeadIsFoundWhereTheModelSaysItIs(t *testing.T) {
	raw := modelFile(custom, 64, 64,
		boneOf("body", cubes(`{"origin":[-4,12,-2],"size":[8,12,4],"uv":[0,0]}`)),
		boneOf("head", cubes(boxCube(16, 32))),
		boneOf("hat", cubes(`{"origin":[-4,24,-4],"size":[8,8,8],"inflate":0.5,"uv":[32,32]}`)))
	at, err := headOf(t, raw, custom, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	// The north face of a box lies one depth right of and below its corner.
	if at.Face != image.Rect(24, 40, 32, 48) || at.Hat != image.Rect(40, 40, 48, 48) {
		t.Fatalf("face %v, hat %v", at.Face, at.Hat)
	}

	px := canvas(64, 64, elsewhere)
	paint(px, 64, 24, 40, 8, 8, skinTone)
	paint(px, 64, 40, 40, 8, 8, clear)
	paint(px, 64, 40, 40, 8, 1, hair)
	head, err := HeadAt(64, 64, px, at)
	if err != nil {
		t.Fatal(err)
	}
	if head.Bounds() != image.Rect(0, 0, 8, 8) {
		t.Fatalf("head is %v", head.Bounds())
	}
	if got := head.NRGBAAt(7, 0); got != solid(hair) {
		t.Errorf("top row is %v, want the hat", got)
	}
	if got := head.NRGBAAt(0, 1); got != solid(skinTone) {
		t.Errorf("below it is %v, want the face", got)
	}
}

func TestHeadFollowsTheImageAtAMultipleOfTheDeclaredTexture(t *testing.T) {
	raw := modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(16, 32))))
	at, err := headOf(t, raw, custom, 128, 128)
	if err != nil {
		t.Fatal(err)
	}
	if at.Face != image.Rect(48, 80, 64, 96) || !at.Hat.Empty() {
		t.Errorf("face %v, hat %v", at.Face, at.Hat)
	}
}

// The shape production reported for a character-creator skin: an image
// taller than it is wide. Nothing here knows where such a skin keeps its
// head; a model that says where, in a texture of that shape, is followed.
func TestHeadInATextureThatIsNotSquare(t *testing.T) {
	raw := modelFile(custom, 64, 128, boneOf("head", cubes(boxCube(0, 64))))
	at, err := headOf(t, raw, custom, 64, 128)
	if err != nil {
		t.Fatal(err)
	}
	if at.Face != image.Rect(8, 72, 16, 80) {
		t.Errorf("face %v", at.Face)
	}
}

func TestHeadFromACubeThatMapsEachFace(t *testing.T) {
	raw := modelFile(custom, 64, 64, boneOf("HEAD", cubes(
		`{"origin":[-4,24,-4],"size":[8,8,8],"uv":{"north":{"uv":[20,4],"uv_size":[10,10]},"up":{"uv":[0,0],"uv_size":[8,8]}}}`)))
	at, err := headOf(t, raw, custom, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if at.Face != image.Rect(20, 4, 30, 14) {
		t.Errorf("face %v", at.Face)
	}

	// A face that names no size is as big as the cube.
	raw = modelFile(custom, 64, 64, boneOf("head", cubes(`{"size":[8,8,8],"uv":{"north":{"uv":[20,4]}}}`)))
	if at, err = headOf(t, raw, custom, 64, 64); err != nil || at.Face != image.Rect(20, 4, 28, 12) {
		t.Errorf("face %v, err %v", at.Face, err)
	}
}

// Ears and horns are cubes on the head bone too. The head is the largest,
// and a second cube of its size is the layer drawn over it.
func TestHeadIsTheLargestCubeAndItsTwinTheLayer(t *testing.T) {
	raw := modelFile(custom, 64, 64, boneOf("head", cubes(
		`{"size":[2,2,1],"uv":[0,0]}`,
		boxCube(0, 16),
		`{"size":[8,8,8],"inflate":0.5,"uv":[32,16]}`)))
	at, err := headOf(t, raw, custom, 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	if at.Face != image.Rect(8, 24, 16, 32) || at.Hat != image.Rect(40, 24, 48, 32) {
		t.Errorf("face %v, hat %v", at.Face, at.Hat)
	}
}

// What a model can say that this does not follow. Each is refused with its
// own reason rather than read as something it might not be.
func TestHeadRefusesAModelItDoesNotUnderstand(t *testing.T) {
	head := func(cube string) []byte { return modelFile(custom, 64, 64, boneOf("head", cubes(cube))) }
	for name, c := range map[string]struct {
		raw  []byte
		w, h int
		want GeometryError
	}{
		"another model's file":  {modelFile("geometry.other", 64, 64, boneOf("head", cubes(boxCube(0, 0)))), 64, 64, ErrNoModel},
		"no head bone":          {modelFile(custom, 64, 64, boneOf("body", cubes(boxCube(0, 0)))), 64, 64, ErrNoHead},
		"a head that is a bone": {modelFile(custom, 64, 64, boneOf("head", "")), 64, 64, ErrHeadEmpty},
		"a head of polygons":    {modelFile(custom, 64, 64, boneOf("head", `"poly_mesh":{"polys":"quad_list"}`)), 64, 64, ErrHeadMesh},
		"a turned bone":         {modelFile(custom, 64, 64, boneOf("head", `"rotation":[0,90,0],`+cubes(boxCube(0, 0)))), 64, 64, ErrHeadCube},
		"a turned cube":         {head(`{"size":[8,8,8],"uv":[0,0],"rotation":[0,0,180]}`), 64, 64, ErrHeadCube},
		"a mirrored bone":       {modelFile(custom, 64, 64, boneOf("head", `"mirror":true,`+cubes(boxCube(0, 0)))), 64, 64, ErrHeadCube},
		"a mirrored cube":       {head(`{"size":[8,8,8],"uv":[0,0],"mirror":true}`), 64, 64, ErrHeadCube},
		"a cube of no size":     {head(`{"uv":[0,0]}`), 64, 64, ErrHeadCube},
		"a size of two numbers": {head(`{"size":[8,8],"uv":[0,0]}`), 64, 64, ErrHeadCube},
		"half a unit":           {head(`{"size":[8,8.5,8],"uv":[0,0]}`), 64, 64, ErrHeadCube},
		"a negative size":       {head(`{"size":[8,-8,8],"uv":[0,0]}`), 64, 64, ErrHeadCube},
		"no texture at all":     {head(`{"size":[8,8,8]}`), 64, 64, ErrHeadCube},
		"a corner of one":       {head(`{"size":[8,8,8],"uv":[0]}`), 64, 64, ErrHeadCube},
		"a corner of words":     {head(`{"size":[8,8,8],"uv":["a","b"]}`), 64, 64, ErrHeadCube},
		"no north face":         {head(`{"size":[8,8,8],"uv":{"south":{"uv":[0,0]}}}`), 64, 64, ErrHeadCube},
		"a turned face":         {head(`{"size":[8,8,8],"uv":{"north":{"uv":[0,0],"uv_rotation":90}}}`), 64, 64, ErrHeadCube},
		"a face of three sizes": {head(`{"size":[8,8,8],"uv":{"north":{"uv":[0,0],"uv_size":[8,8,8]}}}`), 64, 64, ErrHeadCube},

		"a negative corner":       {head(`{"size":[8,8,8],"uv":[-16,0]}`), 64, 64, ErrHeadUV},
		"a corner between pixels": {head(`{"size":[8,8,8],"uv":[0.5,0]}`), 64, 64, ErrHeadUV},
		"a flipped face":          {head(`{"size":[8,8,8],"uv":{"north":{"uv":[16,16],"uv_size":[-8,8]}}}`), 64, 64, ErrHeadUV},
		"an empty face":           {head(`{"size":[8,8,8],"uv":{"north":{"uv":[16,16],"uv_size":[0,8]}}}`), 64, 64, ErrHeadUV},
		"off the right edge":      {head(`{"size":[8,8,8],"uv":[49,0]}`), 64, 64, ErrHeadUV},
		"off the bottom edge":     {head(`{"size":[8,8,8],"uv":[0,49]}`), 64, 64, ErrHeadUV},
		"far outside":             {head(`{"size":[8,8,8],"uv":[4000,4000]}`), 64, 64, ErrHeadUV},
		"beyond any number":       {head(`{"size":[8,8,8],"uv":[1e300,0]}`), 64, 64, ErrHeadUV},
		// Two to the sixty-fourth: converted unchecked and doubled for the
		// image's scale, it would wrap round to the image's own corner.
		"a number that wraps": {head(`{"size":[8,8,8],"uv":{"north":{"uv":[18446744073709551616,0]}}}`), 128, 128, ErrHeadUV},
		"a face beyond any":   {head(`{"size":[8,8,8],"uv":{"north":{"uv":[0,0],"uv_size":[1e300,1e300]}}}`), 64, 64, ErrHeadUV},

		"a face wider than tall": {head(`{"size":[12,8,8],"uv":[0,0]}`), 64, 64, ErrHeadShape},
		"a face too small":       {head(`{"size":[4,4,4],"uv":[0,0]}`), 64, 64, ErrHeadShape},

		"no declared texture":              {[]byte(`{"minecraft:geometry":[{"description":{"identifier":"` + custom + `"},"bones":[` + boneOf("head", cubes(boxCube(0, 0))) + `]}]}`), 64, 64, ErrHeadTexture},
		"a texture of nothing":             {modelFile(custom, 0, 0, boneOf("head", cubes(boxCube(0, 0)))), 64, 64, ErrHeadTexture},
		"a texture declared one way":       {[]byte(`{"minecraft:geometry":[{"description":{"identifier":"` + custom + `","texture_width":64},"bones":[` + boneOf("head", cubes(boxCube(0, 0))) + `]}]}`), 64, 64, ErrHeadTexture},
		"a texture of half a pixel":        {[]byte(`{"minecraft:geometry":[{"description":{"identifier":"` + custom + `","texture_width":64.5,"texture_height":64},"bones":[` + boneOf("head", cubes(boxCube(0, 0))) + `]}]}`), 64, 64, ErrHeadTexture},
		"a texture beyond any":             {[]byte(`{"minecraft:geometry":[{"description":{"identifier":"` + custom + `","texture_width":1e300,"texture_height":1e300},"bones":[` + boneOf("head", cubes(boxCube(0, 0))) + `]}]}`), 64, 64, ErrHeadTexture},
		"an image smaller than declared":   {modelFile(custom, 128, 128, boneOf("head", cubes(boxCube(0, 0)))), 64, 64, ErrHeadTexture},
		"an image no multiple of declared": {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0)))), 96, 96, ErrHeadTexture},
		"an image stretched one way":       {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0)))), 64, 128, ErrHeadTexture},
		"an image of nothing":              {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0)))), 0, 0, ErrHeadTexture},
		"an image too large":               {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0)))), 2048, 2048, ErrHeadTexture},

		"a layer off the image":  {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))), boneOf("hat", cubes(boxCube(56, 0)))), 64, 64, ErrHatUV},
		"a layer that is turned": {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))), boneOf("hat", cubes(`{"size":[8,8,8],"uv":[32,0],"rotation":[0,45,0]}`))), 64, 64, ErrHatUV},
		"a layer of another size on the image": {modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))),
			boneOf("hat", cubes(`{"size":[8,8,8],"uv":{"north":{"uv":[32,8],"uv_size":[4,4]}}}`))), 64, 64, ErrHatUV},
	} {
		t.Run(name, func(t *testing.T) {
			at, err := headOf(t, c.raw, custom, c.w, c.h)
			if !errors.Is(err, c.want) || at != (HeadUV{}) {
				t.Errorf("head %v, err %v, want %v", at, err, c.want)
			}
		})
	}
}

// A hat of another shape than the head, a brim or a crown, is no layer
// over the face and costs the player nothing.
func TestAHatOfAnotherShapeIsLeftOff(t *testing.T) {
	raw := modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))), boneOf("hat", cubes(`{"size":[10,2,10],"uv":[60,60]}`)))
	at, err := headOf(t, raw, custom, 64, 64)
	if err != nil || !at.Hat.Empty() || at.Face != image.Rect(8, 8, 16, 16) {
		t.Errorf("face %v, hat %v, err %v", at.Face, at.Hat, err)
	}
}

// The file is as large, as deep and as crowded as another player's client
// cared to make it.
func TestParseGeometryBoundsWhatAFileCanCost(t *testing.T) {
	good := modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))))
	padded := append(append([]byte(`{"pad":"`), make([]byte, MaxGeometryBytes)...), `"}`...)
	for i := range padded[8 : len(padded)-2] {
		padded[8+i] = 'a'
	}
	deep := []byte(`{"minecraft:geometry":` + strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth) + `}`)
	crowded := []byte(`{"minecraft:geometry":[{"description":{"identifier":"` + custom + `"},"bones":[` + strings.Repeat(`{},`, maxContainers) + `{}]}]}`)
	manyModels := []byte(`{"minecraft:geometry":[` + strings.Repeat(`{},`, maxModels) + `{}]}`)

	for name, c := range map[string]struct {
		raw  []byte
		want GeometryError
	}{
		"too many bytes":      {padded, ErrGeometrySize},
		"nested too deep":     {deep, ErrGeometryDepth},
		"too many values":     {crowded, ErrGeometryCount},
		"too many models":     {manyModels, ErrGeometryCount},
		"not JSON":            {[]byte(`{"minecraft:geometry":`), ErrGeometryJSON},
		"a list for a file":   {[]byte(`[1,2,3]`), ErrGeometryJSON},
		"words for numbers":   {[]byte(`{"minecraft:geometry":[{"bones":[{"cubes":[{"size":"big"}]}]}]}`), ErrGeometryJSON},
		"a number out of all": {[]byte(`{"minecraft:geometry":[{"bones":[{"cubes":[{"size":[1e999,1,1]}]}]}]}`), ErrGeometryJSON},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := ParseGeometry(c.raw)
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			if g == nil || g.Facts().Bytes != len(c.raw) || len(g.Facts().Models) != 0 {
				t.Errorf("a refused file is described as %+v", g.Facts())
			}
		})
	}
	if _, err := ParseGeometry(good); err != nil {
		t.Errorf("a file of one model: %v", err)
	}
	// Brackets inside a string are text, not nesting.
	quoted := []byte(`{"format_version":"` + strings.Repeat("[{", 4*maxDepth) + `\"[","minecraft:geometry":[]}`)
	if _, err := ParseGeometry(quoted); err != nil {
		t.Errorf("brackets in a string: %v", err)
	}
}

func TestHeadBoundsTheBonesAndCubesItLooksThrough(t *testing.T) {
	bones := make([]string, 0, maxBones+1)
	for range maxBones {
		bones = append(bones, boneOf("limb", ""))
	}
	bones = append(bones, boneOf("head", cubes(boxCube(0, 0))))
	if _, err := headOf(t, modelFile(custom, 64, 64, bones...), custom, 64, 64); !errors.Is(err, ErrGeometryCount) {
		t.Errorf("%d bones: %v", len(bones), err)
	}
	if _, err := headOf(t, modelFile(custom, 64, 64, bones[1:]...), custom, 64, 64); err != nil {
		t.Errorf("%d bones: %v", len(bones)-1, err)
	}

	many := make([]string, maxCubes+1)
	for i := range many {
		many[i] = boxCube(0, 0)
	}
	if _, err := headOf(t, modelFile(custom, 64, 64, boneOf("head", cubes(many...))), custom, 64, 64); !errors.Is(err, ErrGeometryCount) {
		t.Errorf("%d cubes on the head: %v", len(many), err)
	}
	if _, err := headOf(t, modelFile(custom, 64, 64, boneOf("head", cubes(boxCube(0, 0))), boneOf("hat", cubes(many...))), custom, 64, 64); !errors.Is(err, ErrGeometryCount) {
		t.Errorf("%d cubes on the hat: %v", len(many), err)
	}
}

func TestNoGeometryIsWhatABuiltInModelSends(t *testing.T) {
	for _, raw := range []string{"", "null", " null\n", "\n"} {
		if !NoGeometry([]byte(raw)) {
			t.Errorf("%q is read as a model", raw)
		}
	}
	if NoGeometry([]byte(`{}`)) || NoGeometry([]byte(`nulls`)) {
		t.Error("a file is read as none")
	}
}

// HeadAt is given rectangles and trusts none of them.
func TestHeadAtRefusesWhatLiesOutsideTheImage(t *testing.T) {
	px := canvas(64, 64, skinTone)
	face := image.Rect(8, 8, 16, 16)
	for name, c := range map[string]struct {
		w, h int
		px   []byte
		at   HeadUV
		want GeometryError
	}{
		"fewer bytes than the size": {64, 64, px[:len(px)-4], HeadUV{Face: face}, ErrHeadTexture},
		"more bytes than the size":  {64, 32, px, HeadUV{Face: face}, ErrHeadTexture},
		"no image":                  {0, 0, nil, HeadUV{Face: face}, ErrHeadTexture},
		"a negative size":           {-64, -64, px, HeadUV{Face: face}, ErrHeadTexture},
		"an image too large":        {1025, 8, make([]byte, 1025*8*4), HeadUV{Face: image.Rect(0, 0, 8, 8)}, ErrHeadTexture},
		"no face":                   {64, 64, px, HeadUV{}, ErrHeadShape},
		"a face off the right":      {64, 64, px, HeadUV{Face: image.Rect(60, 8, 68, 16)}, ErrHeadShape},
		"a face off the bottom":     {64, 64, px, HeadUV{Face: image.Rect(8, 60, 16, 68)}, ErrHeadShape},
		"a face before the start":   {64, 64, px, HeadUV{Face: image.Rect(-8, -8, 0, 0)}, ErrHeadShape},
		"a face that is not square": {64, 64, px, HeadUV{Face: image.Rect(8, 8, 24, 16)}, ErrHeadShape},
		"a face too small":          {64, 64, px, HeadUV{Face: image.Rect(8, 8, 12, 12)}, ErrHeadShape},
		"a hat off the image":       {64, 64, px, HeadUV{Face: face, Hat: image.Rect(60, 60, 68, 68)}, ErrHatUV},
		"a hat of another size":     {64, 64, px, HeadUV{Face: face, Hat: image.Rect(32, 8, 48, 24)}, ErrHatUV},
	} {
		t.Run(name, func(t *testing.T) {
			head, err := HeadAt(c.w, c.h, c.px, c.at)
			if !errors.Is(err, c.want) || head != nil {
				t.Errorf("head %v, err %v, want %v", head != nil, err, c.want)
			}
		})
	}
}

// The map takes no head over 32 pixels a side, so a larger face is made
// smaller, and is still the face.
func TestHeadAtMakesALargeFaceSmallEnoughForTheMap(t *testing.T) {
	px := canvas(512, 512, elsewhere)
	paint(px, 512, 64, 64, 64, 64, skinTone)
	paint(px, 512, 64, 64, 32, 64, hair)
	head, err := HeadAt(512, 512, px, HeadUV{Face: image.Rect(64, 64, 128, 128)})
	if err != nil {
		t.Fatal(err)
	}
	if head.Bounds() != image.Rect(0, 0, MaxHeadSide, MaxHeadSide) {
		t.Fatalf("head is %v", head.Bounds())
	}
	if l, r := head.NRGBAAt(15, 31), head.NRGBAAt(16, 0); l != solid(hair) || r != solid(skinTone) {
		t.Errorf("left half %v, right half %v", l, r)
	}
}

func TestHeadAtBlendsAHalfClearHatAndIsAlwaysSolid(t *testing.T) {
	px := canvas(64, 64, clear)
	paint(px, 64, 0, 0, 8, 8, rgba{100, 100, 100, 0})
	paint(px, 64, 8, 0, 8, 8, rgba{200, 200, 200, 128})
	head, err := HeadAt(64, 64, px, HeadUV{Face: image.Rect(0, 0, 8, 8), Hat: image.Rect(8, 0, 16, 8)})
	if err != nil {
		t.Fatal(err)
	}
	got := head.NRGBAAt(3, 3)
	if got.A != 255 || got.R < 148 || got.R > 152 {
		t.Errorf("a half-clear hat over the face gives %v, want about 150 and solid", got)
	}
}

// The same skin read both ways gives the same head, so a classic skin that
// brings its own copy of the standard model loses nothing by it.
func TestAStandardModelReadFromItsGeometryMatchesTheClassicCrop(t *testing.T) {
	px := canvas(64, 64, elsewhere)
	paint(px, 64, 8, 8, 8, 8, skinTone)
	paint(px, 64, 40, 8, 8, 8, clear)
	paint(px, 64, 40, 8, 8, 3, hair)
	raw := modelFile("geometry.humanoid.custom", 64, 64, boneOf("head", cubes(boxCube(0, 0))),
		boneOf("hat", cubes(`{"origin":[-4,24,-4],"size":[8,8,8],"inflate":0.5,"uv":[32,0]}`)))
	at, err := headOf(t, raw, "geometry.humanoid.custom", 64, 64)
	if err != nil {
		t.Fatal(err)
	}
	modelled, err := HeadAt(64, 64, px, at)
	if err != nil {
		t.Fatal(err)
	}
	classic, err := Head(64, 64, px)
	if err != nil {
		t.Fatal(err)
	}
	if string(modelled.Pix) != string(classic.Pix) {
		t.Error("the two readings of one skin differ")
	}
}
