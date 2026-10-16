package atlas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Options tunes Build. The zero value is the production configuration.
type Options struct {
	// TileSize is the edge length every texture is normalised to. Zero means 16.
	TileSize int
}

func (o Options) tileSize() int {
	if o.TileSize == 0 {
		return 16
	}
	return o.TileSize
}

// Build reads an extracted vanilla resource_pack directory and produces the
// atlas and block table.
func Build(packDir string) (*Atlas, error) { return BuildWithOptions(packDir, Options{}) }

type textureEntry struct {
	path    string
	overlay string
}

type builder struct {
	dir      string
	keys     map[string]json.RawMessage
	images   map[string]*image.NRGBA // by texture path; nil means unreadable
	formats  map[string]int
	unmapped map[string]bool
}

// BuildWithOptions is Build with explicit options.
func BuildWithOptions(packDir string, opts Options) (*Atlas, error) {
	tile := opts.tileSize()
	b := &builder{
		dir:      packDir,
		images:   map[string]*image.NRGBA{},
		formats:  map[string]int{},
		unmapped: map[string]bool{},
	}

	var terrain struct {
		TextureData map[string]struct {
			Textures json.RawMessage `json:"textures"`
		} `json:"texture_data"`
	}
	if err := readJSON(filepath.Join(packDir, "textures", "terrain_texture.json"), &terrain); err != nil {
		return nil, err
	}
	b.keys = make(map[string]json.RawMessage, len(terrain.TextureData))
	for k, v := range terrain.TextureData {
		b.keys[k] = v.Textures
	}

	var blocks map[string]json.RawMessage
	if err := readJSON(filepath.Join(packDir, "blocks.json"), &blocks); err != nil {
		return nil, err
	}

	type resolved struct {
		name     string
		sound    string
		faces    map[string]textureEntry
		tint     string
		model    string
		material string
	}
	var mapped []resolved
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		var def struct {
			Textures        json.RawMessage `json:"textures"`
			CarriedTextures json.RawMessage `json:"carried_textures"`
			Sound           string          `json:"sound"`
		}
		// Entries like format_version are not objects.
		if json.Unmarshal(blocks[name], &def) != nil || def.Textures == nil {
			continue
		}
		primary, carried := faceKeys(def.Textures), faceKeys(def.CarriedTextures)
		faces := map[string]textureEntry{}
		complete := true
		for _, f := range faceNames {
			entry, ok := b.resolve(primary[f])
			if !ok {
				entry, ok = b.resolve(carried[f])
			}
			if !ok {
				complete = false
				continue
			}
			faces[f] = entry
		}
		if !complete {
			continue
		}
		r := resolved{name: name, sound: def.Sound, faces: faces, tint: tintFor(name), model: modelFor(name)}
		r.material = firstMatch(materialRules, name, def.Sound)
		if r.material == "" {
			r.material = "default"
		}
		mapped = append(mapped, r)
	}

	// Assign tiles in sorted path order so the atlas is reproducible.
	pathSet := map[string]bool{}
	for _, r := range mapped {
		for _, e := range r.faces {
			pathSet[e.path] = true
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	cols := int(math.Ceil(math.Sqrt(float64(max(len(paths), 1)))))
	rows := (len(paths) + cols - 1) / cols
	rows = max(rows, 1)
	width, height := cols*tile, rows*tile
	sheet := image.NewNRGBA(image.Rect(0, 0, width, height))
	rects := map[string]Rect{}
	avg := map[string]color.NRGBA{}
	for i, p := range paths {
		x0, y0 := (i%cols)*tile, (i/cols)*tile
		t := normalise(b.images[p], tile)
		draw.Draw(sheet, image.Rect(x0, y0, x0+tile, y0+tile), t, image.Point{}, draw.Src)
		rects[p] = Rect{
			float32(x0) / float32(width), float32(y0) / float32(height),
			float32(x0+tile) / float32(width), float32(y0+tile) / float32(height),
		}
		avg[p] = average(t)
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, sheet); err != nil {
		return nil, err
	}

	tbl := Table{
		Version: tableVersion, TileSize: tile, Width: width, Height: height,
		Blocks: map[string]Block{}, Materials: map[string]string{}, WaterColors: map[string]string{},
		Formats: b.formats,
	}
	type sum struct{ r, g, b, n int }
	classes := map[string]*sum{}
	for _, r := range mapped {
		blk := Block{Tint: r.tint, Model: r.model, Material: r.material}
		face := func(f string) Face {
			e := r.faces[f]
			tinted := r.tint != TintNone
			// Only the top of a grass block takes the biome colour; its sides
			// carry a separate overlay colour.
			if r.tint == TintGrass && (r.name == "grass" || r.name == "grass_block") && f != "up" {
				tinted = false
			}
			return Face{UV: rects[e.path], Tinted: tinted, Overlay: e.overlay}
		}
		blk.Up, blk.Down, blk.Side = face("up"), face("down"), face("side")
		blk.North, blk.South, blk.East, blk.West = face("north"), face("south"), face("east"), face("west")
		up, side := avg[r.faces["up"].path], avg[r.faces["side"].path]
		c := color.NRGBA{uint8((int(up.R) + int(side.R)) / 2), uint8((int(up.G) + int(side.G)) / 2), uint8((int(up.B) + int(side.B)) / 2), 255}
		blk.Color = hex(c)
		tbl.Blocks["minecraft:"+r.name] = blk
		if alias, ok := blockAliases[r.name]; ok {
			tbl.Blocks["minecraft:"+alias] = blk
		}
		s := classes[r.material]
		if s == nil {
			s = &sum{}
			classes[r.material] = s
		}
		s.r, s.g, s.b, s.n = s.r+int(c.R), s.g+int(c.G), s.b+int(c.B), s.n+1
	}
	for class, hexCol := range materialDefaults {
		tbl.Materials[class] = hexCol
	}
	for class, s := range classes {
		if class == "default" {
			continue
		}
		tbl.Materials[class] = hex(color.NRGBA{uint8(s.r / s.n), uint8(s.g / s.n), uint8(s.b / s.n), 255})
	}
	for k := range b.unmapped {
		tbl.Unmapped = append(tbl.Unmapped, k)
	}
	sort.Strings(tbl.Unmapped)

	b.loadBiomeWater(&tbl)
	b.loadColormaps(&tbl)
	return &Atlas{png: enc.Bytes(), table: tbl}, nil
}

var faceNames = []string{"up", "down", "side", "north", "south", "east", "west"}

// faceKeys expands a blocks.json texture spec into a key per face. A single
// string covers every face; in a map, "side" fills the horizontal faces and,
// absent a specific key, the vertical ones.
func faceKeys(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		for _, f := range faceNames {
			out[f] = one
		}
		return out
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for _, f := range faceNames {
		switch {
		case m[f] != "":
			out[f] = m[f]
		case m["side"] != "":
			out[f] = m["side"]
		}
	}
	return out
}

// resolve maps a terrain texture key to its first variation and loads it,
// recording the key when it cannot be satisfied.
func (b *builder) resolve(key string) (textureEntry, bool) {
	if key == "" {
		return textureEntry{}, false
	}
	entry, ok := b.firstEntry(key)
	if ok {
		if _, loaded := b.images[entry.path]; !loaded {
			b.images[entry.path] = b.loadImage(entry.path)
		}
		ok = b.images[entry.path] != nil
	}
	if !ok {
		b.unmapped[key] = true
		return textureEntry{}, false
	}
	return entry, true
}

func (b *builder) firstEntry(key string) (textureEntry, bool) {
	raw, ok := b.keys[key]
	if !ok {
		return textureEntry{}, false
	}
	// The spec is a path, an object, or a list of either; the first list item
	// is variation zero.
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	if len(list) == 0 {
		return textureEntry{}, false
	}
	var path string
	if json.Unmarshal(list[0], &path) == nil {
		return textureEntry{path: path}, path != ""
	}
	var obj struct {
		Path         string `json:"path"`
		OverlayColor string `json:"overlay_color"`
	}
	if json.Unmarshal(list[0], &obj) != nil || obj.Path == "" {
		return textureEntry{}, false
	}
	return textureEntry{path: obj.Path, overlay: obj.OverlayColor}, true
}

// loadImage prefers PNG over TGA when a texture has both.
func (b *builder) loadImage(texPath string) *image.NRGBA {
	base := filepath.Join(b.dir, filepath.FromSlash(texPath))
	if data, err := os.ReadFile(base + ".png"); err == nil {
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			b.formats["png"]++
			return toNRGBA(img)
		}
	}
	if data, err := os.ReadFile(base + ".tga"); err == nil {
		if img, err := decodeTGA(data); err == nil {
			b.formats["tga"]++
			return img
		}
	}
	return nil
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// normalise reduces a texture to a square tile. Animated textures are tall
// strips of square frames, so only the first frame is kept.
func normalise(src *image.NRGBA, tile int) *image.NRGBA {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	out := image.NewNRGBA(image.Rect(0, 0, tile, tile))
	for y := 0; y < tile; y++ {
		for x := 0; x < tile; x++ {
			out.SetNRGBA(x, y, src.NRGBAAt(b.Min.X+x*side/tile, b.Min.Y+y*side/tile))
		}
	}
	return out
}

// average is the alpha-weighted mean colour; fully transparent tiles are grey.
func average(img *image.NRGBA) color.NRGBA {
	var r, g, bl, a, n int
	for i := 0; i < len(img.Pix); i += 4 {
		w := int(img.Pix[i+3])
		r, g, bl, a, n = r+int(img.Pix[i])*w, g+int(img.Pix[i+1])*w, bl+int(img.Pix[i+2])*w, a+w, n+1
	}
	if a == 0 {
		return color.NRGBA{128, 128, 128, 255}
	}
	return color.NRGBA{uint8(r / a), uint8(g / a), uint8(bl / a), 255}
}

func (b *builder) loadBiomeWater(tbl *Table) {
	var biomes struct {
		Biomes map[string]struct {
			Water string `json:"water_surface_color"`
		} `json:"biomes"`
	}
	if readJSON(filepath.Join(b.dir, "biomes_client.json"), &biomes) != nil {
		return
	}
	for name, v := range biomes.Biomes {
		if v.Water != "" {
			if c, ok := parseHex(v.Water); ok {
				tbl.WaterColors[name] = hex(c)
			}
		}
	}
}

func (b *builder) loadColormaps(tbl *Table) {
	for kind, file := range map[string]string{TintGrass: "grass.png", TintFoliage: "foliage.png"} {
		data, err := os.ReadFile(filepath.Join(b.dir, "textures", "colormap", file))
		if err != nil {
			continue
		}
		if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
			continue
		}
		if tbl.Colormaps == nil {
			tbl.Colormaps = map[string][]byte{}
		}
		tbl.Colormaps[kind] = data
	}
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("atlas: %w", err)
	}
	if err := unmarshalLoose(data, v); err != nil {
		return fmt.Errorf("atlas: parse %s: %w", filepath.Base(path), err)
	}
	return nil
}
