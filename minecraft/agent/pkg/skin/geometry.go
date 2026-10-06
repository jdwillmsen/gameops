package skin

import (
	"bytes"
	"encoding/json"
	"image"
	"math"
	"strings"
)

// GeometryError is why a skin's own model gave no head. Its text is short
// and fixed, so it can be logged as the reason beside the skin's facts.
type GeometryError string

func (e GeometryError) Error() string { return "skin: geometry: " + string(e) }

const (
	ErrGeometrySize  GeometryError = "geometry_size"
	ErrGeometryDepth GeometryError = "geometry_depth"
	ErrGeometryCount GeometryError = "geometry_count"
	ErrGeometryJSON  GeometryError = "geometry_json"
	// ErrNoModel is a file that does not hold the model the skin names.
	ErrNoModel GeometryError = "model_missing"
	ErrNoHead  GeometryError = "head_missing"
	// ErrHeadEmpty is a head bone that draws nothing in this model: the
	// head is drawn by another model of the file, from another image.
	ErrHeadEmpty GeometryError = "head_empty"
	// ErrHeadMesh is a head built of free polygons rather than a cube.
	ErrHeadMesh GeometryError = "head_mesh"
	// ErrHeadCube is a head cube that is turned, mirrored, or mapped in a
	// way whose front face this does not know how to read.
	ErrHeadCube    GeometryError = "head_cube"
	ErrHeadTexture GeometryError = "head_texture"
	ErrHeadUV      GeometryError = "head_uv"
	ErrHeadShape   GeometryError = "head_shape"
	ErrHatUV       GeometryError = "hat_uv"
)

// Everything in a geometry file was written by another player's client, so
// each of these is a ceiling on what one skin can make this process do.
const (
	// MaxGeometryBytes is room for a model of several thousand polygons; one
	// made of boxes is a few kilobytes.
	MaxGeometryBytes = 1 << 20
	// maxDepth is how deep the arrays and objects may nest. The deepest
	// thing the format defines, a face of a cube, is ten levels down.
	maxDepth = 16
	// maxContainers is how many arrays and objects the file may hold. The
	// decoder allocates for each one, an empty one included, so the byte
	// limit alone would let a megabyte of "{}" cost fifty times its size.
	maxContainers = 1 << 16
	maxModels     = 16
	maxBones      = 512
	maxCubes      = 64
	// maxUnit bounds every coordinate before it is converted or multiplied.
	maxUnit = 4096
	// maxImageSide is the largest skin image a head is taken from.
	maxImageSide = 1024
	// minHeadSide and MaxHeadSide are the sizes the map accepts a head in.
	minHeadSide = 8
	MaxHeadSide = 32
)

// Geometry is a skin's own description of the model it is drawn on, as
// far as finding the head needs it.
type Geometry struct {
	format string
	models []model
	// Measured before anything is decoded, and so known even for a file
	// that was refused.
	bytes, depth, containers int
}

type model struct {
	Description struct {
		Identifier    string   `json:"identifier"`
		TextureWidth  *float64 `json:"texture_width"`
		TextureHeight *float64 `json:"texture_height"`
	} `json:"description"`
	Bones []bone `json:"bones"`
}

type bone struct {
	Name     string          `json:"name"`
	Rotation []float64       `json:"rotation"`
	Mirror   bool            `json:"mirror"`
	Cubes    []cube          `json:"cubes"`
	PolyMesh json.RawMessage `json:"poly_mesh"`
}

type cube struct {
	Origin   []float64       `json:"origin"`
	Size     []float64       `json:"size"`
	Rotation []float64       `json:"rotation"`
	Inflate  float64         `json:"inflate"`
	Mirror   *bool           `json:"mirror"`
	UV       json.RawMessage `json:"uv"`
}

// NoGeometry reports whether a skin sent no model of its own, which is
// what a client using one of the game's built-in models does: the field is
// empty or the JSON null.
func NoGeometry(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// measure walks the file once without building anything, so that a file
// too deep or too crowded is refused before the decoder allocates for it.
func measure(raw []byte) (depth, containers int) {
	level, inString, escaped := 0, false, false
	for _, c := range raw {
		switch {
		case escaped:
			escaped = false
		case inString:
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			level++
			containers++
			depth = max(depth, level)
		case c == '}' || c == ']':
			level--
		}
	}
	return depth, containers
}

// ParseGeometry reads a skin's geometry file. The Geometry is returned
// even with an error, holding the measurements that were taken before the
// file was refused.
func ParseGeometry(raw []byte) (*Geometry, error) {
	g := &Geometry{bytes: len(raw)}
	if len(raw) > MaxGeometryBytes {
		return g, ErrGeometrySize
	}
	g.depth, g.containers = measure(raw)
	if g.depth > maxDepth {
		return g, ErrGeometryDepth
	}
	if g.containers > maxContainers {
		return g, ErrGeometryCount
	}
	var file struct {
		Format string  `json:"format_version"`
		Models []model `json:"minecraft:geometry"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return g, ErrGeometryJSON
	}
	if len(file.Models) > maxModels {
		return g, ErrGeometryCount
	}
	g.format, g.models = file.Format, file.Models
	return g, nil
}

// HeadUV is where a skin's image holds the front of the head, and the
// layer drawn over it if the model has one. Both are in pixels of the
// image and lie inside it.
type HeadUV struct {
	Face image.Rectangle
	// Hat is empty for a model with no layer over the head.
	Hat image.Rectangle
}

func part(bones []bone, name string) *bone {
	for i := range bones {
		// The game matches bone names without regard to case.
		if strings.EqualFold(bones[i].Name, name) {
			return &bones[i]
		}
	}
	return nil
}

// Head finds the head of the model named identifier in an image of the
// given size.
//
// The front of the head is the north face of the head bone's largest cube.
// The layer over it is a cube of the same size on the hat bone, or another
// on the head bone. A model that draws its head any other way is refused
// rather than guessed at.
func (g *Geometry) Head(identifier string, width, height int) (HeadUV, error) {
	var m *model
	for i := range g.models {
		if g.models[i].Description.Identifier == identifier {
			m = &g.models[i]
			break
		}
	}
	if m == nil {
		return HeadUV{}, ErrNoModel
	}
	if len(m.Bones) > maxBones {
		return HeadUV{}, ErrGeometryCount
	}
	head := part(m.Bones, "head")
	if head == nil {
		return HeadUV{}, ErrNoHead
	}
	if len(head.Cubes) == 0 {
		if len(head.PolyMesh) > 0 {
			return HeadUV{}, ErrHeadMesh
		}
		return HeadUV{}, ErrHeadEmpty
	}
	if len(head.Cubes) > maxCubes {
		return HeadUV{}, ErrGeometryCount
	}
	scale, err := m.scale(width, height)
	if err != nil {
		return HeadUV{}, err
	}

	// Ears, horns and the like hang off the head bone as smaller cubes.
	face, volume := 0, 0.0
	for i, c := range head.Cubes {
		if v := c.volume(); v > volume {
			face, volume = i, v
		}
	}
	out := HeadUV{}
	if out.Face, err = head.Cubes[face].north(head, scale, width, height); err != nil {
		return HeadUV{}, err
	}
	if side := out.Face.Dx(); side != out.Face.Dy() || side < minHeadSide {
		return HeadUV{}, ErrHeadShape
	}

	type layer struct {
		of *bone
		c  cube
	}
	var layers []layer
	if hat := part(m.Bones, "hat"); hat != nil {
		if len(hat.Cubes) > maxCubes {
			return HeadUV{}, ErrGeometryCount
		}
		for _, c := range hat.Cubes {
			layers = append(layers, layer{hat, c})
		}
	}
	for i, c := range head.Cubes {
		if i != face {
			layers = append(layers, layer{head, c})
		}
	}
	for _, l := range layers {
		if !sameSize(l.c.Size, head.Cubes[face].Size) {
			continue
		}
		// A layer that is there but cannot be read is not left off: the
		// head without it would not be the one the game draws.
		hat, err := l.c.north(l.of, scale, width, height)
		if err != nil || hat.Size() != out.Face.Size() {
			return HeadUV{}, ErrHatUV
		}
		out.Hat = hat
		break
	}
	return out, nil
}

// scale is how many pixels of the image one unit of the model's texture
// coordinates covers. A model declares the size of the texture its
// coordinates are written against, and the image may be that size or a
// whole multiple of it.
func (m *model) scale(width, height int) (int, error) {
	tw, th := m.Description.TextureWidth, m.Description.TextureHeight
	if tw == nil || th == nil || !whole(*tw, 1, maxImageSide) || !whole(*th, 1, maxImageSide) {
		return 0, ErrHeadTexture
	}
	w, h := int(*tw), int(*th)
	if width < 1 || height < 1 || width > maxImageSide || height > maxImageSide || width%w != 0 || height%h != 0 || width/w != height/h {
		return 0, ErrHeadTexture
	}
	return width / w, nil
}

// whole reports whether v is a whole number from lo to hi. It is asked
// before any conversion, since a JSON number can be far outside an int.
func whole(v float64, lo, hi int) bool {
	return v >= float64(lo) && v <= float64(hi) && v == math.Trunc(v)
}

func turned(rotation []float64) bool {
	for _, r := range rotation {
		if r != 0 {
			return true
		}
	}
	return false
}

func sameSize(a, b []float64) bool {
	return len(a) == 3 && len(b) == 3 && a[0] == b[0] && a[1] == b[1] && a[2] == b[2]
}

func (c cube) volume() float64 {
	if len(c.Size) != 3 || !whole(c.Size[0], 1, maxUnit) || !whole(c.Size[1], 1, maxUnit) || !whole(c.Size[2], 1, maxUnit) {
		return 0
	}
	return c.Size[0] * c.Size[1] * c.Size[2]
}

// north is where the image holds the cube's north face, the one a player
// model's face is on, in pixels. A cube gives its texture coordinates
// either as one corner, from which the six faces are laid out in the
// game's fixed pattern, or as a rectangle for each face.
func (c cube) north(of *bone, scale, width, height int) (image.Rectangle, error) {
	mirrored := of.Mirror
	if c.Mirror != nil {
		mirrored = *c.Mirror
	}
	if c.volume() == 0 || mirrored || turned(of.Rotation) || turned(c.Rotation) {
		return image.Rectangle{}, ErrHeadCube
	}
	w, h, d := c.Size[0], c.Size[1], c.Size[2]
	var u, v float64
	uv := bytes.TrimSpace(c.UV)
	switch {
	case len(uv) > 0 && uv[0] == '[':
		var corner []float64
		if err := json.Unmarshal(uv, &corner); err != nil || len(corner) != 2 {
			return image.Rectangle{}, ErrHeadCube
		}
		// The pattern puts the west face, as wide as the cube is deep, to
		// the left of the north one, and the top above it.
		u, v = corner[0]+d, corner[1]+d
	case len(uv) > 0 && uv[0] == '{':
		var faces struct {
			North *struct {
				UV       []float64 `json:"uv"`
				Size     []float64 `json:"uv_size"`
				Rotation float64   `json:"uv_rotation"`
			} `json:"north"`
		}
		if err := json.Unmarshal(uv, &faces); err != nil || faces.North == nil || len(faces.North.UV) != 2 || faces.North.Rotation != 0 {
			return image.Rectangle{}, ErrHeadCube
		}
		u, v = faces.North.UV[0], faces.North.UV[1]
		// A face that names no size takes the cube's. A negative size
		// draws the face flipped, which is refused below.
		if size := faces.North.Size; size != nil {
			if len(size) != 2 {
				return image.Rectangle{}, ErrHeadCube
			}
			w, h = size[0], size[1]
		}
	default:
		return image.Rectangle{}, ErrHeadCube
	}
	if !whole(u, 0, maxUnit) || !whole(v, 0, maxUnit) || !whole(w, 1, maxUnit) || !whole(h, 1, maxUnit) {
		return image.Rectangle{}, ErrHeadUV
	}
	// Every term is at most maxUnit and the scale at most maxImageSide, so
	// none of this can overflow.
	r := image.Rect(int(u)*scale, int(v)*scale, (int(u)+int(w))*scale, (int(v)+int(h))*scale)
	if !r.In(image.Rect(0, 0, width, height)) {
		return image.Rectangle{}, ErrHeadUV
	}
	return r, nil
}

// HeadAt takes the head from where a skin's own model says it is: the face
// with the hat laid over it, as Head does for a classic skin, made smaller
// if it is more than the map accepts. rgba is row-major RGBA.
//
// The rectangles are checked again here against the image, so a caller
// that built them some other way cannot read outside it.
func HeadAt(width, height int, rgba []byte, at HeadUV) (*image.NRGBA, error) {
	if width < 1 || height < 1 || width > maxImageSide || height > maxImageSide || len(rgba) != width*height*4 {
		return nil, ErrHeadTexture
	}
	bounds := image.Rect(0, 0, width, height)
	side := at.Face.Dx()
	if at.Face.Empty() || !at.Face.In(bounds) || side != at.Face.Dy() || side < minHeadSide {
		return nil, ErrHeadShape
	}
	layered := !at.Hat.Empty()
	if layered && (!at.Hat.In(bounds) || at.Hat.Size() != at.Face.Size()) {
		return nil, ErrHatUV
	}
	size := min(side, MaxHeadSide)
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			// The middle of each pixel of the result, in the face: exact
			// when nothing is made smaller, and the nearest pixel when it is.
			sx, sy := (2*x+1)*side/(2*size), (2*y+1)*side/(2*size)
			face := rgba[((at.Face.Min.Y+sy)*width+at.Face.Min.X+sx)*4:]
			px := out.Pix[(y*size+x)*4:]
			copy(px[:3], face[:3])
			if layered {
				hat := rgba[((at.Hat.Min.Y+sy)*width+at.Hat.Min.X+sx)*4:]
				a := uint32(hat[3])
				for c := range 3 {
					px[c] = byte((uint32(hat[c])*a + uint32(face[c])*(255-a) + 127) / 255)
				}
			}
			px[3] = 255
		}
	}
	return out, nil
}
