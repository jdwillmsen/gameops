package task

import (
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

func expandNames(t *testing.T, body string, world skill.ChestWorld, held map[string]int) string {
	t.Helper()
	tpl, err := ParseTemplate([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	s := snap(held)
	skills, err := Expand(tpl, world, func() belief.Snapshot { return s })
	if err != nil {
		t.Fatal(err)
	}
	skills = flatten(t, skills, s)
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
		"mine_block", "near",
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
