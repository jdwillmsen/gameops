package icons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

const (
	modelDir      = "models/entity/"
	legacyModels  = "models/mobs.json"
	controllerDir = "render_controllers/"
	terrainPath   = "resource_pack/textures/terrain_texture.json"

	maxTerrainBytes  = 2 << 20
	maxModelTextures = 600

	// artPlan stands among what a set is missing for the recipes
	// themselves: the models could not all be read, so which pictures
	// there are to make is not yet known.
	artPlan = "art/plan"

	// ArtRevision is raised when the pictures made here would come out
	// differently from the same samples: a face chosen otherwise, a kind
	// of picture added. A volume written under another has its made
	// pictures served as they are until they have been made again.
	ArtRevision = 1
)

// mobLook is a mob type's client definition as far as drawing its face
// goes.
type mobLook struct {
	appearance
	// egg is whether the definition gives it a spawn egg, which is what
	// tells a mob from a boat or an arrow.
	egg bool
}

// soft fetches every path a few at a time and gives back what it got. A
// file the source answers it does not hold is left out; one it could not
// be asked for is left out and counted, and after the first of those no
// more are asked for, since a source out of reach is out of reach for all.
func (s *Source) soft(ctx context.Context, paths []string, limit int64, total *budget) (bodies map[string][]byte, unreached []string) {
	var (
		mu   sync.Mutex
		down atomic.Bool
	)
	miss := func(path string) {
		down.Store(true)
		mu.Lock()
		defer mu.Unlock()
		unreached = append(unreached, path)
	}
	bodies, err := each(ctx, paths, func(ctx context.Context, path string) ([]byte, error) {
		if down.Load() {
			miss(path)
			return nil, nil
		}
		body, err := s.get(ctx, s.raw("resource_pack/"+path), limit, total)
		switch {
		case err == nil:
			return body, nil
		case !errors.Is(err, errSettled):
			miss(path)
		}
		return nil, nil
	})
	if err != nil {
		return nil, paths
	}
	for path, body := range bodies {
		if body == nil {
			delete(bodies, path)
		}
	}
	return bodies, unreached
}

// art reads the models and render controllers, works out a recipe for
// every picture made here, and makes them. It never fails: what it could
// not do is in the set it returns, as missing, unreached or rejected. A
// model or controller file the source could not be asked for leaves every
// recipe unmade, with artPlan among the missing, since a face worked out
// from half the models may be the wrong one.
func (s *Source) art(ctx context.Context, ls listing, looks map[string]mobLook, items atlas, total *budget) Set {
	unplanned := Set{Missing: []string{artPlan}, Unreached: []string{artPlan}}
	files, unreached := s.soft(ctx, slices.Concat(ls.models, ls.controllers), maxModelBytes, total)
	if len(unreached) > 0 {
		return unplanned
	}
	var terrain atlas
	raw, err := s.get(ctx, s.raw(terrainPath), maxTerrainBytes, total)
	switch {
	case err == nil:
		// One that does not parse lists no block, which leaves each
		// block out as it would any it did not name.
		_ = json.Unmarshal(stripComments(raw), &terrain)
	case !errors.Is(err, errSettled):
		return unplanned
	}
	lib := library{models: map[string]model{}, controllers: map[string]controller{}}
	for _, path := range ls.models {
		models, err := parseModels(files[path])
		if err != nil {
			continue
		}
		for _, m := range models {
			if len(lib.models) < maxModels {
				lib.models[m.id] = m
			}
		}
	}
	for _, path := range ls.controllers {
		controllers, err := parseControllers(files[path])
		if err != nil {
			continue
		}
		for id, c := range controllers {
			if len(lib.controllers) < maxControllers {
				lib.controllers[id] = c
			}
		}
	}
	lib.tga = ls.tga
	recipes, rejected := plan(lib, looks, items, terrain)
	out := s.make(ctx, recipes, slices.Sorted(maps.Keys(recipes)), total)
	out.Recipes, out.Replanned = recipes, true
	maps.Copy(out.Rejected, rejected)
	return out
}

// plan is the recipe for every picture made here that one can be worked
// out for, and for each that one cannot, why not.
func plan(lib library, looks map[string]mobLook, items, terrain atlas) (recipes map[string]Recipe, rejected map[string]string) {
	recipes, rejected = map[string]Recipe{}, map[string]string{}
	for _, kind := range slices.Sorted(maps.Keys(looks)) {
		look := looks[kind]
		o, special := overrides[kind]
		if !look.egg && !special {
			continue
		}
		key := faceKey(kind)
		if o.egg {
			rejected[key] = "kept as its spawn egg: " + o.why
			continue
		}
		recipe, err := lib.face(look.appearance, o)
		if err != nil {
			rejected[key] = err.Error()
			continue
		}
		recipes[key] = recipe
		if kind != villagerKind {
			continue
		}
		for profession, texture := range professions {
			worn := o
			worn.swap = map[string]string{villagerUnskilled: texture}
			if _, held := look.Textures[texture]; !held {
				continue
			}
			if recipe, err := lib.face(look.appearance, worn); err == nil {
				recipes["villager/"+profession] = recipe
			}
		}
	}
	for _, kind := range StructureKinds {
		item := structureItems[kind]
		recipe, faced := recipes[faceKey(structureFaces[kind])]
		if !faced {
			recipe = Recipe{Flat: item}
		} else {
			recipe.Else = item
		}
		recipes["structure/"+kind] = recipe
	}
	// A bell is no box: its sides are a bell with air round it, and three
	// of them drawn as a block are three bells adrift. The picture the
	// game shows for one in the hand is used as it is.
	if path := terrain.pathIn("bell_carried", 0, modelTexture); path != "" {
		recipes["block/bell"] = Recipe{Flat: path}
	}
	for key, block := range blocks(terrain) {
		if err := block.check(); err != nil {
			rejected[key] = err.Error()
			continue
		}
		recipes[key] = Recipe{Block: block}
	}
	for _, key := range blockKeys() {
		if _, made := recipes[key]; !made && rejected[key] == "" {
			rejected[key] = "the terrain atlas does not list its textures"
		}
	}
	return recipes, rejected
}

// make makes the pictures named in keys from their recipes. Each comes out
// one of four ways: made; missing, because the source answered that it
// does not hold a texture it needs; missing and unreached, because the
// source could not be asked; or rejected, because it was made and is not a
// picture of anything. One that cannot be made and has a plain texture to
// fall back on is that texture.
func (s *Source) make(ctx context.Context, recipes map[string]Recipe, keys []string, total *budget) Set {
	out := Set{Pictures: map[string][]byte{}, Rejected: map[string]string{}}
	var paths []string
	for _, key := range keys {
		r := recipes[key]
		for _, path := range append(r.textures(), r.Else) {
			if path != "" && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) > maxModelTextures {
		for _, key := range keys {
			out.Rejected[key] = fmt.Sprintf("the pictures name %d textures, over the limit of %d", len(paths), maxModelTextures)
		}
		return out
	}
	files := make([]string, len(paths))
	for i, path := range paths {
		files[i] = fileOf(path)
	}
	bodies, unreached := s.soft(ctx, files, maxModelTextureBytes, total)
	textures := map[string]*image.NRGBA{}
	// Why a texture that was fetched is not held: it is not a picture
	// within bounds.
	refused := map[string]error{}
	for _, path := range paths {
		body, got := bodies[fileOf(path)]
		if !got {
			continue
		}
		tex, err := decodeTexture(path, body)
		if err != nil {
			refused[path] = err
			continue
		}
		textures[path] = tex
	}
	away := func(path string) bool { return path != "" && slices.Contains(unreached, fileOf(path)) }
	for _, key := range keys {
		r, known := recipes[key]
		if !known {
			out.Missing = append(out.Missing, key)
			continue
		}
		img, err := r.render(textures)
		if err == nil {
			if out.Pictures[key], err = encode(img); err == nil {
				continue
			}
		}
		if r.Else != "" && textures[r.Else] != nil {
			if out.Pictures[key], err = encode(textures[r.Else]); err == nil {
				continue
			}
		}
		needs := append(r.textures(), r.Else)
		switch {
		case slices.ContainsFunc(needs, away):
			out.Missing = append(out.Missing, key)
			out.Unreached = append(out.Unreached, key)
		case slices.ContainsFunc(r.needs(), func(path string) bool { return textures[path] == nil && refused[path] == nil }):
			out.Missing = append(out.Missing, key)
		default:
			out.Rejected[key] = err.Error()
		}
	}
	slices.Sort(out.Missing)
	slices.Sort(out.Unreached)
	return out
}

// blockKeys is the key of every block picture there can be.
func blockKeys() []string {
	keys := []string{"block/chest", "block/trapped_chest", "block/ender_chest", "block/barrel", "block/bell", "block/spawner", "block/vault", "block/shulker_" + markers.Undyed}
	for _, colour := range markers.Colours {
		keys = append(keys, "block/shulker_"+colour, "block/bed_"+colour)
	}
	return keys
}

// blocks is every block drawn from three of its sides, by its key. A chest,
// a barrel and their like have no item texture, since the game draws the
// block itself in a slot; the terrain atlas names the textures it draws
// each from, and one the atlas does not list is left out. A shulker box and
// a bed are not in the atlas at all: each is drawn by a model, and its
// sides are taken from the texture the model is wrapped in.
func blocks(terrain atlas) map[string]*Block {
	out := map[string]*Block{}
	whole := func(name string) []Layer {
		path := terrain.pathIn(name, 0, modelTexture)
		if path == "" {
			return nil
		}
		return []Layer{{Texture: path, Units: [2]int{boxSide, boxSide}, Src: [4]int{0, 0, boxSide, boxSide}, Dst: [4]int{0, 0, boxSide, boxSide}}}
	}
	cubeOf := func(key, top, left, right string) {
		t, l, r := whole(top), whole(left), whole(right)
		if t != nil && l != nil && r != nil {
			out[key] = &Block{W: boxSide, H: boxSide, D: boxSide, Top: t, Left: l, Right: r}
		}
	}
	cubeOf("block/chest", "chest_inventory_top", "chest_inventory_front", "chest_inventory_side")
	cubeOf("block/trapped_chest", "chest_inventory_top", "trapped_chest_inventory_front", "chest_inventory_side")
	cubeOf("block/ender_chest", "ender_chest_inventory_top", "ender_chest_inventory_front", "ender_chest_inventory_side")
	cubeOf("block/barrel", "barrel_top", "barrel_side", "barrel_side")
	cubeOf("block/spawner", "mob_spawner", "mob_spawner", "mob_spawner")
	cubeOf("block/vault", "vault_top", "vault_front", "vault_side")

	sheet := [2]int{sheetUnits, sheetUnits}
	for _, colour := range append([]string{markers.Undyed}, markers.Colours...) {
		path := "textures/entity/shulker/shulker_" + legacyColour(colour)
		// The lid comes down over the top of the base, as it does shut.
		side := func(x int) []Layer {
			return []Layer{
				{Texture: path, Units: sheet, Src: [4]int{x, 28 + 16, boxSide, baseTall}, Dst: [4]int{0, boxSide - baseTall, boxSide, baseTall}},
				{Texture: path, Units: sheet, Src: [4]int{x, 16, boxSide, lidTall}, Dst: [4]int{0, 0, boxSide, lidTall}},
			}
		}
		out["block/shulker_"+colour] = &Block{
			W: boxSide, H: boxSide, D: boxSide,
			Top:  []Layer{{Texture: path, Units: sheet, Src: [4]int{16, 0, boxSide, boxSide}, Dst: [4]int{0, 0, boxSide, boxSide}}},
			Left: side(16), Right: side(32),
		}
	}
	for _, colour := range markers.Colours {
		out["block/bed_"+colour] = bed("textures/entity/bed/" + legacyColour(colour))
	}
	return out
}

// A bed's model is two slabs, each 16 by 16 and bedTall thick, lying end
// to end: the pillow's and the foot's. Each is a box stood on end in the
// texture, so its 16 by 16 face is the top of the bed, and its long sides
// are strips to be turned to lie along it.
const (
	bedTall = 6
	bedFoot = 22
)

func bed(path string) *Block {
	sheet := [2]int{sheetUnits, sheetUnits}
	return &Block{
		W: boxSide, H: bedTall, D: 2 * boxSide,
		Top: []Layer{
			{Texture: path, Units: sheet, Src: [4]int{bedTall, bedTall, boxSide, boxSide}, Dst: [4]int{0, 0, boxSide, boxSide}},
			{Texture: path, Units: sheet, Src: [4]int{bedTall, bedFoot + bedTall, boxSide, boxSide}, Dst: [4]int{0, boxSide, boxSide, boxSide}},
		},
		Left: []Layer{{Texture: path, Units: sheet, Src: [4]int{bedTall + boxSide, bedFoot, boxSide, bedTall}, Dst: [4]int{0, 0, boxSide, bedTall}}},
		Right: []Layer{
			{Texture: path, Units: sheet, Src: [4]int{bedTall + boxSide, bedFoot + bedTall, bedTall, boxSide}, Dst: [4]int{0, 0, boxSide, bedTall}, Turn: 1},
			{Texture: path, Units: sheet, Src: [4]int{bedTall + boxSide, bedTall, bedTall, boxSide}, Dst: [4]int{boxSide, 0, boxSide, bedTall}, Turn: 1},
		},
	}
}

// split parts what a set is missing into what is fetched as it is and what
// is made from a recipe.
func split(missing []string) (plain, made []string) {
	for _, what := range missing {
		if what == artPlan || derived(what) {
			made = append(made, what)
		} else {
			plain = append(plain, what)
		}
	}
	return plain, made
}

// remake is the made pictures a set is missing, made again. With a recipe
// for each it asks only for their textures. Without, or with the recipes
// themselves missing, everything is worked out afresh from the models,
// which takes the listing and every definition again.
func (s *Source) remake(ctx context.Context, missing []string, recipes map[string]Recipe, total *budget) Set {
	replan := slices.Contains(missing, artPlan) || slices.ContainsFunc(missing, func(key string) bool {
		_, known := recipes[key]
		return !known
	})
	if !replan {
		return s.make(ctx, recipes, missing, total)
	}
	unplanned := Set{Missing: []string{artPlan}, Unreached: []string{artPlan}}
	ls, err := s.list(ctx, total)
	if err != nil {
		return unplanned
	}
	raw, err := s.get(ctx, s.raw(atlasPath), maxAtlasBytes, total)
	if err != nil {
		return unplanned
	}
	var items atlas
	_ = json.Unmarshal(stripComments(raw), &items)
	chosen, err := s.definitions(ctx, ls.definitions, items, total)
	if err != nil {
		return unplanned
	}
	looks := map[string]mobLook{}
	for kind, c := range chosen {
		looks[kind] = c.look
	}
	return s.art(ctx, ls, looks, items, total)
}
