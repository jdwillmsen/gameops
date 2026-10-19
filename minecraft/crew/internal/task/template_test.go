package task

import (
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

const goodBody = `{"template":"gather_and_deposit","item":"cobblestone","qty":16,"chest":{"x":1,"y":2,"z":3}}`

func TestTemplateRejectsUnknownFieldsAndItems(t *testing.T) {
	cases := map[string]string{
		"unknown template": `{"template":"build_house","item":"cobblestone","qty":1,"chest":{"x":0,"y":0,"z":0}}`,
		"unknown item":     `{"template":"gather_and_deposit","item":"diamond","qty":1,"chest":{"x":0,"y":0,"z":0}}`,
		"extra field":      `{"template":"gather_and_deposit","item":"cobblestone","qty":1,"chest":{"x":0,"y":0,"z":0},"radius":9}`,
		"extra chest key":  `{"template":"gather_and_deposit","item":"cobblestone","qty":1,"chest":{"x":0,"y":0,"z":0,"w":1}}`,
		"missing chest":    `{"template":"gather_and_deposit","item":"cobblestone","qty":1}`,
		"trailing data":    goodBody + `{}`,
		"not json":         `nope`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := ParseTemplate([]byte(goodBody)); err != nil {
		t.Fatalf("good body: %v", err)
	}
}

func TestQtyOutOfRangeIsRejected(t *testing.T) {
	for _, qty := range []string{"0", "-1", "257"} {
		body := strings.Replace(goodBody, `"qty":16`, `"qty":`+qty, 1)
		if _, err := ParseTemplate([]byte(body)); err == nil {
			t.Errorf("qty %s accepted", qty)
		}
	}
	for _, qty := range []string{"1", "256"} {
		body := strings.Replace(goodBody, `"qty":16`, `"qty":`+qty, 1)
		if _, err := ParseTemplate([]byte(body)); err != nil {
			t.Errorf("qty %s: %v", qty, err)
		}
	}
}

func TestGatherAndDepositExpandsToMineThenDeposit(t *testing.T) {
	tpl, err := ParseTemplate([]byte(goodBody))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range skills {
		names = append(names, s.Name())
	}
	if got := strings.Join(names, ","); got != "mine_block,deposit_chest" {
		t.Errorf("skills = %s", got)
	}
}

func TestGatherAcceptsOakLog(t *testing.T) {
	body := strings.Replace(goodBody, `"cobblestone"`, `"oak_log"`, 1)
	tpl, err := ParseTemplate([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} }); err != nil || len(skills) != 2 {
		t.Fatalf("skills %v, err %v", skills, err)
	}
}

