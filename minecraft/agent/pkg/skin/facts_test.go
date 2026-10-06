package skin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// boxMesh is the front of a box as a polygon mesh, with the texture's
// second coordinate counting up the image or down it.
func boxMesh(up bool) string {
	top, bottom := 0.25, 0.5
	if up {
		top, bottom = 0.75, 0.5
	}
	return fmt.Sprintf(`"poly_mesh":{"normalized_uvs":true,
		"positions":[[-4,24,-4],[4,24,-4],[4,32,-4],[-4,32,-4]],
		"normals":[[0,0,-1]],
		"uvs":[[0.25,%[2]v],[0.5,%[2]v],[0.5,%[1]v],[0.25,%[1]v]],
		"polys":[[[0,0,0],[1,0,1],[2,0,2],[3,0,3]]]}`, top, bottom)
}

// A file shaped as a character-creator skin is said to be: the model the
// patch names has a head that draws nothing, and a second model, textured
// by another image, has the head as polygons. No head is taken from it;
// what is logged has to be enough to learn how one could be.
func TestFactsDescribeAHeadKeptInAnotherModelAsPolygons(t *testing.T) {
	raw := []byte(`{"format_version":"1.14.0","minecraft:geometry":[
		{"description":{"identifier":"geometry.persona_0123456789abcdef_0_1","texture_width":64,"texture_height":128},
		 "bones":[{"name":"body",` + boxMesh(true) + `},{"name":"head"},{"name":"hat"}]},
		{"description":{"identifier":"geometry.animated_face_persona-0123456789abcdef","texture_width":32,"texture_height":64},
		 "bones":[{"name":"head",` + boxMesh(true) + `},{"name":"hat",` + boxMesh(false) + `}]}]}`)
	g, err := ParseGeometry(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Head("geometry.persona_0123456789abcdef_0_1", 64, 128); err != ErrHeadEmpty {
		t.Errorf("the body's model: %v", err)
	}
	if _, err := g.Head("geometry.animated_face_persona-0123456789abcdef", 32, 64); err != ErrHeadMesh {
		t.Errorf("the face's model: %v", err)
	}

	f := g.Facts()
	if f.Bytes != len(raw) || f.Format != "1.14.0" || len(f.Models) != 2 {
		t.Fatalf("facts %+v", f)
	}
	body, face := f.Models[0], f.Models[1]
	if body.Name != "geometry.persona_#_0_1" || face.Name != "geometry.animated_face_persona-#" {
		t.Errorf("names %q and %q carry the skin's identifier", body.Name, face.Name)
	}
	if *body.TextureWidth != 64 || *body.TextureHeight != 128 || body.Bones != 3 || body.Meshes != 1 || body.Cubes != 0 {
		t.Errorf("body %+v", body)
	}
	if body.Head == nil || body.Head.Cubes != 0 || body.Head.Mesh != nil {
		t.Errorf("the body's head %+v", body.Head)
	}
	mesh := face.Head.Mesh
	if mesh == nil || mesh.Form != "indexed" || mesh.Positions != 4 || mesh.UVs != 4 || mesh.Normals != 1 || mesh.Polys != 1 || !*mesh.Normalized || len(mesh.Faces) != 1 {
		t.Fatalf("the face's head %+v", mesh)
	}
	front := mesh.Faces[0]
	if front.Normal[2] != -1 || front.Low[1] != 24 || front.High[1] != 32 || front.UVLow[0] != 0.25 || front.UVHigh[0] != 0.5 {
		t.Errorf("front %+v", front)
	}
	// Which way the second coordinate runs is read off the polygon.
	if front.VTop != 0.75 || front.VBottom != 0.5 {
		t.Errorf("counting up: top %v, bottom %v", front.VTop, front.VBottom)
	}
	if hat := face.Hat.Mesh.Faces[0]; hat.VTop != 0.25 || hat.VBottom != 0.5 {
		t.Errorf("counting down: top %v, bottom %v", hat.VTop, hat.VBottom)
	}
	if _, err := json.Marshal(f); err != nil {
		t.Errorf("the facts cannot be logged: %v", err)
	}
}

func TestFactsDescribeACube(t *testing.T) {
	raw := modelFile(custom, 64, 64,
		boneOf("head", `"mirror":true,`+cubes(`{"origin":[-4,24,-4],"size":[8,8,8],"inflate":0.5,"uv":[16,32],"rotation":[0,0,5]}`)),
		boneOf("hat", cubes(`{"size":[8,8,8],"uv":{"north":{"uv":[1,2],"uv_size":[3,4]}}}`, boxCube(0, 0))))
	g, err := ParseGeometry(raw)
	if err != nil {
		t.Fatal(err)
	}
	m := g.Facts().Models[0]
	head, hat := m.Head.Cube, m.Hat.Cube
	if m.Cubes != 3 || m.Hat.Cubes != 2 || head.UV != "box" || head.At[0] != 16 || head.At[1] != 32 || head.Size[2] != 8 || head.Inflate != 0.5 || !head.Mirror || !head.Turned {
		t.Errorf("head %+v", head)
	}
	if hat.UV != "faces" || hat.At[1] != 2 || hat.Extent[0] != 3 || hat.Mirror || hat.Turned {
		t.Errorf("hat %+v", hat)
	}
	bare, _ := ParseGeometry(modelFile(custom, 64, 64, boneOf("head", cubes(`{"uv":null}`))))
	if got := bare.Facts().Models[0].Head.Cube.UV; got != "none" {
		t.Errorf("a cube with no texture is described as %q", got)
	}
}

// What is logged is bounded however much the file holds, and is always
// something the log can encode.
func TestFactsAreBoundedAndAlwaysLoggable(t *testing.T) {
	var models []string
	for i := range maxFactModels + 3 {
		models = append(models, fmt.Sprintf(`{"description":{"identifier":"m%d"},"bones":[]}`, i))
	}
	g, err := ParseGeometry([]byte(`{"minecraft:geometry":[` + strings.Join(models, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	if f := g.Facts(); len(f.Models) != maxFactModels || f.More != 3 {
		t.Errorf("%d models listed, %d more", len(f.Models), f.More)
	}

	var polys, positions []string
	for i := range 3 * maxFactFaces {
		positions = append(positions, `[0,0,0]`, `[1e308,1,0]`, `[1,-1e308,0]`)
		polys = append(polys, fmt.Sprintf(`[[%d,0,0],[%d,0,1],[%d,0,2]]`, 3*i, 3*i+1, 3*i+2))
	}
	long := strings.Repeat("x", 4*maxNameLength)
	raw := []byte(`{"format_version":"` + long + `","minecraft:geometry":[{"description":{"identifier":"` + long + `","texture_width":1e308},"bones":[
		{"name":"head","poly_mesh":{"positions":[` + strings.Join(positions, ",") + `],"uvs":[[1e308,-1e308],[0,0],[1,1]],"polys":[` + strings.Join(polys, ",") + `]}}]}]}`)
	g, err = ParseGeometry(raw)
	if err != nil {
		t.Fatal(err)
	}
	f := g.Facts()
	mesh := f.Models[0].Head.Mesh
	if len(f.Format) != maxNameLength || len(f.Models[0].Name) != maxNameLength || mesh.Polys != 3*maxFactFaces || len(mesh.Faces) != maxFactFaces {
		t.Errorf("format %d long, name %d long, %d of %d faces", len(f.Format), len(f.Models[0].Name), len(mesh.Faces), mesh.Polys)
	}
	line, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("the facts cannot be logged: %v", err)
	}
	if len(line) > 8<<10 {
		t.Errorf("the facts are %d bytes of log", len(line))
	}

	// A model with more bones than a head is looked for among is counted
	// and not searched.
	crowd := strings.Repeat(`{"name":"head","cubes":[{"size":[8,8,8],"uv":[0,0]}]},`, maxBones) + `{"name":"head"}`
	g, err = ParseGeometry([]byte(`{"minecraft:geometry":[{"description":{"identifier":"m"},"bones":[` + crowd + `]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := g.Facts().Models[0]; m.Bones != maxBones+1 || m.Head != nil {
		t.Errorf("%d bones, head %+v", m.Bones, m.Head)
	}
}

func TestFactsOfAMeshThatIsNotWhatTheFormatSays(t *testing.T) {
	mesh := func(body string) *MeshFacts {
		t.Helper()
		g, err := ParseGeometry([]byte(`{"minecraft:geometry":[{"description":{"identifier":"m"},"bones":[{"name":"head","poly_mesh":` + body + `}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return g.Facts().Models[0].Head.Mesh
	}
	if m := mesh(`{"positions":"none"}`); !m.Unreadable {
		t.Errorf("words for positions: %+v", m)
	}
	if m := mesh(`{"positions":[[0,0,0]],"polys":"hexagons"}`); !m.Unreadable || m.Positions != 1 {
		t.Errorf("an unknown form: %+v", m)
	}
	if m := mesh(`[1,2]`); !m.Unreadable {
		t.Errorf("a list for a mesh: %+v", m)
	}
	// The list forms take the three lists in step.
	quads := mesh(`{"positions":[[0,0,0],[1,0,0],[1,1,0],[0,1,0],[9,9,9]],"uvs":[[0,1],[1,1],[1,0],[0,0]],"normals":[[0,0,-1]],"polys":"quad_list"}`)
	if quads.Form != "quad_list" || quads.Polys != 1 || len(quads.Faces) != 1 || quads.Faces[0].VTop != 0 || quads.Faces[0].VBottom != 1 {
		t.Errorf("a quad list: %+v", quads)
	}
	// Corners that name nothing in the lists, or are not corners at all,
	// are passed over and cost nothing else.
	odd := mesh(`{"positions":[[0,0,0],[1,0,0],[1,1,0]],"uvs":[[0,0],[1,0],[1,1]],"polys":[
		[[0,0,0],[1,0,1],[7,0,2]], [[0,0,0],[1,0,1],[2,0,-1]], [[0,0,0],[1,0,1],[2,0,1.5]], [[0,0,0],[1,0,1]],
		[[0,0,0],[1,0,1],[2,0,2],[0,0,0],[1,0,1]], [[0,0],[1,0,1],[2,0,2]], [[0,0,0],[1,0,1],[2,0,1e308]],
		[[0,99,0],[1,0,1],[2,0,2]]]}`)
	if odd.Polys != 8 || len(odd.Faces) != 1 || odd.Faces[0].Normal != nil {
		t.Errorf("of eight polygons one is whole: %+v", odd)
	}
}

func TestMaskNameKeepsTheKindAndDropsTheIdentifier(t *testing.T) {
	for name, want := range map[string]string{
		"geometry.humanoid.customSlim":              "geometry.humanoid.customSlim",
		"geometry.persona_d1625e47f4c9399f_0_1":     "geometry.persona_#_0_1",
		"geometry.animated_face_persona-DEADBEEF01": "geometry.animated_face_persona-#",
		// A UUID goes whole: its short middle groups would still tell two
		// lines about one skin apart from the rest.
		"c18e65aa-7b21-4637-9b63-8ad63622ef01_Alex": "#_Alex",
		"C18E65AA-7B21-4637-9B63-8AD63622EF01":      "#",
		"":                                          "",
	} {
		if got := MaskName(name); got != want {
			t.Errorf("%q masked to %q, want %q", name, got, want)
		}
	}
}

// A file may hold eight models, each with a head and a hat of polygons and
// every number as long as a number can be written. What is logged of it is
// still one line of bounded size, and says how much was left out.
func TestFactsOfTheLargestFileStillFitOneLogLine(t *testing.T) {
	var positions, uvs, polys []string
	for i := range maxFactFaces {
		positions = append(positions, `[-123456.78901,-123456.78901,-123456.78901]`, `[123456.78901,123456.78901,123456.78901]`, `[-123456.78901,123456.78901,0.12345]`)
		uvs = append(uvs, `[-123456.78901,-123456.78901]`, `[123456.78901,123456.78901]`, `[0.12345,0.54321]`)
		polys = append(polys, fmt.Sprintf(`[[%d,0,%d],[%d,0,%d],[%d,0,%d]]`, 3*i, 3*i, 3*i+1, 3*i+1, 3*i+2, 3*i+2))
	}
	mesh := `"poly_mesh":{"normalized_uvs":false,"positions":[` + strings.Join(positions, ",") + `],"normals":[[-0.57735,-0.57735,-0.57735]],"uvs":[` + strings.Join(uvs, ",") + `],"polys":[` + strings.Join(polys, ",") + `]}`
	cube := `"cubes":[{"origin":[-1.7976931348623157e308,-1.7976931348623157e308,-1.7976931348623157e308],"size":[1.7976931348623157e308,1.7976931348623157e308,1.7976931348623157e308],"inflate":1.7976931348623157e308,
		"uv":{"north":{"uv":[-1.7976931348623157e308,-1.7976931348623157e308],"uv_size":[-1.7976931348623157e308,-1.7976931348623157e308]}}}]`
	var models []string
	for i := range maxFactModels {
		models = append(models, fmt.Sprintf(`{"description":{"identifier":"%s%d","texture_width":1.7976931348623157e308,"texture_height":1.7976931348623157e308},"bones":[{"name":"head",%s,%s},{"name":"hat",%s,%s}]}`,
			strings.Repeat("m", maxNameLength), i, cube, mesh, cube, mesh))
	}
	g, err := ParseGeometry([]byte(`{"minecraft:geometry":[` + strings.Join(models, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	f := g.Facts()
	line, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("the facts cannot be logged: %v", err)
	}
	if len(line) > maxFactBytes {
		t.Errorf("the facts are %d bytes of log, over %d", len(line), maxFactBytes)
	}
	faces := 0
	for _, m := range f.Models {
		for _, p := range []*PartFacts{m.Head, m.Hat} {
			if p != nil && p.Mesh != nil {
				faces += len(p.Mesh.Faces)
			}
		}
	}
	if faces > maxFactFacesInAll || len(f.Models)+f.More != maxFactModels || len(f.Models) == 0 {
		t.Errorf("%d faces listed, %d models and %d more", faces, len(f.Models), f.More)
	}
	// The first model's head, the one most worth seeing, is whole.
	if got := len(f.Models[0].Head.Mesh.Faces); got != maxFactFaces {
		t.Errorf("the first head lists %d faces", got)
	}
}

// The cut to length comes after the masking, so it cannot leave the start
// of an identifier too short to be recognised as one.
func TestMaskNameMasksBeforeItCuts(t *testing.T) {
	name := strings.Repeat("x", maxNameLength-5) + "0123456789abcdef"
	if got, want := MaskName(name), strings.Repeat("x", maxNameLength-5)+"#"; got != want {
		t.Errorf("masked to %q", got)
	}
	if got := MaskName(strings.Repeat("0123456789abcdef-", 1000)); len(got) > maxNameLength || strings.ContainsAny(got, "0123456789") {
		t.Errorf("masked to %q", got)
	}
}

// An ordinary file with polygons on every model keeps all its models in
// the line: the polygons are listed for the first of them and counted for
// the rest, so no model's texture size is lost to another's detail.
func TestFactsListEveryModelOfAnOrdinaryFile(t *testing.T) {
	var models []string
	for i := range maxFactModels {
		quads := strings.Repeat(`[[0,0,0],[1,0,1],[2,0,2],[3,0,3]],`, maxFactFaces-1) + `[[0,0,0],[1,0,1],[2,0,2],[3,0,3]]`
		mesh := strings.Replace(boxMesh(true), `[[[0,0,0],[1,0,1],[2,0,2],[3,0,3]]]`, `[`+quads+`]`, 1)
		models = append(models, fmt.Sprintf(`{"description":{"identifier":"geometry.part%d","texture_width":64,"texture_height":64},"bones":[{"name":"head",%s},{"name":"hat",%s}]}`, i, mesh, mesh))
	}
	g, err := ParseGeometry([]byte(`{"minecraft:geometry":[` + strings.Join(models, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	f := g.Facts()
	if len(f.Models) != maxFactModels || f.More != 0 {
		t.Fatalf("%d models listed, %d left out", len(f.Models), f.More)
	}
	if first, last := f.Models[0], f.Models[maxFactModels-1]; len(first.Head.Mesh.Faces) != maxFactFaces || len(first.Hat.Mesh.Faces) != maxFactFaces ||
		len(last.Head.Mesh.Faces) != 0 || last.Head.Mesh.Polys != maxFactFaces || *last.TextureWidth != 64 {
		t.Errorf("first head %d faces, last head %+v", len(first.Head.Mesh.Faces), last.Head.Mesh)
	}
}
