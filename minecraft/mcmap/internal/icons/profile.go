package icons

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
)

// A profile is a mob seen from its side, as it stands in the game: whole,
// or the bones that make it known (a horse's head and neck), with its
// bones set at the angles the model gives them. Seen from the side, a turn
// about the model's x axis, which runs from flank to flank, turns a box in
// the plane of the picture, so it can be drawn: it is what a face cannot
// draw. A turn the other two ways takes a box out of that plane, and a box
// turned so more than squareWithin is left out.
//
// The view is from the mob's left, with its front to the left of the
// picture; or, for a mob known by the spread of its wings or the whole of
// its front, from above or the front, where the turn in the picture's
// plane is the one about y or about z.

// The most a profile may measure along its longer side against its
// shorter: a fish is long, and that is how it is known.
const profileAspect = 3

// turn is a rotation in the picture's plane and a shift after it: where a
// point of a box ends up once every bone above it is turned.
type turn struct {
	angle  float64
	dx, dy float64
}

func (t turn) apply(x, y float64) (float64, float64) {
	sin, cos := math.Sincos(t.angle * math.Pi / 180)
	return x*cos - y*sin + t.dx, x*sin + y*cos + t.dy
}

// then is t followed by u.
func (t turn) then(u turn) turn {
	x, y := u.apply(t.dx, t.dy)
	return turn{angle: t.angle + u.angle, dx: x, dy: y}
}

// about is a turn of angle degrees, clockwise in the picture, about a point.
func about(angle, x, y float64) turn {
	t := turn{angle: angle}
	rx, ry := t.apply(x, y)
	t.dx, t.dy = x-rx, y-ry
	return t
}

// flat reports whether a rotation leaves a box in the plane of the view,
// and how far it turns it there, clockwise in the picture. A turn of the
// model's about x that is more than nought tips a box's top forwards,
// which seen with the mob's front to the left is anticlockwise.
func flat(rotation []float64, from string) (angle float64, ok bool) {
	axis, sign := 0, -1.0
	switch from {
	case faceUp:
		axis, sign = 1, 1
	case faceNorth:
		axis, sign = 2, 1
	}
	for i, r := range rotation {
		if i != axis && math.Abs(r) > squareWithin {
			return 0, false
		}
	}
	if len(rotation) <= axis {
		return 0, true
	}
	return sign * rotation[axis], true
}

// pointIn is a model's point as it lies in the view, y downwards: from the
// side, z across and y up; from above, x across and z down.
func pointIn(p []float64, from string) (x, y float64) {
	if len(p) != 3 {
		return 0, 0
	}
	switch from {
	case faceUp:
		return p[0], -p[2]
	case faceNorth:
		return p[0], -p[1]
	}
	return p[2], -p[1]
}

// placement is where each bone of a model lies in the side view, by its
// place in the bones, and false for one that is turned out of the plane
// or hangs off one that is.
func (m model) placement(from string) (turns []turn, ok []bool) {
	turns, ok = make([]turn, len(m.bones)), make([]bool, len(m.bones))
	done := make([]bool, len(m.bones))
	var place func(i, depth int)
	place = func(i, depth int) {
		if done[i] {
			return
		}
		done[i] = true
		b := m.bones[i]
		angle, inPlane := flat(b.Rotation, from)
		parent := turn{}
		if b.Parent != "" {
			p := m.bone(b.Parent)
			if p < 0 || p == i || depth > maxModelBones {
				return
			}
			place(p, depth+1)
			if !ok[p] {
				return
			}
			parent = turns[p]
		}
		if !inPlane {
			return
		}
		x, y := pointIn(b.Pivot, from)
		turns[i], ok[i] = about(angle, x, y).then(parent), true
	}
	for i := range m.bones {
		place(i, 0)
	}
	return turns, ok
}

// sideOf is a model's boxes as they lie seen from the side: from the bones
// named and all that hangs off them, or from every bone. A bone the model
// never draws, or the controller hides, is left out with all under it.
func (m model) sideOf(from string, roots, skip []string, hides func(string) bool) []sidePart {
	turns, ok := m.placement(from)
	keep := make([]bool, len(m.bones))
	for i, b := range m.bones {
		keep[i] = len(roots) == 0 || slices.Contains(roots, b.Name)
	}
	if len(roots) > 0 {
		for i, b := range m.bones {
			if slices.Contains(roots, b.Name) {
				for _, j := range m.under(i) {
					keep[j] = true
				}
			}
		}
	}
	gone := make([]bool, len(m.bones))
	for i, b := range m.bones {
		if b.NeverRender || slices.Contains(skip, b.Name) || (hides != nil && hides(b.Name)) {
			gone[i] = true
			for _, j := range m.under(i) {
				gone[j] = true
			}
		}
	}
	var out []sidePart
	for i, b := range m.bones {
		if !keep[i] || gone[i] || !ok[i] {
			continue
		}
		pose, posed := flat(b.BindPose, from)
		if !posed {
			continue
		}
		for _, c := range b.Cubes {
			angle, inPlane := flat(c.Rotation, from)
			if !inPlane {
				continue
			}
			p, drawn := place(c, b.Mirror, b.Inflate, from)
			if !drawn {
				continue
			}
			t := turns[i]
			if pose != 0 {
				x, y := pointIn(b.Pivot, from)
				t = about(pose, x, y).then(t)
			}
			if angle != 0 {
				x, y := pointIn(c.Pivot, from)
				t = about(angle, x, y).then(t)
			}
			// Where the box is, not where the whole units of its face
			// on the texture would put it: a parrot is a few units tall
			// and its boxes are set in tenths.
			inflate := b.Inflate
			if c.Inflate != nil {
				inflate = *c.Inflate
			}
			o, size := c.Origin, c.Size
			cx, cy := t.apply(pointIn([]float64{o[0] + size[0]/2, o[1] + size[1]/2, o[2] + size[2]/2}, from))
			w, h := size[2]+2*inflate, size[1]+2*inflate
			switch from {
			case faceUp:
				w, h = size[0]+2*inflate, size[2]+2*inflate
			case faceNorth:
				w = size[0] + 2*inflate
			}
			if w <= 0 || h <= 0 {
				continue
			}
			part := sidePart{placed: p, angle: math.Mod(t.angle, 360), cx: cx, cy: cy, w: w, h: h, bone: b.Name}
			out = append(out, part)
			// A sheet with no thickness, a tadpole's tail or a fin, is one
			// plane with a face on either side, and which of the two the
			// texture fills is up to the model: the far one is drawn too,
			// turned to face this way.
			if from == faceWest && size[0] == 0 {
				if back, ok := place(c, b.Mirror, b.Inflate, faceEast); ok {
					part.placed = back
					part.src.Flip = !part.src.Flip
					out = append(out, part)
				}
			}
		}
	}
	return out
}

// sidePart is one box's side as it lies in the view once turned: its
// middle, and how far it is turned about it.
type sidePart struct {
	placed
	angle, cx, cy, w, h float64
	bone                string
}

// bounds is the part of the view a box covers.
func (p sidePart) bounds() (x0, y0, x1, y1 float64) {
	sin, cos := math.Sincos(p.angle * math.Pi / 180)
	hw, hh := p.w/2, p.h/2
	ex, ey := math.Abs(hw*cos)+math.Abs(hh*sin), math.Abs(hw*sin)+math.Abs(hh*cos)
	return p.cx - ex, p.cy - ey, p.cx + ex, p.cy + ey
}

// profile is the recipe for a mob seen from the side: every model its
// controllers draw it with, each with its textures laid over the last, the
// nearest box last.
func (lib library) profile(a appearance, o override) (Recipe, error) {
	parts, err := lib.parts(a, o)
	if err != nil {
		return Recipe{}, err
	}
	type layered struct {
		sidePart
		texture string
		units   [2]int
		skin    bool
	}
	from := o.from
	if from == "" {
		from = faceWest
	}
	var all []layered
	for n, part := range parts {
		for _, texture := range part.textures {
			if !modelTexture.MatchString(texture) {
				continue
			}
			for _, p := range part.model.sideOf(from, o.bones, o.skip, part.hides) {
				if len(all) >= maxLayers {
					return Recipe{}, fmt.Errorf("the profile is over %d layers", maxLayers)
				}
				all = append(all, layered{p, texture, [2]int{part.model.texW, part.model.texH}, n == 0})
			}
		}
	}
	if len(all) == 0 {
		return Recipe{}, errors.New("nothing of the model is drawn from the side")
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].depth < all[b].depth })
	left, top, right, bottom := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	mainBox := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range all {
		x0, y0, x1, y1 := p.bounds()
		left, top, right, bottom = min(left, x0), min(top, y0), max(right, x1), max(bottom, y1)
		if o.bone == "" || p.bone == o.bone {
			mainBox = [4]float64{min(mainBox[0], x0), min(mainBox[1], y0), max(mainBox[2], x1), max(mainBox[3], y1)}
		}
	}
	const slack = 1e-9
	l, t := math.Floor(left+slack), math.Floor(top+slack)
	// Sparse: a mob seen whole, or a head at a slant, leaves much of its
	// bounds empty, which a face does not.
	out := Recipe{W: int(math.Ceil(right-slack) - l), H: int(math.Ceil(bottom-slack) - t), Sparse: true}
	if math.IsInf(mainBox[0], 0) {
		mainBox = [4]float64{left, top, right, bottom}
	}
	mx, my := max(int(math.Floor(mainBox[0]+slack)-l), 0), max(int(math.Floor(mainBox[1]+slack)-t), 0)
	out.Main = [4]int{mx, my, min(int(math.Ceil(mainBox[2]-slack)-l), out.W) - mx, min(int(math.Ceil(mainBox[3]-slack)-t), out.H) - my}
	if short, long := min(out.W, out.H), max(out.W, out.H); !o.strip && (short < minFaceShort || long > short*profileAspect) {
		return Recipe{}, fmt.Errorf("the profile is %dx%d, a strip that would be a speck at a marker's size", out.W, out.H)
	}
	for _, p := range all {
		layer := Layer{Texture: p.texture, Units: p.units, Skin: p.skin, Flip: p.src.Flip, Src: [4]int{p.src.X, p.src.Y, p.src.W, p.src.H},
			Span: [2]float64{p.w, p.h}, Centre: [2]float64{p.cx - l, p.cy - t}, Angle: p.angle, Tint: o.tints[p.texture]}
		b := freeBounds(layer)
		layer.Dst = [4]int{b.Min.X, b.Min.Y, b.Dx(), b.Dy()}
		out.Layers = append(out.Layers, layer)
	}
	return out, out.check()
}
