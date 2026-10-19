package atlas

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, root, rel string, w, h int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4], img.Pix[i*4+3] = uint8(i), 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, rel, b.String())
}

func def(id, engine, textures, geometry, scale string) string {
	s := `{"minecraft:client_entity":{"description":{"identifier":"` + id + `","min_engine_version":"` + engine + `","textures":{` + textures + `},"geometry":{` + geometry + `}`
	if scale != "" {
		s += `,"scripts":{"scale":"` + scale + `"}`
	}
	return s + `}}}`
}

func TestParseModelsInheritanceAndBounds(t *testing.T) {
	raw := []byte(`{
	  // comment
	  "geometry.base": {"texturewidth": 64, "textureheight": 32, "bones": [
	    {"name":"body","pivot":[0,12,0],"cubes":[{"origin":[-4,12,-2],"size":[8,12,4],"uv":[16,16]}]},
	    {"name":"head","pivot":[0,24,0],"cubes":[{"origin":[-4,24,-4],"size":[8,8,8],"uv":[0,0]}]}]},
	  "geometry.kid:geometry.base": {"bones": [
	    {"name":"head","pivot":[0,20,0],"cubes":[{"origin":[-3,20,-3],"size":[6,6,6],"uv":[0,0]}]},
	    {"name":"body","pivot":[0,10,0]},
	    {"name":"tail","parent":"body","pivot":[0,10,2],"cubes":[{"origin":[0,8,2],"size":[1,1,4],"uv":[40,0]}]}]},
	  "geometry.huge": {"bones":[{"name":"a","cubes":[{"origin":[0,0,0],"size":[5000,1,1],"uv":[0,0]}]}]},
	  "not_geometry": {}
	}`)
	list, err := parseModels(raw)
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]geoModel{}
	for _, m := range list {
		models[m.id] = m
	}
	if _, ok := models["geometry.huge"]; ok {
		t.Fatal("a box far out of range was kept")
	}
	kid, err := resolveModel(models, "geometry.kid", 0)
	if err != nil {
		t.Fatal(err)
	}
	if kid.texW != 64 || kid.texH != 32 {
		t.Fatalf("texture size not inherited: %dx%d", kid.texW, kid.texH)
	}
	var names []string
	for _, b := range kid.bones {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "body,head,tail" {
		t.Fatalf("bones = %v", names)
	}
	if kid.bones[0].Pivot[1] != 10 || len(kid.bones[0].Cubes) != 1 {
		t.Fatalf("a bone without boxes should keep the parent's boxes with its own pivot: %+v", kid.bones[0])
	}
	if kid.bones[1].Cubes[0].Size[0] != 6 {
		t.Fatalf("head not replaced: %+v", kid.bones[1])
	}
}

func TestResolveRefusesInheritanceLoops(t *testing.T) {
	models := map[string]geoModel{
		"geometry.a": {id: "geometry.a", parent: "geometry.b"},
		"geometry.b": {id: "geometry.b", parent: "geometry.a"},
	}
	if _, err := resolveModel(models, "geometry.a", 0); err == nil {
		t.Fatal("loop was resolved")
	}
}

func TestParseModelsRefusesBigAndDeepFiles(t *testing.T) {
	if _, err := parseModels(bytes.Repeat([]byte(" "), maxModelBytes+1)); err == nil {
		t.Fatal("oversized file accepted")
	}
	deep := strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)
	if _, err := parseModels([]byte(`{"x":` + deep + `}`)); err == nil {
		t.Fatal("deep file accepted")
	}
}

func TestFaceRectBoxUVAndMirror(t *testing.T) {
	c := geoCube{Size: [3]float64{8, 12, 4}, UV: []byte(`[16,16]`)}
	// Front is right of the left side, which is z wide.
	r, ok := faceRect(c, false, faceN)
	if !ok || r != (texRect{20, 20, 8, 12, false}) {
		t.Fatalf("north = %+v %v", r, ok)
	}
	r, _ = faceRect(c, false, faceE)
	if r != (texRect{16, 20, 4, 12, false}) {
		t.Fatalf("east = %+v", r)
	}
	// A mirrored box swaps its sides and turns each face.
	r, _ = faceRect(c, true, faceE)
	if r != (texRect{28, 20, 4, 12, true}) {
		t.Fatalf("mirrored east = %+v", r)
	}
	r, _ = faceRect(c, false, faceD)
	if r != (texRect{28, 16, 8, 4, false}) {
		t.Fatalf("down = %+v", r)
	}
}

func TestFaceRectPerFaceUV(t *testing.T) {
	c := geoCube{Size: [3]float64{2, 3, 4}, UV: []byte(`{"north":{"uv":[10,20],"uv_size":[2,3]},"up":{"uv":[0,0]},"south":{"uv":[8,8],"uv_size":[-2,3]}}`)}
	r, ok := faceRect(c, false, faceN)
	if !ok || r != (texRect{10, 20, 2, 3, false}) {
		t.Fatalf("north = %+v", r)
	}
	r, _ = faceRect(c, false, faceU)
	if r.W != 2 || r.H != 4 {
		t.Fatalf("up should default to x by z: %+v", r)
	}
	r, _ = faceRect(c, false, faceS)
	if r != (texRect{6, 8, 2, 3, true}) {
		t.Fatalf("a negative size runs backwards: %+v", r)
	}
	if _, ok := faceRect(c, false, faceW); ok {
		t.Fatal("a face not listed was drawn")
	}
}

func TestScaleOf(t *testing.T) {
	for in, want := range map[string]float32{
		"": 1, "0.9375": 0.9375, "query.is_baby ? 2.0 : 1.0": 1, "query.is_baby ? 2.0 : 0.87": 0.87,
		"query.is_baby ? (1 / 0.4) : (1 / 0.6)": float32(1 / 0.6), "animation.ghast.scale": 1, "99": 1,
	} {
		if got := scaleOf(in); got != want {
			t.Errorf("scaleOf(%q) = %v, want %v", in, got, want)
		}
	}
}

func fixturePack(t *testing.T) string {
	root := t.TempDir()
	writeFile(t, root, "entity/cow.entity.json", def("minecraft:cow", "1.8.0", `"default":"textures/entity/cow/cow","baby":"textures/entity/cow/baby"`, `"default":"geometry.cow","baby":"geometry.cowbaby"`, "query.is_baby ? 2.0 : 1.0"))
	writeFile(t, root, "entity/cow.v1.0.entity.json", def("minecraft:cow", "", `"default":"textures/entity/cow/old"`, `"default":"geometry.cow"`, ""))
	writeFile(t, root, "entity/sheep.entity.json", def("minecraft:sheep", "1.8.0", `"default":"textures/entity/sheep"`, `"default":"geometry.wool","sheared":"geometry.bare"`, ""))
	writeFile(t, root, "entity/ghost.entity.json", def("minecraft:ghost", "1.8.0", `"default":"textures/entity/missing"`, `"default":"geometry.cow"`, ""))
	writeFile(t, root, "entity/odd.entity.json", def("minecraft:odd", "1.8.0", `"first":"textures/entity/cow/cow","second":"textures/entity/sheep"`, `"default":"geometry.cow"`, ""))
	writeFile(t, root, "models/entity/cow.geo.json", `{"format_version":"1.8.0",
	  "geometry.cow":{"texturewidth":64,"textureheight":32,"bones":[
	    {"name":"body","pivot":[0,12,0],"cubes":[{"origin":[-4,12,-2],"size":[8,12,4],"uv":[16,16]}]},
	    {"name":"leg0","parent":"body","pivot":[-2,12,0],"mirror":true,"cubes":[{"origin":[-4,0,-2],"size":[4,12,4],"uv":[0,16]}]}]},
	  "geometry.cowbaby":{"texturewidth":32,"textureheight":32,"bones":[{"name":"body","cubes":[{"origin":[-2,0,-2],"size":[4,4,4],"uv":[0,0]}]}]}}`)
	writeFile(t, root, "models/entity/sheep.geo.json", `{
	  "geometry.bare":{"texturewidth":64,"textureheight":64,"bones":[{"name":"head","cubes":[{"origin":[-3,16,-14],"size":[6,6,8],"uv":[0,0]}]},{"name":"body","cubes":[{"origin":[-4,13,-5],"size":[8,16,6],"uv":[28,8]}]}]},
	  "geometry.wool:geometry.bare":{"bones":[{"name":"head","cubes":[{"origin":[-3,16,-12],"size":[6,6,6],"uv":[0,32],"inflate":0.6}]}]}}`)
	writePNG(t, root, "textures/entity/cow/cow.png", 64, 32)
	writePNG(t, root, "textures/entity/cow/baby.png", 32, 32)
	writePNG(t, root, "textures/entity/sheep.png", 64, 64)
	return root
}

func TestEntityTextureRefsOnlyNamedByChosenVariants(t *testing.T) {
	refs := entityTextureRefs(fixturePack(t))
	// The legacy cow definition and the odd one's second texture are not drawn.
	want := "textures/entity/cow/baby,textures/entity/cow/cow,textures/entity/missing,textures/entity/sheep"
	if got := strings.Join(refs, ","); got != want {
		t.Fatalf("refs = %s", got)
	}
}

func TestBuildEntities(t *testing.T) {
	root := fixturePack(t)
	sheet, tbl, err := buildEntities(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, bad := tbl.Models["minecraft:ghost"]; bad {
		t.Fatal("an entity with no texture was compiled")
	}
	img, err := png.Decode(bytes.NewReader(sheet))
	if err != nil || img.Bounds().Dx() != tbl.Width || img.Bounds().Dy() != tbl.Height {
		t.Fatalf("sheet does not match table: %v", err)
	}
	cow := tbl.Models["minecraft:cow"]
	if len(cow.Default) != 1 || len(cow.Baby) != 1 || cow.Scale != 0 {
		t.Fatalf("cow = %+v", cow)
	}
	if cow.Height != 1.5 {
		t.Fatalf("height = %v", cow.Height)
	}
	leg := cow.Default[0].Bones[1]
	if leg.Parent != "body" {
		t.Fatalf("leg = %+v", leg)
	}
	north := leg.Cubes[0].UV[faceN]
	// The mirrored box turns each face, so u0 > u1, and every rect lies in the sheet.
	if north[0] <= north[2] {
		t.Fatalf("mirrored north not flipped: %v", north)
	}
	for _, m := range tbl.Models {
		for _, layers := range [][]Layer{m.Default, m.Baby, m.Sheared} {
			for _, l := range layers {
				for _, b := range l.Bones {
					for _, c := range b.Cubes {
						for _, r := range c.UV {
							if r[0] < 0 || r[2] < 0 || r[0] > float32(tbl.Width) || r[1] > float32(tbl.Height) || r[3] > float32(tbl.Height) || r[2] > float32(tbl.Width) {
								t.Fatalf("rect %v outside the %dx%d sheet", r, tbl.Width, tbl.Height)
							}
						}
					}
				}
			}
		}
	}
	// The baby's texture is 32 wide with a 32-wide model, so its box fractions
	// land one-to-one on pixels.
	bc := cow.Baby[0].Bones[0].Cubes[0].UV[faceN]
	if bc[2]-bc[0] != 4 || bc[3]-bc[1] != 4 {
		t.Fatalf("baby north = %v", bc)
	}

	sheep := tbl.Models["minecraft:sheep"]
	if len(sheep.Default) != 2 || !sheep.Default[1].Tint || sheep.Default[0].Tint || len(sheep.Sheared) != 1 {
		t.Fatalf("sheep layers = %+v", sheep)
	}
	if len(sheep.Default[1].Bones) != 1 {
		t.Fatalf("wool should hold only its own bones: %+v", sheep.Default[1].Bones)
	}
	if got := sheep.Default[0].Bones[0].Cubes[0].UV[faceN]; got == sheep.Default[1].Bones[0].Cubes[0].UV[faceN] {
		t.Fatal("skin and wool share a rect")
	}
	if odd := tbl.Models["minecraft:odd"]; len(odd.Default) != 1 {
		t.Fatalf("odd = %+v", odd)
	}
}

func TestBuildEntitiesPrefersNewestDefinition(t *testing.T) {
	defs, err := readEntityDefs(fixturePack(t))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := defs["minecraft:cow"].defaultTexture(); p != "textures/entity/cow/cow" {
		t.Fatalf("cow texture = %s", p)
	}
}

func TestBuildEntitiesWithoutPackData(t *testing.T) {
	if _, _, err := buildEntities(t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestEntityTextureRejectsHugeAndEscapingPaths(t *testing.T) {
	root := t.TempDir()
	writePNG(t, root, "textures/entity/big.png", maxEntityTexSide+1, 1)
	eb := &entityBuilder{b: &builder{dir: root, images: map[string]*image.NRGBA{}, formats: map[string]int{}}, imgs: map[string]*image.NRGBA{}}
	if eb.texture("textures/entity/big") != nil {
		t.Fatal("an oversized texture was loaded")
	}
	if eb.texture("textures/../../etc/passwd") != nil {
		t.Fatal("an escaping path was loaded")
	}
}

func TestEntityTablePersists(t *testing.T) {
	a := &Atlas{png: []byte("p"), table: Table{Version: tableVersion}}
	a.entPNG, a.entities = []byte("e"), EntityTable{Version: entityTableVersion, Width: 1, Height: 1, Models: map[string]EntityModel{"minecraft:x": {Height: 1}}}
	a.itemsErr = nil
	dir := t.TempDir()
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	b, err := LoadAtlas(dir)
	if err != nil || b.EntitiesErr() != nil || b.Entities().Models["minecraft:x"].Height != 1 {
		t.Fatalf("load = %v %v", err, b.EntitiesErr())
	}
	if err := os.Remove(filepath.Join(dir, entTableFile)); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadAtlas(dir)
	if b.EntitiesErr() == nil {
		t.Fatal("an entry without its entity table should report it")
	}
}

func bone(name, parent string, pivot [3]float32, size [3]float32) Bone {
	return Bone{Name: name, Parent: parent, Pivot: pivot, Cubes: []Cube{{Size: size}}}
}

func TestLayBodiesLaysAQuadrupedAndLeavesAnUprightMob(t *testing.T) {
	quad := []Bone{
		bone("body", "", [3]float32{0, 10, 2}, [3]float32{6, 9, 6}),
		bone("head", "", [3]float32{0, 10, -7}, [3]float32{6, 6, 4}),
		bone("leg0", "", [3]float32{-2, 8, 7}, [3]float32{2, 8, 2}), bone("leg1", "", [3]float32{2, 8, 7}, [3]float32{2, 8, 2}),
		bone("leg2", "", [3]float32{-2, 8, -4}, [3]float32{2, 8, 2}), bone("leg3", "", [3]float32{2, 8, -4}, [3]float32{2, 8, 2}),
	}
	layBodies(quad)
	if quad[0].Bind == nil || quad[0].Bind[0] != 90 || quad[0].Rot != nil {
		t.Fatalf("quadruped body = %+v", quad[0])
	}
	creeper := []Bone{
		bone("body", "", [3]float32{0, 18, 0}, [3]float32{4, 12, 4}),
		bone("head", "", [3]float32{0, 18, 0}, [3]float32{8, 8, 8}),
		bone("leg0", "", [3]float32{-2, 6, 4}, [3]float32{4, 6, 4}), bone("leg1", "", [3]float32{2, 6, 4}, [3]float32{4, 6, 4}),
		bone("leg2", "", [3]float32{-2, 6, -4}, [3]float32{4, 6, 4}), bone("leg3", "", [3]float32{2, 6, -4}, [3]float32{4, 6, 4}),
	}
	layBodies(creeper)
	if creeper[0].Bind != nil {
		t.Fatalf("a creeper's body was laid down: %+v", creeper[0])
	}
}

func TestSolidifyMasksKeepsRealTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	for i, a := range []uint8{0, 3, 100, 255} {
		img.Pix[i*4+3] = a
	}
	solidifyMasks(img)
	for i, want := range []uint8{0, 255, 100, 255} {
		if got := img.Pix[i*4+3]; got != want {
			t.Errorf("pixel %d alpha = %d, want %d", i, got, want)
		}
	}
}

func TestHiddenBonesDrawNothing(t *testing.T) {
	m := geoModel{bones: []geoBone{
		{Name: "body", Cubes: []geoCube{{Size: [3]float64{1, 1, 1}, UV: []byte(`[0,0]`)}}},
		{Name: "Saddle", Cubes: []geoCube{{Size: [3]float64{1, 1, 1}, UV: []byte(`[0,0]`)}}},
	}}
	bones := compileBones(m, 16, 16)
	if len(bones[0].Cubes) != 1 || len(bones[1].Cubes) != 0 {
		t.Fatalf("bones = %+v", bones)
	}
}

func TestATurnedBoxWithoutAPivotTurnsAboutItsCentre(t *testing.T) {
	c := compileCube(geoCube{Origin: [3]float64{-4, 0, -2}, Size: [3]float64{8, 6, 4}, Rotation: []float64{90, 0, 0}, UV: []byte(`[0,0]`)}, geoBone{}, 16, 16)
	if len(c.Pivot) != 3 || c.Pivot[0] != 0 || c.Pivot[1] != 3 || c.Pivot[2] != 0 {
		t.Fatalf("pivot = %v", c.Pivot)
	}
}
