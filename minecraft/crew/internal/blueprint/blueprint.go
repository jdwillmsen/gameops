package blueprint

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type Block struct {
	Name   string         `json:"name"`
	States map[string]any `json:"states,omitempty"`
}

type Size struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}
type Position struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}
type Material struct {
	Item  string `json:"item"`
	Count int    `json:"count"`
}
type Check struct {
	Set      Position `json:"set"`
	Block    Block    `json:"block"`
	Expect   Position `json:"expect"`
	Expected Block    `json:"expected"`
}

type Blueprint struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Size       Size       `json:"size"`
	Palette    []Block    `json:"palette"`
	Layers     []string   `json:"layers"`
	BuildOrder []Position `json:"build_order"`
	Materials  []Material `json:"materials"`
	Checks     []Check    `json:"checks,omitempty"`
}

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_"

func indexAt(layer string, i int) int { return strings.IndexByte(alphabet, layer[i]) }

func Load(path string) (*Blueprint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bp Blueprint
	if err := json.Unmarshal(b, &bp); err != nil {
		return nil, err
	}
	// encoding/json decodes numbers as float64, but rotation and command
	// emission below must see integral states as ints.
	for i := range bp.Palette {
		normalizeStates(bp.Palette[i].States)
	}
	for i := range bp.Checks {
		normalizeStates(bp.Checks[i].Block.States)
		normalizeStates(bp.Checks[i].Expected.States)
	}
	if err := bp.Validate(); err != nil {
		return nil, err
	}
	return &bp, nil
}

// normalizeStates turns the JSON float64 encoding of an integral state back
// into an int; fractional values are left alone for Validate to reject later.
func normalizeStates(states map[string]any) {
	for k, v := range states {
		if f, ok := v.(float64); ok && f == math.Trunc(f) {
			states[k] = int(f)
		}
	}
}

func (b *Blueprint) Warnings() []string {
	var warns []string
	for _, block := range b.Palette {
		clean := strings.TrimPrefix(block.Name, "minecraft:")
		if strings.Contains(clean, "repeater") {
			warns = append(warns, fmt.Sprintf("palette entry %q: repeaters placed by console commands stay unpowered on BDS 1.26", block.Name))
			break
		}
	}
	return warns
}

func (b *Blueprint) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("name is required")
	}
	if b.Kind != "house" && b.Kind != "redstone" && b.Kind != "farm" && b.Kind != "infrastructure" {
		return fmt.Errorf("invalid kind %q", b.Kind)
	}
	if b.Size.X < 1 || b.Size.Y < 1 || b.Size.Z < 1 || b.Size.X > 48 || b.Size.Y > 48 || b.Size.Z > 48 {
		return fmt.Errorf("size axes must be 1..48")
	}
	if len(b.Layers) != b.Size.Y {
		return fmt.Errorf("got %d layers, want %d", len(b.Layers), b.Size.Y)
	}
	if len(b.Palette) == 0 || len(b.Palette) > len(alphabet) {
		return fmt.Errorf("palette must contain 1..64 blocks")
	}
	for i, block := range b.Palette {
		if block.Name == "" {
			return fmt.Errorf("palette entry %d has no block name", i)
		}
	}
	counts := map[string]int{}
	for y, layer := range b.Layers {
		if len(layer) != b.Size.X*b.Size.Z {
			return fmt.Errorf("layer %d has %d cells, want %d", y, len(layer), b.Size.X*b.Size.Z)
		}
		for i := range layer {
			p := indexAt(layer, i)
			if p < 0 || p >= len(b.Palette) {
				return fmt.Errorf("palette index %q out of range at layer %d", layer[i], y)
			}
			block := b.Palette[p]
			if block.Name != "air" && !(strings.Contains(block.Name, "door") && block.States["upper_block_bit"] == true) {
				counts[block.Name]++
			}
		}
	}
	if len(counts) == 0 {
		return fmt.Errorf("blueprint contains only air")
	}
	want := materials(counts)
	if len(want) != len(b.Materials) {
		return fmt.Errorf("materials do not match blocks")
	}
	for i := range want {
		if want[i] != b.Materials[i] {
			return fmt.Errorf("materials do not match blocks")
		}
	}
	order := map[Position]int{}
	lastY := -1
	for i, p := range b.BuildOrder {
		if p.X < 0 || p.X >= b.Size.X || p.Y < 0 || p.Y >= b.Size.Y || p.Z < 0 || p.Z >= b.Size.Z {
			return fmt.Errorf("build_order position %v is outside size", p)
		}
		if p.Y < lastY {
			return fmt.Errorf("build_order must place bottom-up layers")
		}
		lastY = p.Y
		order[p] = i
	}
	if len(order) != b.Size.X*b.Size.Y*b.Size.Z {
		return fmt.Errorf("build_order must contain every cell exactly once")
	}
	for y, layer := range b.Layers {
		for z := 0; z < b.Size.Z; z++ {
			for x := 0; x < b.Size.X; x++ {
				idx := indexAt(layer, z*b.Size.X+x)
				name := b.Palette[idx].Name
				pos := Position{x, y, z}
				if _, ok := order[pos]; !ok {
					return fmt.Errorf("build_order omits %v", pos)
				}
				if needsSupport(name) {
					support := Position{x, y - 1, z}
					if y == 0 || order[support] >= order[pos] {
						return fmt.Errorf("%s at %v is ordered before its support", name, pos)
					}
				}
			}
		}
	}
	if len(b.Checks) > 0 && b.Kind != "redstone" {
		return fmt.Errorf("checks are only valid for redstone blueprints")
	}
	for _, c := range b.Checks {
		for _, p := range []Position{c.Set, c.Expect} {
			if p.X < 0 || p.X >= b.Size.X || p.Y < 0 || p.Y >= b.Size.Y || p.Z < 0 || p.Z >= b.Size.Z {
				return fmt.Errorf("check position %v is outside size", p)
			}
		}
		if c.Block.Name == "" || c.Expected.Name == "" {
			return fmt.Errorf("checks require set and expected block names")
		}
	}
	return nil
}

func needsSupport(name string) bool {
	clean := strings.TrimPrefix(name, "minecraft:")
	if clean == "redstone_lamp" || clean == "lit_redstone_lamp" || clean == "redstone_block" {
		return false
	}
	return strings.Contains(clean, "torch") || strings.Contains(clean, "door") || clean == "ladder" || clean == "redstone_wire" || strings.Contains(clean, "repeater") || strings.Contains(clean, "comparator") || strings.Contains(clean, "button") || strings.Contains(clean, "lever") || strings.Contains(clean, "rail")
}

func materials(counts map[string]int) []Material {
	out := make([]Material, 0, len(counts))
	for k, v := range counts {
		out = append(out, Material{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item < out[j].Item })
	return out
}

func (b *Blueprint) RebuildMaterials() {
	counts := map[string]int{}
	for _, l := range b.Layers {
		for i := range l {
			idx := indexAt(l, i)
			if idx >= 0 && idx < len(b.Palette) {
				block := b.Palette[idx]
				if block.Name != "air" && !(strings.Contains(block.Name, "door") && block.States["upper_block_bit"] == true) {
					counts[block.Name]++
				}
			}
		}
	}
	b.Materials = materials(counts)
}
