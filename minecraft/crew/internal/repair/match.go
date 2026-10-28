package repair

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ignoredStates are block states that change while the arena is used and are
// not damage: a pressed button, a toggled door or lever, a redstone signal.
// A plan that names one is not held to it.
var ignoredStates = map[string]bool{
	"open_bit": true, "button_pressed_bit": true, "redstone_signal": true,
	"powered_bit": true, "output_lit_bit": true, "output_subtract_bit": true,
}

// aliases fold the lit or powered twin of a block into the block the plan
// names, so the lamp ring in mid-ripple is not reported as replaced.
var aliases = map[string]string{
	"lit_redstone_lamp": "redstone_lamp", "powered_repeater": "unpowered_repeater",
	"powered_comparator": "unpowered_comparator", "lit_furnace": "furnace", "lit_blast_furnace": "blast_furnace",
	"lit_smoker": "smoker", "lit_redstone_ore": "redstone_ore",
	"bubble_column": "water", "iron_chain": "chain",
}

type variant struct {
	text   string
	states map[string]string
}

// Matcher names a scanned block the way the plan spells it, so a scan from a
// world copy and a scan from a unit's chunks fold a toggled door or a lit lamp
// into the planned block in the same way.
type Matcher struct {
	variants map[string][]variant // block name -> the plan's spellings of it
}

// NewMatcher learns the spellings plan uses; without a plan every block keeps
// its full states.
func NewMatcher(plan []Op) *Matcher {
	m := &Matcher{variants: map[string][]variant{}}
	seen := map[string]bool{}
	for _, op := range plan {
		if seen[op.Block] {
			continue
		}
		seen[op.Block] = true
		name, states := splitBlock(op.Block)
		m.variants[name] = append(m.variants[name], variant{text: op.Block, states: states})
	}
	for _, vs := range m.variants {
		// The most specific spelling wins, so a plain `chest` never shadows a
		// plan that also names the chest's facing.
		sort.SliceStable(vs, func(i, j int) bool { return len(vs[i].states) > len(vs[j].states) })
	}
	return m
}

// RenderStates writes block states the way a plan spells them: bits as
// true or false, text quoted, numbers plain.
func RenderStates(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case uint8:
			out[k] = strconv.FormatBool(t != 0)
		case int8:
			out[k] = strconv.FormatBool(t != 0)
		case string:
			out[k] = strconv.Quote(t)
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
}

// Canonical names a scanned block the way the plan would: the plan's own
// spelling when one of them is a subset of the scan, otherwise the full
// sorted states, which will not equal any planned block.
func (m *Matcher) Canonical(name string, states map[string]string) string {
	name = strings.TrimPrefix(name, "minecraft:")
	if a, ok := aliases[name]; ok {
		name = a
	}
	for _, v := range m.variants[name] {
		match := true
		for k, want := range v.states {
			if ignoredStates[k] {
				continue
			}
			if got, ok := states[k]; !ok || got != want {
				match = false
				break
			}
		}
		if match {
			return v.text
		}
	}
	if len(states) == 0 {
		return name
	}
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = strconv.Quote(k) + "=" + states[k]
	}
	return name + " [" + strings.Join(parts, ",") + "]"
}

// splitBlock separates a normalised block string into its name and states.
func splitBlock(block string) (string, map[string]string) {
	name, rest, ok := strings.Cut(block, " [")
	if !ok {
		return block, nil
	}
	rest = strings.TrimSuffix(rest, "]")
	states := map[string]string{}
	var key, val strings.Builder
	inQuote, inVal := false, false
	flush := func() {
		if key.Len() > 0 {
			k, _ := strconv.Unquote(key.String())
			states[k] = val.String()
		}
		key.Reset()
		val.Reset()
		inVal = false
	}
	for _, c := range rest {
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == '=' && !inQuote && !inVal:
			inVal = true
			continue
		case c == ',' && !inQuote:
			flush()
			continue
		}
		if inVal {
			val.WriteRune(c)
		} else {
			key.WriteRune(c)
		}
	}
	flush()
	return name, states
}
