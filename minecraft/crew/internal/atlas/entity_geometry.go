package atlas

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Bounds on entity models. The pack is trusted only as far as it is
// downloaded from a fixed repository, but the numbers end up as vertex
// positions and texture coordinates in every viewer, so each is held to a
// range before anything is computed from it. The real set is a few hundred
// models of at most a few dozen bones; these are several times that.
const (
	maxModelBytes  = 256 << 10
	maxModels      = 2000
	maxModelBones  = 512
	maxBoneCubes   = 256
	maxModelCubes  = 1024
	maxJSONDepth   = 24
	maxInheritance = 8
	// maxModelUnit bounds how far from the origin, and how large, a box may
	// be in model units, and the texture size a model may declare.
	maxModelUnit = 1024
)

var (
	geometryID = regexp.MustCompile(`^geometry\.[A-Za-z0-9_.-]{1,96}$`)
	boneName   = regexp.MustCompile(`^[A-Za-z0-9_. -]{1,64}$`)
)

// jsonWithin reports whether no value in raw is nested deeper than depth. The
// decoder would follow a file as deep as it went, so this is asked first.
func jsonWithin(raw []byte, depth int) bool {
	level := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			for i++; i < len(raw) && raw[i] != '"'; i++ {
				if raw[i] == '\\' {
					i++
				}
			}
		case '{', '[':
			if level++; level > depth {
				return false
			}
		case '}', ']':
			level--
		}
	}
	return true
}

type geoCube struct {
	Origin   [3]float64      `json:"origin"`
	Size     [3]float64      `json:"size"`
	UV       json.RawMessage `json:"uv"`
	Inflate  *float64        `json:"inflate"`
	Mirror   *bool           `json:"mirror"`
	Rotation []float64       `json:"rotation"`
	Pivot    []float64       `json:"pivot"`
}

type geoBone struct {
	Name        string    `json:"name"`
	Parent      string    `json:"parent"`
	Mirror      bool      `json:"mirror"`
	Inflate     float64   `json:"inflate"`
	NeverRender bool      `json:"neverRender"`
	Rotation    []float64 `json:"rotation"`
	Pivot       []float64 `json:"pivot"`
	// BindPose turns only the bone's own boxes about its pivot: the older
	// models lie a body down this way.
	BindPose []float64 `json:"bind_pose_rotation"`
	// Cubes is nil where the bone lists none, which in a model that inherits
	// means the parent's are kept.
	Cubes []geoCube `json:"cubes"`
}

// geoModel is one geometry as its file wrote it, before inheritance.
type geoModel struct {
	id, parent string
	// texW and texH are the texture size the model counts in, or zero where
	// it does not say and the texture's own size is used.
	texW, texH int
	bones      []geoBone
}

func inRange(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > maxModelUnit {
			return false
		}
	}
	return true
}

// vec3 reads an optional three-number list; ok is false for any other length.
func vec3(v []float64) (out [3]float32, ok bool) {
	if len(v) == 0 {
		return out, true
	}
	if len(v) != 3 {
		return out, false
	}
	return [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}, true
}

// check holds a model to the bounds. A model outside them is refused whole:
// one odd box is as likely a sign the file is not what it seems as a slip.
func (m *geoModel) check() error {
	if !geometryID.MatchString(m.id) || (m.parent != "" && !geometryID.MatchString(m.parent)) {
		return errors.New("not a geometry's name")
	}
	if m.texW < 0 || m.texH < 0 || m.texW > maxModelUnit || m.texH > maxModelUnit {
		return fmt.Errorf("texture size %dx%d is out of range", m.texW, m.texH)
	}
	if len(m.bones) > maxModelBones {
		return fmt.Errorf("%d bones, over the limit of %d", len(m.bones), maxModelBones)
	}
	cubes := 0
	for _, b := range m.bones {
		if !boneName.MatchString(b.Name) || (b.Parent != "" && !boneName.MatchString(b.Parent)) {
			return errors.New("a bone has no usable name")
		}
		if len(b.Cubes) > maxBoneCubes {
			return fmt.Errorf("bone %s has %d boxes, over the limit of %d", b.Name, len(b.Cubes), maxBoneCubes)
		}
		if cubes += len(b.Cubes); cubes > maxModelCubes {
			return fmt.Errorf("over %d boxes", maxModelCubes)
		}
		for _, v := range [][]float64{b.Rotation, b.Pivot, b.BindPose} {
			if _, ok := vec3(v); !ok || !inRange(v...) {
				return fmt.Errorf("bone %s has a number out of range", b.Name)
			}
		}
		if !inRange(b.Inflate) {
			return fmt.Errorf("bone %s has a number out of range", b.Name)
		}
		for _, c := range b.Cubes {
			_, rok := vec3(c.Rotation)
			_, pok := vec3(c.Pivot)
			if !rok || !pok || !inRange(c.Origin[:]...) || !inRange(c.Size[:]...) || !inRange(c.Rotation...) || !inRange(c.Pivot...) ||
				(c.Inflate != nil && !inRange(*c.Inflate)) || c.Size[0] < 0 || c.Size[1] < 0 || c.Size[2] < 0 || len(c.UV) > 1024 {
				return fmt.Errorf("bone %s has a box with a number out of range", b.Name)
			}
		}
	}
	return nil
}

// parseModels reads every model in one geometry file. Two layouts exist: the
// older has a key per model, geometry.<name> or geometry.<name>:geometry.<parent>
// for one that inherits, and the newer a list under minecraft:geometry with
// the name in a description. A model that does not parse or is out of bounds
// is left out, and the rest of the file is still read.
func parseModels(raw []byte) ([]geoModel, error) {
	if len(raw) > maxModelBytes {
		return nil, fmt.Errorf("the model file is %d bytes, over the limit of %d", len(raw), maxModelBytes)
	}
	raw = stripJSONComments(raw)
	if !jsonWithin(raw, maxJSONDepth) {
		return nil, fmt.Errorf("the model file is nested deeper than %d", maxJSONDepth)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	var out []geoModel
	keep := func(m geoModel) {
		if m.check() == nil && len(out) < maxModels {
			out = append(out, m)
		}
	}
	for key, body := range top {
		if key == "minecraft:geometry" {
			var list []struct {
				Description struct {
					Identifier string  `json:"identifier"`
					Width      float64 `json:"texture_width"`
					Height     float64 `json:"texture_height"`
				} `json:"description"`
				Bones []geoBone `json:"bones"`
			}
			if json.Unmarshal(body, &list) != nil {
				continue
			}
			for _, g := range list {
				id, parent, _ := strings.Cut(g.Description.Identifier, ":")
				keep(geoModel{id: id, parent: parent, texW: wholeOf(g.Description.Width), texH: wholeOf(g.Description.Height), bones: g.Bones})
			}
			continue
		}
		if !strings.HasPrefix(key, "geometry.") {
			continue
		}
		var g struct {
			Width  float64   `json:"texturewidth"`
			Height float64   `json:"textureheight"`
			Bones  []geoBone `json:"bones"`
		}
		if json.Unmarshal(body, &g) != nil {
			continue
		}
		id, parent, _ := strings.Cut(key, ":")
		keep(geoModel{id: id, parent: parent, texW: wholeOf(g.Width), texH: wholeOf(g.Height), bones: g.Bones})
	}
	return out, nil
}

// wholeOf is a size as a whole number, or -1, which check refuses, for one
// that is not.
func wholeOf(v float64) int {
	if math.IsNaN(v) || v < 0 || v > maxModelUnit || v != math.Trunc(v) {
		return -1
	}
	return int(v)
}

// resolveModel puts what a model inherits under it: every bone of its parent
// that it does not replace, and the parent's texture size where it gives
// none. A bone of the same name replaces the parent's, except that one which
// lists no boxes keeps the parent's, which is how the older files move a bone
// without redrawing it.
func resolveModel(models map[string]geoModel, id string, depth int) (geoModel, error) {
	m, ok := models[id]
	if !ok {
		return geoModel{}, fmt.Errorf("no model is named %s", id)
	}
	if m.parent == "" {
		return m, nil
	}
	// The limit is also what ends a model that inherits from itself.
	if depth >= maxInheritance {
		return geoModel{}, fmt.Errorf("%s inherits through more than %d models", id, maxInheritance)
	}
	base, err := resolveModel(models, m.parent, depth+1)
	if err != nil {
		return geoModel{}, err
	}
	out := geoModel{id: m.id, texW: m.texW, texH: m.texH, bones: append([]geoBone(nil), base.bones...)}
	if out.texW == 0 || out.texH == 0 {
		out.texW, out.texH = base.texW, base.texH
	}
	at := map[string]int{}
	for i, b := range out.bones {
		at[b.Name] = i
	}
	for _, b := range m.bones {
		i, held := at[b.Name]
		if !held {
			at[b.Name] = len(out.bones)
			out.bones = append(out.bones, b)
			continue
		}
		if b.Cubes == nil {
			b.Cubes = out.bones[i].Cubes
		}
		if b.Parent == "" {
			b.Parent = out.bones[i].Parent
		}
		out.bones[i] = b
	}
	if len(out.bones) > maxModelBones {
		return geoModel{}, fmt.Errorf("%s has over %d bones with what it inherits", id, maxModelBones)
	}
	// Each model passed the cube limit alone; inheriting adds a parent's.
	cubes := 0
	for _, b := range out.bones {
		cubes += len(b.Cubes)
	}
	if cubes > maxModelCubes {
		return geoModel{}, fmt.Errorf("%s has over %d boxes with what it inherits", id, maxModelCubes)
	}
	return out, nil
}

// Face order in a compiled cube.
const (
	faceN = iota
	faceS
	faceE
	faceW
	faceU
	faceD
	faceCount
)

var faceKeysByIndex = [faceCount]string{"north", "south", "east", "west", "up", "down"}

// texRect is a rectangle of the model's texture in the model's own units, and
// whether it is drawn turned left to right.
type texRect struct {
	X, Y, W, H float64
	Flip       bool
}

// faceRect is where on the texture one face of a box is. A box says so with
// one corner, from which its six faces unfold the way a box does (top and
// bottom side by side above a row of the four sides, each as large as the
// box's whole-number size makes it), or face by face. A mirrored box has each
// face turned left to right and its two sides exchanged. ok is false for a
// face the box does not draw or one of no area.
func faceRect(c geoCube, boneMirror bool, face int) (r texRect, ok bool) {
	x, y, z := math.Floor(c.Size[0]), math.Floor(c.Size[1]), math.Floor(c.Size[2])
	var corner []float64
	if json.Unmarshal(c.UV, &corner) == nil {
		if len(corner) != 2 || !inRange(corner...) {
			return texRect{}, false
		}
		mirror := boneMirror
		if c.Mirror != nil {
			mirror = *c.Mirror
		}
		if mirror {
			switch face {
			case faceE:
				face = faceW
			case faceW:
				face = faceE
			}
		}
		u, v := math.Floor(corner[0]), math.Floor(corner[1])
		switch face {
		case faceN:
			r = texRect{u + z, v + z, x, y, mirror}
		case faceS:
			r = texRect{u + 2*z + x, v + z, x, y, mirror}
		case faceE:
			r = texRect{u, v + z, z, y, mirror}
		case faceW:
			r = texRect{u + z + x, v + z, z, y, mirror}
		case faceU:
			r = texRect{u + z, v, x, z, mirror}
		case faceD:
			r = texRect{u + z + x, v, x, z, mirror}
		}
		return r, r.W > 0 && r.H > 0
	}
	var faces map[string]struct {
		UV   []float64 `json:"uv"`
		Size []float64 `json:"uv_size"`
	}
	if json.Unmarshal(c.UV, &faces) != nil {
		return texRect{}, false
	}
	f, drawn := faces[faceKeysByIndex[face]]
	if !drawn || len(f.UV) != 2 || !inRange(f.UV...) {
		return texRect{}, false
	}
	w, h := x, y
	switch face {
	case faceE, faceW:
		w = z
	case faceU, faceD:
		h = z
	}
	if len(f.Size) == 2 && inRange(f.Size...) {
		w, h = f.Size[0], f.Size[1]
	}
	r = texRect{X: math.Floor(f.UV[0]), Y: math.Floor(f.UV[1]), W: math.Floor(math.Abs(w)), H: math.Floor(math.Abs(h))}
	// A size below zero is a face drawn backwards from its far edge.
	if w < 0 {
		r.X, r.Flip = r.X-r.W, true
	}
	if h < 0 {
		r.Y -= r.H
	}
	return r, r.W > 0 && r.H > 0
}
