package icons

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Bounds on a picture made here. A head is 8 units a side and the largest
// model texture 256 pixels, which stored unpacked is 262 KB; a pack drawn
// at a higher resolution is allowed for, but not much.
const (
	maxModelTextureBytes = 512 << 10
	maxModelTextureSide  = 1024
	// maxTextureScale is how many pixels a texture may have to each unit
	// its model counts it in.
	maxTextureScale = 4
	maxLayers       = 64
	// maxFaceUnits is the widest a face may come to with its ears and
	// horns, and minFaceUnits the narrowest box that can be one.
	maxFaceUnits = 48
	minFaceUnits = 3
	// faceBox is the side of the box the page fits every face into, in
	// the model's units, and minFaceShort the least a face may measure
	// along its shorter side once everything on it is drawn.
	faceBox      = 16
	minFaceShort = 4
	maxRecipes   = 600
)

// modelTexture is a path this service will ask for a model's texture at.
var modelTexture = regexp.MustCompile(`^textures/(entity|blocks|items|map)/[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+){0,3}(\.tga)?$`)

// tgaSuffix ends the path of a texture kept as a TGA and not a PNG.
const tgaSuffix = ".tga"

// fileOf is the file a texture's path names.
func fileOf(texture string) string {
	if strings.HasSuffix(texture, tgaSuffix) {
		return texture
	}
	return texture + ".png"
}

// Layer is one rectangle of a texture drawn into a picture.
type Layer struct {
	// Texture is under resource_pack, without the extension unless it is
	// a TGA.
	Texture string `json:"t"`
	// Units is the size the model takes the texture to be, which Src is
	// counted in. Zero is the texture's own size.
	Units [2]int `json:"u"`
	// Src and Dst are x, y, width and height: of the texture, and of the
	// picture in the model's units.
	Src [4]int `json:"s"`
	Dst [4]int `json:"d"`
	// Flip draws it turned left to right, and Turn that many quarter
	// turns clockwise first.
	Flip bool `json:"f,omitempty"`
	Turn int  `json:"r,omitempty"`
	// Skin marks the mob's own hide, as against what is laid over it.
	Skin bool `json:"k,omitempty"`
}

// Recipe is how one picture is made from textures: a mob's face, as the
// boxes of its head seen from the front, or a block drawn from three of
// its sides. It is worked out once from the models and kept, so that a
// picture whose texture could not be fetched is made later from the
// texture alone.
type Recipe struct {
	// W and H are the picture in the model's units, and Main the part of
	// it the head itself fills, which is what has to look like a face.
	W      int     `json:"w,omitempty"`
	H      int     `json:"h,omitempty"`
	Main   [4]int  `json:"m,omitempty"`
	Layers []Layer `json:"l,omitempty"`
	Block  *Block  `json:"b,omitempty"`
	// Sparse marks a picture that is rightly mostly air: an item drawn
	// small in its square.
	Sparse bool `json:"q,omitempty"`
	// Flat is a texture that is the picture as it is.
	Flat string `json:"p,omitempty"`
	// Else is a texture to use whole if the picture cannot be made.
	Else string `json:"e,omitempty"`
}

// base is the texture the picture cannot be made without.
func (r Recipe) base() string {
	switch {
	case r.Flat != "":
		return r.Flat
	case r.Block != nil && len(r.Block.Top) > 0:
		return r.Block.Top[0].Texture
	case len(r.Layers) > 0:
		return r.Layers[0].Texture
	}
	return ""
}

// needs is the textures the picture cannot be made without: every side of
// a block, and of a face the skin alone, since what is laid over a skin
// may be a texture of nothing.
func (r Recipe) needs() []string {
	if r.Block != nil {
		return r.textures()
	}
	return []string{r.base()}
}

// textures is every texture the recipe draws from.
func (r Recipe) textures() []string {
	var out []string
	add := func(layers []Layer) {
		for _, l := range layers {
			if !slices.Contains(out, l.Texture) {
				out = append(out, l.Texture)
			}
		}
	}
	add(r.Layers)
	if r.Block != nil {
		add(r.Block.Top)
		add(r.Block.Left)
		add(r.Block.Right)
	}
	if r.Flat != "" && !slices.Contains(out, r.Flat) {
		out = append(out, r.Flat)
	}
	return out
}

func checkLayers(layers []Layer, w, h int) error {
	if len(layers) == 0 || len(layers) > maxLayers {
		return fmt.Errorf("%d layers", len(layers))
	}
	for _, l := range layers {
		if !modelTexture.MatchString(l.Texture) || l.Turn < 0 || l.Turn > 3 {
			return errors.New("a layer names no texture")
		}
		for _, n := range [...]int{l.Units[0], l.Units[1], l.Src[0], l.Src[1]} {
			if n < 0 || n > maxModelUnit {
				return errors.New("a layer is outside any texture")
			}
		}
		if l.Src[2] < 1 || l.Src[3] < 1 || l.Src[2] > maxModelUnit || l.Src[3] > maxModelUnit {
			return errors.New("a layer is of no size")
		}
		if l.Dst[0] < 0 || l.Dst[1] < 0 || l.Dst[2] < 1 || l.Dst[3] < 1 || l.Dst[0]+l.Dst[2] > w || l.Dst[1]+l.Dst[3] > h {
			return errors.New("a layer is outside its picture")
		}
	}
	return nil
}

// check holds a recipe to the bounds. One read back from the volume has
// nothing to vouch for it but its place there.
func (r Recipe) check() error {
	if r.Else != "" && !modelTexture.MatchString(r.Else) {
		return errors.New("the fallback names no texture")
	}
	switch {
	case r.Flat != "":
		if !modelTexture.MatchString(r.Flat) || r.Block != nil || len(r.Layers) > 0 {
			return errors.New("a flat picture names no texture")
		}
		return nil
	case r.Block != nil:
		if len(r.Layers) > 0 {
			return errors.New("a block with a face's layers")
		}
		return r.Block.check()
	}
	if r.W < minFaceUnits || r.H < minFaceUnits || r.W > maxFaceUnits || r.H > maxFaceUnits {
		return fmt.Errorf("a face of %dx%d units", r.W, r.H)
	}
	m := r.Main
	if m[0] < 0 || m[1] < 0 || m[2] < minFaceUnits || m[3] < minFaceUnits || m[0]+m[2] > r.W || m[1]+m[3] > r.H {
		return errors.New("the head is outside its picture")
	}
	return checkLayers(r.Layers, r.W, r.H)
}

// view says which part of a model is the face and from where it is seen.
type view struct {
	// bones are tried in order; the first the model has is used.
	bones []string
	face  string
	// cube picks the box that is the head by its place in the bone;
	// below zero picks the one with the largest face.
	cube int
	// alone leaves out every other box: the ears, the horns, the snout.
	alone bool
	// skip names bones that are not drawn though they hang off the head.
	skip []string
	// reach is how far from the head, in the model's units, a box may lie
	// and still be drawn. Zero is half the head's own size.
	reach int
	// top keeps only that many units of the head's box from its top, for
	// a head and neck that are one box.
	top int
	// with names bones drawn though they do not hang off the head.
	with []string
	// hides says which bones the mob's render controller leaves undrawn.
	hides func(bone string) bool
}

// The bones a face is looked for in when nothing says otherwise. Most
// models name the head; a few spell it otherwise, and one with no head at
// all is a body whose front is its face.
var defaultBones = []string{"head", "Head", "head1", "mainHead", "bb_main", "body", "Body"}

func (m model) bone(name string) int {
	return slices.IndexFunc(m.bones, func(b bone) bool { return b.Name == name })
}

// under is every bone hung off the one at root, at any remove.
func (m model) under(root int) []int {
	var out []int
	names := []string{m.bones[root].Name}
	for range maxInheritance {
		grew := false
		for i, b := range m.bones {
			if i != root && !slices.Contains(out, i) && slices.Contains(names, b.Parent) {
				out = append(out, i)
				names = append(names, b.Name)
				grew = true
			}
		}
		if !grew {
			break
		}
	}
	return out
}

// seen is the boxes of a model's head as they lie seen from one side: the
// head's own box, which is main, and every box on or about it. A box
// turned out of square is left out, since its face is no longer a
// rectangle of the texture laid flat, and so is a bone the model itself
// never draws. What reaches further from the head than half its size is
// left out too: a pair of antlers would leave the face a speck between
// them.
func (m model) seen(v view) (faces []placed, main placed, err error) {
	face := v.face
	if face == "" {
		face = faceNorth
	}
	// The largest face of a box with some thickness is the head.
	largest := func(b bone) (at, area int) {
		at = -1
		for pass := 0; pass < 2 && at < 0; pass++ {
			for i, c := range b.Cubes {
				if p, ok := place(c, b.Mirror, b.Inflate, face); ok && p.w*p.h > area && (pass == 1 || c.solid()) {
					at, area = i, p.w*p.h
				}
			}
		}
		return at, area
	}
	root := -1
	for _, name := range v.bones {
		if root = m.bone(name); root < 0 {
			continue
		}
		// Some models hang the head's box off an empty bone called head,
		// and that box is looked for among what hangs off it.
		if len(m.bones[root].Cubes) == 0 {
			from, best := root, 0
			for _, i := range m.under(from) {
				if _, area := largest(m.bones[i]); area > best {
					root, best = i, area
				}
			}
		}
		if len(m.bones[root].Cubes) > 0 {
			break
		}
		root = -1
	}
	if root < 0 {
		return nil, placed{}, errors.New("the model has no head")
	}
	head := m.bones[root]
	at := v.cube
	if at < 0 {
		at, _ = largest(head)
	}
	if at < 0 || at >= len(head.Cubes) {
		return nil, placed{}, errors.New("the head has no box with a face")
	}
	// The head's own box may be set at an angle: its face is still the
	// same rectangle of the texture.
	main, ok := place(head.Cubes[at], head.Mirror, head.Inflate, face)
	if !ok {
		return nil, placed{}, errors.New("the head's box has no face on that side")
	}
	if v.top > 0 && main.h > v.top {
		main.h, main.src.H = v.top, v.top
	}
	// A box that names each face's place on the texture may be any size
	// beside it: a ghast is 72 units across and 16 pixels. The view is
	// counted in the head's pixels, so that one of them is one of the
	// picture's.
	rx, ry := float64(main.src.W)/float64(main.w), float64(main.src.H)/float64(main.h)
	fit := func(p placed) placed {
		if rx == 1 && ry == 1 {
			return p
		}
		p.x, p.y = int(math.Round(float64(p.x)*rx)), int(math.Round(float64(p.y)*ry))
		p.w, p.h = max(int(math.Round(float64(p.w)*rx)), 1), max(int(math.Round(float64(p.h)*ry)), 1)
		return p
	}
	main = fit(main)
	if main.w < minFaceUnits || main.h < minFaceUnits {
		return nil, placed{}, fmt.Errorf("the head is %dx%d, too small to be a face", main.w, main.h)
	}
	faces = []placed{main}
	if v.alone {
		return faces, main, nil
	}
	for i, c := range head.Cubes {
		if p, ok := place(c, head.Mirror, head.Inflate, face); ok && i != at && !turned(c.Rotation) && near(fit(p), main, v.reach) {
			faces = append(faces, fit(p))
		}
	}
	others := m.under(root)
	for _, name := range v.with {
		if i := m.bone(name); i >= 0 && i != root && !slices.Contains(others, i) {
			others = append(others, i)
		}
	}
	for _, i := range others {
		b := m.bones[i]
		if b.NeverRender || turned(b.Rotation) || slices.Contains(v.skip, b.Name) || (v.hides != nil && v.hides(b.Name)) {
			continue
		}
		for _, c := range b.Cubes {
			if p, ok := place(c, b.Mirror, b.Inflate, face); ok && !turned(c.Rotation) && near(fit(p), main, v.reach) {
				faces = append(faces, fit(p))
			}
		}
	}
	// The nearest last, so it is drawn over what is behind it.
	sort.SliceStable(faces, func(a, b int) bool { return faces[a].depth < faces[b].depth })
	return faces, main, nil
}

// near reports whether a box's face lies within reach of the head's.
func near(p, main placed, reach int) bool {
	reachX, reachY := reach, reach
	if reach == 0 {
		reachX, reachY = main.w/2, main.h/2
	}
	return p.x >= main.x-reachX && p.y >= main.y-reachY && p.x+p.w <= main.x+main.w+reachX && p.y+p.h <= main.y+main.h+reachY
}

// drawn is one model and the textures laid over it, in order, with the
// bones its controller leaves undrawn.
type drawn struct {
	model    model
	textures []string
	hides    func(bone string) bool
}

// faceRecipe is the recipe for a face seen in the first of the models that
// has one, with every later one laid over it where it lies about the same
// head: a stray's clothes over its bones, a villager's profession over its
// skin, a slime's jelly round its core.
func faceRecipe(parts []drawn, v view) (Recipe, error) {
	type layered struct {
		placed
		texture string
		units   [2]int
		skin    bool
	}
	var (
		all   []layered
		main  placed
		found bool
		first = errors.New("nothing is drawn")
	)
	for _, part := range parts {
		v.hides = part.hides
		faces, head, err := part.model.seen(v)
		if err != nil {
			if !found {
				first = err
			}
			continue
		}
		skin := !found
		if !found {
			main, found = head, true
		}
		for _, texture := range part.textures {
			if !modelTexture.MatchString(texture) {
				continue
			}
			for _, p := range faces {
				if near(p, main, v.reach) {
					all = append(all, layered{p, texture, [2]int{part.model.texW, part.model.texH}, skin})
				}
			}
		}
	}
	if !found || len(all) == 0 {
		return Recipe{}, first
	}
	if len(all) > maxLayers {
		return Recipe{}, fmt.Errorf("the face is %d layers, over the limit of %d", len(all), maxLayers)
	}
	// The nearest last, whichever model it is of: the wool of a sheep's
	// head is a box set back from its face, not a layer over it.
	sort.SliceStable(all, func(a, b int) bool { return all[a].depth < all[b].depth })
	left, top, right, bottom := main.x, main.y, main.x+main.w, main.y+main.h
	for _, p := range all {
		left, top = min(left, p.x), min(top, p.y)
		right, bottom = max(right, p.x+p.w), max(bottom, p.y+p.h)
	}
	out := Recipe{W: right - left, H: bottom - top, Main: [4]int{main.x - left, main.y - top, main.w, main.h}}
	// A face is shown in a box 16 pixels a side. One that its ears and
	// horns make larger than that is drawn as the head alone, so that it
	// is not made smaller than the rest to fit.
	if max(out.W, out.H) > faceBox && !v.alone && v.reach == 0 {
		v.alone = true
		return faceRecipe(parts, v)
	}
	// And one that is a sliver, or a strip, is no face at any size.
	if short, long := min(out.W, out.H), max(out.W, out.H); short < minFaceShort || long*2 > short*5 {
		return Recipe{}, fmt.Errorf("the face is %dx%d, a strip that would be a speck at a marker's size", out.W, out.H)
	}
	for _, p := range all {
		out.Layers = append(out.Layers, Layer{
			Texture: p.texture, Units: p.units, Skin: p.skin, Flip: p.src.Flip,
			Src: [4]int{p.src.X, p.src.Y, p.src.W, p.src.H}, Dst: [4]int{p.x - left, p.y - top, p.w, p.h},
		})
	}
	return out, out.check()
}

// appearance is the part of a mob's client definition that says what it
// is drawn with.
type appearance struct {
	Textures    map[string]string `json:"textures"`
	Geometry    map[string]string `json:"geometry"`
	Controllers []json.RawMessage `json:"render_controllers"`
}

// controllers is the render controllers a grown mob of the default variant
// is drawn by, in order. One listed with a condition is in only if the
// condition holds of such a mob.
func (a appearance) controllers() []string {
	var out []string
	for _, raw := range a.Controllers {
		var name string
		var gated map[string]string
		switch {
		case json.Unmarshal(raw, &name) == nil:
			out = append(out, name)
		case json.Unmarshal(raw, &gated) == nil:
			for id, condition := range gated {
				if holds(condition) {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// library is every model and controller the samples hold.
type library struct {
	models      map[string]model
	controllers map[string]controller
	// tga is the textures, by path without an extension, that the samples
	// keep as a TGA and not a PNG.
	tga map[string]bool
}

// parts works out what a mob is drawn with: for each of its render
// controllers, the model and the textures that controller would choose
// for a grown mob of the default variant. A controller that cannot be
// read, or that chooses nothing the definition holds, falls back to what
// the definition calls default; one with neither is left out.
func (lib library) parts(a appearance, o override) ([]drawn, error) {
	texture := func(key string) string {
		if swapped, ok := o.swap[key]; ok {
			key = swapped
		}
		path := a.Textures[key]
		if lib.tga[path] {
			path += tgaSuffix
		}
		return path
	}
	model := func(key string) (model, bool) {
		id, ok := a.Geometry[key]
		if !ok {
			return model{}, false
		}
		m, err := resolved(lib.models, id)
		return m, err == nil
	}
	if len(o.textures) > 0 {
		var out []drawn
		for _, named := range append([]overlay{{o.geometry, o.textures}}, o.over...) {
			key := named.geometry
			if key == "" {
				key = "default"
			}
			m, ok := model(key)
			if !ok {
				return nil, fmt.Errorf("the definition has no model called %s", key)
			}
			part := drawn{model: m}
			for _, key := range named.textures {
				if path := texture(key); path != "" {
					part.textures = append(part.textures, path)
				}
			}
			if len(part.textures) == 0 {
				return nil, errors.New("the definition has none of the textures named for it")
			}
			out = append(out, part)
		}
		return out, nil
	}
	var out []drawn
	for _, id := range a.controllers() {
		if slices.Contains(o.drop, id) {
			continue
		}
		c, known := lib.controllers[id]
		key, ok := c.pick(c.Geometry)
		if o.geometry != "" {
			key, ok = o.geometry, true
		}
		if !known || !ok {
			key = "default"
		}
		m, ok := model(key)
		if !ok {
			if m, ok = model("default"); !ok {
				continue
			}
		}
		part := drawn{model: m}
		if known {
			part.hides = c.hides
		}
		for _, expression := range c.Textures {
			if key, ok := c.pick(expression); ok {
				if path := texture(key); path != "" {
					part.textures = append(part.textures, path)
				}
			}
		}
		if len(part.textures) == 0 {
			if path := texture("default"); path != "" && len(out) == 0 {
				part.textures = []string{path}
			}
		}
		if len(part.textures) > 0 {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no render controller chooses a model and a texture the definition holds")
	}
	return out, nil
}

// face is the recipe for a mob's face.
func (lib library) face(a appearance, o override) (Recipe, error) {
	if o.rect != nil && len(o.textures) > 0 {
		path := a.Textures[o.textures[0]]
		r := *o.rect
		out := Recipe{W: r[2], H: r[3], Main: [4]int{0, 0, r[2], r[3]}, Layers: []Layer{{Texture: path, Units: o.units, Src: r, Dst: [4]int{0, 0, r[2], r[3]}}}}
		return out, out.check()
	}
	parts, err := lib.parts(a, o)
	if err != nil {
		return Recipe{}, err
	}
	v := view{bones: defaultBones, cube: -1, face: o.face, alone: o.alone, skip: o.skip, reach: o.reach, top: o.top, with: o.with}
	if o.bone != "" {
		v.bones = []string{o.bone}
	}
	if o.cube != nil {
		v.cube = *o.cube
	}
	return faceRecipe(parts, v)
}

// decodeTexture decodes a model's texture once its header has shown it to
// be within bounds.
func decodeTexture(texture string, raw []byte) (*image.NRGBA, error) {
	if strings.HasSuffix(texture, tgaSuffix) {
		return decodeTGA(raw, maxModelTextureSide)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "png" {
		return nil, errNotPNG
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxModelTextureSide || cfg.Height > maxModelTextureSide {
		return nil, fmt.Errorf("texture is %dx%d, outside 1 to %d a side", cfg.Width, cfg.Height, maxModelTextureSide)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errNotPNG
	}
	out := image.NewNRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	b := img.Bounds()
	for y := range cfg.Height {
		for x := range cfg.Width {
			out.SetNRGBA(x, y, color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA))
		}
	}
	return out, nil
}

// scaleOf is how many pixels a texture has to each of the units a layer
// counts it in. The layer's rectangle must lie within the texture, and the
// texture must be a whole number of times the size its model says, the
// same both ways: anything else is a texture that is not the one the model
// was made for.
func scaleOf(l Layer, tex *image.NRGBA) (int, error) {
	w, h := tex.Rect.Dx(), tex.Rect.Dy()
	uw, uh := l.Units[0], l.Units[1]
	if uw == 0 || uh == 0 {
		uw, uh = w, h
	}
	if w%uw != 0 || h%uh != 0 || w/uw != h/uh || w/uw > maxTextureScale {
		return 0, fmt.Errorf("%s is %dx%d, not a whole multiple of the %dx%d its model takes it to be", l.Texture, w, h, uw, uh)
	}
	if l.Src[0]+l.Src[2] > uw || l.Src[1]+l.Src[3] > uh {
		return 0, fmt.Errorf("a rectangle at %d,%d of %dx%d is outside %s, which is %dx%d", l.Src[0], l.Src[1], l.Src[2], l.Src[3], l.Texture, uw, uh)
	}
	return w / uw, nil
}

// compose draws layers into a picture of w by h units at scale pixels to
// the unit, each over the last. Every pixel is taken from the one nearest
// it in the texture and none is blended, so a texture's pixels stay whole.
// base is the texture the picture cannot do without: a layer of any other
// that is not held, or does not fit its texture, is left off, as a mob
// with no markings has a texture of nothing where its markings would be.
// The fourth channel of a TGA is whatever its material makes of it, which
// for a mob's own skin is never how see-through it is, so the skin of such
// a texture is drawn whole; solid draws any skin so.
func compose(layers []Layer, w, h, scale int, textures map[string]*image.NRGBA, base string, solid bool) (*image.NRGBA, error) {
	tga := strings.HasSuffix(base, tgaSuffix)
	out := image.NewNRGBA(image.Rect(0, 0, w*scale, h*scale))
	for _, l := range layers {
		tex := textures[l.Texture]
		if tex == nil {
			if l.Texture != base {
				continue
			}
			return nil, fmt.Errorf("texture %s is not held", l.Texture)
		}
		k, err := scaleOf(l, tex)
		if err != nil {
			if l.Texture != base {
				continue
			}
			return nil, err
		}
		sw, sh := l.Src[2]*k, l.Src[3]*k
		dw, dh := l.Dst[2]*scale, l.Dst[3]*scale
		for y := range dh {
			for x := range dw {
				// The place in the layer as it lies in the picture, then
				// flipped and turned back to where it is in the texture.
				u, v := (float64(x)+0.5)/float64(dw), (float64(y)+0.5)/float64(dh)
				if l.Flip {
					u = 1 - u
				}
				for range l.Turn {
					u, v = v, 1-u
				}
				c := tex.NRGBAAt(l.Src[0]*k+min(int(u*float64(sw)), sw-1), l.Src[1]*k+min(int(v*float64(sh)), sh-1))
				if l.Skin && l.Texture == base && (solid || tga) {
					c.A = 255
				}
				if c.A == 0 {
					continue
				}
				under := out.NRGBAAt(l.Dst[0]*scale+x, l.Dst[1]*scale+y)
				out.SetNRGBA(l.Dst[0]*scale+x, l.Dst[1]*scale+y, over(c, under))
			}
		}
	}
	return out, nil
}

func over(top, under color.NRGBA) color.NRGBA {
	if top.A == 255 || under.A == 0 {
		return top
	}
	a := int(top.A)
	rest := int(under.A) * (255 - a) / 255
	all := a + rest
	mix := func(t, u uint8) uint8 { return uint8((int(t)*a + int(u)*rest) / all) }
	return color.NRGBA{mix(top.R, under.R), mix(top.G, under.G), mix(top.B, under.B), uint8(all)}
}

// errImplausible marks a picture that was made as the recipe said and does
// not look like anything: the model or the texture is not laid out the way
// this reads them.
var errImplausible = errors.New("the picture is blank or all of one colour")

// plausible reports whether a rectangle of a picture could be a face: most
// of it is there, and it is not all one colour. A crop of the wrong part
// of a texture is usually empty or flat.
func plausible(img *image.NRGBA, r image.Rectangle) bool {
	return covered(img, r, 2)
}

// covered is plausible with at least one pixel in every so many there.
func covered(img *image.NRGBA, r image.Rectangle, every int) bool {
	var solid, total int
	lo, hi := 255, 0
	colours := map[color.NRGBA]struct{}{}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			total++
			c := img.NRGBAAt(x, y)
			if c.A < 128 {
				continue
			}
			solid++
			c.A = 255
			colours[c] = struct{}{}
			luma := (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
			lo, hi = min(lo, luma), max(hi, luma)
		}
	}
	return total > 0 && solid*every >= total && len(colours) >= 3 && hi-lo >= 12
}

// render makes the picture a recipe describes from the textures it names.
// A face comes out square, with the head in the middle of whatever room
// its width or height leaves, at one pixel to each pixel of its texture:
// the page enlarges it by a whole number to suit the screen, which a size
// fixed here could not.
func (r Recipe) render(textures map[string]*image.NRGBA) (*image.NRGBA, error) {
	if err := r.check(); err != nil {
		return nil, err
	}
	switch {
	case r.Flat != "":
		tex := textures[r.Flat]
		if tex == nil {
			return nil, fmt.Errorf("texture %s is not held", r.Flat)
		}
		if tex.Rect.Dx() > maxIconSide || tex.Rect.Dy() > maxIconSide {
			return nil, fmt.Errorf("picture is %dx%d, over %d a side", tex.Rect.Dx(), tex.Rect.Dy(), maxIconSide)
		}
		return tex, nil
	case r.Block != nil:
		return r.Block.render(textures)
	}
	base := r.Layers[0].Texture
	first := textures[base]
	if first == nil {
		return nil, fmt.Errorf("texture %s is not held", base)
	}
	scale, err := scaleOf(r.Layers[0], first)
	if err != nil {
		return nil, err
	}
	main := image.Rect(r.Main[0]*scale, r.Main[1]*scale, (r.Main[0]+r.Main[2])*scale, (r.Main[1]+r.Main[3])*scale)
	img, err := compose(r.Layers, r.W, r.H, scale, textures, base, false)
	if err != nil {
		return nil, err
	}
	if r.Sparse {
		if !covered(img, main, 8) {
			return nil, errImplausible
		}
	} else if !plausible(img, main) {
		// A texture that says it is wholly see-through where the head is,
		// is drawn by a material that pays the channel no heed.
		if img, err = compose(r.Layers, r.W, r.H, scale, textures, base, true); err != nil {
			return nil, err
		}
		if !plausible(img, main) {
			return nil, errImplausible
		}
	}
	side := max(img.Rect.Dx(), img.Rect.Dy())
	square := image.NewNRGBA(image.Rect(0, 0, side, side))
	dx, dy := (side-img.Rect.Dx())/2, (side-img.Rect.Dy())/2
	for y := range img.Rect.Dy() {
		copy(square.Pix[square.PixOffset(dx, dy+y):], img.Pix[img.PixOffset(0, y):img.PixOffset(0, y)+img.Rect.Dx()*4])
	}
	return square, nil
}

func encode(img *image.NRGBA) ([]byte, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// faceKey is the key of a mob's face among the pictures, and of one of
// its variants.
func faceKey(kind string) string { return "face/" + kind }

// derived reports whether a picture's key is one made here from a recipe
// and not a texture served as it came.
func derived(key string) bool {
	group, _, _ := strings.Cut(key, "/")
	return group == "face" || group == "block" || group == "structure" || group == "villager"
}
