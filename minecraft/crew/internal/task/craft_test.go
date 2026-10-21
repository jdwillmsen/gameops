package task

import (
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const craftBody = `{"template":"craft","item":"oak_planks","count":4}`

func TestParseCraft(t *testing.T) {
	tpl, err := ParseTemplate([]byte(craftBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Template != "craft" || tpl.Item != "oak_planks" || tpl.Qty != 4 {
		t.Errorf("template = %+v, want craft oak_planks x4", tpl)
	}

	for _, body := range []string{
		`{"template":"craft","item":"wooden_hoe","count":1}`,
		`{"template":"craft","item":"oak_planks","count":64}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}

	for name, body := range map[string]string{
		"count zero":        `{"template":"craft","item":"oak_planks","count":0}`,
		"count negative":    `{"template":"craft","item":"oak_planks","count":-1}`,
		"count over a full": `{"template":"craft","item":"oak_planks","count":65}`,
		"count missing":     `{"template":"craft","item":"oak_planks"}`,
		"item missing":      `{"template":"craft","count":4}`,
		"item uppercase":    `{"template":"craft","item":"Oak_Planks","count":4}`,
		"item spaced":       `{"template":"craft","item":"oak planks","count":4}`,
		"item dashed":       `{"template":"craft","item":"oak-planks","count":4}`,
		"item namespaced":   `{"template":"craft","item":"minecraft:oak_planks","count":4}`,
		"item too long":     `{"template":"craft","item":"` + strings.Repeat("a", 33) + `","count":4}`,
		"unknown field":     `{"template":"craft","item":"oak_planks","count":4,"chest":{"x":0,"y":0,"z":0}}`,
		"trailing data":     craftBody + `{}`,
	} {
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCraftExpandsToOneCraftSkill(t *testing.T) {
	tpl, err := ParseTemplate([]byte(craftBody))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name() != "craft" {
		t.Fatalf("skills = %v, want one craft", skills)
	}
	if p, ok := skills[0].(skill.Progresser); !ok || p.Progress() != 0 {
		t.Errorf("the craft skill does not count its progress")
	}
}
