package players

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/nbt"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	// maxRecord is far past a full inventory, an ender chest and every
	// recipe; a record over it is not read.
	maxRecord = 4 << 20
	// maxItems bounds one list of stacks: a chest holds 27 and an
	// inventory 36.
	maxItems = 64
	// maxEnchants is how many are kept on one item.
	maxEnchants = 16
	maxName     = 128
)

var errRecord = errors.New("not a player record")

type compound = map[string]any

// number reads any numeric NBT tag, which the game is not consistent in
// the width of between versions.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case uint8:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), !math.IsNaN(float64(n)) && !math.IsInf(float64(n), 0)
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return 0, false
}

func intOf(m compound, key string) (int, bool) {
	f, ok := number(m[key])
	if !ok || math.Abs(f) > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func clip(s string) string {
	if len(s) > maxName {
		s = s[:maxName]
	}
	return strings.ToValidUTF8(s, "")
}

// Decode reads one player's record. It returns the player's UniqueID with
// it, which is how a live player is found among the records.
func Decode(data []byte) (int64, Record, error) {
	var m compound
	if len(data) > maxRecord {
		return 0, Record{}, errRecord
	}
	if err := nbt.UnmarshalEncoding(data, &m, nbt.LittleEndian); err != nil {
		return 0, Record{}, fmt.Errorf("%w: %v", errRecord, err)
	}
	id, ok := m["UniqueID"].(int64)
	if !ok {
		return 0, Record{}, errRecord
	}
	r := Record{Armor: []Item{}, Inventory: []Item{}, EnderChest: []Item{}}

	attributes := map[string]Gauge{}
	for _, a := range list(m["Attributes"]) {
		a, ok := a.(compound)
		if !ok {
			continue
		}
		name, _ := a["Name"].(string)
		cur, okC := number(a["Current"])
		max, okM := number(a["Max"])
		if name != "" && okC && okM && len(attributes) < maxItems {
			attributes[strings.TrimPrefix(name, "minecraft:")] = Gauge{cur, max}
		}
	}
	gauge := func(name string) *Gauge {
		if g, ok := attributes[name]; ok {
			return &g
		}
		return nil
	}
	r.Health, r.Hunger = gauge("health"), gauge("player.hunger")
	r.Saturation, r.Absorption = gauge("player.saturation"), gauge("absorption")
	if air, ok := intOf(m, "Air"); ok {
		r.Air = &air
	}
	if level, ok := intOf(m, "PlayerLevel"); ok {
		r.Level = &level
	} else if g := gauge("player.level"); g != nil {
		level := int(g.Current)
		r.Level = &level
	}
	if p, ok := number(m["PlayerLevelProgress"]); ok {
		p = math.Min(1, math.Max(0, p))
		r.Progress = &p
	} else if g := gauge("player.experience"); g != nil {
		p := math.Min(1, math.Max(0, g.Current))
		r.Progress = &p
	}
	if mode, ok := intOf(m, "PlayerGameMode"); ok {
		r.GameMode = gameMode(mode)
	}
	if d, ok := intOf(m, "DimensionId"); ok {
		r.Dimension = chunks.Dimension(d).Name()
	}
	if pos := list(m["Pos"]); len(pos) == 3 && r.Dimension != "" {
		x, okX := number(pos[0])
		y, okY := number(pos[1])
		z, okZ := number(pos[2])
		if okX && okY && okZ {
			r.Saved = &Place{int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)), r.Dimension}
		}
	}

	r.Armor = items(m["Armor"], false)
	r.Inventory = items(m["Inventory"], true)
	r.EnderChest = items(m["EnderChestInventory"], true)
	if held := items(m["Mainhand"], false); len(held) > 0 {
		r.Mainhand = &held[0]
	}
	if held := items(m["Offhand"], false); len(held) > 0 {
		r.Offhand = &held[0]
	}

	if died, _ := number(m["HasDiedBefore"]); died != 0 {
		r.Death = place(m, "DeathPosition", "DeathDimension")
	}
	r.Spawn = place(m, "SpawnBlockPosition", "SpawnDimension")

	if rec, ok := m["recipe_unlocking"].(compound); ok {
		r.RecipesUnlocked = len(list(rec["unlocked_recipes"]))
	}
	if seen, _ := number(m["HasSeenCredits"]); seen != 0 {
		r.SeenCredits = true
	}
	if w, ok := intOf(m, "WardenThreatLevel"); ok {
		r.WardenThreat = &w
	}
	return id, r, nil
}

func gameMode(n int) string {
	switch n {
	case 0:
		return "survival"
	case 1:
		return "creative"
	case 2:
		return "adventure"
	case 6:
		return "spectator"
	}
	// 5 is the world's default, which the save does not resolve.
	return "default"
}

// place reads a block position written as three int tags with a prefix and
// an axis letter. The game writes the lowest int where there is none.
func place(m compound, prefix, dimension string) *Place {
	x, okX := intOf(m, prefix+"X")
	y, okY := intOf(m, prefix+"Y")
	z, okZ := intOf(m, prefix+"Z")
	if !okX || !okY || !okZ {
		return nil
	}
	raw := func(key string) int64 { v, _ := m[key].(int32); return int64(v) }
	if raw(prefix+"X") == math.MinInt32 || raw(prefix+"Y") == math.MinInt32 || raw(prefix+"Z") == math.MinInt32 {
		return nil
	}
	d, ok := intOf(m, dimension)
	if !ok {
		d = 0
	}
	return &Place{x, y, z, chunks.Dimension(d).Name()}
}

// items reads a list of stacks. Empty slots are left out; a list kept by
// slot (an inventory) says which each is, and one that is not (what is
// worn) is numbered by position.
func items(v any, bySlot bool) []Item {
	out := []Item{}
	for i, e := range list(v) {
		if len(out) >= maxItems {
			break
		}
		it, ok := e.(compound)
		if !ok {
			continue
		}
		name, _ := it["Name"].(string)
		name = strings.TrimPrefix(name, "minecraft:")
		count, _ := intOf(it, "Count")
		if name == "" || count <= 0 {
			continue
		}
		item := Item{Slot: i, Name: clip(name), Count: count}
		if bySlot {
			if s, ok := intOf(it, "Slot"); ok {
				item.Slot = s
			}
		}
		if tag, ok := it["tag"].(compound); ok {
			item.Damage, _ = intOf(tag, "Damage")
			if item.Damage > 0 {
				item.MaxDamage = maxDamage[name]
			}
			if display, ok := tag["display"].(compound); ok {
				if custom, ok := display["Name"].(string); ok {
					item.Custom = clip(custom)
				}
			}
			for _, e := range list(tag["ench"]) {
				e, ok := e.(compound)
				if !ok || len(item.Enchants) >= maxEnchants {
					continue
				}
				id, okI := intOf(e, "id")
				level, okL := intOf(e, "lvl")
				if okI && okL {
					item.Enchants = append(item.Enchants, Enchantment{ID: id, Name: enchantments[id], Level: level})
				}
			}
		}
		out = append(out, item)
	}
	return out
}
