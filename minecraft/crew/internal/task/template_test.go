package task

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
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

const haulBody = `{"template":"haul","items":{"cobblestone":16,"oak_log":16},"from":[{"x":0,"y":0,"z":0}],"to":{"x":0,"y":0,"z":0}}`

func TestParseHaulTemplate(t *testing.T) {
	tpl, err := ParseTemplate([]byte(haulBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Items["cobblestone"] != 16 || tpl.Items["oak_log"] != 16 || len(tpl.From) != 1 || tpl.To == nil {
		t.Errorf("parsed %+v", tpl)
	}
	chests := func(n int) string {
		return `[` + strings.TrimSuffix(strings.Repeat(`{"x":0,"y":0,"z":0},`, n), ",") + `]`
	}
	nine := `{"cobblestone":1,"oak_log":1,"a":1,"b":1,"c":1,"d":1,"e":1,"f":1,"g":1}`
	cases := map[string]string{
		"unknown item": `{"template":"haul","items":{"diamond":4},"from":[{"x":0,"y":0,"z":0}],"to":{"x":0,"y":0,"z":0}}`,
		"qty zero":     strings.Replace(haulBody, `"cobblestone":16`, `"cobblestone":0`, 1),
		"qty 257":      strings.Replace(haulBody, `"cobblestone":16`, `"cobblestone":257`, 1),
		"no items":     strings.Replace(haulBody, `{"cobblestone":16,"oak_log":16}`, `{}`, 1),
		"nine items":   strings.Replace(haulBody, `{"cobblestone":16,"oak_log":16}`, nine, 1),
		"no sources":   strings.Replace(haulBody, `[{"x":0,"y":0,"z":0}]`, `[]`, 1),
		"five sources": strings.Replace(haulBody, `[{"x":0,"y":0,"z":0}]`, chests(5), 1),
		"missing from": `{"template":"haul","items":{"cobblestone":1},"to":{"x":0,"y":0,"z":0}}`,
		"missing to":   `{"template":"haul","items":{"cobblestone":1},"from":[{"x":0,"y":0,"z":0}]}`,
		"extra field":  strings.Replace(haulBody, `"template":"haul"`, `"template":"haul","qty":1`, 1),
		"gather field": strings.Replace(goodBody, `"template":"gather_and_deposit"`, `"template":"gather_and_deposit","items":{}`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, ok := range []string{chests(4), chests(1)} {
		if _, err := ParseTemplate([]byte(strings.Replace(haulBody, `[{"x":0,"y":0,"z":0}]`, ok, 1))); err != nil {
			t.Errorf("sources %s: %v", ok, err)
		}
	}
}

func TestHaulExpandsToOneHaulSkill(t *testing.T) {
	tpl, err := ParseTemplate([]byte(haulBody))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "haul" {
		t.Fatalf("skills %v, err %v", skills, err)
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

type movedSkill struct{ n int }

func (m *movedSkill) Name() string                { return "haul" }
func (m *movedSkill) Ready(belief.Snapshot) error { return nil }
func (m *movedSkill) Progress() int               { return m.n }
func (m *movedSkill) Step(context.Context, skill.Actuator) (skill.Status, error) {
	return skill.Running, nil
}

func TestHaulProgressIsTheTotalMoved(t *testing.T) {
	r := NewRunner(func(Template) ([]skill.Skill, error) { return []skill.Skill{&movedSkill{n: 7}}, nil },
		fakeAct{}, func() belief.Snapshot { return belief.Snapshot{Spawned: true} }, time.Minute, &captureLog{})
	tk, err := r.Submit(Template{Template: "haul", Qty: 32})
	if err != nil {
		t.Fatal(err)
	}
	r.step(context.Background())
	if got := stateOf(t, r, tk.ID); got.Progress != 7 || got.Qty != 32 {
		t.Errorf("task = %+v, want progress 7 of 32", got)
	}
}
