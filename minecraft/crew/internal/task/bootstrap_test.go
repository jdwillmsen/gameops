package task

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const bootstrapBody = `{"template":"bootstrap","table":{"x":10,"y":64,"z":10},"chest":{"x":12,"y":64,"z":10}}`

type cellWorld map[protocol.BlockPos]string

func (w cellWorld) Block(p protocol.BlockPos) (string, bool) {
	n, ok := w[p]
	return n, ok
}
func (cellWorld) BlockHash(protocol.BlockPos) (uint32, bool) { return 0, false }

func TestParseBootstrap(t *testing.T) {
	tpl, err := ParseTemplate([]byte(bootstrapBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Template != "bootstrap" || tpl.Block == nil || tpl.To == nil || tpl.Block.X != 10 || tpl.To.X != 12 {
		t.Errorf("template = %+v", tpl)
	}
	for _, body := range []string{
		`{"template":"bootstrap","table":{"x":10,"y":64,"z":10}}`,
		bootstrapBody[:len(bootstrapBody)-1] + `,"search":{"min":{"x":0,"y":60,"z":0},"max":{"x":99,"y":90,"z":99}}}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	for name, body := range map[string]string{
		"table missing":   `{"template":"bootstrap"}`,
		"table outside":   `{"template":"bootstrap","table":{"x":9000,"y":64,"z":0}}`,
		"chest outside":   `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"chest":{"x":9000,"y":64,"z":0}}`,
		"same cell":       `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"chest":{"x":1,"y":64,"z":1}}`,
		"search half":     `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"search":{"min":{"x":0,"y":0,"z":0}}}`,
		"search too wide": `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"search":{"min":{"x":-500,"y":0,"z":0},"max":{"x":499,"y":9,"z":9}}}`,
		"unknown field":   `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"qty":4}`,
		"item field":      `{"template":"bootstrap","table":{"x":1,"y":64,"z":1},"item":"oak_log"}`,
		"trailing data":   bootstrapBody + `{}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// fieldString reads an unexported string field of a skill, following pointer
// fields by name.
func fieldString(sk skill.Skill, path ...string) string {
	v := reflect.ValueOf(sk)
	for _, name := range path {
		v = reflect.Indirect(v).FieldByName(name)
	}
	return v.String()
}

// planned expands a template and, as the runner does when the task starts,
// works out a bootstrap's chain from the unit as it is then.
func planned(t *testing.T, tpl Template, world skill.ChestWorld, self func() belief.Snapshot) []skill.Skill {
	t.Helper()
	skills, err := Expand(tpl, world, self)
	if err != nil {
		t.Fatal(err)
	}
	return flatten(t, skills, self())
}

func expandNames(t *testing.T, body string, world skill.ChestWorld, held map[string]int) string {
	t.Helper()
	tpl, err := ParseTemplate([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	s := snap(held)
	skills := planned(t, tpl, world, func() belief.Snapshot { return s })
	names := make([]string, len(skills))
	for i, sk := range skills {
		names[i] = sk.Name()
	}
	return strings.Join(names, ",")
}

// A unit with nothing fells, crafts the table, places it, makes the chest
// and wooden tools beside it, places the chest, mines stone, comes back and
// makes the stone tools and the furnace.
func TestBootstrapFromNothing(t *testing.T) {
	got := expandNames(t, bootstrapBody, nil, nil)
	want := []string{
		"fell_tree",
		"craft", "craft", // planks, sticks
		"craft", "place_block", // table
		"near",
		"craft",                   // chest
		"craft", "craft", "craft", // wooden pickaxe, axe, sword
		"place_block", // chest
		"hotbar_gate", "mine_block", "near",
		"craft", "craft", "craft", // stone pickaxe, axe, sword
		"craft", // furnace
	}
	if got != strings.Join(want, ",") {
		t.Errorf("skills = %s\nwant     %s", got, strings.Join(want, ","))
	}
}

func TestBootstrapSearchesBeforeEachGather(t *testing.T) {
	body := bootstrapBody[:len(bootstrapBody)-1] + `,"search":{"min":{"x":0,"y":60,"z":0},"max":{"x":99,"y":90,"z":99}}}`
	got := expandNames(t, body, nil, nil)
	if !strings.HasPrefix(got, "explore,fell_tree,") || !strings.Contains(got, "explore,mine_block,near") {
		t.Errorf("skills = %s", got)
	}
}

// A unit that already holds the logs and the cobblestone does not gather.
func TestBootstrapSkipsGatheringWhatIsHeld(t *testing.T) {
	got := expandNames(t, bootstrapBody, nil, map[string]int{"oak_log": 7, "cobblestone": 16})
	if strings.Contains(got, "fell_tree") || strings.Contains(got, "mine_block") {
		t.Errorf("gathers what it holds: %s", got)
	}
	short := expandNames(t, bootstrapBody, nil, map[string]int{"oak_log": 6})
	if !strings.HasPrefix(short, "fell_tree") {
		t.Errorf("one log short should fell: %s", short)
	}
}

// A table or chest that already stands is not crafted or placed again.
func TestBootstrapSkipsWhatAlreadyStands(t *testing.T) {
	world := cellWorld{
		{10, 64, 10}: "crafting_table",
		{12, 64, 10}: "chest",
	}
	got := expandNames(t, bootstrapBody, world, map[string]int{"oak_log": 7, "cobblestone": 16})
	if strings.Contains(got, "place_block") {
		t.Errorf("places again: %s", got)
	}
	want := "craft,craft,near,craft,craft,craft,near,craft,craft,craft,craft"
	if got != want {
		t.Errorf("skills = %s\nwant     %s", got, want)
	}
}

// Without a chest cell the bootstrap places no chest.
func TestBootstrapWithoutChest(t *testing.T) {
	got := expandNames(t, `{"template":"bootstrap","table":{"x":10,"y":64,"z":10}}`, nil, nil)
	if strings.Count(got, "place_block") != 1 {
		t.Errorf("skills = %s, want one placement", got)
	}
}

// The plan reads the unit as the task starts, not as it was when queued: two
// bootstraps queued back to back with an empty pack, the second planned once
// the first has run, does nothing.
func TestBootstrapPlansWhenTheTaskStartsNotWhenQueued(t *testing.T) {
	tpl, err := ParseTemplate([]byte(bootstrapBody))
	if err != nil {
		t.Fatal(err)
	}
	s := snap(nil)
	world := cellWorld{}
	self := func() belief.Snapshot { return s }
	queued, err := Expand(tpl, world, self)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].Name() != "bootstrap" {
		t.Fatalf("queued skills = %v, want the one placeholder", queued)
	}
	// The first run finishes before this one starts: the unit holds
	// everything and both blocks stand.
	s = snap(map[string]int{
		"wooden_pickaxe": 1, "wooden_axe": 1, "wooden_sword": 1,
		"stone_pickaxe": 1, "stone_axe": 1, "stone_sword": 1,
		"furnace": 1,
	})
	world[protocol.BlockPos{10, 64, 10}] = "crafting_table"
	world[protocol.BlockPos{12, 64, 10}] = "chest"
	got, err := queued[0].(*skill.Lazy).Expand(s)
	if err != nil || len(got) != 1 || got[0].Name() != "bootstrap" {
		t.Fatalf("a repeat plans %v (err %v), want the one settled step", got, err)
	}
	if st, err := got[0].Step(context.Background(), nil); st != skill.Done || err != nil {
		t.Errorf("settled step = %v, %v, want Done", st, err)
	}
}

// Planks follow the logs the unit will have: the felled ones are oak, so a
// stray birch log does not turn every plank into birch.
func TestBootstrapPlanksFollowTheLogsGathered(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(bootstrapBody))
	s := snap(map[string]int{"birch_log": 1})
	skills := planned(t, tpl, nil, func() belief.Snapshot { return s })
	var crafts []string
	for _, sk := range skills {
		if sk.Name() == "craft" {
			crafts = append(crafts, fieldString(sk, "item"))
		}
	}
	if len(crafts) < 3 || crafts[0] != "oak_planks" || crafts[1] != "birch_planks" {
		t.Errorf("first crafts = %v, want oak planks, then the stray birch log's", crafts)
	}
}

// An axe in the pack cannot be held; only one on the hotbar is used.
func TestBootstrapUsesOnlyAHotbarAxe(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(bootstrapBody))
	for name, slot := range map[string]int{"hotbar": 3, "pack": 20} {
		t.Run(name, func(t *testing.T) {
			s := snap(nil)
			s.Inventory = []belief.Item{{Slot: slot, Name: "stone_axe", Count: 1}}
			skills := planned(t, tpl, nil, func() belief.Snapshot { return s })
			if skills[0].Name() != "fell_tree" {
				t.Fatalf("first skill %s", skills[0].Name())
			}
			if got, want := fieldString(skills[0], "mine", "tool") == "axe", slot < 9; got != want {
				t.Errorf("axe used = %v, want %v", got, want)
			}
		})
	}
}

// The tool a step swings has to be on the hotbar; a unit without one stops
// before it walks off to the stone.
func TestBootstrapStopsEarlyWithoutAHotbarPickaxe(t *testing.T) {
	for name, tc := range map[string]struct {
		slot int
		ok   bool
	}{
		"on the hotbar": {4, true},
		"in the pack":   {15, false},
	} {
		t.Run(name, func(t *testing.T) {
			s := snap(nil)
			s.Inventory = []belief.Item{{Slot: tc.slot, Name: "wooden_pickaxe", Count: 1}}
			g := &hotbarGate{self: func() belief.Snapshot { return s }, tool: "pickaxe"}
			st, err := g.Step(context.Background(), nil)
			if (st == skill.Done) != tc.ok || (err == nil) != tc.ok {
				t.Errorf("status %v, err %v, want ok=%v", st, err, tc.ok)
			}
			if err != nil && !strings.Contains(err.Error(), "hotbar") {
				t.Errorf("err = %v, want it to name the hotbar", err)
			}
		})
	}
}
