package task

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const houseBody = `{"template":"build_blueprint","blueprint":"small_house","at":{"x":10,"y":64,"z":-300}}`

func TestBuildBlueprintParsesItsFields(t *testing.T) {
	tpl, err := ParseTemplate([]byte(`{"template":"build_blueprint","blueprint":"small_house","at":{"x":10,"y":64,"z":-300},"rotation":90,"dry_run":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Blueprint != "small_house" || tpl.At == nil || tpl.At.X != 10 || tpl.Rotation != 90 || !tpl.DryRun || tpl.Qty != 173 {
		t.Errorf("template = %+v", tpl)
	}
	plain, err := ParseTemplate([]byte(houseBody))
	if err != nil || plain.Rotation != 0 || plain.DryRun {
		t.Errorf("defaults: %+v, %v", plain, err)
	}
}

func TestBuildBlueprintRejectsBadRequestsWithoutNamingCoordinates(t *testing.T) {
	cases := map[string]string{
		"unknown blueprint":   `{"template":"build_blueprint","blueprint":"castle","at":{"x":0,"y":64,"z":0}}`,
		"path in name":        `{"template":"build_blueprint","blueprint":"../x","at":{"x":0,"y":64,"z":0}}`,
		"missing blueprint":   `{"template":"build_blueprint","at":{"x":0,"y":64,"z":0}}`,
		"missing at":          `{"template":"build_blueprint","blueprint":"small_house"}`,
		"bad rotation":        `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":45}`,
		"negative rotation":   `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":-90}`,
		"at outside lab":      `{"template":"build_blueprint","blueprint":"small_house","at":{"x":7000,"y":64,"z":0}}`,
		"far corner outside":  `{"template":"build_blueprint","blueprint":"small_house","at":{"x":496,"y":64,"z":0}}`,
		"far corner above":    `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":318,"z":0}}`,
		"rotated corner z":    `{"template":"build_blueprint","blueprint":"redstone_sample","at":{"x":0,"y":64,"z":496},"rotation":90}`,
		"unknown field":       `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"origin":1}`,
		"string rotation":     `{"template":"build_blueprint","blueprint":"small_house","at":{"x":0,"y":64,"z":0},"rotation":"90"}`,
		"coordinate as float": `{"template":"build_blueprint","blueprint":"small_house","at":{"x":1.5,"y":64,"z":0}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTemplate([]byte(body))
			if err == nil {
				t.Fatal("accepted")
			}
			for _, c := range []string{"7000", "496", "318", "1.5"} {
				if strings.Contains(err.Error(), c) {
					t.Errorf("error %q names a coordinate", err)
				}
			}
		})
	}
}

// The footprint is checked after rotation: redstone_sample is 5 wide and 3
// deep, so a quarter turn moves the extent from x to z.
func TestBuildBlueprintChecksTheRotatedFootprint(t *testing.T) {
	edge := `{"template":"build_blueprint","blueprint":"redstone_sample","at":{"x":495,"y":64,"z":496},"rotation":%d}`
	if _, err := ParseTemplate([]byte(strings.Replace(edge, "%d", "0", 1))); err != nil {
		t.Errorf("unrotated fits: %v", err)
	}
	if _, err := ParseTemplate([]byte(strings.Replace(edge, "%d", "90", 1))); err == nil {
		t.Error("rotated footprint runs past the lab and was accepted")
	}
}

func TestBuildBlueprintExpandsToTheBuildSkill(t *testing.T) {
	tpl, err := ParseTemplate([]byte(houseBody))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := Expand(tpl, nil, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "build_blueprint" {
		t.Fatalf("skills = %v, %v", skills, err)
	}
}

func TestOnlyABuilderTakesBuildBlueprint(t *testing.T) {
	for role, want := range map[string]bool{"": false, "mender": false, "vanguard": false, "builder": true} {
		mux, h := newAPI(t, "tok")
		h.r.SetRole(role)
		rec := do(mux, "POST", "/v1/tasks", "Bearer tok", houseBody)
		if want && rec.Code != http.StatusAccepted {
			t.Errorf("role %q: %d %s, want 202", role, rec.Code, rec.Body)
		}
		if !want && rec.Code != http.StatusConflict {
			t.Errorf("role %q: %d %s, want 409", role, rec.Code, rec.Body)
		}
		if !want && len(h.r.List()) != 0 {
			t.Errorf("role %q: a refused task was queued", role)
		}
	}
}

func TestOtherTemplatesDoNotCareAboutRole(t *testing.T) {
	mux, _ := newAPI(t, "tok")
	if rec := do(mux, "POST", "/v1/tasks", "Bearer tok", goodBody); rec.Code != http.StatusAccepted {
		t.Errorf("gather on a unit with no role: %d", rec.Code)
	}
}

type reportingSkill struct {
	fakeSkill
	rep skill.BuildReport
}

func (r *reportingSkill) Report() skill.BuildReport { return r.rep }
func (r *reportingSkill) Progress() int             { return r.rep.Placed }
func (r *reportingSkill) Step(ctx context.Context, a skill.Actuator) (skill.Status, error) {
	r.rep.Placed++
	r.rep.SkippedState = map[string]int{"wooden_door": 1}
	return r.fakeSkill.Step(ctx, a)
}

// The task record carries the stamp's report and its progress against the
// blueprint's block count.
func TestTaskRecordCarriesTheBuildReport(t *testing.T) {
	h := newHarness(t, time.Minute, 2)
	sk := &reportingSkill{fakeSkill: fakeSkill{name: "build_blueprint", steps: 2, ticks: true}}
	h.r.newSkills = func(Template) ([]skill.Skill, error) { return []skill.Skill{sk}, nil }
	h.r.SetRole("builder")
	tpl, err := ParseTemplate([]byte(houseBody))
	if err != nil {
		t.Fatal(err)
	}
	task, err := h.r.Submit(tpl)
	if err != nil {
		t.Fatal(err)
	}
	h.tick(10)
	got := stateOf(t, h.r, task.ID)
	if got.State != Done || got.Qty != 173 || got.Progress != 3 || got.Build == nil || got.Build.SkippedState["wooden_door"] != 1 {
		t.Errorf("record = %+v, build %+v", got, got.Build)
	}
}
