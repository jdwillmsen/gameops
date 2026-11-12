package task

import (
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const (
	mineOreBody = `{"template":"mine_ore","entrance":{"x":10,"y":64,"z":10},"facing":"east","depth":24,` +
		`"branches":2,"branch_length":16,"area":{"min":{"x":0,"y":30,"z":-10},"max":{"x":99,"y":70,"z":40}},` +
		`"to":{"x":8,"y":64,"z":10},"ores":["coal","iron"],"torches":true,"goal":{"raw_iron":8}}`
	smeltBody   = `{"template":"smelt","item":"raw_iron","qty":8,"block":{"x":14,"y":64,"z":10}}`
	ironAgeBody = `{"template":"iron_age","table":{"x":10,"y":64,"z":12},"furnace":{"x":14,"y":64,"z":10},"chest":{"x":8,"y":64,"z":10},` +
		`"entrance":{"x":10,"y":64,"z":10},"facing":"east","depth":24,"branches":2,"branch_length":16,` +
		`"area":{"min":{"x":0,"y":30,"z":-10},"max":{"x":99,"y":70,"z":40}}}`
	enchantBody = `{"template":"enchanting_table","table":{"x":10,"y":64,"z":12},"at":{"x":16,"y":64,"z":12}}`
)

func withField(body, extra string) string { return body[:len(body)-1] + "," + extra + "}" }

func TestParseMineOre(t *testing.T) {
	tpl, err := ParseTemplate([]byte(mineOreBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Template != "mine_ore" || tpl.Depth != 24 || tpl.Branches != 2 || !tpl.Torches ||
		len(tpl.Ores) != 2 || tpl.Items["raw_iron"] != 8 || tpl.To == nil || tpl.Area == nil {
		t.Errorf("template = %+v", tpl)
	}
	for _, body := range []string{
		strings.Replace(mineOreBody, `"torches":true,"goal":{"raw_iron":8}`, `"torches":false`, 1),
		strings.Replace(mineOreBody, `"depth":24`, `"depth":64`, 1),
		strings.Replace(mineOreBody, `"ores":["coal","iron"]`, `"ores":["coal","iron","diamond"]`, 1),
	} {
		if _, err := ParseTemplate([]byte(body)); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	for name, body := range map[string]string{
		"no ores":           strings.Replace(mineOreBody, `"ores":["coal","iron"]`, `"ores":[]`, 1),
		"unknown ore":       strings.Replace(mineOreBody, `"ores":["coal","iron"]`, `"ores":["coal","gold"]`, 1),
		"ore twice":         strings.Replace(mineOreBody, `"ores":["coal","iron"]`, `"ores":["coal","coal"]`, 1),
		"goal not dropped":  strings.Replace(mineOreBody, `"raw_iron":8`, `"diamond":1`, 1),
		"goal zero":         strings.Replace(mineOreBody, `"raw_iron":8`, `"raw_iron":0`, 1),
		"goal too big":      strings.Replace(mineOreBody, `"raw_iron":8`, `"raw_iron":257`, 1),
		"too deep":          strings.Replace(mineOreBody, `"depth":24`, `"depth":65`, 1),
		"depth zero":        strings.Replace(mineOreBody, `"depth":24`, `"depth":0`, 1),
		"bad facing":        strings.Replace(mineOreBody, `"east"`, `"up"`, 1),
		"branches no len":   strings.Replace(mineOreBody, `"branch_length":16`, `"branch_length":0`, 1),
		"branch too long":   strings.Replace(mineOreBody, `"branch_length":16`, `"branch_length":49`, 1),
		"too many branches": strings.Replace(mineOreBody, `"branches":2`, `"branches":4`, 1),
		"no area":           strings.Replace(mineOreBody, `"area":{"min":{"x":0,"y":30,"z":-10},"max":{"x":99,"y":70,"z":40}},`, ``, 1),
		"area too wide":     strings.Replace(mineOreBody, `"x":99`, `"x":200`, 1),
		"area inverted":     strings.Replace(mineOreBody, `"min":{"x":0,"y":30,"z":-10}`, `"min":{"x":0,"y":80,"z":0}`, 1),
		"no entrance":       strings.Replace(mineOreBody, `"entrance":{"x":10,"y":64,"z":10},`, ``, 1),
		"no chest":          strings.Replace(mineOreBody, `"to":{"x":8,"y":64,"z":10},`, ``, 1),
		"unknown field":     withField(mineOreBody, `"qty":4`),
		"trailing data":     mineOreBody + `{}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMineOreExpandsToOneShaftAndRefusesAPlanOutsideItsArea(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(mineOreBody))
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name() != "mine_shaft" {
		t.Fatalf("skills = %v", skills)
	}
	outside := strings.Replace(mineOreBody, `"y":30`, `"y":60`, 1)
	tpl, err = ParseTemplate([]byte(outside))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} }); err == nil {
		t.Error("a 24-deep stair in an area 4 deep was accepted")
	}
}

func TestParseSmelt(t *testing.T) {
	tpl, err := ParseTemplate([]byte(smeltBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Template != "smelt" || tpl.Item != "raw_iron" || tpl.Qty != 8 || tpl.Block == nil || tpl.Block.X != 14 {
		t.Errorf("template = %+v", tpl)
	}
	for name, body := range map[string]string{
		"unknown item":  strings.Replace(smeltBody, "raw_iron", "diamond", 1),
		"item missing":  `{"template":"smelt","qty":8,"block":{"x":14,"y":64,"z":10}}`,
		"qty zero":      strings.Replace(smeltBody, `"qty":8`, `"qty":0`, 1),
		"qty too big":   strings.Replace(smeltBody, `"qty":8`, `"qty":65`, 1),
		"block missing": `{"template":"smelt","item":"raw_iron","qty":8}`,
		"block outside": strings.Replace(smeltBody, `"x":14`, `"x":9000`, 1),
		"unknown field": withField(smeltBody, `"chest":{"x":1,"y":1,"z":1}`),
		"trailing data": smeltBody + `{}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, item := range []string{"raw_copper", "cobblestone", "beef", "potato"} {
		if _, err := ParseTemplate([]byte(strings.Replace(smeltBody, "raw_iron", item, 1))); err != nil {
			t.Errorf("%s: %v", item, err)
		}
	}
}

func TestSmeltExpandsToOneSmeltSkill(t *testing.T) {
	got := expandNames(t, smeltBody, nil, nil)
	if got != "smelt" {
		t.Errorf("skills = %s", got)
	}
}

func TestParseIronAge(t *testing.T) {
	tpl, err := ParseTemplate([]byte(ironAgeBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Block == nil || tpl.Furnace == nil || tpl.To == nil || tpl.Entrance == nil || tpl.Area == nil || tpl.Depth != 24 {
		t.Errorf("template = %+v", tpl)
	}
	withSearch := withField(ironAgeBody, `"search":{"min":{"x":0,"y":60,"z":0},"max":{"x":99,"y":90,"z":99}}`)
	if _, err := ParseTemplate([]byte(withSearch)); err != nil {
		t.Error(err)
	}
	for name, body := range map[string]string{
		"table missing":   strings.Replace(ironAgeBody, `"table":{"x":10,"y":64,"z":12},`, ``, 1),
		"furnace missing": strings.Replace(ironAgeBody, `"furnace":{"x":14,"y":64,"z":10},`, ``, 1),
		"chest missing":   strings.Replace(ironAgeBody, `"chest":{"x":8,"y":64,"z":10},`, ``, 1),
		"furnace outside": strings.Replace(ironAgeBody, `"x":14`, `"x":9000`, 1),
		"same cells":      strings.Replace(ironAgeBody, `"furnace":{"x":14,"y":64,"z":10}`, `"furnace":{"x":10,"y":64,"z":12}`, 1),
		"search half":     withField(ironAgeBody, `"search":{"min":{"x":0,"y":0,"z":0}}`),
		"ores field":      withField(ironAgeBody, `"ores":["iron"]`),
		"too deep":        strings.Replace(ironAgeBody, `"depth":24`, `"depth":65`, 1),
		"trailing data":   ironAgeBody + `{}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPlanIronAge(t *testing.T) {
	// 24 steps, 2 branches of 16: 8 + 5 + 2 = 15 torches, 4 crafts.
	if got := torchesFor(24, 2, 16); got != 15 {
		t.Fatalf("torches = %d, want 15", got)
	}
	cases := []struct {
		name                                         string
		held                                         map[string]int
		ingots, rawIron, coal, torchCrafts, sticksOf int
	}{
		{"from stone tools", map[string]int{}, 8, 8, 5, 4, 9},
		{"torches in hand", map[string]int{"torch": 16}, 8, 8, 1, 0, 5},
		{"coal in hand", map[string]int{"coal": 5}, 8, 8, 0, 4, 9},
		{"charcoal counts as fuel", map[string]int{"charcoal": 5}, 8, 8, 0, 4, 9},
		{"raw iron in hand", map[string]int{"raw_iron": 8}, 8, 0, 1, 0, 5},
		{"ingots in hand", map[string]int{"iron_ingot": 8}, 0, 0, 0, 0, 5},
		{"some ingots", map[string]int{"iron_ingot": 3}, 5, 5, 5, 4, 9},
		{"iron pickaxe held", map[string]int{"iron_pickaxe": 1}, 5, 5, 5, 4, 7},
		{"all tools held", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1}, 0, 0, 0, 0, 0},
	}
	for _, c := range cases {
		p := planIronAge(Inventory(c.held), 24, 2, 16)
		if p.ingots != c.ingots || p.rawIron != c.rawIron || p.coal != c.coal || p.torchCrafts != c.torchCrafts || p.demand.Gross[itemStick] != c.sticksOf {
			t.Errorf("%s: ingots %d raw %d coal %d torch crafts %d sticks %d; want %d %d %d %d %d", c.name,
				p.ingots, p.rawIron, p.coal, p.torchCrafts, p.demand.Gross[itemStick],
				c.ingots, c.rawIron, c.coal, c.torchCrafts, c.sticksOf)
		}
	}
}

func ironAgeWorld() cellWorld {
	return cellWorld{{10, 64, 12}: "crafting_table", {14, 64, 10}: "furnace", {8, 64, 10}: "chest"}
}

func TestIronAgeFromStoneTools(t *testing.T) {
	// Handles for the tools and torches need logs; no coal, no torches, no iron.
	got := expandNames(t, ironAgeBody, ironAgeWorld(), map[string]int{"stone_pickaxe": 1})
	want := []string{
		"fell_tree",
		"craft", "craft", // planks, sticks
		"mine_shaft", "withdraw_chest", // coal
		"craft",                        // torches
		"mine_shaft", "withdraw_chest", // iron
		"smelt",
		"near", "craft", "craft", "craft", // iron pickaxe, axe, sword
	}
	if got != strings.Join(want, ",") {
		t.Errorf("skills = %s\nwant     %s", got, strings.Join(want, ","))
	}
}

func TestIronAgeSkipsWhatTheUnitHolds(t *testing.T) {
	logs := map[string]int{"oak_planks": 8, "stick": 9}
	held := func(extra map[string]int) map[string]int {
		m := map[string]int{"stone_pickaxe": 1}
		for k, v := range logs {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, c := range []struct {
		name  string
		extra map[string]int
		want  string
	}{
		{"coal and torches held skips the coal dive",
			map[string]int{"coal": 5, "torch": 16},
			"mine_shaft,withdraw_chest,smelt,near,craft,craft,craft"},
		{"raw iron held skips the iron dive",
			map[string]int{"coal": 1, "raw_iron": 8},
			"smelt,near,craft,craft,craft"},
		{"ingots held skips the smelt",
			map[string]int{"iron_ingot": 8},
			"near,craft,craft,craft"},
		{"only the sword left",
			map[string]int{"iron_ingot": 2, "iron_pickaxe": 1, "iron_axe": 1},
			"near,craft"},
	} {
		got := expandNames(t, ironAgeBody, ironAgeWorld(), held(c.extra))
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestIronAgeSearchesForATreeFirst(t *testing.T) {
	body := withField(ironAgeBody, `"search":{"min":{"x":0,"y":60,"z":0},"max":{"x":99,"y":90,"z":99}}`)
	got := expandNames(t, body, ironAgeWorld(), map[string]int{"stone_pickaxe": 1})
	if !strings.HasPrefix(got, "explore,fell_tree,") {
		t.Errorf("skills = %s", got)
	}
}

func TestIronAgeStopsFirstWhenThereIsNoFurnaceOrTable(t *testing.T) {
	cases := []struct {
		name  string
		world cellWorld
		held  map[string]int
		want  string
	}{
		{"no furnace anywhere", cellWorld{{10, 64, 12}: "crafting_table"}, map[string]int{}, "no furnace in inventory"},
		{"no table anywhere", cellWorld{{14, 64, 10}: "furnace"}, map[string]int{}, "no crafting_table in inventory"},
		{"furnace carried is enough", cellWorld{{10, 64, 12}: "crafting_table"}, map[string]int{"furnace": 1}, ""},
	}
	for _, c := range cases {
		tpl, _ := ParseTemplate([]byte(ironAgeBody))
		s := snap(c.held)
		skills, err := Expand(tpl, c.world, func() belief.Snapshot { return s })
		if err != nil {
			t.Fatal(err)
		}
		err = skills[0].Ready(s)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: first skill %s, err %v", c.name, skills[0].Name(), err)
			}
			continue
		}
		if skills[0].Name() != "require" || err == nil || err.Error() != c.want {
			t.Errorf("%s: first skill %s, err %v; want a require failing with %q", c.name, skills[0].Name(), err, c.want)
		}
	}
}

func TestIronAgeWithAllTheToolsHasNothingToDo(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(ironAgeBody))
	s := snap(map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1})
	if _, err := Expand(tpl, ironAgeWorld(), func() belief.Snapshot { return s }); err == nil {
		t.Error("a unit with the full set was given a chain")
	}
}

func TestPlacesTheTableItCarries(t *testing.T) {
	got := expandNames(t, ironAgeBody, cellWorld{{14, 64, 10}: "furnace"}, map[string]int{"crafting_table": 1, "iron_ingot": 8})
	if !strings.HasPrefix(got, "place_block,") {
		t.Errorf("skills = %s", got)
	}
}

func TestParseEnchanting(t *testing.T) {
	tpl, err := ParseTemplate([]byte(enchantBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Block == nil || tpl.At == nil || tpl.At.X != 16 {
		t.Errorf("template = %+v", tpl)
	}
	if _, err := ParseTemplate([]byte(`{"template":"enchanting_table","table":{"x":10,"y":64,"z":12}}`)); err != nil {
		t.Error(err)
	}
	for name, body := range map[string]string{
		"table missing": `{"template":"enchanting_table"}`,
		"table outside": `{"template":"enchanting_table","table":{"x":9000,"y":64,"z":12}}`,
		"at outside":    `{"template":"enchanting_table","table":{"x":1,"y":64,"z":1},"at":{"x":9000,"y":64,"z":12}}`,
		"same cell":     `{"template":"enchanting_table","table":{"x":1,"y":64,"z":1},"at":{"x":1,"y":64,"z":1}}`,
		"item field":    withField(enchantBody, `"item":"book"`),
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestEnchantingChecksTheIngredientsBeforeWalking(t *testing.T) {
	full := map[string]int{"obsidian": 4, "diamond": 2, "book": 1}
	if got := expandNames(t, enchantBody, nil, full); got != "require,near,craft,place_block" {
		t.Errorf("skills = %s", got)
	}
	if got := expandNames(t, `{"template":"enchanting_table","table":{"x":10,"y":64,"z":12}}`, nil, full); got != "require,near,craft" {
		t.Errorf("without at: skills = %s", got)
	}
	// A table already held is only placed.
	if got := expandNames(t, enchantBody, nil, map[string]int{"enchanting_table": 1}); got != "place_block" {
		t.Errorf("table held: skills = %s", got)
	}
	short := map[string]int{"obsidian": 4, "diamond": 1, "book": 1}
	tpl, _ := ParseTemplate([]byte(enchantBody))
	s := snap(short)
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return s })
	if err != nil {
		t.Fatal(err)
	}
	if err := skills[0].Ready(s); err == nil || err.Error() != "no diamond in inventory" {
		t.Errorf("Ready = %v, want it to name the diamond", err)
	}
	if ReasonCode("no diamond in inventory") != ReasonMissingTool {
		t.Error("a missing ingredient is not a missing_tool failure")
	}
}

func TestEnchantingTableCost(t *testing.T) {
	if EnchantingTableCost["obsidian"] != 4 || EnchantingTableCost["diamond"] != 2 || EnchantingTableCost["book"] != 1 || len(EnchantingTableCost) != 3 {
		t.Errorf("cost = %v", EnchantingTableCost)
	}
}

func TestLadderNamesTheNextStage(t *testing.T) {
	cases := []struct {
		name string
		held map[string]int
		want string
	}{
		{"nothing", map[string]int{}, "stone_tools"},
		{"wooden tools only", map[string]int{"wooden_pickaxe": 1}, "stone_tools"},
		{"stone tools", map[string]int{"stone_pickaxe": 1, "furnace": 1}, "coal"},
		{"some coal", map[string]int{"stone_pickaxe": 1, "coal": 7}, "coal"},
		{"enough coal", map[string]int{"stone_pickaxe": 1, "coal": 8}, "iron_tools"},
		{"charcoal counts", map[string]int{"stone_pickaxe": 1, "charcoal": 8}, "iron_tools"},
		{"two of three iron tools", map[string]int{"stone_pickaxe": 1, "coal": 8, "iron_pickaxe": 1, "iron_axe": 1}, "iron_tools"},
		{"iron tools, coal spent", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1}, "diamonds"},
		{"one diamond", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1, "diamond": 1}, "diamonds"},
		{"two diamonds", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1, "diamond": 2}, "obsidian"},
		{"obsidian", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1, "diamond": 2, "obsidian": 4}, "book"},
		{"everything", map[string]int{"iron_pickaxe": 1, "iron_axe": 1, "iron_sword": 1, "diamond": 2, "obsidian": 4, "book": 1}, "enchanting_table"},
	}
	for _, c := range cases {
		st, ok := NextStage(snap(c.held))
		if !ok || st.Name != c.want {
			t.Errorf("%s: stage %q ok=%v, want %q", c.name, st.Name, ok, c.want)
		}
	}
	done := map[string]int{"enchanting_table": 1}
	if st, ok := NextStage(snap(done)); ok {
		t.Errorf("a unit with the table is still at %q", st.Name)
	}
}

func TestEveryStageNamesAParsableTemplateOrNone(t *testing.T) {
	known := map[string]bool{bootstrapTemplate: true, mineOreTemplate: true, ironAgeTemplate: true, enchantingTemplate: true, "": true}
	seen := map[string]bool{}
	for _, st := range Ladder {
		if !known[st.Template] {
			t.Errorf("stage %s names unknown template %q", st.Name, st.Template)
		}
		if seen[st.Name] {
			t.Errorf("stage %s is listed twice", st.Name)
		}
		seen[st.Name] = true
	}
}

func TestOreItemsCanBeGathered(t *testing.T) {
	for _, item := range []string{"coal", "raw_iron", "diamond"} {
		body := `{"template":"gather_and_deposit","item":"` + item + `","qty":4,"chest":{"x":8,"y":64,"z":10}}`
		if _, err := ParseTemplate([]byte(body)); err != nil {
			t.Errorf("%s: %v", item, err)
		}
	}
	// A wooden pickaxe cannot gather iron, and the task says what it wants.
	tpl, _ := ParseTemplate([]byte(`{"template":"gather_and_deposit","item":"raw_iron","qty":4,"chest":{"x":8,"y":64,"z":10}}`))
	s := snap(map[string]int{"wooden_pickaxe": 1})
	world := cellWorld{}
	skills, err := Expand(tpl, world, func() belief.Snapshot { return s })
	if err != nil {
		t.Fatal(err)
	}
	if err := skills[0].Ready(s); err == nil || err.Error() != "no stone_pickaxe or better in inventory" {
		t.Errorf("wooden pickaxe: Ready = %v", err)
	}
	s = snap(map[string]int{"stone_pickaxe": 1})
	if err := skills[0].Ready(s); err != nil {
		t.Errorf("stone pickaxe: Ready = %v", err)
	}
	_ = protocol.BlockPos{}
}
