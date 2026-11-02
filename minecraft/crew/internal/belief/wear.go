package belief

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
)

// ArmourPiece is one worn item. DurabilityPct follows Item's rule.
type ArmourPiece struct {
	Slot          string `json:"slot"`
	Name          string `json:"name"`
	DurabilityPct *int   `json:"durability_pct,omitempty"`
}

// durabilityPct is the share of an item's wear that is left. The wire gives
// only the damage taken, so the maximum comes from the item's name; an item
// without one, or a stack whose Damage is not a plain in-range number, has
// no percentage rather than a guessed one.
func durabilityPct(name string, wire *protocol.ItemStack) *int {
	limit := playerdata.MaxDamage(name)
	if limit == 0 || wire == nil {
		return nil
	}
	damage := 0
	// A pristine stack carries no Damage tag at all.
	if raw, present := wire.NBTData["Damage"]; present {
		d, ok := wholeNumber(raw)
		if !ok || d < 0 || d > limit {
			return nil
		}
		damage = d
	}
	pct := (limit - damage) * 100 / limit
	return &pct
}

// wholeNumber reads the integer widths the NBT decoder can produce.
func wholeNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int32:
		return int(n), true
	case int16:
		return int(n), true
	case int64:
		return int(n), true
	case int8:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}
