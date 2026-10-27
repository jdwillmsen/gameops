package atlas

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"sync"
)

// Tint kinds. The pack does not mark tinted blocks, so they come from the
// name rules in shapes.go.
const (
	TintNone    = "none"
	TintGrass   = "grass"
	TintFoliage = "foliage"
	TintWater   = "water"
)

// Model kinds. Anything other than a cube is built by the game engine rather
// than described by the pack, so the camera draws a simple stand-in.
const (
	ModelCube   = "cube"
	ModelSlab   = "slab"
	ModelStairs = "stairs"
	ModelTorch  = "torch"
	ModelCross  = "cross"
	ModelFence  = "fence"
	ModelWall   = "wall"
	ModelPane   = "pane"
	ModelLayer  = "layer"
	ModelDoor   = "door"
	ModelLiquid = "liquid"
	ModelNone   = "none"
)

// Rect is a texture rectangle in normalised atlas coordinates: u0, v0, u1, v1
// with the origin at the top left.
type Rect [4]float32

// Face is one side of a block.
type Face struct {
	UV Rect `json:"uv"`
	// Tinted reports whether the block's tint colour multiplies this face.
	Tinted bool `json:"tinted,omitempty"`
	// Overlay is the pack's fixed tint colour for this texture, as #rrggbb.
	Overlay string `json:"overlay,omitempty"`
}

// Block describes how to draw one block. Side is the default for the four
// horizontal faces; North, South, East and West carry the resolved result.
type Block struct {
	Up    Face   `json:"up"`
	Down  Face   `json:"down"`
	Side  Face   `json:"side"`
	North Face   `json:"north"`
	South Face   `json:"south"`
	East  Face   `json:"east"`
	West  Face   `json:"west"`
	Tint  string `json:"tint"`
	Model string `json:"model"`
	// Material is the class used to group colour fallbacks.
	Material string `json:"material"`
	// Color is the average block colour as #rrggbb, for distant or untextured drawing.
	Color string `json:"color"`
}

// The camera has no biome data, so grass is coloured for one climate: temperate
// and moderately wet, the plains biome.
const (
	DefaultTemperature = 0.8
	DefaultDownfall    = 0.4
)

// tableVersion invalidates cached builds when the table layout changes.
const tableVersion = 2

// Table is the serialisable lookup the camera loads alongside the atlas PNG.
type Table struct {
	Version  int `json:"version"`
	TileSize int `json:"tile_size"`
	Width    int `json:"width"`
	Height   int `json:"height"`
	// Blocks is keyed by full block name, e.g. "minecraft:stone".
	Blocks map[string]Block `json:"blocks"`
	// Materials maps a material class to its fallback colour (#rrggbb).
	Materials map[string]string `json:"materials"`
	// WaterColors maps a biome name to its water surface colour (#rrggbb).
	WaterColors map[string]string `json:"water_colors"`
	// WaterAlphas maps a biome name to how opaque its water surface is, 0 to 1.
	WaterAlphas map[string]float64 `json:"water_alphas,omitempty"`
	// Colormaps holds the grass and foliage climate lookup images as PNG bytes.
	Colormaps map[string][]byte `json:"colormaps,omitempty"`
	// Unmapped lists texture keys the pack references but could not supply.
	Unmapped []string `json:"unmapped,omitempty"`
	// Formats counts the texture files read, by file format.
	Formats map[string]int `json:"formats"`
}

// Atlas is a built texture atlas and its lookup table.
type Atlas struct {
	png   []byte
	table Table

	itemPNG []byte
	items   ItemTable
	// itemsErr is why there is no item sheet; the block atlas is usable
	// without one.
	itemsErr error

	entPNG   []byte
	entities EntityTable
	// entitiesErr is why there are no entity models, which only costs the
	// camera its mob models.
	entitiesErr error
	// entitiesFailed is set on a loaded entry whose entity build was tried
	// and failed, so it is not rebuilt for that alone.
	entitiesFailed bool
	// entitiesOld is set on a loaded entry whose entity table predates the
	// current layout: usable, but without what the newer one added.
	entitiesOld bool

	cmOnce sync.Once
	cms    map[string]image.Image
}

// Complete reports whether the atlas holds everything the current build
// makes. One that does not is still served, and rebuilt when the pack can be
// fetched.
func (a *Atlas) Complete() bool {
	return a.itemsErr == nil && ((a.entitiesErr == nil && !a.entitiesOld) || a.entitiesFailed)
}

// PNG returns the encoded atlas image.
func (a *Atlas) PNG() []byte { return a.png }

// Table returns the block lookup table.
func (a *Atlas) Table() Table { return a.table }

// FallbackColor returns a #rrggbb colour for a block, using its own average
// colour when textured and its material class otherwise.
func (t Table) FallbackColor(name string) string {
	if b, ok := t.Blocks[name]; ok && b.Color != "" {
		return b.Color
	}
	if c, ok := t.Materials[fallbackClass(name)]; ok {
		return c
	}
	return t.Materials["default"]
}

// WaterColor returns the water surface colour for a biome, falling back to the
// pack default.
func (t Table) WaterColor(biome string) color.NRGBA {
	for _, key := range biomeKeys(biome) {
		if c, ok := parseHex(t.WaterColors[key]); ok {
			return c
		}
	}
	return color.NRGBA{0x44, 0xaf, 0xf5, 255}
}

// DefaultWaterAlpha is the opacity of water in a pack that does not say.
const DefaultWaterAlpha = 0.65

// WaterAlpha returns how opaque a biome's water surface is.
func (t Table) WaterAlpha(biome string) float64 {
	for _, key := range biomeKeys(biome) {
		if a, ok := t.WaterAlphas[key]; ok && a > 0 && a <= 1 {
			return a
		}
	}
	return DefaultWaterAlpha
}

// biomeKeys lists the spellings a pack may key a biome by, then its default.
func biomeKeys(biome string) []string {
	return []string{biome, "minecraft:" + biome, "default"}
}

// TintColor samples the grass or foliage colour map for a climate. Other kinds
// (and a missing colour map) yield white, i.e. no tint. Water is per biome:
// use Table.WaterColor.
func (a *Atlas) TintColor(kind string, temperature, downfall float64) color.NRGBA {
	white := color.NRGBA{255, 255, 255, 255}
	if kind != TintGrass && kind != TintFoliage {
		return white
	}
	a.cmOnce.Do(func() {
		a.cms = map[string]image.Image{}
		for name, data := range a.table.Colormaps {
			if img, err := png.Decode(bytes.NewReader(data)); err == nil {
				a.cms[name] = img
			}
		}
	})
	img, ok := a.cms[kind]
	if !ok {
		return white
	}
	temperature = min(max(temperature, 0), 1)
	downfall = min(max(downfall, 0), 1) * temperature
	b := img.Bounds()
	x := b.Min.X + int((1-temperature)*float64(b.Dx()-1)+0.5)
	y := b.Min.Y + int((1-downfall)*float64(b.Dy()-1)+0.5)
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func hex(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func parseHex(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, true
}
