package task

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

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

const haulBody = `{"template":"haul","items":{"cobblestone":16,"oak_log":16},"from":[{"x":0,"y":0,"z":0}],"to":{"x":9,"y":9,"z":9}}`

func TestParseHaulTemplate(t *testing.T) {
	tpl, err := ParseTemplate([]byte(haulBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Items["cobblestone"] != 16 || tpl.Items["oak_log"] != 16 || len(tpl.From) != 1 || tpl.To == nil {
		t.Errorf("parsed %+v", tpl)
	}
	chests := func(n int) string {
		var cs []string
		for i := 0; i < n; i++ {
			cs = append(cs, fmt.Sprintf(`{"x":%d,"y":0,"z":0}`, i))
		}
		return `[` + strings.Join(cs, ",") + `]`
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
		"to in from":   strings.Replace(haulBody, `[{"x":0,"y":0,"z":0}]`, `[{"x":0,"y":0,"z":0},{"x":9,"y":9,"z":9}]`, 1),
		"duplicate":    strings.Replace(haulBody, `[{"x":0,"y":0,"z":0}]`, `[{"x":0,"y":0,"z":0},{"x":0,"y":0,"z":0}]`, 1),
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

// transferBody is the withdraw wire form; the deposit cases swap the name.
const transferBody = `{"template":"withdraw","chest":{"x":1,"y":2,"z":3},"items":{"oak_log":4,"cobblestone":16}}`

func TestParseTransferTemplates(t *testing.T) {
	for _, name := range []string{"withdraw", "deposit"} {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(transferBody, `"withdraw"`, `"`+name+`"`, 1)
			tpl, err := ParseTemplate([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if tpl.Template != name || tpl.Chest != (Chest{X: 1, Y: 2, Z: 3}) ||
				tpl.Items["oak_log"] != 4 || tpl.Items["cobblestone"] != 16 || tpl.Qty != 20 {
				t.Errorf("parsed %+v", tpl)
			}
			nine := `{"cobblestone":1,"oak_log":1,"a":1,"b":1,"c":1,"d":1,"e":1,"f":1,"g":1}`
			cases := map[string]string{
				"missing chest": `{"template":"` + name + `","items":{"oak_log":4}}`,
				"missing items": `{"template":"` + name + `","chest":{"x":0,"y":0,"z":0}}`,
				"empty items":   strings.Replace(body, `{"oak_log":4,"cobblestone":16}`, `{}`, 1),
				"nine items":    strings.Replace(body, `{"oak_log":4,"cobblestone":16}`, nine, 1),
				"qty zero":      strings.Replace(body, `"oak_log":4`, `"oak_log":0`, 1),
				"qty 257":       strings.Replace(body, `"oak_log":4`, `"oak_log":257`, 1),
				"upper case":    strings.Replace(body, `"oak_log"`, `"Oak_Log"`, 1),
				"spaced":        strings.Replace(body, `"oak_log"`, `"oak log"`, 1),
				"namespaced":    strings.Replace(body, `"oak_log"`, `"minecraft:oak_log"`, 1),
				"too long":      strings.Replace(body, `"oak_log"`, `"`+strings.Repeat("a", 33)+`"`, 1),
				"x low":         strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":-501,"y":2,"z":3`, 1),
				"x high":        strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":500,"y":2,"z":3`, 1),
				"y low":         strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":1,"y":-65,"z":3`, 1),
				"y high":        strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":1,"y":321,"z":3`, 1),
				"z low":         strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":1,"y":2,"z":-501`, 1),
				"z high":        strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":1,"y":2,"z":500`, 1),
				"extra field":   strings.Replace(body, `"template":"`+name+`"`, `"template":"`+name+`","qty":1`, 1),
				"extra key":     strings.Replace(body, `"x":1,"y":2,"z":3`, `"x":1,"y":2,"z":3,"w":1`, 1),
				"trailing":      body + `{}`,
				"not json":      `nope`,
			}
			for name, body := range cases {
				t.Run(name, func(t *testing.T) {
					if _, err := ParseTemplate([]byte(body)); err == nil {
						t.Fatal("accepted")
					}
				})
			}
			for _, ok := range []string{
				`{"template":"` + name + `","chest":{"x":-500,"y":-64,"z":-500},"items":{"oak_log":1}}`,
				`{"template":"` + name + `","chest":{"x":499,"y":320,"z":499},"items":{"oak_log":256}}`,
			} {
				if _, err := ParseTemplate([]byte(ok)); err != nil {
					t.Errorf("boundary %s: %v", ok, err)
				}
			}
		})
	}
}

// probeWorld answers every block query with air so a chest visit fails at its
// first step, after naming the position it asked about.
type probeWorld struct{ asked []protocol.BlockPos }

func (w *probeWorld) Block(p protocol.BlockPos) (string, bool) {
	w.asked = append(w.asked, p)
	return "air", true
}
func (w *probeWorld) BlockHash(protocol.BlockPos) (uint32, bool) { return 0, false }

// probeAct is just enough actuator for a visit that fails before it clicks.
type probeAct struct{ skill.Actuator }

func (probeAct) Container() (bool, byte, protocol.BlockPos) { return false, 0, protocol.BlockPos{} }

func TestWithdrawExpandsToOneVisitOfTheChest(t *testing.T) {
	tpl, err := ParseTemplate([]byte(transferBody))
	if err != nil {
		t.Fatal(err)
	}
	w := &probeWorld{}
	skills, err := Expand(tpl, w, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "withdraw_chest" {
		t.Fatalf("skills %v, err %v", skills, err)
	}
	st, err := skills[0].Step(context.Background(), probeAct{})
	if st != skill.Failed || err == nil || err.Error() != "no chest at the target" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if len(w.asked) != 1 || w.asked[0] != (protocol.BlockPos{1, 2, 3}) {
		t.Errorf("asked about %v, want the one chest", w.asked)
	}
}

func TestDepositExpandsOneVisitPerItem(t *testing.T) {
	body := strings.Replace(transferBody, `"withdraw"`, `"deposit"`, 1)
	tpl, err := ParseTemplate([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	w := &probeWorld{}
	skills, err := Expand(tpl, w, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills %v, err %v", skills, err)
	}
	// Sorted, so the same request always expands the same way.
	for i, want := range []string{"have 0 cobblestone, need 16", "have 0 oak_log, need 4"} {
		if skills[i].Name() != "deposit_chest" {
			t.Errorf("skill %d = %s, want deposit_chest", i, skills[i].Name())
		}
		if err := skills[i].Ready(belief.Snapshot{}); err == nil || err.Error() != want {
			t.Errorf("skill %d Ready = %v, want %q", i, err, want)
		}
		st, err := skills[i].Step(context.Background(), probeAct{})
		if st != skill.Failed || err == nil {
			t.Fatalf("skill %d: status %v, err %v", i, st, err)
		}
	}
	if len(w.asked) != 2 || w.asked[0] != (protocol.BlockPos{1, 2, 3}) || w.asked[1] != (protocol.BlockPos{1, 2, 3}) {
		t.Errorf("asked about %v, want the chest twice", w.asked)
	}
}

func TestGatherAcceptsOakLog(t *testing.T) {
	body := strings.Replace(goodBody, `"cobblestone"`, `"oak_log"`, 1)
	tpl, err := ParseTemplate([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills %v, err %v", skills, err)
	}
	if skills[0].Name() != "fell_tree" || skills[1].Name() != "deposit_chest" {
		t.Errorf("skills = %s, %s: oak logs are felled, not just mined", skills[0].Name(), skills[1].Name())
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
		&fakeAct{}, func() belief.Snapshot { return belief.Snapshot{Spawned: true} }, time.Minute, &captureLog{})
	tk, err := r.Submit(Template{Template: "haul", Qty: 32})
	if err != nil {
		t.Fatal(err)
	}
	r.step(context.Background())
	if got := stateOf(t, r, tk.ID); got.Progress != 7 || got.Qty != 32 {
		t.Errorf("task = %+v, want progress 7 of 32", got)
	}
}

func TestParseGatherArea(t *testing.T) {
	with := func(area string) string {
		return strings.Replace(goodBody, `"chest"`, `"area":`+area+`,"chest"`, 1)
	}
	tpl, err := ParseTemplate([]byte(with(`{"min":{"x":-12,"y":99,"z":-12},"max":{"x":12,"y":106,"z":12}}`)))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Area == nil || tpl.Area.Min.X != -12 || tpl.Area.Max.Y != 106 {
		t.Errorf("parsed %+v", tpl.Area)
	}
	if tpl, err := ParseTemplate([]byte(goodBody)); err != nil || tpl.Area != nil {
		t.Errorf("no area: %+v, %v", tpl.Area, err)
	}
	if _, err := ParseTemplate([]byte(with(`{"min":{"x":0,"y":0,"z":0},"max":{"x":63,"y":63,"z":63}}`))); err != nil {
		t.Errorf("64 blocks wide rejected: %v", err)
	}
	cases := map[string]string{
		"inverted x":    with(`{"min":{"x":5,"y":0,"z":0},"max":{"x":4,"y":0,"z":0}}`),
		"inverted y":    with(`{"min":{"x":0,"y":5,"z":0},"max":{"x":0,"y":4,"z":0}}`),
		"oversize":      with(`{"min":{"x":0,"y":0,"z":0},"max":{"x":64,"y":0,"z":0}}`),
		"huge span":     with(`{"min":{"x":-2147483648,"y":0,"z":0},"max":{"x":2147483647,"y":0,"z":0}}`),
		"unknown field": with(`{"min":{"x":0,"y":0,"z":0},"max":{"x":1,"y":1,"z":1},"pad":1}`),
		"unknown coord": with(`{"min":{"x":0,"y":0,"z":0,"w":1},"max":{"x":1,"y":1,"z":1}}`),
		"missing max":   with(`{"min":{"x":0,"y":0,"z":0}}`),
		"null parts":    with(`{}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTemplate([]byte(body))
			if err == nil {
				t.Fatal("accepted")
			}
			// The span limit is a constant, not a coordinate.
			if strings.ContainsAny(strings.ReplaceAll(err.Error(), "at most 64 blocks", ""), "0123456789") {
				t.Errorf("error carries numbers: %q", err)
			}
		})
	}
}

const shaftBody = `{"template":"mine_shaft","entrance":{"x":10,"y":1,"z":10},"facing":"east","depth":4,"branches":2,"branch_length":3,` +
	`"area":{"min":{"x":5,"y":-15,"z":2},"max":{"x":25,"y":6,"z":20}},"to":{"x":8,"y":1,"z":5}}`

func TestMineShaftTemplateParsesAndExpandsToOneSkill(t *testing.T) {
	tpl, err := ParseTemplate([]byte(shaftBody))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "mine_shaft" {
		t.Fatalf("skills %v, err %v", skills, err)
	}
}

func TestMineShaftTemplateRejectsWhatWouldLeaveTheAreaOrIsMalformed(t *testing.T) {
	cases := map[string]string{
		"no area":       strings.Replace(shaftBody, `"area":{"min":{"x":5,"y":-15,"z":2},"max":{"x":25,"y":6,"z":20}},`, ``, 1),
		"no to":         strings.Replace(shaftBody, `,"to":{"x":8,"y":1,"z":5}`, ``, 1),
		"no entrance":   strings.Replace(shaftBody, `"entrance":{"x":10,"y":1,"z":10},`, ``, 1),
		"bad facing":    strings.Replace(shaftBody, `"east"`, `"up"`, 1),
		"zero depth":    strings.Replace(shaftBody, `"depth":4`, `"depth":0`, 1),
		"deep":          strings.Replace(shaftBody, `"depth":4`, `"depth":33`, 1),
		"four branches": strings.Replace(shaftBody, `"branches":2`, `"branches":4`, 1),
		"no length":     strings.Replace(shaftBody, `"branch_length":3`, `"branch_length":0`, 1),
		"length alone":  strings.Replace(shaftBody, `"branches":2`, `"branches":0`, 1),
		"extra field":   strings.Replace(shaftBody, `"depth":4`, `"depth":4,"qty":1`, 1),
		"huge area":     strings.Replace(shaftBody, `"x":25,"y":6`, `"x":125,"y":6`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	// The plan, not just the entrance, must fit: this one parses but cannot
	// be submitted.
	for name, body := range map[string]string{
		"too deep for the area": strings.Replace(shaftBody, `"y":-15`, `"y":-1`, 1),
		"branch past the edge":  strings.Replace(shaftBody, `"x":25`, `"x":14`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			tpl, err := ParseTemplate([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} }); err == nil {
				t.Fatal("expanded")
			}
		})
	}
}

func TestParseGoto(t *testing.T) {
	tpl, err := ParseTemplate([]byte(`{"template":"goto","to":{"x":-500,"y":320,"z":499}}`))
	if err != nil {
		t.Fatalf("valid goto: %v", err)
	}
	if tpl.Template != "goto" || tpl.To == nil || *tpl.To != (Chest{X: -500, Y: 320, Z: 499}) {
		t.Fatalf("parsed = %+v", tpl)
	}
	cases := map[string]string{
		"missing to": `{"template":"goto"}`,
		"x low":      `{"template":"goto","to":{"x":-501,"y":0,"z":0}}`,
		"x high":     `{"template":"goto","to":{"x":500,"y":0,"z":0}}`,
		"y low":      `{"template":"goto","to":{"x":0,"y":-65,"z":0}}`,
		"y high":     `{"template":"goto","to":{"x":0,"y":321,"z":0}}`,
		"z low":      `{"template":"goto","to":{"x":0,"y":0,"z":-501}}`,
		"z high":     `{"template":"goto","to":{"x":0,"y":0,"z":500}}`,
		"extra":      `{"template":"goto","to":{"x":0,"y":0,"z":0},"item":"oak_log"}`,
		"extra key":  `{"template":"goto","to":{"x":0,"y":0,"z":0,"w":1}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestGotoExpandsToOneNavigate(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(`{"template":"goto","to":{"x":1,"y":2,"z":3}}`))
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "navigate" {
		t.Fatalf("skills = %v, err = %v", skills, err)
	}
}

func TestParseInspect(t *testing.T) {
	tpl, err := ParseTemplate([]byte(`{"template":"inspect","chest":{"x":-500,"y":320,"z":499}}`))
	if err != nil {
		t.Fatalf("valid inspect: %v", err)
	}
	if tpl.Template != "inspect" || tpl.Chest != (Chest{X: -500, Y: 320, Z: 499}) {
		t.Fatalf("parsed = %+v", tpl)
	}
	cases := map[string]string{
		"missing chest": `{"template":"inspect"}`,
		"x low":         `{"template":"inspect","chest":{"x":-501,"y":0,"z":0}}`,
		"x high":        `{"template":"inspect","chest":{"x":500,"y":0,"z":0}}`,
		"y low":         `{"template":"inspect","chest":{"x":0,"y":-65,"z":0}}`,
		"y high":        `{"template":"inspect","chest":{"x":0,"y":321,"z":0}}`,
		"z low":         `{"template":"inspect","chest":{"x":0,"y":0,"z":-501}}`,
		"z high":        `{"template":"inspect","chest":{"x":0,"y":0,"z":500}}`,
		"extra":         `{"template":"inspect","chest":{"x":0,"y":0,"z":0},"item":"oak_log"}`,
		"extra key":     `{"template":"inspect","chest":{"x":0,"y":0,"z":0,"w":1}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestInspectExpandsToOneInspect(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(`{"template":"inspect","chest":{"x":1,"y":2,"z":3}}`))
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "inspect" {
		t.Fatalf("skills %v, err %v", skills, err)
	}
}

const placeBody = `{"template":"place_block","block":{"x":7,"y":1,"z":7},"item":"cobblestone"}`

func TestParsePlaceBlock(t *testing.T) {
	tpl, err := ParseTemplate([]byte(placeBody))
	if err != nil {
		t.Fatalf("valid place_block: %v", err)
	}
	if tpl.Template != "place_block" || tpl.Block == nil || *tpl.Block != (Chest{X: 7, Y: 1, Z: 7}) || tpl.Item != "cobblestone" {
		t.Fatalf("parsed = %+v", tpl)
	}
	cases := map[string]string{
		"missing block":   `{"template":"place_block","item":"cobblestone"}`,
		"missing item":    `{"template":"place_block","block":{"x":0,"y":0,"z":0}}`,
		"x low":           `{"template":"place_block","block":{"x":-501,"y":0,"z":0},"item":"cobblestone"}`,
		"x high":          `{"template":"place_block","block":{"x":500,"y":0,"z":0},"item":"cobblestone"}`,
		"y low":           `{"template":"place_block","block":{"x":0,"y":-65,"z":0},"item":"cobblestone"}`,
		"y high":          `{"template":"place_block","block":{"x":0,"y":321,"z":0},"item":"cobblestone"}`,
		"z low":           `{"template":"place_block","block":{"x":0,"y":0,"z":-501},"item":"cobblestone"}`,
		"z high":          `{"template":"place_block","block":{"x":0,"y":0,"z":500},"item":"cobblestone"}`,
		"upper case item": `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"Cobblestone"}`,
		"space in item":   `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"cobble stone"}`,
		"namespaced item": `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"minecraft:cobblestone"}`,
		"empty item":      `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":""}`,
		"long item":       `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"` + strings.Repeat("a", 33) + `"}`,
		"extra field":     `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"cobblestone","qty":1}`,
		"extra block key": `{"template":"place_block","block":{"x":0,"y":0,"z":0,"w":1},"item":"cobblestone"}`,
		"gather field":    `{"template":"place_block","block":{"x":0,"y":0,"z":0},"item":"cobblestone","chest":{"x":0,"y":0,"z":0}}`,
		"trailing data":   placeBody + `{}`,
		"not json":        `nope`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestPlaceBlockExpandsToOneSkill(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(placeBody))
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "place_block" {
		t.Fatalf("skills %v, err %v", skills, err)
	}
}

func TestParseObserve(t *testing.T) {
	tpl, err := ParseTemplate([]byte(`{"template":"observe_blocks","box":{"min":{"x":-500,"y":-64,"z":-500},"max":{"x":-485,"y":-49,"z":-485}}}`))
	if err != nil {
		t.Fatalf("a 16 by 16 by 16 box is 4096 cells: %v", err)
	}
	if tpl.Template != "observe_blocks" || tpl.Box == nil ||
		tpl.Box.Min != (Chest{X: -500, Y: -64, Z: -500}) || tpl.Box.Max != (Chest{X: -485, Y: -49, Z: -485}) {
		t.Fatalf("parsed = %+v", tpl)
	}
	cases := map[string]string{
		"missing box":     `{"template":"observe_blocks"}`,
		"missing max":     `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0}}}`,
		"min over max":    `{"template":"observe_blocks","box":{"min":{"x":5,"y":0,"z":0},"max":{"x":4,"y":0,"z":0}}}`,
		"too many cells":  `{"template":"observe_blocks","box":{"min":{"x":0,"y":64,"z":0},"max":{"x":16,"y":64,"z":240}}}`,
		"x low":           `{"template":"observe_blocks","box":{"min":{"x":-501,"y":0,"z":0},"max":{"x":0,"y":0,"z":0}}}`,
		"x high":          `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":500,"y":0,"z":0}}}`,
		"y low":           `{"template":"observe_blocks","box":{"min":{"x":0,"y":-65,"z":0},"max":{"x":0,"y":0,"z":0}}}`,
		"y high":          `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":321,"z":0}}}`,
		"z high":          `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":500}}}`,
		"extra":           `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":0}},"item":"oak_log"}`,
		"extra key":       `{"template":"observe_blocks","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":0,"w":1}}}`,
		"area is not box": `{"template":"observe_blocks","area":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":0}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTemplate([]byte(body)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestObserveExpandsToOneObserve(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(`{"template":"observe_blocks","box":{"min":{"x":1,"y":2,"z":3},"max":{"x":4,"y":5,"z":6}}}`))
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "observe_blocks" {
		t.Fatalf("skills = %v, err = %v", skills, err)
	}
}

func TestDecodeErrorsQuoteNoCoordinate(t *testing.T) {
	_, err := ParseTemplate([]byte(`{"template":"withdraw","chest":{"x":123.5,"y":64,"z":0},"items":{"oak_log":1}}`))
	if err == nil {
		t.Fatal("a fractional coordinate was accepted")
	}
	if strings.Contains(err.Error(), "123") {
		t.Errorf("error %q quotes the coordinate", err)
	}
}
