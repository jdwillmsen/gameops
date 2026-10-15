package players

// enchantments are the Bedrock enchantments by the number the save writes.
// The save has no names; an id not listed shows as its number.
var enchantments = map[int]string{
	0: "Protection", 1: "Fire Protection", 2: "Feather Falling", 3: "Blast Protection",
	4: "Projectile Protection", 5: "Thorns", 6: "Respiration", 7: "Depth Strider",
	8: "Aqua Affinity", 9: "Sharpness", 10: "Smite", 11: "Bane of Arthropods",
	12: "Knockback", 13: "Fire Aspect", 14: "Looting", 15: "Efficiency",
	16: "Silk Touch", 17: "Unbreaking", 18: "Fortune", 19: "Power",
	20: "Punch", 21: "Flame", 22: "Infinity", 23: "Luck of the Sea",
	24: "Lure", 25: "Frost Walker", 26: "Mending", 27: "Curse of Binding",
	28: "Curse of Vanishing", 29: "Impaling", 30: "Riptide", 31: "Loyalty",
	32: "Channeling", 33: "Multishot", 34: "Piercing", 35: "Quick Charge",
	36: "Soul Speed", 37: "Swift Sneak", 38: "Wind Burst", 39: "Density",
	40: "Breach",
}

// maxDamage is how much durability an item has, which the save leaves out:
// it records only how much is used. Armour and tools are looked up by
// material and kind below; the rest are listed.
var maxDamage = map[string]int{
	"bow": 384, "crossbow": 465, "shield": 336, "elytra": 432, "fishing_rod": 384,
	"trident": 250, "flint_and_steel": 64, "shears": 238, "mace": 500,
	"carrot_on_a_stick": 25, "warped_fungus_on_a_stick": 100, "brush": 64,
	"turtle_helmet": 275, "wolf_armor": 64,
}

var (
	armourDamage = map[string][4]int{
		"leather": {55, 80, 75, 65}, "chainmail": {165, 240, 225, 195}, "iron": {165, 240, 225, 195},
		"golden": {77, 112, 105, 91}, "diamond": {363, 528, 495, 429}, "netherite": {407, 592, 555, 481},
	}
	armourKinds = []string{"helmet", "chestplate", "leggings", "boots"}
	toolDamage  = map[string]int{"wooden": 59, "stone": 131, "iron": 250, "golden": 32, "diamond": 1561, "netherite": 2031}
	toolKinds   = []string{"sword", "pickaxe", "axe", "shovel", "hoe"}
)

func init() {
	for material, uses := range armourDamage {
		for i, kind := range armourKinds {
			maxDamage[material+"_"+kind] = uses[i]
		}
	}
	for material, uses := range toolDamage {
		for _, kind := range toolKinds {
			maxDamage[material+"_"+kind] = uses
		}
	}
}
