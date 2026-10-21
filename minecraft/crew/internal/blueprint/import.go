package blueprint

import (
	"fmt"
	"os"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

type structureNBT struct {
	Size      []int32 `nbt:"size"`
	Structure struct {
		BlockIndices [][]int32 `nbt:"block_indices"`
		Palette      struct {
			Default struct {
				BlockPalette []struct {
					Name   string         `nbt:"name"`
					States map[string]any `nbt:"states"`
				} `nbt:"block_palette"`
			} `nbt:"default"`
		} `nbt:"palette"`
	} `nbt:"structure"`
}

func stateInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func Import(path, name, kind string) (*Blueprint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s structureNBT
	if err := nbt.UnmarshalEncoding(data, &s, nbt.LittleEndian); err != nil {
		return nil, err
	}
	if len(s.Size) != 3 || len(s.Structure.BlockIndices) == 0 {
		return nil, fmt.Errorf("structure is missing size or block_indices")
	}
	b := &Blueprint{Name: name, Kind: kind, Size: Size{int(s.Size[0]), int(s.Size[1]), int(s.Size[2])}}
	for _, p := range s.Structure.Palette.Default.BlockPalette {
		states := p.States
		// Bedrock NBT stores boolean states as bytes; *_bit keys are the
		// booleans, and they must serialize as JSON true/false, not 0 and 1.
		for k, v := range states {
			if strings.HasSuffix(k, "_bit") {
				if n, ok := stateInt(v); ok {
					states[k] = n != 0
				}
			}
		}
		b.Palette = append(b.Palette, Block{Name: strings.TrimPrefix(p.Name, "minecraft:"), States: states})
	}
	indices := s.Structure.BlockIndices[0]
	if len(indices) != b.Size.X*b.Size.Y*b.Size.Z {
		return nil, fmt.Errorf("block_indices has %d entries, want %d", len(indices), b.Size.X*b.Size.Y*b.Size.Z)
	}
	if len(b.Palette) > len(alphabet) {
		return nil, fmt.Errorf("palette has %d entries; compact layer format supports %d", len(b.Palette), len(alphabet))
	}
	b.Layers = make([]string, b.Size.Y)
	for y := 0; y < b.Size.Y; y++ {
		layer := make([]byte, b.Size.X*b.Size.Z)
		for z := 0; z < b.Size.Z; z++ {
			for x := 0; x < b.Size.X; x++ {
				// Bedrock .mcstructure layers index blocks as (x*Size.Y + y)*Size.Z + z (ZYX order).
				mcIdx := (x*b.Size.Y+y)*b.Size.Z + z
				idx := indices[mcIdx]
				if idx < 0 {
					airIdx := -1
					for pi, blk := range b.Palette {
						if blk.Name == "air" {
							airIdx = pi
							break
						}
					}
					if airIdx == -1 {
						if len(b.Palette) >= len(alphabet) {
							return nil, fmt.Errorf("palette full; cannot add air for void block")
						}
						airIdx = len(b.Palette)
						b.Palette = append(b.Palette, Block{Name: "air"})
					}
					idx = int32(airIdx)
				}
				if idx >= int32(len(b.Palette)) {
					return nil, fmt.Errorf("palette index %d out of range", idx)
				}
				layer[z*b.Size.X+x] = alphabet[idx]
			}
		}
		b.Layers[y] = string(layer)
	}
	b.RebuildMaterials()
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *Blueprint) Export(path string) error {
	var s structureNBT
	s.Size = []int32{int32(b.Size.X), int32(b.Size.Y), int32(b.Size.Z)}
	s.Structure.Palette.Default.BlockPalette = make([]struct {
		Name   string         `nbt:"name"`
		States map[string]any `nbt:"states"`
	}, len(b.Palette))
	for i, blk := range b.Palette {
		name := blk.Name
		if !strings.Contains(name, ":") {
			name = "minecraft:" + name
		}
		states := blk.States
		if states == nil {
			states = map[string]any{}
		}
		s.Structure.Palette.Default.BlockPalette[i].Name = name
		s.Structure.Palette.Default.BlockPalette[i].States = states
	}
	total := b.Size.X * b.Size.Y * b.Size.Z
	indices := make([]int32, total)
	for y := 0; y < b.Size.Y; y++ {
		layer := b.Layers[y]
		for z := 0; z < b.Size.Z; z++ {
			for x := 0; x < b.Size.X; x++ {
				idx := indexAt(layer, z*b.Size.X+x)
				mcIdx := (x*b.Size.Y+y)*b.Size.Z + z
				indices[mcIdx] = int32(idx)
			}
		}
	}
	s.Structure.BlockIndices = [][]int32{indices}
	data, err := nbt.MarshalEncoding(s, nbt.LittleEndian)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
