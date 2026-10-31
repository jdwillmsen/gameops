package atlas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// entityTableVersion invalidates cached entity sheets when the layout of the
// table changes.
const entityTableVersion = 3

// Bounds on what the entity build reads and produces.
const (
	maxEntityDefs     = 1000
	maxEntityDefBytes = 128 << 10
	maxEntityTexSide  = 2048
	maxEntitySheet    = 4096
)

// maxEntityTableBytes caps the table every viewer downloads; the real one is
// a few hundred KB. A variable for tests.
var maxEntityTableBytes = 8 << 20

// maxEntityPixels bounds the decoded textures held at once, before they are
// packed; the sheet's own limit is 4096 squared. A variable for tests.
var maxEntityPixels = maxEntitySheet * maxEntitySheet

// EntityTable is what the page loads next to the entity sheet. Rects are in
// sheet pixels, so Width and Height turn them into texture coordinates.
type EntityTable struct {
	Version int `json:"version"`
	Width   int `json:"width"`
	Height  int `json:"height"`
	// Models is keyed by full entity identifier, e.g. "minecraft:cow".
	Models map[string]EntityModel `json:"models"`
	// Armor maps a worn-armour texture ("iron_1" for helmet, chestplate and
	// boots, "iron_2" for leggings) to its rect on the sheet, as u0, v0, u1, v1
	// in sheet pixels. The pack lays every one out like a 64x32 skin.
	Armor map[string][4]float32 `json:"armor,omitempty"`
	// Trims maps an armour trim pattern ("coast") and its leggings variant
	// ("coast_leggings") to its rect on the sheet, a 64x32 grey picture whose
	// shades pick a colour of the trim material's palette.
	Trims map[string][4]float32 `json:"trims,omitempty"`
	// TrimPalettes maps a trim material ("quartz", "iron_darker") to its eight
	// colours, lightest first.
	TrimPalettes map[string][][3]uint8 `json:"trim_palettes,omitempty"`
}

// ShieldModel is the key of the shield in Models. A shield is held, not an
// entity, so it is named so as never to collide with an entity identifier.
const ShieldModel = "attachable:shield"

// EntityModel is how to draw one kind of entity. Geometry is in the pack's
// model units (1/16 block), facing north (-Z), y up.
type EntityModel struct {
	// Height is the model's top in blocks, for placing a name label.
	Height float32 `json:"h"`
	// Scale is the adult's size factor; zero means one.
	Scale float32 `json:"s,omitempty"`
	// Default is drawn unless a variant applies. Baby replaces it for a baby
	// that has its own model; one that has none is the default at half size.
	Default []Layer `json:"d"`
	Baby    []Layer `json:"b,omitempty"`
	Sheared []Layer `json:"sh,omitempty"`
	// Variants holds one layer set per value of the entity's variant, where
	// the pack picks the texture (and hides parts) by it; Default is the first.
	Variants [][]Layer `json:"v,omitempty"`
	// Anim is the pack's animations, which the page evaluates per frame.
	Anim *AnimSet `json:"an,omitempty"`
}

// layerSets lists every set of layers the model can draw.
func (m EntityModel) layerSets() [][]Layer {
	return append([][]Layer{m.Default, m.Baby, m.Sheared}, m.Variants...)
}

// Layer is one geometry in its texture. Drawing several in order is how a
// sheep's wool goes over its skin.
type Layer struct {
	Bones []Bone `json:"bones"`
	// Tint marks a layer the entity's dye colour multiplies.
	Tint bool `json:"tint,omitempty"`
	// Glass marks a layer drawn see-through, like a slime's outer shell.
	Glass bool `json:"glass,omitempty"`

	tex string
}

// Bone is a node of the skeleton. Pivot and the cubes are in absolute model
// coordinates; Rot and Bind are the pack's degrees, in its own convention.
type Bone struct {
	Name   string     `json:"n"`
	Parent string     `json:"p,omitempty"`
	Pivot  [3]float32 `json:"pv"`
	Rot    []float32  `json:"r,omitempty"`
	// Bind turns only this bone's own cubes about its pivot.
	Bind  []float32 `json:"bp,omitempty"`
	Cubes []Cube    `json:"c,omitempty"`
}

// Cube is a box. UV holds the sheet rect of each face in the order north,
// south, east, west, up, down as u0, v0, u1, v1; u0 > u1 draws the face
// turned left to right, and an all-zero rect is a face that is not drawn.
type Cube struct {
	Origin  [3]float32    `json:"o"`
	Size    [3]float32    `json:"sz"`
	Inflate float32       `json:"i,omitempty"`
	Rot     []float32     `json:"r,omitempty"`
	Pivot   []float32     `json:"pv,omitempty"`
	UV      [6][4]float32 `json:"uv"`
}

// entityDef is the part of a client entity definition the build reads.
type entityDef struct {
	id        string
	engine    []int
	textures  orderedStrings
	geometry  orderedStrings
	adultSize float32

	// The rest is read only for a pack's own entities, which are animated.
	animations  orderedStrings
	animate     []animRun
	controllers []string
}

// orderedStrings keeps a JSON object's string values in file order, because
// "the first texture" is meaningful where there is no "default".
type orderedStrings struct {
	keys []string
	vals map[string]string
}

func (o orderedStrings) get(keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := o.vals[k]; ok && v != "" {
			return v, true
		}
	}
	return "", false
}

func (o orderedStrings) first() (string, bool) {
	if len(o.keys) == 0 {
		return "", false
	}
	return o.vals[o.keys[0]], true
}

func parseOrdered(raw json.RawMessage) orderedStrings {
	out := orderedStrings{vals: map[string]string{}}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return out
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := kt.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return out
		}
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			if _, dup := out.vals[key]; !dup {
				out.keys = append(out.keys, key)
			}
			out.vals[key] = s
		}
	}
	return out
}

var (
	plainScale = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*$`)
	// An adult's scale is the else branch of "query.is_baby ? a : b", where b
	// is a number or the reciprocal of one.
	adultScale = regexp.MustCompile(`query\.is_baby\s*\?[^:]*:\s*(?:\(\s*1\s*/\s*([0-9]*\.?[0-9]+)\s*\)|([0-9]*\.?[0-9]+))\s*$`)
)

// scaleOf reads the definition's scale script for an adult. Scripts that
// animate the scale are not followed.
func scaleOf(script string) float32 {
	if m := plainScale.FindStringSubmatch(script); m != nil {
		return clampScale(m[1], false)
	}
	if m := adultScale.FindStringSubmatch(script); m != nil {
		if m[1] != "" {
			return clampScale(m[1], true)
		}
		return clampScale(m[2], false)
	}
	return 1
}

func clampScale(s string, reciprocal bool) float32 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 1
	}
	if reciprocal {
		v = 1 / v
	}
	if v < 0.1 || v > 10 {
		return 1
	}
	return float32(v)
}

func engineVersion(v json.RawMessage) []int {
	var s string
	if json.Unmarshal(v, &s) == nil {
		var out []int
		for _, p := range strings.Split(s, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil
			}
			out = append(out, n)
		}
		return out
	}
	var list []int
	_ = json.Unmarshal(v, &list)
	return list
}

func newerVersion(a, b []int) bool {
	for i := 0; i < max(len(a), len(b)); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parseEntityDef(data []byte) (entityDef, bool) {
	return parseEntityDefIn(data, func(id string) bool { return strings.HasPrefix(id, "minecraft:") })
}

// parseEntityDefIn reads a definition whose identifier allow accepts.
func parseEntityDefIn(data []byte, allow func(id string) bool) (entityDef, bool) {
	if len(data) > maxEntityDefBytes {
		return entityDef{}, false
	}
	data = stripJSONComments(data)
	if !jsonWithin(data, maxJSONDepth) {
		return entityDef{}, false
	}
	var doc struct {
		Client struct {
			Description struct {
				Identifier string          `json:"identifier"`
				Engine     json.RawMessage `json:"min_engine_version"`
				Textures   json.RawMessage `json:"textures"`
				Geometry   json.RawMessage `json:"geometry"`
				Animations json.RawMessage `json:"animations"`
				Scripts    struct {
					Scale   string            `json:"scale"`
					Animate []json.RawMessage `json:"animate"`
				} `json:"scripts"`
				Controllers []json.RawMessage `json:"render_controllers"`
			} `json:"description"`
		} `json:"minecraft:client_entity"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return entityDef{}, false
	}
	d := doc.Client.Description
	if !allow(d.Identifier) || len(d.Identifier) > 64 {
		return entityDef{}, false
	}
	def := entityDef{id: d.Identifier, engine: engineVersion(d.Engine), textures: parseOrdered(d.Textures), geometry: parseOrdered(d.Geometry), adultSize: scaleOf(d.Scripts.Scale),
		animations: parseOrdered(d.Animations), animate: parseAnimRuns(d.Scripts.Animate), controllers: parseNamed(d.Controllers)}
	return def, len(def.textures.keys) > 0 && len(def.geometry.keys) > 0
}

// entityTextureRefs lists every texture path (without extension) the
// definitions under packDir would draw with, so the download can fetch those
// and no others. A definition's other textures are variants the page does
// not draw.
func entityTextureRefs(packDir string) []string {
	defs, _ := readEntityDefs(packDir)
	seen := map[string]bool{}
	var out []string
	for _, id := range sortedKeys(defs) {
		for _, p := range defs[id].chosenTextures() {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// defaultTexture and babyTexture are the paths the page draws a definition
// with. A definition with no "default" falls back to its first texture, which
// for variant-per-key definitions is the first variant.
func (d entityDef) defaultTexture() (string, bool) {
	if p, ok := d.textures.get("default"); ok {
		return p, true
	}
	return d.textures.first()
}

func (d entityDef) babyTexture() (string, bool) {
	if p, ok := d.textures.get("baby", "baby_default"); ok {
		return p, true
	}
	for _, k := range d.textures.keys {
		if strings.HasPrefix(k, "baby") {
			return d.textures.vals[k], true
		}
	}
	return d.defaultTexture()
}

func (d entityDef) chosenTextures() []string {
	var out []string
	if p, ok := d.defaultTexture(); ok {
		out = append(out, p)
	}
	if _, ok := d.geometry.get("baby"); ok {
		if p, ok := d.babyTexture(); ok {
			out = append(out, p)
		}
	}
	return out
}

// readEntityDefs reads the definitions in packDir/entity and keeps, for an
// identifier defined more than once, the one with the newest engine version:
// the pack still carries the legacy files beside the current ones.
func readEntityDefs(packDir string) (map[string]entityDef, error) {
	return readEntityDefsWith(packDir, parseEntityDef)
}

func readEntityDefsWith(packDir string, parse func([]byte) (entityDef, bool)) (map[string]entityDef, error) {
	entries, err := os.ReadDir(filepath.Join(packDir, "entity"))
	if err != nil {
		return nil, fmt.Errorf("atlas: %w", err)
	}
	defs := map[string]entityDef{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || len(defs) >= maxEntityDefs {
			continue
		}
		data, err := os.ReadFile(filepath.Join(packDir, "entity", e.Name()))
		if err != nil {
			continue
		}
		def, ok := parse(data)
		if !ok {
			continue
		}
		if old, held := defs[def.id]; held && !newerVersion(def.engine, old.engine) {
			continue
		}
		defs[def.id] = def
	}
	if len(defs) == 0 {
		return nil, errors.New("atlas: the pack has no entity definitions")
	}
	return defs, nil
}

// readGeometries parses every model file under models/entity and the legacy
// models/mobs.json into one table by identifier. The first file in name
// order wins a repeated identifier.
func readGeometries(packDir string) map[string]geoModel {
	models := map[string]geoModel{}
	add := func(p string) {
		data, err := os.ReadFile(p)
		if err != nil {
			return
		}
		list, err := parseModels(data)
		if err != nil {
			return
		}
		for _, m := range list {
			if _, held := models[m.id]; !held && len(models) < maxModels {
				models[m.id] = m
			}
		}
	}
	root := filepath.Join(packDir, "models", "entity")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			add(p)
		}
		return nil
	})
	add(filepath.Join(packDir, "models", "mobs.json"))
	return models
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hiddenBones are parts the game shows only under conditions the camera
// does not know: harness and saddle bags on a horse, a llama's chests, a
// sleeping fox's second head.
var hiddenBones = map[string]bool{
	"bagl": true, "bagr": true, "bridle": true, "reinsl": true, "reinsr": true, "reins": true,
	"saddle": true, "chest1": true, "chest2": true, "head_sleeping": true,
}

// Compilation turns a resolved model into bones whose UV rects are fractions
// of the model's texture; packing later maps them into the shared sheet.
func compileBones(m geoModel, texW, texH int) []Bone {
	bones := make([]Bone, 0, len(m.bones))
	for _, gb := range m.bones {
		b := Bone{Name: gb.Name, Parent: gb.Parent}
		if p, ok := vec3(gb.Pivot); ok {
			b.Pivot = p
		}
		if r, _ := vec3(gb.Rotation); r != ([3]float32{}) {
			b.Rot = r[:]
		}
		if r, _ := vec3(gb.BindPose); r != ([3]float32{}) {
			b.Bind = r[:]
		}
		if !gb.NeverRender && !hiddenBones[strings.ToLower(gb.Name)] {
			for _, gc := range gb.Cubes {
				b.Cubes = append(b.Cubes, compileCube(gc, gb, texW, texH))
			}
		}
		bones = append(bones, b)
	}
	dropBadParents(bones)
	layBodies(bones)
	return bones
}

// dropBadParents detaches a bone whose parent is missing, is itself or leads
// back to it, so the page's bone tree always reaches the root.
func dropBadParents(bones []Bone) {
	parent := make(map[string]string, len(bones))
	for _, b := range bones {
		parent[b.Name] = b.Parent
	}
	for i := range bones {
		p := bones[i].Parent
		if p == "" {
			continue
		}
		if _, ok := parent[p]; !ok {
			bones[i].Parent = ""
			continue
		}
		for at, hops := p, 0; at != ""; at, hops = parent[at], hops+1 {
			if at == bones[i].Name || hops > len(bones) {
				bones[i].Parent = ""
				break
			}
		}
	}
}

// layBodies turns the body of a four-legged mob onto its side. Many of the
// pack's quadruped models draw the torso upright and leave lying it down to
// a setup animation this build does not read. A mob counts as one when its
// legs are spread further front to back than side to side and its head is
// out in front of the body, which keeps a creeper's upright body as it is; a
// body that already carries a rotation is left alone.
func layBodies(bones []Bone) {
	var legs [][3]float32
	var body, head *Bone
	for i := range bones {
		n := strings.ToLower(bones[i].Name)
		if strings.HasPrefix(n, "leg") || strings.Contains(n, "backleg") || strings.Contains(n, "frontleg") {
			legs = append(legs, bones[i].Pivot)
		}
		if n == "body" {
			body = &bones[i]
		}
		if n == "head" {
			head = &bones[i]
		}
	}
	if len(legs) < 4 || body == nil || head == nil || head.Pivot[2] > body.Pivot[2]-2 || body.Rot != nil || body.Bind != nil || len(body.Cubes) == 0 {
		return
	}
	lo, hi := legs[0], legs[0]
	for _, l := range legs {
		for i := range 3 {
			lo[i], hi[i] = min(lo[i], l[i]), max(hi[i], l[i])
		}
	}
	if hi[2]-lo[2] < 1.5*(hi[0]-lo[0]) {
		return
	}
	for _, c := range body.Cubes {
		if c.Rot != nil || c.Size[1] <= c.Size[2]*1.2 {
			return
		}
	}
	for i := range bones {
		if n := strings.ToLower(bones[i].Name); (n == "body" || n == "upperbody") && bones[i].Rot == nil {
			// Only the torso's own boxes lie down: the head and legs hang off it.
			bones[i].Bind = []float32{90, 0, 0}
		}
	}
}

func compileCube(gc geoCube, gb geoBone, texW, texH int) Cube {
	c := Cube{Inflate: float32(gb.Inflate)}
	for i := range 3 {
		c.Origin[i], c.Size[i] = float32(gc.Origin[i]), float32(gc.Size[i])
	}
	if gc.Inflate != nil {
		c.Inflate = float32(*gc.Inflate)
	}
	if r, _ := vec3(gc.Rotation); r != ([3]float32{}) {
		c.Rot = r[:]
	}
	if p, _ := vec3(gc.Pivot); p != ([3]float32{}) {
		c.Pivot = p[:]
	} else if c.Rot != nil {
		// A turned box that names no pivot turns about its own centre.
		c.Pivot = []float32{c.Origin[0] + c.Size[0]/2, c.Origin[1] + c.Size[1]/2, c.Origin[2] + c.Size[2]/2}
	}
	for f := range faceCount {
		r, ok := faceRect(gc, gb.Mirror, f)
		if !ok {
			continue
		}
		u0, v0 := r.X/float64(texW), r.Y/float64(texH)
		u1, v1 := (r.X+r.W)/float64(texW), (r.Y+r.H)/float64(texH)
		if r.Flip {
			u0, u1 = u1, u0
		}
		c.UV[f] = [4]float32{float32(u0), float32(v0), float32(u1), float32(v1)}
	}
	return c
}

// entityBuilder gathers the textures the compiled layers use.
type entityBuilder struct {
	b      *builder
	models map[string]geoModel
	// imgs holds each texture asked for by path; nil means unreadable.
	imgs   map[string]*image.NRGBA
	pixels int
}

func (eb *entityBuilder) texture(p string) *image.NRGBA {
	if img, ok := eb.imgs[p]; ok {
		return img
	}
	var img *image.NRGBA
	if path.Clean(p) == p && strings.HasPrefix(p, "textures/") {
		img = eb.b.loadImage(p)
	}
	if img != nil {
		if bd := img.Bounds(); bd.Dx() > maxEntityTexSide || bd.Dy() > maxEntityTexSide {
			img = nil
		} else if eb.pixels += bd.Dx() * bd.Dy(); eb.pixels > maxEntityPixels {
			// Later textures are skipped, so their entities fall back to boxes.
			img = nil
		} else {
			solidifyMasks(img)
		}
	}
	eb.imgs[p] = img
	return img
}

// armorDir holds the textures worn armour is drawn with.
const armorDir = "textures/models/armor"

// armorTextures adds each worn-armour texture the pack ships to used and
// returns their paths by name. The baby variants and elytra do not follow the
// _1/_2 layout the camera draws and are left out.
func (eb *entityBuilder) armorTextures(used map[string]*image.NRGBA) map[string]string {
	entries, err := os.ReadDir(filepath.Join(eb.b.dir, filepath.FromSlash(armorDir)))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".png")
		if !ok || !(strings.HasSuffix(key, "_1") || strings.HasSuffix(key, "_2")) {
			continue
		}
		p := armorDir + "/" + key
		if img := eb.texture(p); img != nil {
			used[p] = img
			out[key] = p
		}
	}
	return out
}

// trimDir holds the armour trim pictures and, below it, the palettes.
const trimDir = "textures/trims"

// maxTrims bounds the trim pictures read; the game has about forty.
const maxTrims = 128

// trimTextures adds each trim picture to used and returns their paths by key.
func (eb *entityBuilder) trimTextures(used map[string]*image.NRGBA) map[string]string {
	entries, err := os.ReadDir(filepath.Join(eb.b.dir, filepath.FromSlash(trimDir)))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".png")
		if !ok || e.IsDir() || len(out) >= maxTrims {
			continue
		}
		p := trimDir + "/" + key
		if img := eb.texture(p); img != nil && img.Bounds().Dx() == 64 && img.Bounds().Dy() == 32 {
			used[p] = img
			out[key] = p
		}
	}
	return out
}

// trimPalettes reads the eight-colour strips the trim materials colour the
// pictures with.
func (eb *entityBuilder) trimPalettes() map[string][][3]uint8 {
	dir := filepath.Join(eb.b.dir, filepath.FromSlash(trimDir), "color_palettes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string][][3]uint8{}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".png")
		if !ok || e.IsDir() || len(out) >= maxTrims {
			continue
		}
		img := eb.b.loadImage(trimDir + "/color_palettes/" + key)
		if img == nil || img.Bounds().Dx() != 8 || img.Bounds().Dy() != 1 {
			continue
		}
		pal := make([][3]uint8, 8)
		for x := range pal {
			c := img.NRGBAAt(img.Bounds().Min.X+x, img.Bounds().Min.Y)
			pal[x] = [3]uint8{c.R, c.G, c.B}
		}
		out[key] = pal
	}
	return out
}

// shieldTexture is the shield's picture in the pack.
const shieldTexture = "textures/entity/shield"

// solidifyMasks makes pixels with an alpha of 1 to 15 opaque. The pack uses
// such nearly invisible alphas as a mask for effects the camera does not draw
// (a sheep's skin is painted at an alpha of 3), and a viewer that took them
// at face value would see through the animal.
func solidifyMasks(img *image.NRGBA) {
	for i := 3; i < len(img.Pix); i += 4 {
		if a := img.Pix[i]; a > 0 && a < 16 {
			img.Pix[i] = 255
		}
	}
}

// layer compiles geometry id drawn with texture texPath, or false if either
// is missing. When ownBones is set, only the bones the model itself declares
// are kept, not those it inherits.
func (eb *entityBuilder) layer(id, texPath string, ownBones bool) (Layer, bool) {
	img := eb.texture(texPath)
	if img == nil {
		return Layer{}, false
	}
	m, err := resolveModel(eb.models, id, 0)
	if err != nil {
		return Layer{}, false
	}
	if ownBones {
		m.bones = eb.models[id].bones
	}
	texW, texH := m.texW, m.texH
	if texW == 0 || texH == 0 {
		texW, texH = img.Bounds().Dx(), img.Bounds().Dy()
	}
	bones := compileBones(m, texW, texH)
	for _, b := range bones {
		if len(b.Cubes) > 0 {
			return Layer{Bones: bones, tex: texPath}, true
		}
	}
	return Layer{}, false
}

func (eb *entityBuilder) model(def entityDef) (EntityModel, bool) {
	texPath, ok := def.defaultTexture()
	if !ok {
		return EntityModel{}, false
	}
	defID, ok := def.geometry.get("default")
	if !ok {
		defID, _ = def.geometry.first()
	}
	var out EntityModel
	if def.adultSize != 1 {
		out.Scale = def.adultSize
	}
	shearedID, hasSheared := def.geometry.get("sheared")
	// A sheep's wool is a second geometry that inherits the bare one and is
	// drawn over it, not instead of it.
	if hasSheared && eb.models[defID].parent == shearedID {
		skin, ok := eb.layer(shearedID, texPath, false)
		wool, wok := eb.layer(defID, texPath, true)
		if !ok || !wok {
			return EntityModel{}, false
		}
		wool.Tint = true
		out.Default = []Layer{skin, wool}
		out.Sheared = []Layer{skin}
	} else {
		l, ok := eb.layer(defID, texPath, false)
		if !ok {
			return EntityModel{}, false
		}
		out.Default = []Layer{l}
		if hasSheared {
			if s, ok := eb.layer(shearedID, texPath, false); ok {
				out.Sheared = []Layer{s}
			}
		}
	}
	// A slime's shell is a second geometry over its core, not a variant.
	if shellID, ok := def.geometry.get("armor"); ok && def.id == "minecraft:slime" {
		if l, ok := eb.layer(shellID, texPath, false); ok {
			l.Glass = true
			out.Default = append(out.Default, l)
		}
	}
	if babyID, ok := def.geometry.get("baby"); ok {
		if bt, ok := def.babyTexture(); ok {
			if l, ok := eb.layer(babyID, bt, false); ok {
				l.Tint = hasSheared
				out.Baby = []Layer{l}
			}
		}
	}
	out.setHeight(def.adultSize)
	return out, true
}

// setHeight measures the default layers' top, for placing a name label.
func (m *EntityModel) setHeight(adult float32) {
	for _, l := range m.Default {
		for _, b := range l.Bones {
			for _, c := range b.Cubes {
				m.Height = max(m.Height, (c.Origin[1]+c.Size[1]+c.Inflate)/16)
			}
		}
	}
	m.Height = min(max(m.Height*max(adult, 0.1), 0.1), 8)
}

type placement struct{ x, y, w, h int }

// packTextures places the textures on shelves, tallest first, and returns
// the sheet size and where each landed.
func packTextures(imgs map[string]*image.NRGBA) (width, height int, at map[string]placement, err error) {
	paths := sortedKeys(imgs)
	sort.SliceStable(paths, func(i, j int) bool {
		hi, hj := imgs[paths[i]].Bounds().Dy(), imgs[paths[j]].Bounds().Dy()
		return hi > hj
	})
	area, widest := 0, 0
	for _, p := range paths {
		b := imgs[p].Bounds()
		area += b.Dx() * b.Dy()
		widest = max(widest, b.Dx())
	}
	width = 256
	for width < widest || float64(width) < math.Sqrt(float64(area)*1.2) {
		width *= 2
	}
	if width > maxEntitySheet {
		return 0, 0, nil, fmt.Errorf("atlas: entity textures need a sheet wider than %d", maxEntitySheet)
	}
	at = make(map[string]placement, len(paths))
	x, y, shelf := 0, 0, 0
	for _, p := range paths {
		b := imgs[p].Bounds()
		if x+b.Dx() > width {
			x, y, shelf = 0, y+shelf, 0
		}
		at[p] = placement{x, y, b.Dx(), b.Dy()}
		x += b.Dx()
		shelf = max(shelf, b.Dy())
	}
	height = y + shelf
	if height > maxEntitySheet {
		return 0, 0, nil, fmt.Errorf("atlas: entity textures need a sheet taller than %d", maxEntitySheet)
	}
	return width, height, at, nil
}

// buildEntities compiles every entity the pack defines with a model and a
// texture into one table and sheet.
func buildEntities(packDir string) ([]byte, EntityTable, error) {
	defs, err := readEntityDefs(packDir)
	if err != nil {
		return nil, EntityTable{}, err
	}
	eb := &entityBuilder{
		b:      &builder{dir: packDir, images: map[string]*image.NRGBA{}, formats: map[string]int{}, unmapped: map[string]bool{}},
		models: readGeometries(packDir),
		imgs:   map[string]*image.NRGBA{},
	}
	models := map[string]EntityModel{}
	for _, id := range sortedKeys(defs) {
		if m, ok := eb.model(defs[id]); ok {
			models[id] = m
		}
	}
	if len(models) == 0 {
		return nil, EntityTable{}, errors.New("atlas: no entity has both a model and a texture")
	}
	if l, ok := eb.layer("geometry.shield", shieldTexture, false); ok {
		models[ShieldModel] = EntityModel{Height: 1.4, Default: []Layer{l}}
	}

	return eb.sheet(models, true)
}

// sheet packs the textures the models use into one picture and places every
// layer on it. The vanilla pack's worn armour and trims share the sheet; a
// pack's own entities have none.
func (eb *entityBuilder) sheet(models map[string]EntityModel, withWorn bool) ([]byte, EntityTable, error) {
	used := map[string]*image.NRGBA{}
	for _, m := range models {
		for _, layers := range m.layerSets() {
			for _, l := range layers {
				used[l.tex] = eb.imgs[l.tex]
			}
		}
	}
	var armorPaths, trimPaths map[string]string
	if withWorn {
		armorPaths = eb.armorTextures(used)
		trimPaths = eb.trimTextures(used)
	}
	width, height, at, err := packTextures(used)
	if err != nil {
		return nil, EntityTable{}, err
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, width, height))
	for p, img := range used {
		pl := at[p]
		draw.Draw(sheet, image.Rect(pl.x, pl.y, pl.x+pl.w, pl.y+pl.h), img, img.Bounds().Min, draw.Src)
	}
	for _, m := range models {
		for _, layers := range m.layerSets() {
			for li := range layers {
				placeLayer(&layers[li], at[layers[li].tex])
			}
		}
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, sheet); err != nil {
		return nil, EntityTable{}, err
	}
	tbl := EntityTable{Version: entityTableVersion, Width: width, Height: height, Models: models}
	if len(armorPaths) > 0 {
		tbl.Armor = make(map[string][4]float32, len(armorPaths))
		for key, p := range armorPaths {
			pl := at[p]
			tbl.Armor[key] = [4]float32{float32(pl.x), float32(pl.y), float32(pl.x + pl.w), float32(pl.y + pl.h)}
		}
	}
	if len(trimPaths) > 0 {
		tbl.Trims = make(map[string][4]float32, len(trimPaths))
		for key, p := range trimPaths {
			pl := at[p]
			tbl.Trims[key] = [4]float32{float32(pl.x), float32(pl.y), float32(pl.x + pl.w), float32(pl.y + pl.h)}
		}
		tbl.TrimPalettes = eb.trimPalettes()
	}
	doc, err := json.Marshal(tbl)
	if err != nil {
		return nil, EntityTable{}, err
	}
	if len(doc) > maxEntityTableBytes {
		return nil, EntityTable{}, fmt.Errorf("atlas: entity table is %d bytes, over the limit of %d", len(doc), maxEntityTableBytes)
	}
	return enc.Bytes(), tbl, nil
}

// placeLayer maps a layer's texture fractions to sheet pixels. It builds new
// slices because the sheep's skin layer is held by two variants, each of
// which must be placed from the original fractions.
func placeLayer(l *Layer, pl placement) {
	bones := make([]Bone, len(l.Bones))
	for i, b := range l.Bones {
		cubes := make([]Cube, len(b.Cubes))
		for j, c := range b.Cubes {
			for f, r := range c.UV {
				if r == ([4]float32{}) {
					continue
				}
				c.UV[f] = [4]float32{
					float32(pl.x) + r[0]*float32(pl.w), float32(pl.y) + r[1]*float32(pl.h),
					float32(pl.x) + r[2]*float32(pl.w), float32(pl.y) + r[3]*float32(pl.h),
				}
			}
			cubes[j] = c
		}
		b.Cubes = cubes
		bones[i] = b
	}
	l.Bones = bones
}
