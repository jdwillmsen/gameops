package arena

import "fmt"

const (
	waveTag   = "arena_wave"
	markerTag = "arena_tiered"
	bossTag   = "arena_boss"

	// Items never inherit tags, but the negation keeps a drop that happens to
	// carry one out of the survivor count.
	countSelector = "@e[tag=" + waveTag + ",type=!item]"
	bossSelector  = "@e[tag=" + bossTag + ",type=!item]"

	// spawnRadius bounds the after-summon tag to the pen or dais the mob was
	// summoned onto, so a neighbour in another pen is never claimed.
	spawnRadius = 4
)

func countCommand() string { return "testfor " + countSelector }

func bossCountCommand() string { return "testfor " + bossSelector }

func warriorCommand(name string) string {
	return fmt.Sprintf("testfor @e[name=%s,type=player]", name)
}

func killWaveCommand() string { return "kill @e[tag=" + waveTag + "]" }

func penCloseCommand(p Pen) string { return p.Gate.Fill("iron_bars") }

// tickingAreaName scopes the run's areas so a build's crew-arena-* areas are
// never removed by a director that did not add them.
func tickingAreaName(i int) string { return fmt.Sprintf("crew-arena-run-%d", i) }

func tickingAddCommand(i int, a Area) string {
	return fmt.Sprintf("tickingarea add %d 0 %d %d 0 %d %s", a.X0, a.Z0, a.X1, a.Z1, tickingAreaName(i))
}

func tickingRemoveCommand(i int) string {
	return "tickingarea remove " + tickingAreaName(i)
}

// spawn is one mob of a wave, already placed at its pen or at the dais.
type spawn struct {
	Type    string
	Name    string
	Variant string
	Tier    int
	Boss    bool
	Point   Point
}

// radiusTags are the tags every wave mob gets. The per-mob marker is added
// first so effects, gear and the enchant hit exactly that mob; the wave and
// tier tags then make it visible to the count and to status.
func (s spawn) commands(recipe Tier) []string {
	cmds := []string{
		s.summonCommand(),
		tagAtCommand(s.Point, s.Type, markerTag),
		"tag @e[tag=" + markerTag + "] add " + waveTag,
		fmt.Sprintf("tag @e[tag=%s] add %s_t%d", markerTag, waveTag, s.Tier),
	}
	if s.Boss {
		cmds = append(cmds, "tag @e[tag="+markerTag+"] add "+bossTag)
	}
	for _, e := range recipe.Effects {
		cmds = append(cmds, fmt.Sprintf("effect give @e[tag=%s] %s %d %d", markerTag, e.ID, e.Seconds, e.Amplifier))
	}
	for _, g := range recipe.Gear {
		cmds = append(cmds, fmt.Sprintf("replaceitem entity @e[tag=%s] slot.%s 0 %s 1", markerTag, trimSlot(g.Slot), g.Item))
	}
	if recipe.Enchant != nil && hasMainhand(recipe) {
		cmds = append(cmds, fmt.Sprintf("enchant @e[tag=%s] %s %d", markerTag, recipe.Enchant.ID, recipe.Enchant.Level))
	}
	return append(cmds, "tag @e[tag="+markerTag+"] remove "+markerTag)
}

func (s spawn) summonCommand() string {
	at := fmt.Sprintf("execute positioned %d %d %d run summon %s", s.Point.X, s.Point.Y, s.Point.Z, s.Type)
	switch {
	case s.Variant != "" && s.Name != "":
		// Rotations are mandatory before the spawn-event token.
		return fmt.Sprintf("%s ~ ~ ~ 0 0 %s %q", at, s.Variant, s.Name)
	case s.Variant != "":
		return fmt.Sprintf("%s ~ ~ ~ 0 0 %s", at, s.Variant)
	case s.Name != "":
		return fmt.Sprintf("%s %q ~ ~ ~", at, s.Name)
	default:
		return at + " ~ ~ ~"
	}
}

// tagAtCommand claims the mob just summoned. Wave-tagged mobs are excluded as
// well as marker-tagged ones because the marker is removed from each mob
// before the next summon.
func tagAtCommand(p Point, mob, tag string) string {
	return fmt.Sprintf("execute positioned %d %d %d run tag @e[type=%s,tag=!%s,tag=!%s,c=1,r=%d] add %s",
		p.X, p.Y, p.Z, mob, tag, waveTag, spawnRadius, tag)
}

func trimSlot(slot string) string {
	if len(slot) > len("slot.") && slot[:len("slot.")] == "slot." {
		return slot[len("slot."):]
	}
	return slot
}

func hasMainhand(t Tier) bool {
	for _, g := range t.Gear {
		if trimSlot(g.Slot) == "weapon.mainhand" {
			return true
		}
	}
	return false
}
