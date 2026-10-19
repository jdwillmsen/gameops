package camera

import (
	"encoding/json"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/atlas"
)

// The camera does not receive biome data, so tint is sampled for one climate
// everywhere: temperate, moderately wet, which is the plains biome. Grass in
// a desert or a taiga therefore has the plains colour.
const (
	defaultBiome    = "plains"
	defaultTemp     = 0.8
	defaultDownfall = 0.4
)

// atlasDoc is what the page fetches next to the PNG. The colour maps are
// sampled here, so they are dropped from the table rather than sent as
// base64.
type atlasDoc struct {
	Table atlas.Table `json:"table"`
	// Tints maps a tint kind to the #rrggbb multiplier at the default climate.
	Tints map[string]string `json:"tints"`
	Biome string            `json:"default_biome"`
}

func encodeAtlas(a *atlas.Atlas) ([]byte, error) {
	t := a.Table()
	t.Colormaps = nil
	hex := func(r, g, b uint8) string {
		const d = "0123456789abcdef"
		return string([]byte{'#', d[r>>4], d[r&15], d[g>>4], d[g&15], d[b>>4], d[b&15]})
	}
	grass := a.TintColor(atlas.TintGrass, defaultTemp, defaultDownfall)
	foliage := a.TintColor(atlas.TintFoliage, defaultTemp, defaultDownfall)
	water := t.WaterColor(defaultBiome)
	return json.Marshal(atlasDoc{
		Table: t,
		Tints: map[string]string{
			atlas.TintGrass:   hex(grass.R, grass.G, grass.B),
			atlas.TintFoliage: hex(foliage.R, foliage.G, foliage.B),
			atlas.TintWater:   hex(water.R, water.G, water.B),
		},
		Biome: defaultBiome,
	})
}

// SetAtlas makes the atlas available and tells every viewer. The atlas is
// built in the background after the server version is known, so viewers that
// connected earlier learn of it here and later ones in their connection head.
func (h *Hub) SetAtlas(a *atlas.Atlas) error {
	doc, err := encodeAtlas(a)
	if err != nil {
		return err
	}
	items, err := json.Marshal(a.Items())
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.atlasPNG, h.atlasDoc = a.PNG(), doc
	h.itemsPNG, h.itemsDoc = a.ItemPNG(), items
	if a.ItemsErr() != nil {
		h.itemsPNG, h.itemsDoc = nil, nil
	}
	h.pushOrderedLocked(atlasFrame())
	return nil
}

// atlasData returns the encoded atlas, or ok=false while there is none.
func (h *Hub) atlasData() (png, doc []byte, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.atlasPNG, h.atlasDoc, h.atlasDoc != nil
}

// itemData returns the item sprite sheet, or ok=false while there is none.
func (h *Hub) itemData() (png, doc []byte, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.itemsPNG, h.itemsDoc, h.itemsDoc != nil
}
