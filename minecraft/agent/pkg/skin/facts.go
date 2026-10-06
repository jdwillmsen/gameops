package skin

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
)

// What Facts will list of one file, so that a log line stays a line.
const (
	maxFactModels = 8
	maxFactFaces  = 8
	// maxFactFacesInAll is how many polygons are listed across the file.
	// The head is in one model or two, so this is room for all of it.
	maxFactFacesInAll = 2 * maxFactFaces
	// maxFactBytes is the most the facts of one file may come to, encoded.
	maxFactBytes  = 8 << 10
	maxNameLength = 96
)

// Facts is what a skin's geometry says about where its head is, in the
// form it is logged in. A head is only taken from a layout that has been
// seen and understood; for any other, this record of what a real client
// sent is how the layout gets to be known.
type Facts struct {
	Bytes      int          `json:"bytes"`
	Depth      int          `json:"depth,omitempty"`
	Containers int          `json:"containers,omitempty"`
	Format     string       `json:"format,omitempty"`
	Models     []ModelFacts `json:"models,omitempty"`
	// More is how many models were left out of Models.
	More int `json:"more,omitempty"`
}

// ModelFacts is one model of the file.
type ModelFacts struct {
	Name string `json:"name"`
	// TextureWidth and TextureHeight are as declared, and absent if the
	// model declares none.
	TextureWidth  *float64   `json:"texture_width,omitempty"`
	TextureHeight *float64   `json:"texture_height,omitempty"`
	Bones         int        `json:"bones"`
	Cubes         int        `json:"cubes"`
	Meshes        int        `json:"meshes"`
	Head          *PartFacts `json:"head,omitempty"`
	Hat           *PartFacts `json:"hat,omitempty"`
}

// PartFacts is the head bone or the hat bone of a model.
type PartFacts struct {
	Cubes int `json:"cubes"`
	// Cube is the first of them.
	Cube *CubeFacts `json:"cube,omitempty"`
	Mesh *MeshFacts `json:"mesh,omitempty"`
}

// CubeFacts is a cube as written, unchecked.
type CubeFacts struct {
	Origin []float64 `json:"origin,omitempty"`
	Size   []float64 `json:"size,omitempty"`
	// UV is "box" for one corner, "faces" for a rectangle a face, or
	// "none". At and Extent are the corner, or the north face's rectangle.
	UV      string    `json:"uv"`
	At      []float64 `json:"at,omitempty"`
	Extent  []float64 `json:"extent,omitempty"`
	Inflate float64   `json:"inflate,omitempty"`
	Mirror  bool      `json:"mirror,omitempty"`
	Turned  bool      `json:"turned,omitempty"`
}

// MeshFacts is a bone drawn as free polygons.
type MeshFacts struct {
	Unreadable bool  `json:"unreadable,omitempty"`
	Normalized *bool `json:"normalized_uvs,omitempty"`
	Positions  int   `json:"positions"`
	Normals    int   `json:"normals"`
	UVs        int   `json:"uvs"`
	Polys      int   `json:"polys"`
	// Form is "indexed", "tri_list" or "quad_list".
	Form  string      `json:"form,omitempty"`
	Faces []FaceFacts `json:"faces,omitempty"`
}

// FaceFacts is one polygon: which way it faces, the box around it in the
// model and the box around it in the texture, both as written. VTop and
// VBottom are the texture's second coordinate at the polygon's highest and
// lowest corner, which is what says whether that coordinate counts up the
// image or down it.
type FaceFacts struct {
	Normal  []float64 `json:"normal,omitempty"`
	Low     []float64 `json:"low"`
	High    []float64 `json:"high"`
	UVLow   []float64 `json:"uv_low"`
	UVHigh  []float64 `json:"uv_high"`
	VTop    float64   `json:"v_top"`
	VBottom float64   `json:"v_bottom"`
}

// A UUID is matched whole, ahead of the long runs in it.
var idRun = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{8,}`)

// MaskName shortens a model's name to its kind. A character-creator model
// is named after an identifier of the skin, which is no fact about how the
// model is laid out and is kept out of the log.
func MaskName(name string) string {
	// Masked before it is cut: a cut that fell inside an identifier would
	// leave a piece of it too short to be taken for one.
	name = idRun.ReplaceAllString(name, "#")
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	return name
}

func first(values []float64, n int) []float64 {
	return values[:min(len(values), n)]
}

// Facts describes the file. It reads only what ParseGeometry kept, so all
// of that function's limits hold here too.
func (g *Geometry) Facts() Facts {
	f := Facts{Bytes: g.bytes, Depth: g.depth, Containers: g.containers, Format: MaskName(g.format)}
	faces := maxFactFacesInAll
	for i := range g.models {
		if len(f.Models) == maxFactModels {
			f.More = len(g.models) - maxFactModels
			break
		}
		m := &g.models[i]
		mf := ModelFacts{
			Name:          MaskName(m.Description.Identifier),
			TextureWidth:  m.Description.TextureWidth,
			TextureHeight: m.Description.TextureHeight,
			Bones:         len(m.Bones),
		}
		for _, b := range m.Bones {
			mf.Cubes += len(b.Cubes)
			if len(b.PolyMesh) > 0 {
				mf.Meshes++
			}
		}
		if len(m.Bones) <= maxBones {
			mf.Head, mf.Hat = partFacts(part(m.Bones, "head"), &faces), partFacts(part(m.Bones, "hat"), &faces)
		}
		f.Models = append(f.Models, mf)
	}
	// The counts above bound the line for any file a client would write.
	// Numbers written as long as they can be are still within every count,
	// so the size itself is checked, and models are left off the end until
	// it fits.
	for len(f.Models) > 1 {
		if line, err := json.Marshal(f); err == nil && len(line) <= maxFactBytes {
			break
		}
		f.Models, f.More = f.Models[:len(f.Models)-1], f.More+1
	}
	return f
}

// partFacts describes a bone, listing no more polygons than faces has left.
func partFacts(b *bone, faces *int) *PartFacts {
	if b == nil {
		return nil
	}
	p := &PartFacts{Cubes: len(b.Cubes)}
	if len(b.Cubes) > 0 {
		p.Cube = cubeFacts(b, b.Cubes[0])
	}
	if len(b.PolyMesh) > 0 {
		p.Mesh = meshFacts(b.PolyMesh, min(*faces, maxFactFaces))
		*faces -= len(p.Mesh.Faces)
	}
	return p
}

func cubeFacts(of *bone, c cube) *CubeFacts {
	f := &CubeFacts{Origin: round(first(c.Origin, 3)), Size: round(first(c.Size, 3)), UV: "none", Inflate: rounded(c.Inflate), Mirror: of.Mirror, Turned: turned(of.Rotation) || turned(c.Rotation)}
	if c.Mirror != nil {
		f.Mirror = *c.Mirror
	}
	var corner []float64
	var faces struct {
		North struct {
			UV   []float64 `json:"uv"`
			Size []float64 `json:"uv_size"`
		} `json:"north"`
	}
	uv := bytes.TrimSpace(c.UV)
	switch {
	case len(uv) > 0 && uv[0] == '[' && json.Unmarshal(uv, &corner) == nil:
		f.UV, f.At = "box", round(first(corner, 2))
	case len(uv) > 0 && uv[0] == '{' && json.Unmarshal(uv, &faces) == nil:
		f.UV, f.At, f.Extent = "faces", round(first(faces.North.UV, 2)), round(first(faces.North.Size, 2))
	}
	return f
}

func meshFacts(raw json.RawMessage, listed int) *MeshFacts {
	var mesh struct {
		Normalized *bool           `json:"normalized_uvs"`
		Positions  [][]float64     `json:"positions"`
		Normals    [][]float64     `json:"normals"`
		UVs        [][]float64     `json:"uvs"`
		Polys      json.RawMessage `json:"polys"`
	}
	if err := json.Unmarshal(raw, &mesh); err != nil {
		return &MeshFacts{Unreadable: true}
	}
	f := &MeshFacts{Normalized: mesh.Normalized, Positions: len(mesh.Positions), Normals: len(mesh.Normals), UVs: len(mesh.UVs)}

	// A polygon is a list of corners, each naming a position, a normal and
	// a texture coordinate by index. The two list forms have no indices:
	// the corners are the three lists taken in step, three or four at a time.
	var polys [][][]float64
	var form string
	switch {
	case json.Unmarshal(mesh.Polys, &polys) == nil && polys != nil:
		f.Form, f.Polys = "indexed", len(polys)
	case json.Unmarshal(mesh.Polys, &form) == nil && (form == "tri_list" || form == "quad_list"):
		per := 3
		if form == "quad_list" {
			per = 4
		}
		f.Form, f.Polys = form, len(mesh.Positions)/per
		for i := 0; i < f.Polys && i < listed; i++ {
			var poly [][]float64
			for j := i * per; j < (i+1)*per; j++ {
				poly = append(poly, []float64{float64(j), float64(j), float64(j)})
			}
			polys = append(polys, poly)
		}
	default:
		f.Unreadable = true
		return f
	}

	for _, poly := range polys {
		if len(f.Faces) == listed {
			break
		}
		face, ok := faceFacts(poly, mesh.Positions, mesh.Normals, mesh.UVs)
		if ok {
			f.Faces = append(f.Faces, face)
		}
	}
	return f
}

// indexed is the entry of list that a corner names, if it names one.
func indexed(list [][]float64, index float64, size int) ([]float64, bool) {
	if index < 0 || index >= float64(len(list)) || index != math.Trunc(index) || len(list[int(index)]) < size {
		return nil, false
	}
	return list[int(index)][:size], true
}

func faceFacts(poly, positions, normals, uvs [][]float64) (FaceFacts, bool) {
	// More corners than a quad is not a polygon the format has.
	if len(poly) < 3 || len(poly) > 4 {
		return FaceFacts{}, false
	}
	var f FaceFacts
	top, bottom := math.Inf(-1), math.Inf(1)
	for i, corner := range poly {
		if len(corner) < 3 {
			return FaceFacts{}, false
		}
		pos, okPos := indexed(positions, corner[0], 3)
		uv, okUV := indexed(uvs, corner[2], 2)
		if !okPos || !okUV {
			return FaceFacts{}, false
		}
		if i == 0 {
			if normal, ok := indexed(normals, corner[1], 3); ok {
				f.Normal = round(normal)
			}
			f.Low, f.High = round(pos), round(pos)
			f.UVLow, f.UVHigh = round(uv), round(uv)
		}
		for k := range 3 {
			f.Low[k], f.High[k] = min(f.Low[k], rounded(pos[k])), max(f.High[k], rounded(pos[k]))
		}
		for k := range 2 {
			f.UVLow[k], f.UVHigh[k] = min(f.UVLow[k], rounded(uv[k])), max(f.UVHigh[k], rounded(uv[k]))
		}
		if pos[1] > top {
			top, f.VTop = pos[1], rounded(uv[1])
		}
		if pos[1] < bottom {
			bottom, f.VBottom = pos[1], rounded(uv[1])
		}
	}
	return f, true
}

// rounded keeps a logged number to what a texture coordinate needs, and
// inside what the log's encoding can hold.
func rounded(v float64) float64 {
	const limit = 1e9
	return math.Round(max(-limit, min(limit, v))*1e5) / 1e5
}

func round(values []float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = rounded(v)
	}
	return out
}
