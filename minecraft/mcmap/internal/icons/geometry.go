package icons

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Bounds on the models. The real set is 193 files of 13 KB at most holding
// about 225 models, the largest of 60 bones and a bone of 30 boxes; these
// are several times that. A model is numbers from outside that end up
// choosing which pixels of a texture are copied, so every one of them is
// held to a range before anything is computed from it.
const (
	maxModelBytes  = 256 << 10
	maxModelFiles  = 600
	maxModels      = 2000
	maxModelBones  = 512
	maxBoneCubes   = 256
	maxModelCubes  = 4096
	maxJSONDepth   = 24
	maxInheritance = 8
	// maxModelUnit is the furthest from the origin, and the largest, a box
	// may be, in the model's units, and the largest texture a model may
	// say it is wrapped in.
	maxModelUnit = 1024
)

var (
	geometryID = regexp.MustCompile(`^geometry\.[A-Za-z0-9_.-]{1,96}$`)
	boneName   = regexp.MustCompile(`^[A-Za-z0-9_. -]{1,64}$`)
)

// jsonWithin reports whether no value in raw is nested deeper than depth.
// The decoder would follow a file as deep as it went; this is asked first.
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

// A box of a model: where its lower corner is, how large it is, and where
// on the texture its faces are.
type cube struct {
	Origin   [3]float64      `json:"origin"`
	Size     [3]float64      `json:"size"`
	UV       json.RawMessage `json:"uv"`
	Inflate  *float64        `json:"inflate"`
	Mirror   *bool           `json:"mirror"`
	Rotation []float64       `json:"rotation"`
}

type bone struct {
	Name        string    `json:"name"`
	Parent      string    `json:"parent"`
	Mirror      bool      `json:"mirror"`
	Inflate     float64   `json:"inflate"`
	NeverRender bool      `json:"neverRender"`
	Rotation    []float64 `json:"rotation"`
	// Cubes is nil where the bone names none, which in a model that
	// inherits means the parent's are kept.
	Cubes []cube `json:"cubes"`
}

// model is one geometry as its file wrote it, before what it inherits is
// put under it.
type model struct {
	id, parent string
	// texW and texH are the size the texture is assumed to have, or zero
	// where the model does not say and the texture's own size is used.
	texW, texH int
	bones      []bone
}

func turned(rotation []float64) bool {
	for _, r := range rotation {
		if r != 0 {
			return true
		}
	}
	return false
}

func inRange(values ...float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > maxModelUnit {
			return false
		}
	}
	return true
}

// check holds a model to the bounds. A model outside them is refused
// whole: one odd box is as likely a sign the file is not what it seems as
// it is a slip.
func (m *model) check() error {
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
		if !inRange(b.Inflate) || !inRange(b.Rotation...) {
			return fmt.Errorf("bone %s has a number out of range", b.Name)
		}
		for _, c := range b.Cubes {
			if !inRange(c.Origin[:]...) || !inRange(c.Size[:]...) || !inRange(c.Rotation...) || (c.Inflate != nil && !inRange(*c.Inflate)) ||
				c.Size[0] < 0 || c.Size[1] < 0 || c.Size[2] < 0 || len(c.UV) > 1024 {
				return fmt.Errorf("bone %s has a box with a number out of range", b.Name)
			}
		}
	}
	return nil
}

// parseModels reads every model in one geometry file. The files come in
// two layouts: the older has a key per model, geometry.<name> or
// geometry.<name>:geometry.<parent> for one that inherits, and the newer a
// list under minecraft:geometry with the name in a description. A model
// that does not parse or is out of bounds is left out, and the rest of the
// file is still read.
func parseModels(raw []byte) ([]model, error) {
	if len(raw) > maxModelBytes {
		return nil, fmt.Errorf("the model file is %d bytes, over the limit of %d", len(raw), maxModelBytes)
	}
	raw = stripComments(raw)
	if !jsonWithin(raw, maxJSONDepth) {
		return nil, fmt.Errorf("the model file is nested deeper than %d", maxJSONDepth)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	var out []model
	keep := func(m model) {
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
				Bones []bone `json:"bones"`
			}
			if json.Unmarshal(body, &list) != nil {
				continue
			}
			for _, g := range list {
				id, parent, _ := strings.Cut(g.Description.Identifier, ":")
				keep(model{id: id, parent: parent, texW: wholeOf(g.Description.Width), texH: wholeOf(g.Description.Height), bones: g.Bones})
			}
			continue
		}
		if !strings.HasPrefix(key, "geometry.") {
			continue
		}
		var g struct {
			Width  float64 `json:"texturewidth"`
			Height float64 `json:"textureheight"`
			Bones  []bone  `json:"bones"`
		}
		if json.Unmarshal(body, &g) != nil {
			continue
		}
		id, parent, _ := strings.Cut(key, ":")
		keep(model{id: id, parent: parent, texW: wholeOf(g.Width), texH: wholeOf(g.Height), bones: g.Bones})
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

// resolved is a model with what it inherits put under it: every bone of
// its parent that it does not replace, and its parent's texture size where
// it gives none. A bone of the same name replaces the parent's, except
// that one which lists no boxes keeps the parent's boxes, which is how the
// older files move a bone without redrawing it.
func resolved(models map[string]model, id string) (model, error) {
	return resolve(models, id, 0)
}

func resolve(models map[string]model, id string, depth int) (model, error) {
	m, ok := models[id]
	if !ok {
		return model{}, fmt.Errorf("no model is named %s", id)
	}
	if m.parent == "" {
		return m, nil
	}
	// The limit is also what ends a model that inherits from itself.
	if depth >= maxInheritance {
		return model{}, fmt.Errorf("%s inherits through more than %d models", id, maxInheritance)
	}
	base, err := resolve(models, m.parent, depth+1)
	if err != nil {
		return model{}, err
	}
	out := model{id: m.id, texW: m.texW, texH: m.texH, bones: append([]bone(nil), base.bones...)}
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
		return model{}, fmt.Errorf("%s has over %d bones with what it inherits", id, maxModelBones)
	}
	return out, nil
}

// The six faces of a box, by the names the files use. A mob faces north.
const (
	faceNorth = "north"
	faceSouth = "south"
	faceEast  = "east"
	faceWest  = "west"
	faceUp    = "up"
)

// texRect is a rectangle of a texture, in the units the model counts its
// texture in, and whether it is drawn turned left to right.
type texRect struct {
	X, Y, W, H int
	Flip       bool
}

// faceRect is where on the texture one face of a box is. A box says so
// either with one corner, from which its six faces are laid out the way a
// box unfolds (the top and bottom side by side above a row of the four
// sides, each as large as the box's whole-number size makes it), or face
// by face. A mirrored box has each face turned left to right and its two
// sides exchanged. ok is false for a face the box does not draw, or one of
// no area.
func faceRect(c cube, boneMirror bool, face string) (r texRect, ok bool) {
	x, y, z := int(math.Floor(c.Size[0])), int(math.Floor(c.Size[1])), int(math.Floor(c.Size[2]))
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
			case faceEast:
				face = faceWest
			case faceWest:
				face = faceEast
			}
		}
		u, v := int(math.Floor(corner[0])), int(math.Floor(corner[1]))
		switch face {
		case faceNorth:
			r = texRect{u + z, v + z, x, y, mirror}
		case faceSouth:
			r = texRect{u + 2*z + x, v + z, x, y, mirror}
		case faceEast:
			r = texRect{u, v + z, z, y, mirror}
		case faceWest:
			r = texRect{u + z + x, v + z, z, y, mirror}
		case faceUp:
			r = texRect{u + z, v, x, z, mirror}
		default:
			return texRect{}, false
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
	f, drawn := faces[face]
	if !drawn || len(f.UV) != 2 || !inRange(f.UV...) {
		return texRect{}, false
	}
	w, h := float64(x), float64(y)
	switch face {
	case faceEast, faceWest:
		w = float64(z)
	case faceUp:
		h = float64(z)
	}
	if len(f.Size) == 2 && inRange(f.Size...) {
		w, h = f.Size[0], f.Size[1]
	}
	r = texRect{X: int(math.Floor(f.UV[0])), Y: int(math.Floor(f.UV[1])), W: int(math.Floor(math.Abs(w))), H: int(math.Floor(math.Abs(h)))}
	// A size below zero is a face drawn backwards from its far edge.
	if w < 0 {
		r.X, r.Flip = r.X-r.W, true
	}
	if h < 0 {
		r.Y -= r.H
	}
	return r, r.W > 0 && r.H > 0
}

// placed is one box's face as it lies in a flat view of the model: which
// part of the texture, where in the view in model units with y downwards,
// and how near the viewer.
type placed struct {
	src        texRect
	x, y, w, h int
	// depth is larger the nearer the viewer; the nearer is drawn later.
	depth float64
}

// place is where a box's face lies seen square on. Seen from the front, a
// mob's own right is on the left; the two sides are the box unfolded to
// either hand of the front, and the top is seen with the front at the
// bottom.
func place(c cube, boneMirror bool, boneInflate float64, face string) (placed, bool) {
	src, ok := faceRect(c, boneMirror, face)
	if !ok || turned(c.Rotation) {
		return placed{}, false
	}
	inflate := boneInflate
	if c.Inflate != nil {
		inflate = *c.Inflate
	}
	o, s := c.Origin, c.Size
	// A size is cut down to a whole number, as the game cuts it to find
	// the face on the texture; a place is taken to the nearest, so that
	// two boxes set a fraction either side of the middle stay a pair.
	floor := func(v float64) int { return int(math.Floor(v)) }
	near := func(v float64) int { return int(math.Round(v)) }
	p := placed{src: src}
	switch face {
	case faceNorth:
		p.x, p.y, p.w, p.h, p.depth = near(o[0]), -near(o[1]+s[1]), floor(s[0]), floor(s[1]), -(o[2] - inflate)
	case faceSouth:
		p.x, p.y, p.w, p.h, p.depth = -near(o[0]+s[0]), -near(o[1]+s[1]), floor(s[0]), floor(s[1]), o[2]+s[2]+inflate
	case faceEast:
		p.x, p.y, p.w, p.h, p.depth = -near(o[2]+s[2]), -near(o[1]+s[1]), floor(s[2]), floor(s[1]), -(o[0] - inflate)
	case faceWest:
		p.x, p.y, p.w, p.h, p.depth = near(o[2]), -near(o[1]+s[1]), floor(s[2]), floor(s[1]), o[0]+s[0]+inflate
	case faceUp:
		p.x, p.y, p.w, p.h, p.depth = near(o[0]), -near(o[2]+s[2]), floor(s[0]), floor(s[2]), o[1]+s[1]+inflate
	default:
		return placed{}, false
	}
	// A box grown by more than half a unit shows as a border a pixel wide
	// round whatever it wraps: a sheep's wool round its face.
	if grow := int(inflate + 0.4); grow > 0 && p.w > 0 && p.h > 0 {
		p.x, p.y, p.w, p.h = p.x-grow, p.y-grow, p.w+2*grow, p.h+2*grow
	}
	return p, p.w > 0 && p.h > 0
}
