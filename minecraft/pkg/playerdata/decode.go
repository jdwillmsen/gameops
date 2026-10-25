package playerdata

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

const (
	// maxRecord is far past a full inventory, an ender chest and every
	// recipe; a record over it is not read.
	maxRecord = 1 << 20
	// maxItems bounds one list of stacks: a chest holds 27 and an
	// inventory 36.
	maxItems = 64
	// maxEnchants is how many are kept on one item.
	maxEnchants = 16
	maxName     = 128
	// maxNesting is how many containers deep a stack's contents are read.
	// The game does not let a shulker box into another, so more than one
	// level is only a save that was made by hand; the page opens as many.
	maxNesting = 3
	// maxContained bounds every stack read out of other stacks in one record.
	// All 63 inventory and ender chest slots as full shulker boxes come to
	// 1,701; the rest is a save that was made to be large.
	maxContained = 2048
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
	if err := guard(data); err != nil {
		return 0, Record{}, err
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
		r.Dimension = dimensionName(d)
	}
	if pos := list(m["Pos"]); len(pos) == 3 && r.Dimension != "" {
		x, okX := number(pos[0])
		y, okY := number(pos[1])
		z, okZ := number(pos[2])
		if okX && okY && okZ {
			r.Saved = &Place{int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)), r.Dimension}
		}
	}

	read := &reader{spare: maxContained}
	r.Inventory = read.items(m["Inventory"], true, true, 0)
	r.EnderChest = read.items(m["EnderChestInventory"], true, true, 0)
	r.Armor = read.items(m["Armor"], false, false, 0)
	if held := read.items(m["Mainhand"], false, false, 0); len(held) > 0 {
		r.Mainhand = &held[0]
	}
	if held := read.items(m["Offhand"], false, false, 0); len(held) > 0 {
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

// dimensionName is Bedrock's own number for a dimension, named as the map
// does. The save writes the number; a reader wants a word.
func dimensionName(d int) string {
	switch d {
	case 0:
		return "overworld"
	case 1:
		return "nether"
	case 2:
		return "end"
	}
	return fmt.Sprintf("dimension-%d", d)
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
	return &Place{x, y, z, dimensionName(d)}
}

// reader reads lists of stacks for one record, and holds how many more
// stacks it may read out of the contents of others.
type reader struct {
	spare int
}

// isContainer is whether a stack of this item keeps what is inside it in its
// own tag: a shulker box of any colour.
func isContainer(name string) bool {
	return strings.HasSuffix(name, "shulker_box")
}

// items reads a list of stacks. Empty slots are left out; a list kept by
// slot (an inventory) says which each is, and one that is not (what is
// worn) is numbered by position. depth is how many containers the list is
// inside. A box is opened only where opens is set: the page has no way into
// one that is worn or held, and reading them would let a save made by hand
// spend the allowance before the inventory is read.
func (r *reader) items(v any, bySlot, opens bool, depth int) []Item {
	out := []Item{}
	for i, e := range list(v) {
		if len(out) >= maxItems {
			break
		}
		// A stack inside another is paid for out of the record's allowance,
		// so that boxes of boxes cannot multiply a small record into a
		// large answer.
		if depth > 0 && r.spare <= 0 {
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
		if depth > 0 {
			r.spare--
		}
		// A pristine tool has no Damage in its save, but its maximum is still
		// what a reader needs to show it as full.
		item := Item{Slot: i, Name: clip(name), Count: count, MaxDamage: maxDamage[name]}
		if bySlot {
			if s, ok := intOf(it, "Slot"); ok {
				item.Slot = s
			}
		}
		if tag, ok := it["tag"].(compound); ok {
			item.Damage, _ = intOf(tag, "Damage")
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
			if opens && isContainer(name) && depth < maxNesting {
				if inside := r.items(tag["Items"], true, true, depth+1); len(inside) > 0 {
					item.Contents = inside
				}
			}
		}
		out = append(out, item)
	}
	return out
}
