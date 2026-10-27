package arena

import (
	"fmt"
	"strings"
	"testing"
)

func testSpawn() spawn {
	return spawn{Type: "zombie", Tier: 1, Point: Point{X: 10, Y: 65, Z: -760}}
}

func TestTierRecipesMatchTheSpikeLadder(t *testing.T) {
	book, err := DefaultWaves()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		tier int
		want []string
	}{
		{1, []string{
			"effect @e[tag=arena_tiered] strength 600 0",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 stone_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 1",
		}},
		{2, []string{
			"effect @e[tag=arena_tiered] resistance 600 1",
			"effect @e[tag=arena_tiered] health_boost 600 2",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 iron_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 3",
		}},
		{3, []string{
			"effect @e[tag=arena_tiered] resistance 600 1",
			"effect @e[tag=arena_tiered] health_boost 600 4",
			"replaceitem entity @e[tag=arena_tiered] slot.armor.chest 0 iron_chestplate 1",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 iron_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 3",
		}},
		{4, []string{
			"effect @e[tag=arena_tiered] health_boost 600 7",
			"effect @e[tag=arena_tiered] resistance 600 3",
			"effect @e[tag=arena_tiered] regeneration 600 5",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 diamond_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 5",
		}},
		{5, []string{
			"effect @e[tag=arena_tiered] absorption 600 4",
			"effect @e[tag=arena_tiered] strength 600 2",
			"replaceitem entity @e[tag=arena_tiered] slot.armor.head 0 diamond_helmet 1",
			"enchant @e[tag=arena_tiered] sharpness 5",
		}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("tier %d", tt.tier), func(t *testing.T) {
			s := testSpawn()
			s.Tier = tt.tier
			cmds := s.commands(book.Tiers[tt.tier])
			for _, want := range tt.want {
				if !hasCommand(cmds, want) {
					t.Errorf("tier %d commands lack %q in\n%s", tt.tier, want, strings.Join(cmds, "\n"))
				}
			}
			enchants := 0
			for _, cmd := range cmds {
				if strings.HasPrefix(cmd, "enchant ") {
					enchants++
				}
			}
			if enchants != 1 {
				t.Errorf("tier %d has %d enchant commands, want only the mainhand one", tt.tier, enchants)
			}
		})
	}
}

func TestSpawnCommandForms(t *testing.T) {
	base := "execute positioned 10 65 -760 run summon zombie"
	tests := []struct {
		name string
		sp   spawn
		want string
	}{
		{"plain", testSpawn(), base + " ~ ~ ~"},
		{"named", spawn{Type: "zombie", Name: "Grunt", Point: Point{10, 65, -760}},
			base + ` "Grunt" ~ ~ ~`},
		{"variant needs rotations", spawn{Type: "zombie", Variant: "minecraft:become_drowned", Point: Point{10, 65, -760}},
			base + " ~ ~ ~ 0 0 minecraft:become_drowned"},
		{"variant and name", spawn{Type: "zombie", Variant: "minecraft:become_drowned", Name: "Drowned", Point: Point{10, 65, -760}},
			base + ` ~ ~ ~ 0 0 minecraft:become_drowned "Drowned"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sp.summonCommand(); got != tt.want {
				t.Errorf("summonCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpawnTagsEveryWaveMob(t *testing.T) {
	cmds := testSpawn().commands(Tier{})
	want := []string{
		"execute positioned 10 65 -760 run tag @e[type=zombie,tag=!arena_tiered,tag=!arena_wave,c=1,r=4] add arena_tiered",
		"tag @e[tag=arena_tiered] add arena_wave",
		"tag @e[tag=arena_tiered] add arena_wave_t1",
		"tag @e[tag=arena_tiered] remove arena_tiered",
	}
	for _, w := range want {
		if !hasCommand(cmds, w) {
			t.Errorf("commands lack %q:\n%s", w, strings.Join(cmds, "\n"))
		}
	}
	if hasCommand(cmds, "add "+bossTag) {
		t.Error("an ordinary mob got the boss tag")
	}

	boss := testSpawn()
	boss.Boss = true
	if !hasCommand(boss.commands(Tier{}), "tag @e[tag=arena_tiered] add arena_boss") {
		t.Error("a boss mob did not get the boss tag")
	}
}

// become_drowned turns the zombie into a drowned, so a type=zombie tag never
// matches; the variant must be claimed by position instead.
func TestVariantSpawnTagsByPosition(t *testing.T) {
	s := testSpawn()
	s.Variant = "minecraft:become_drowned"
	cmds := s.commands(Tier{})
	want := "execute positioned 10 65 -760 run tag @e[type=!player,type=!item,tag=!arena_tiered,tag=!arena_wave,c=1,r=2] add arena_tiered"
	if !hasCommand(cmds, want) {
		t.Errorf("variant tag command missing:\n%s", strings.Join(cmds, "\n"))
	}
	if hasCommand(cmds, "execute positioned 10 65 -760 run tag @e[type=zombie,tag=!arena_tiered,tag=!arena_wave,c=1,r=4] add arena_tiered") {
		t.Error("a variant summon still filters on the pre-variant entity type")
	}
}

// The spike found /enchant only ever touches the mainhand, so an armour-only
// recipe must not emit one.
func TestNoEnchantWithoutMainhand(t *testing.T) {
	recipe := Tier{
		Gear:    []Gear{{Slot: "armor.head", Item: "diamond_helmet"}},
		Enchant: &Enchant{ID: "sharpness", Level: 5},
	}
	if hasCommand(testSpawn().commands(recipe), "enchant ") {
		t.Error("armour-only recipe emitted an enchant")
	}
}

func TestWarriorCommandQuotesTheName(t *testing.T) {
	cmd, err := warriorCommand("Vanguard")
	if err != nil {
		t.Fatalf("warriorCommand: %v", err)
	}
	if want := `testfor @e[name="Vanguard",type=player]`; cmd != want {
		t.Errorf("warriorCommand = %q, want %q", cmd, want)
	}
}

func TestWarriorCommandRejectsSelectorInjection(t *testing.T) {
	for _, name := range []string{"", `Va"nguard`, "Va\nnguard", `Va\nguard`} {
		if _, err := warriorCommand(name); err == nil {
			t.Errorf("warriorCommand(%q) accepted an unsafe name", name)
		}
	}
}

func TestSlotPrefixesAreAcceptedOnce(t *testing.T) {
	for _, slot := range []string{"weapon.mainhand", "slot.weapon.mainhand"} {
		recipe := Tier{Gear: []Gear{{Slot: slot, Item: "iron_sword"}}}
		want := "replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 iron_sword 1"
		if !hasCommand(testSpawn().commands(recipe), want) {
			t.Errorf("slot %q produced no %q", slot, want)
		}
	}
}

func hasCommand(cmds []string, want string) bool {
	for _, cmd := range cmds {
		if cmd == want {
			return true
		}
	}
	return false
}

func TestFloorRepairFillsOnlyAirInTwoBoundedFills(t *testing.T) {
	l := Layout{Center: Point{X: 0, Z: -760}, GroundY: 63, FloorRadius: 45}
	cmds := floorRepairCommands(l)
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %v", len(cmds), cmds)
	}
	want := []string{
		"fill -45 60 -805 45 63 -760 sand replace air",
		"fill -45 60 -759 45 63 -715 sand replace air",
	}
	for i, c := range cmds {
		if c != want[i] {
			t.Errorf("command %d = %q, want %q", i, c, want[i])
		}
	}
	if got := floorRepairCommands(Layout{}); got != nil {
		t.Errorf("a layout without a floor radius gave %v", got)
	}
}

func TestKitSpawnKeepsEffectsAndTagsButNotGear(t *testing.T) {
	s := testSpawn()
	s.Tier = 5
	s.Kit = true
	recipe := Tier{
		Effects: []Effect{{ID: "resistance", Seconds: 600, Amplifier: 2}},
		Gear:    []Gear{{Slot: "armor.head", Item: "diamond_helmet"}},
		Enchant: &Enchant{ID: "sharpness", Level: 5},
	}
	cmds := s.commands(recipe)
	if cmds[0] != "execute positioned 10 65 -760 run function mobs/zombie/t5" {
		t.Errorf("first command = %q", cmds[0])
	}
	for _, want := range []string{
		"tag @e[tag=arena_tiered] add arena_wave_t5",
		"effect @e[tag=arena_tiered] resistance 600 2",
		"tag @e[tag=arena_tiered] remove arena_tiered",
	} {
		if !hasCommand(cmds, want) {
			t.Errorf("kit spawn lacks %q:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	if strings.Contains(strings.Join(cmds, "\n"), "replaceitem") || strings.Contains(strings.Join(cmds, "\n"), "enchant ") || strings.Contains(strings.Join(cmds, "\n"), "summon") {
		t.Errorf("kit spawn regears or summons:\n%s", strings.Join(cmds, "\n"))
	}
}

func TestOnlyPlainCatalogMobsFitAKit(t *testing.T) {
	named, variant, other, high := testSpawn(), testSpawn(), testSpawn(), testSpawn()
	named.Name = "Warlord"
	variant.Variant = "minecraft:become_drowned"
	other.Type = "ravager"
	high.Tier = 7
	if !testSpawn().kitFits() {
		t.Error("a plain zombie should fit a kit")
	}
	for _, s := range []spawn{named, variant, other, high} {
		if s.kitFits() {
			t.Errorf("%+v should keep the summon", s)
		}
	}
}

func TestCustomMobSpawnTagsAndCountsLikeAVanillaOne(t *testing.T) {
	s := testSpawn()
	s.Type = "gameops:training_dummy_brute"
	s.Name = "Sergeant"
	s.Tier = 2
	recipe := Tier{
		Effects: []Effect{{ID: "resistance", Seconds: 600, Amplifier: 1}},
		Gear:    []Gear{{Slot: "armor.head", Item: "iron_helmet"}, {Slot: "weapon.mainhand", Item: "iron_sword"}},
		Enchant: &Enchant{ID: "sharpness", Level: 3},
	}
	cmds := s.commands(recipe)
	for _, want := range []string{
		`execute positioned 10 65 -760 run summon gameops:training_dummy_brute "Sergeant" ~ ~ ~`,
		"execute positioned 10 65 -760 run tag @e[type=gameops:training_dummy_brute,tag=!arena_tiered,tag=!arena_wave,c=1,r=4] add arena_tiered",
		"tag @e[tag=arena_tiered] add arena_wave",
		"tag @e[tag=arena_tiered] add arena_wave_t2",
		"effect @e[tag=arena_tiered] resistance 600 1",
		"tag @e[tag=arena_tiered] remove arena_tiered",
	} {
		if !hasCommand(cmds, want) {
			t.Errorf("commands lack %q:\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	joined := strings.Join(cmds, "\n")
	if strings.Contains(joined, "replaceitem") || strings.Contains(joined, "enchant ") {
		t.Errorf("a pack mob was geared:\n%s", joined)
	}
	if s.kitFits() {
		t.Error("a pack mob must not come from the vanilla kit catalog")
	}
	if got := countCommand(); got != "testfor @e[tag=arena_wave,type=!item]" {
		t.Errorf("countCommand = %q", got)
	}
}

func TestVanillaMobStillGearedBesideAPackMob(t *testing.T) {
	recipe := Tier{
		Gear:    []Gear{{Slot: "weapon.mainhand", Item: "iron_sword"}},
		Enchant: &Enchant{ID: "sharpness", Level: 3},
	}
	vanilla := testSpawn().commands(recipe)
	pack := testSpawn()
	pack.Type = "gameops:training_dummy_brute"
	packCmds := pack.commands(recipe)
	for _, want := range []string{
		"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 iron_sword 1",
		"enchant @e[tag=arena_tiered] sharpness 3",
	} {
		if !hasCommand(vanilla, want) {
			t.Errorf("vanilla mob lacks %q", want)
		}
		if hasCommand(packCmds, want) {
			t.Errorf("pack mob got %q", want)
		}
	}
}
