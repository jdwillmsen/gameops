package atlas

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ItemTable is the lookup that goes with the item sheet. Rects are
// normalised like the block atlas's.
type ItemTable struct {
	TileSize int `json:"tile_size"`
	Width    int `json:"width"`
	Height   int `json:"height"`
	// Items is keyed by full item name, e.g. "minecraft:apple".
	Items map[string]Rect `json:"items"`
	// Placeholder is drawn for a name Items does not hold.
	Placeholder Rect `json:"placeholder"`
}

// ItemPNG returns the encoded item sprite sheet.
func (a *Atlas) ItemPNG() []byte { return a.itemPNG }

// Items returns the item sheet's lookup table.
func (a *Atlas) Items() ItemTable { return a.items }

// ItemsErr reports why the atlas has no item sheet, or nil when it has one.
func (a *Atlas) ItemsErr() error { return a.itemsErr }

// buildItems packs one tile per distinct picture: an item's own texture where
// the pack has one, otherwise a face of the block of the same name. The
// pack's item texture index is optional so a pack without one still yields
// every block.
func buildItems(packDir string, tile int, tbl Table, blockSheet image.Image) ([]byte, ItemTable, error) {
	var index struct {
		TextureData map[string]struct {
			Textures json.RawMessage `json:"textures"`
		} `json:"texture_data"`
	}
	err := readJSON(filepath.Join(packDir, "textures", "item_texture.json"), &index)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ItemTable{}, err
	}
	ib := &builder{
		dir:      packDir,
		keys:     make(map[string]json.RawMessage, len(index.TextureData)),
		images:   map[string]*image.NRGBA{},
		formats:  map[string]int{},
		unmapped: map[string]bool{},
	}
	for k, v := range index.TextureData {
		ib.keys[k] = v.Textures
	}

	// The tile each name draws, by a key shared between names that draw the
	// same picture so the sheet holds it once.
	type source struct {
		key string
		img *image.NRGBA
	}
	srcs := map[string]source{}
	keys := make([]string, 0, len(ib.keys))
	for k := range ib.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if e, ok := ib.resolve(k); ok {
			srcs["minecraft:"+k] = source{"item:" + e.path, normalise(ib.images[e.path], tile)}
		}
	}

	// Many items share one key whose list is indexed by a data value that
	// modern item names no longer carry (every pickaxe under "pickaxe", every
	// bed under "bed"), so each listed picture is also offered under the item
	// name its file is named for.
	for _, k := range keys {
		for _, p := range texturePaths(ib.keys[k]) {
			for _, n := range namesForFile(path.Base(p)) {
				if _, taken := srcs["minecraft:"+n]; taken {
					continue
				}
				if _, loaded := ib.images[p]; !loaded {
					ib.images[p] = ib.loadImage(p)
				}
				if img := ib.images[p]; img != nil {
					srcs["minecraft:"+n] = source{"item:" + p, normalise(img, tile)}
				}
			}
		}
	}

	blocks := make([]string, 0, len(tbl.Blocks))
	for n := range tbl.Blocks {
		blocks = append(blocks, n)
	}
	sort.Strings(blocks)
	sb := toNRGBA(blockSheet)
	for _, n := range blocks {
		b := tbl.Blocks[n]
		if _, taken := srcs[n]; taken || b.Model == ModelNone {
			continue
		}
		r := image.Rect(
			int(math.Round(float64(b.Side.UV[0])*float64(tbl.Width))), int(math.Round(float64(b.Side.UV[1])*float64(tbl.Height))),
			int(math.Round(float64(b.Side.UV[2])*float64(tbl.Width))), int(math.Round(float64(b.Side.UV[3])*float64(tbl.Height))),
		)
		if r.Dx() != tile || r.Dy() != tile || !r.In(sb.Bounds()) {
			continue
		}
		img := image.NewNRGBA(image.Rect(0, 0, tile, tile))
		draw.Draw(img, img.Bounds(), sb, r.Min, draw.Src)
		srcs[n] = source{"block:" + r.String(), img}
	}

	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	slot := map[string]int{}
	var tiles []*image.NRGBA
	slotOf := func(key string, img *image.NRGBA) int {
		if i, ok := slot[key]; ok {
			return i
		}
		slot[key] = len(tiles)
		tiles = append(tiles, img)
		return slot[key]
	}
	slotOf("placeholder", placeholderTile(tile))
	assigned := make(map[string]int, len(names))
	for _, n := range names {
		assigned[n] = slotOf(srcs[n].key, srcs[n].img)
	}

	cols := int(math.Ceil(math.Sqrt(float64(len(tiles)))))
	rows := (len(tiles) + cols - 1) / cols
	width, height := cols*tile, rows*tile
	sheet := image.NewNRGBA(image.Rect(0, 0, width, height))
	rect := func(i int) Rect {
		x0, y0 := (i%cols)*tile, (i/cols)*tile
		draw.Draw(sheet, image.Rect(x0, y0, x0+tile, y0+tile), tiles[i], image.Point{}, draw.Src)
		return Rect{
			float32(x0) / float32(width), float32(y0) / float32(height),
			float32(x0+tile) / float32(width), float32(y0+tile) / float32(height),
		}
	}
	table := ItemTable{TileSize: tile, Width: width, Height: height, Items: make(map[string]Rect, len(names))}
	table.Placeholder = rect(0)
	for _, n := range names {
		table.Items[n] = rect(assigned[n])
	}
	var enc bytes.Buffer
	if err := png.Encode(&enc, sheet); err != nil {
		return nil, ItemTable{}, err
	}
	return enc.Bytes(), table, nil
}

// placeholderTile is a framed grey square, drawn so an item without a
// picture is still visibly an item.
func placeholderTile(tile int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, tile, tile))
	edge, fill := color.NRGBA{0x40, 0x40, 0x40, 255}, color.NRGBA{0x8b, 0x8b, 0x8b, 255}
	for y := 0; y < tile; y++ {
		for x := 0; x < tile; x++ {
			c := fill
			if x < 1 || y < 1 || x >= tile-1 || y >= tile-1 {
				c = edge
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// texturePaths lists every picture a texture spec names: a path, an object
// with a path, or a list of either.
func texturePaths(raw json.RawMessage) []string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return []string{str}
	}
	var obj struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Path != "" {
		return []string{obj.Path}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []string
	for _, item := range list {
		out = append(out, texturePaths(item)...)
	}
	return out
}

// namesForFile is the item names a picture's file name stands for. Files keep
// names from before the items were renamed: wood_pickaxe is wooden_pickaxe,
// gold_axe is golden_axe, bed_red is red_bed.
func namesForFile(base string) []string {
	names := []string{base}
	switch {
	case strings.HasPrefix(base, "wood_"):
		names = append(names, "wooden_"+strings.TrimPrefix(base, "wood_"))
	case strings.HasPrefix(base, "gold_"):
		names = append(names, "golden_"+strings.TrimPrefix(base, "gold_"))
	case strings.HasPrefix(base, "bed_"):
		names = append(names, strings.TrimPrefix(base, "bed_")+"_bed")
	}
	return names
}
