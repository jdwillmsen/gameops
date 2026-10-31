package atlas

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A pack's own entities (the wildlife pack's tiger, say) are compiled into a
// second table beside the vanilla one. They need no download, so the camera
// draws them even where the vanilla assets cannot be fetched.

// Bounds on what a pack may ask the page to evaluate.
const (
	maxAnimations   = 16
	maxAnimBones    = 64
	maxExprBytes    = 256
	maxVariants     = 16
	maxPackRCs      = 256
	maxPackAnimDocs = 64
)

// AnimSet is a pack entity's animations. Expressions stay as the pack wrote
// them; the page evaluates them each frame.
type AnimSet struct {
	// Defs is keyed by the name the entity gives the animation ("walk").
	Defs map[string]AnimDef `json:"defs"`
	// Run lists the animations in the order the entity plays them.
	Run []animRun `json:"run"`
}

// AnimDef moves bones by name.
type AnimDef struct {
	Bones map[string]AnimBone `json:"bones"`
}

// AnimBone adds to a bone's rotation (degrees) and position (model units).
type AnimBone struct {
	Rot *[3]string `json:"r,omitempty"`
	Pos *[3]string `json:"p,omitempty"`
}

// animRun is one entry of an entity's animate script: an animation, played
// always or while When is non-zero.
type animRun struct {
	Name string `json:"n"`
	When string `json:"w,omitempty"`
}

var exprChars = regexp.MustCompile(`^[A-Za-z0-9_.+\-*/%()<>=!&|?:, \t]*$`)

func exprOK(s string) bool { return len(s) <= maxExprBytes && exprChars.MatchString(s) }

// parseAnimRuns reads an animate list whose entries are a name or a
// {name: condition} object.
func parseAnimRuns(list []json.RawMessage) []animRun {
	var out []animRun
	for _, raw := range list {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			out = append(out, animRun{Name: name})
			continue
		}
		var cond map[string]json.RawMessage
		if json.Unmarshal(raw, &cond) != nil {
			continue
		}
		for _, k := range sortedKeys(cond) {
			out = append(out, animRun{Name: k, When: scalarExpr(cond[k])})
		}
	}
	return out
}

// parseNamed reads a list of names or {name: condition} objects, keeping the
// names.
func parseNamed(list []json.RawMessage) []string {
	var out []string
	for _, r := range parseAnimRuns(list) {
		out = append(out, r.Name)
	}
	return out
}

// scalarExpr reads an expression that the file may have written as a number,
// a boolean or a string.
func scalarExpr(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		if b {
			return "1"
		}
		return "0"
	}
	return ""
}

// vec3Expr reads a three-part rotation or position; any other shape (a
// keyframed bone, a lone number) is left out.
func vec3Expr(raw json.RawMessage) *[3]string {
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) != 3 {
		return nil
	}
	var out [3]string
	for i, p := range parts {
		out[i] = scalarExpr(p)
		if out[i] == "" || !exprOK(out[i]) {
			return nil
		}
	}
	return &out
}

// readAnimations reads every animation under animations/ by identifier.
func readAnimations(packDir string) map[string]AnimDef {
	out := map[string]AnimDef{}
	files, _ := filepath.Glob(filepath.Join(packDir, "animations", "*.json"))
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err != nil || i >= maxPackAnimDocs || len(data) > maxModelBytes {
			continue
		}
		data = stripJSONComments(data)
		if !jsonWithin(data, maxJSONDepth) {
			continue
		}
		var doc struct {
			Animations map[string]struct {
				Bones map[string]struct {
					Rotation json.RawMessage `json:"rotation"`
					Position json.RawMessage `json:"position"`
				} `json:"bones"`
			} `json:"animations"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		for id, a := range doc.Animations {
			if _, held := out[id]; held || len(a.Bones) > maxAnimBones {
				continue
			}
			def := AnimDef{Bones: map[string]AnimBone{}}
			for name, b := range a.Bones {
				if !boneName.MatchString(name) {
					continue
				}
				ab := AnimBone{Rot: vec3Expr(b.Rotation), Pos: vec3Expr(b.Position)}
				if ab.Rot != nil || ab.Pos != nil {
					def.Bones[name] = ab
				}
			}
			out[id] = def
		}
	}
	return out
}

// renderController is the part of a render controller the camera follows:
// which geometry, which texture by variant, and which bones show.
type renderController struct {
	geometry string
	textures []string
	arrays   map[string][]string
	// visible lists bone patterns with the condition that shows them, in the
	// order the file gave them; later entries win.
	visible []visibility
}

type visibility struct{ bone, cond string }

func readRenderControllers(packDir string) map[string]renderController {
	out := map[string]renderController{}
	files, _ := filepath.Glob(filepath.Join(packDir, "render_controllers", "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil || len(data) > maxEntityDefBytes {
			continue
		}
		data = stripJSONComments(data)
		if !jsonWithin(data, maxJSONDepth) {
			continue
		}
		var doc struct {
			Controllers map[string]struct {
				Geometry   string            `json:"geometry"`
				Textures   []string          `json:"textures"`
				Visibility []json.RawMessage `json:"part_visibility"`
				Arrays     struct {
					Textures map[string][]string `json:"textures"`
				} `json:"arrays"`
			} `json:"render_controllers"`
		}
		if json.Unmarshal(data, &doc) != nil {
			continue
		}
		for id, c := range doc.Controllers {
			if len(out) >= maxPackRCs {
				break
			}
			rc := renderController{geometry: c.Geometry, textures: c.Textures, arrays: c.Arrays.Textures}
			for _, raw := range c.Visibility {
				var m map[string]json.RawMessage
				if json.Unmarshal(raw, &m) != nil {
					continue
				}
				for _, k := range sortedKeys(m) {
					rc.visible = append(rc.visible, visibility{k, scalarExpr(m[k])})
				}
			}
			out[id] = rc
		}
	}
	return out
}

var (
	variantIndexed = regexp.MustCompile(`(?i)^array\.([A-Za-z0-9_]+)\[\s*query\.variant\s*\]$`)
	variantTest    = regexp.MustCompile(`^query\.variant\s*(==|!=|<=|>=|<|>)\s*([0-9]+)$`)
)

// variantTextures lists the texture keys in play by variant index: a
// controller that indexes an array by query.variant gives one per element,
// one that names a single texture gives one.
func (rc renderController) variantTextures() []string {
	if len(rc.textures) == 0 {
		return nil
	}
	expr := strings.TrimSpace(rc.textures[0])
	if m := variantIndexed.FindStringSubmatch(expr); m != nil {
		for name, list := range rc.arrays {
			if !strings.EqualFold(name, "Array."+m[1]) {
				continue
			}
			keys := make([]string, 0, len(list))
			for _, e := range list {
				keys = append(keys, strings.TrimPrefix(e, "Texture."))
			}
			return keys
		}
		return nil
	}
	if k, ok := strings.CutPrefix(expr, "Texture."); ok {
		return []string{k}
	}
	return nil
}

// shows reports whether the bone is drawn for a variant. A condition the
// camera cannot read leaves the bone drawn.
func (rc renderController) shows(bone string, variant int) bool {
	shown := true
	for _, v := range rc.visible {
		if v.bone != "*" {
			if ok, _ := path.Match(strings.ToLower(v.bone), strings.ToLower(bone)); !ok {
				continue
			}
		}
		shown = condHolds(v.cond, variant)
	}
	return shown
}

func condHolds(cond string, variant int) bool {
	cond = strings.TrimSpace(cond)
	switch strings.ToLower(cond) {
	case "true", "1", "1.0":
		return true
	case "false", "0", "0.0":
		return false
	}
	m := variantTest.FindStringSubmatch(cond)
	if m == nil {
		return true
	}
	n, _ := strconv.Atoi(m[2])
	switch m[1] {
	case "==":
		return variant == n
	case "!=":
		return variant != n
	case "<=":
		return variant <= n
	case ">=":
		return variant >= n
	case "<":
		return variant < n
	}
	return variant > n
}

// packModel compiles one entity of the pack: its geometry once per variant,
// each in its own texture with the bones the render controller hides emptied,
// and its animations.
func (eb *entityBuilder) packModel(def entityDef, rcs map[string]renderController, anims map[string]AnimDef) (EntityModel, error) {
	var rc renderController
	for _, id := range def.controllers {
		if c, ok := rcs[id]; ok {
			rc = c
			break
		}
	}
	if len(rc.textures) == 0 {
		return EntityModel{}, errors.New("no render controller with a texture")
	}
	geoKey := "default"
	if k, ok := strings.CutPrefix(rc.geometry, "Geometry."); ok {
		geoKey = k
	}
	geoID, ok := def.geometry.get(geoKey, "default")
	if !ok {
		if geoID, ok = def.geometry.first(); !ok {
			return EntityModel{}, errors.New("no geometry")
		}
	}
	keys := rc.variantTextures()
	if len(keys) == 0 || len(keys) > maxVariants {
		return EntityModel{}, fmt.Errorf("%d textures by variant", len(keys))
	}
	var out EntityModel
	if def.adultSize != 1 {
		out.Scale = def.adultSize
	}
	for v, key := range keys {
		texPath, ok := def.textures.get(key)
		if !ok {
			return EntityModel{}, fmt.Errorf("no texture named %s", key)
		}
		l, ok := eb.layer(geoID, texPath, false)
		if !ok {
			return EntityModel{}, fmt.Errorf("variant %d: %s in %s did not load", v, geoID, texPath)
		}
		for i := range l.Bones {
			if !rc.shows(l.Bones[i].Name, v) {
				l.Bones[i].Cubes = nil
			}
		}
		out.Variants = append(out.Variants, []Layer{l})
	}
	out.Default = out.Variants[0]
	if len(out.Variants) == 1 {
		out.Variants = nil
	}
	out.setHeight(def.adultSize)
	out.Anim = packAnims(def, anims)
	return out, nil
}

// packAnims resolves the entity's animation names against the pack's
// animations; an entity that plays none has no set.
func packAnims(def entityDef, anims map[string]AnimDef) *AnimSet {
	set := &AnimSet{Defs: map[string]AnimDef{}}
	for _, r := range def.animate {
		id, ok := def.animations.get(r.Name)
		d, found := anims[id]
		if !ok || !found || len(set.Run) >= maxAnimations || (r.When != "" && !exprOK(r.When)) {
			continue
		}
		set.Defs[r.Name] = d
		set.Run = append(set.Run, r)
	}
	if len(set.Run) == 0 {
		return nil
	}
	return set
}

// BuildPackEntities compiles the entities under a resource pack directory
// that are not vanilla: the sheet and the table that goes with it. An entity
// that cannot be compiled is left out and named in skipped.
func BuildPackEntities(packDir string) (sheet []byte, table EntityTable, skipped map[string]error, err error) {
	defs, err := readEntityDefsWith(packDir, func(data []byte) (entityDef, bool) {
		return parseEntityDefIn(data, func(id string) bool {
			ns, name, ok := strings.Cut(id, ":")
			return ok && ns != "" && ns != "minecraft" && name != ""
		})
	})
	if err != nil {
		return nil, EntityTable{}, nil, err
	}
	eb := &entityBuilder{
		b:      &builder{dir: packDir, images: map[string]*image.NRGBA{}, formats: map[string]int{}, unmapped: map[string]bool{}},
		models: readGeometries(packDir),
		imgs:   map[string]*image.NRGBA{},
	}
	rcs, anims := readRenderControllers(packDir), readAnimations(packDir)
	models, skipped := map[string]EntityModel{}, map[string]error{}
	for _, id := range sortedKeys(defs) {
		m, err := eb.packModel(defs[id], rcs, anims)
		if err != nil {
			skipped[id] = err
			continue
		}
		models[id] = m
	}
	if len(models) == 0 {
		return nil, EntityTable{}, skipped, errors.New("atlas: no entity of the pack could be compiled")
	}
	sheet, table, err = eb.sheet(models, false)
	return sheet, table, skipped, err
}
