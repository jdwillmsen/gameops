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
			"effect give @e[tag=arena_tiered] strength 600 0",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 stone_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 1",
		}},
		{2, []string{
			"effect give @e[tag=arena_tiered] resistance 600 1",
			"effect give @e[tag=arena_tiered] health_boost 600 2",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 iron_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 3",
		}},
		{3, []string{
			"effect give @e[tag=arena_tiered] strength 600 1",
			"effect give @e[tag=arena_tiered] resistance 600 2",
			"effect give @e[tag=arena_tiered] health_boost 600 4",
			"replaceitem entity @e[tag=arena_tiered] slot.armor.chest 0 diamond_chestplate 1",
			"enchant @e[tag=arena_tiered] sharpness 5",
		}},
		{4, []string{
			"effect give @e[tag=arena_tiered] health_boost 600 7",
			"effect give @e[tag=arena_tiered] resistance 600 3",
			"effect give @e[tag=arena_tiered] regeneration 600 5",
			"replaceitem entity @e[tag=arena_tiered] slot.weapon.mainhand 0 diamond_sword 1",
			"enchant @e[tag=arena_tiered] sharpness 5",
		}},
		{5, []string{
			"effect give @e[tag=arena_tiered] absorption 600 4",
			"effect give @e[tag=arena_tiered] strength 600 2",
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
