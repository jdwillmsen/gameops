package work

import (
	"context"
	"strings"
	"testing"
)

const (
	repairOK = `{"template":"repair_build","plan":"colosseum","dry_run":true,"area":{"min":{"x":-320,"y":60,"z":-770},"max":{"x":-300,"y":70,"z":-750}}}`
	buildOK  = `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":90,"dry_run":false}`
)

func TestQueueTaskAcceptsRepairAndBuild(t *testing.T) {
	o := newOpRig(t, NewMemory())
	for name, body := range map[string]string{
		"repair":         repairOK,
		"repair no dry":  `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":0,"y":0,"z":0},"max":{"x":63,"y":63,"z":63}}}`,
		"build":          buildOK,
		"build minimal":  `{"template":"build_blueprint","blueprint":"a","at":{"x":0,"y":64,"z":0}}`,
		"build rotation": `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":270}`,
	} {
		if code, resp := o.post(t, "/v1/work/crew-01/tasks", body, nil); code != 202 {
			t.Errorf("%s: %d %s, want 202", name, code, resp)
		}
	}
}

func TestQueueTaskRejectsBadRepairAndBuild(t *testing.T) {
	o := newOpRig(t, NewMemory())
	area := func(min, max string) string {
		return `{"template":"repair_build","plan":"colosseum","area":{"min":` + min + `,"max":` + max + `}}`
	}
	for name, body := range map[string]string{
		"repair no plan":      `{"template":"repair_build","area":{"min":{"x":0,"y":0,"z":0},"max":{"x":1,"y":1,"z":1}}}`,
		"repair other plan":   strings.Replace(repairOK, "colosseum", "village", 1),
		"repair no area":      `{"template":"repair_build","plan":"colosseum"}`,
		"repair half area":    `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":0,"y":0,"z":0}}}`,
		"repair inverted":     area(`{"x":5,"y":0,"z":0}`, `{"x":4,"y":1,"z":1}`),
		"repair wide x":       area(`{"x":0,"y":0,"z":0}`, `{"x":64,"y":1,"z":1}`),
		"repair wide y":       area(`{"x":0,"y":0,"z":0}`, `{"x":1,"y":64,"z":1}`),
		"repair wide z":       area(`{"x":0,"y":0,"z":0}`, `{"x":1,"y":1,"z":64}`),
		"repair y low":        area(`{"x":0,"y":-65,"z":0}`, `{"x":1,"y":-64,"z":1}`),
		"repair y high":       area(`{"x":0,"y":320,"z":0}`, `{"x":1,"y":321,"z":1}`),
		"repair far":          area(`{"x":90000000,"y":0,"z":0}`, `{"x":90000001,"y":1,"z":1}`),
		"repair unknown":      strings.Replace(repairOK, `"dry_run":true`, `"dry_run":true,"speed":1`, 1),
		"repair dry typed":    strings.Replace(repairOK, `"dry_run":true`, `"dry_run":"yes"`, 1),
		"build no name":       `{"template":"build_blueprint","at":{"x":0,"y":64,"z":0}}`,
		"build upper name":    `{"template":"build_blueprint","blueprint":"House","at":{"x":0,"y":64,"z":0}}`,
		"build dash name":     `{"template":"build_blueprint","blueprint":"a-b","at":{"x":0,"y":64,"z":0}}`,
		"build path name":     `{"template":"build_blueprint","blueprint":"../x","at":{"x":0,"y":64,"z":0}}`,
		"build long name":     `{"template":"build_blueprint","blueprint":"` + strings.Repeat("a", 33) + `","at":{"x":0,"y":64,"z":0}}`,
		"build no at":         `{"template":"build_blueprint","blueprint":"small_house"}`,
		"build at outside":    `{"template":"build_blueprint","blueprint":"small_house","at":{"x":500,"y":64,"z":0}}`,
		"build at y":          `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":321,"z":0}}`,
		"build rotation 45":   `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":45}`,
		"build rotation 360":  `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":360}`,
		"build rotation neg":  `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":-90}`,
		"build unknown field": strings.Replace(buildOK, `"rotation":90`, `"rotation":90,"area":1`, 1),
	} {
		code, resp := o.post(t, "/v1/work/crew-01/tasks", body, nil)
		if code != 400 {
			t.Errorf("%s: %d %s, want 400", name, code, resp)
		}
		for _, leak := range []string{"500", "90000000", "-770", "321"} {
			if strings.Contains(resp, leak) {
				t.Errorf("%s: error echoes input: %s", name, resp)
			}
		}
	}
	if got, _ := o.store.Recent(context.Background(), "crew-01", 10); len(got) != 0 {
		t.Fatalf("rejected bodies queued %d items", len(got))
	}
}

func TestQueueTaskRefusesAWrongKnownRole(t *testing.T) {
	o := newOpRig(t, NewMemory())
	roles := map[string]string{"crew-01": "builder", "crew-02": "mender"}
	o.api.Role = func(unit string) string { return roles[unit] }
	for _, c := range []struct {
		unit, body string
		want       int
	}{
		{"crew-01", buildOK, 202},
		{"crew-01", repairOK, 409},
		{"crew-02", repairOK, 202},
		{"crew-02", buildOK, 409},
		{"crew-01", gotoOK, 202},
	} {
		code, resp := o.post(t, "/v1/work/"+c.unit+"/tasks", c.body, nil)
		if code != c.want {
			t.Errorf("%s %.30s: %d %s, want %d", c.unit, c.body, code, resp, c.want)
		}
	}
	for _, unit := range []string{"crew-01", "crew-02"} {
		items, _ := o.store.Recent(context.Background(), unit, 10)
		for _, it := range items {
			if strings.Contains(string(it.Body), "repair_build") && unit == "crew-01" ||
				strings.Contains(string(it.Body), "build_blueprint") && unit == "crew-02" {
				t.Errorf("%s queued a refused task", unit)
			}
		}
	}
}

func TestQueueTaskAllowsAnUnknownRole(t *testing.T) {
	o := newOpRig(t, NewMemory())
	for _, role := range []func(string) string{nil, func(string) string { return "" }} {
		o.api.Role = role
		for _, body := range []string{repairOK, buildOK} {
			if code, resp := o.post(t, "/v1/work/crew-01/tasks", body, nil); code != 202 {
				t.Errorf("unknown role: %d %s, want 202", code, resp)
			}
		}
	}
}

func TestBulkAssignSkipsUnitsWithAWrongKnownRole(t *testing.T) {
	o := newOpRig(t, NewMemory())
	o.api.Role = func(unit string) string {
		if unit == "crew-01" {
			return "ranger"
		}
		return ""
	}
	code, body := o.post(t, "/v1/work/bulk", bulkBody(`["crew-01","crew-02"]`, "2", `"action":"assign","task":`+buildOK), nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if got, _ := o.store.Recent(context.Background(), "crew-01", 10); len(got) != 0 {
		t.Fatalf("refused unit queued %d items", len(got))
	}
	if got, _ := o.store.Recent(context.Background(), "crew-02", 10); len(got) != 1 {
		t.Fatalf("allowed unit queued %d items", len(got))
	}
	if !strings.Contains(body, "role") {
		t.Errorf("result does not say why: %s", body)
	}
}
