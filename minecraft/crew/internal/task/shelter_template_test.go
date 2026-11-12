package task

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

func TestParseShelter(t *testing.T) {
	for _, body := range []string{
		`{"template":"shelter","until":"dawn"}`,
		`{"template":"shelter"}`,
	} {
		tpl, err := ParseTemplate([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if tpl.Until != "dawn" || tpl.MaxTicks != skill.DefaultShelterTicks {
			t.Errorf("%s: template = %+v", body, tpl)
		}
		if got := skillNames(t, body); got != "shelter" {
			t.Errorf("skills = %s", got)
		}
	}
	tpl, err := ParseTemplate([]byte(`{"template":"shelter","until":"dawn","max_ticks":600}`))
	if err != nil || tpl.MaxTicks != 600 {
		t.Errorf("max_ticks 600: %+v, %v", tpl, err)
	}
}

func TestParseShelterRejects(t *testing.T) {
	cases := map[string]string{
		"until noon":       `{"template":"shelter","until":"noon"}`,
		"max_ticks small":  `{"template":"shelter","max_ticks":99}`,
		"max_ticks big":    `{"template":"shelter","max_ticks":24001}`,
		"max_ticks neg":    `{"template":"shelter","max_ticks":-5}`,
		"unknown field":    `{"template":"shelter","until":"dawn","radius":4}`,
		"item field":       `{"template":"shelter","item":"dirt"}`,
		"trailing data":    `{"template":"shelter"}{}`,
		"max_ticks string": `{"template":"shelter","max_ticks":"600"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
