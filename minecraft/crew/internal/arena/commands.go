package arena

import (
	"fmt"
	"strconv"
	"unicode"
)

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

	// variantTagRadius is tighter than spawnRadius: without a type filter the
	// claim must not reach a mob from a neighbouring pen.
	variantTagRadius = 2
)

func countCommand() string { return "testfor " + countSelector }

func bossCountCommand() string { return "testfor " + bossSelector }

func warriorCommand(name string) (string, error) {
	if err := validateWarriorName(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("testfor @e[name=%q,type=player]", name), nil
}

// validateWarriorName keeps the selector argument a quoted literal: an
// unescaped quote or control character could end the name and inject a
// selector of its own.
func validateWarriorName(name string) error {
	if name == "" {
		return fmt.Errorf("arena: warrior name is empty")
	}
	for _, r := range name {
		if r == '"' || r == '\\' || unicode.IsControl(r) {
			return fmt.Errorf("arena: warrior name %q has a quote, backslash or control character", name)
		}
	}
	return nil
}

// maxFill is Bedrock's per-command block limit for /fill.
const maxFill = 32768

// floorRepairBoxes are the halves the floor repair fills; each must stay under
// maxFill.
func floorRepairBoxes(l Layout) []Box {
	r := l.FloorRadius
	if r <= 0 {
		return nil
	}
	x0, x1 := l.Center.X-r, l.Center.X+r
	z0, z1 := l.Center.Z-r, l.Center.Z+r
	y0, y1 := l.GroundY-3, l.GroundY
	zm := l.Center.Z
	return []Box{
		{X0: x0, Y0: y0, Z0: z0, X1: x1, Y1: y1, Z1: zm},
		{X0: x0, Y0: y0, Z0: zm + 1, X1: x1, Y1: y1, Z1: z1},
	}
}

// floorRepairCommands refill air in the floor's top layers. Charged creepers
// blast craters the squad and the mobs then fall into, which breaks pathing
// and leaves survivors out of reach. Two fills keep each under the 32768-block
// limit; only air is replaced, so nothing standing is touched.
func floorRepairCommands(l Layout) []string {
	boxes := floorRepairBoxes(l)
	if boxes == nil {
		return nil
	}
	cmds := make([]string, 0, len(boxes))
	for _, b := range boxes {
		cmds = append(cmds, fmt.Sprintf("fill %d %d %d %d %d %d sand replace air",
			b.X0, b.Y0, b.Z0, b.X1, b.Y1, b.Z1))
	}
	return cmds
}

func killWaveCommand() string { return "kill @e[tag=" + waveTag + "]" }

// squadOffsets spreads a squad across the centre line at run start. They stay
// far inside the fighting floor, so no warrior starts in the stands.
var squadOffsets = []int{0, 3, -3, 6, -6}

// squadStartPoint is the cell a warrior is placed on before wave one and
// returned to whenever it respawns or strays off the floor.
func squadStartPoint(l Layout, i int) Point {
	offset := squadOffsets[i%len(squadOffsets)]
	return Point{X: l.Center.X + offset, Y: l.GroundY + 1, Z: l.Center.Z - 25}
}

func squadPlaceCommand(name string, p Point) (string, error) {
	if err := validateWarriorName(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("tp %q %d %d %d", name, p.X, p.Y, p.Z), nil
}

// rallyOffsets is the ring survivors are pulled to, one round at a time. Every
// offset is 6-9 blocks out: close enough for the warriors' reach, far enough
// that twelve piled mobs do not share a cell.
var rallyOffsets = [][2]int{{8, 0}, {-8, 0}, {0, 8}, {0, -8}, {6, 6}, {-6, 6}, {6, -6}, {-6, -6}}

// rallyQueryCommand reads where the anchor actually stands; offsets are only
// safe once they can be measured against the floor centre.
func rallyQueryCommand(anchor string) (string, error) {
	if err := validateWarriorName(anchor); err != nil {
		return "", err
	}
	return fmt.Sprintf("querytarget @a[name=%q]", anchor), nil
}

// rallyCommand teleports only the wave's own tagged mobs to a fixed offset from
// the anchor. The caller has already checked where that offset lands.
func rallyCommand(anchor string, dx, dz int) (string, error) {
	if err := validateWarriorName(anchor); err != nil {
		return "", err
	}
	return fmt.Sprintf("execute as @e[tag=%s] at @a[name=%q] run tp @s ~%d ~ ~%d", waveTag, anchor, dx, dz), nil
}

// rallyAbsoluteCommand moves only the wave's own tagged mobs to a checked point.
func rallyAbsoluteCommand(to TargetPosition) string {
	return fmt.Sprintf("execute as @e[tag=%s] run tp @s %s %s %s", waveTag,
		formatCoord(to.X), formatCoord(to.Y), formatCoord(to.Z))
}

func formatCoord(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

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
	// Kit spawns the mob from the lab's kit catalog, already wearing its
	// tier's enchanted and trimmed gear, instead of summoning it bare.
	Kit bool
}

// kitMobs are the mob types the lab's kit catalog dresses (`function
// mobs/<type>/t<tier>`, tiers 1-6).
var kitMobs = map[string]bool{
	"zombie": true, "husk": true, "drowned": true, "skeleton": true,
	"stray": true, "wither_skeleton": true, "piglin": true,
}

// kitFits says whether a wave mob can come from the catalog. A spawn event
// can change the entity and a name has no place in the catalog's structure,
// so variants and named mobs keep the summon.
func (s spawn) kitFits() bool {
	return kitMobs[s.Type] && s.Variant == "" && s.Name == "" && s.Tier >= 1 && s.Tier <= 6
}

// radiusTags are the tags every wave mob gets. The per-mob marker is added
// first so effects, gear and the enchant hit exactly that mob; the wave and
// tier tags then make it visible to the count and to status.
func (s spawn) commands(recipe Tier) []string {
	place := s.summonCommand()
	if s.Kit {
		place = fmt.Sprintf("execute positioned %d %d %d run function mobs/%s/t%d", s.Point.X, s.Point.Y, s.Point.Z, s.Type, s.Tier)
	}
	cmds := []string{
		place,
		s.tagCommand(markerTag),
		"tag @e[tag=" + markerTag + "] add " + waveTag,
		fmt.Sprintf("tag @e[tag=%s] add %s_t%d", markerTag, waveTag, s.Tier),
	}
	if s.Boss {
		cmds = append(cmds, "tag @e[tag="+markerTag+"] add "+bossTag)
	}
	for _, e := range recipe.Effects {
		cmds = append(cmds, fmt.Sprintf("effect give @e[tag=%s] %s %d %d", markerTag, e.ID, e.Seconds, e.Amplifier))
	}
	if s.Kit {
		// The catalog mob already wears its gear; replaceitem would swap it
		// for plain items.
		return append(cmds, "tag @e[tag="+markerTag+"] remove "+markerTag)
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

// tagCommand claims the mob just summoned. A spawn event can replace the
// entity type (become_drowned), so a variant is claimed by position alone;
// the type filter would miss it and leave the wave uncounted.
func (s spawn) tagCommand(tag string) string {
	if s.Variant == "" {
		return tagAtCommand(s.Point, s.Type, tag)
	}
	return fmt.Sprintf("execute positioned %d %d %d run tag @e[type=!player,type=!item,tag=!%s,tag=!%s,c=1,r=%d] add %s",
		s.Point.X, s.Point.Y, s.Point.Z, tag, waveTag, variantTagRadius, tag)
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
