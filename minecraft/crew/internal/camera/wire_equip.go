package camera

import (
	"encoding/binary"
	"slices"
)

// Equipment frames. Both are optional and replace what was known for each id
// they name; id 0 is the unit's own body. Pages that predate them skip them.
//
//	equipment     [21][n uvarint]{id uvarint, 2 x {flags u8, len u8, item}}   main hand, then off hand
//	armour detail [22][n uvarint]{id uvarint, 4 x {flags u8, [len u8, pattern, len u8, material]}}   helmet first
//
// Flag bit 0 marks an enchanted item (it glints). In an armour detail slot,
// bit 1 says a trim follows: its pattern and material names without
// namespace. The names of worn armour itself stay in the armour frame.
const (
	frameEquipment   byte = 21
	frameArmourExtra byte = 22

	itemGlint byte = 1
	itemTrim  byte = 2
)

// handItem is what a hand holds.
type handItem struct {
	Name  string
	Glint bool
}

// handSlots is main hand then off hand.
type handSlots [2]handItem

func (s handSlots) zero() bool { return s == handSlots{} }

// Main is the main-hand item.
func (s handSlots) Main() handItem { return s[0] }

// armourMod is the part of a worn piece the armour frame's name does not say.
type armourMod struct {
	Glint             bool
	Pattern, Material string
}

type armourMods [4]armourMod

func (m armourMods) zero() bool { return m == armourMods{} }

func lenPrefixed(f []byte, s string) []byte {
	if len(s) > maxNameLen {
		s = ""
	}
	f = append(f, uint8(len(s)))
	return append(f, s...)
}

func sortedIDs[V any](m map[uint64]V) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func encodeEquipment(held map[uint64]handSlots) []byte {
	f := binary.AppendUvarint([]byte{frameEquipment}, uint64(len(held)))
	for _, id := range sortedIDs(held) {
		f = binary.AppendUvarint(f, id)
		for _, it := range held[id] {
			var flags byte
			if it.Glint {
				flags |= itemGlint
			}
			f = append(f, flags)
			f = lenPrefixed(f, it.Name)
		}
	}
	return f
}

func encodeArmourExtra(mods map[uint64]armourMods) []byte {
	f := binary.AppendUvarint([]byte{frameArmourExtra}, uint64(len(mods)))
	for _, id := range sortedIDs(mods) {
		f = binary.AppendUvarint(f, id)
		for _, m := range mods[id] {
			var flags byte
			if m.Glint {
				flags |= itemGlint
			}
			trimmed := m.Pattern != "" && m.Material != ""
			if trimmed {
				flags |= itemTrim
			}
			f = append(f, flags)
			if trimmed {
				f = lenPrefixed(f, m.Pattern)
				f = lenPrefixed(f, m.Material)
			}
		}
	}
	return f
}
