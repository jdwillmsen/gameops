package task

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

const repairBody = `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-320,"y":60,"z":-770},"max":{"x":-300,"y":70,"z":-750}},"dry_run":true}`

func TestParseRepairBuild(t *testing.T) {
	tpl, err := ParseTemplate([]byte(repairBody))
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Template != "repair_build" || tpl.Plan != "colosseum" || !tpl.DryRun || tpl.Area == nil || tpl.Area.Min.X != -320 {
		t.Errorf("parsed %+v", tpl)
	}
	live := strings.Replace(repairBody, `,"dry_run":true`, ``, 1)
	if tpl, err = ParseTemplate([]byte(live)); err != nil || tpl.DryRun {
		t.Errorf("without dry_run: %+v, %v", tpl, err)
	}
}

func TestRepairBuildRejectsWhatIsMalformedOrOutOfBounds(t *testing.T) {
	area := `"area":{"min":{"x":-320,"y":60,"z":-770},"max":{"x":-300,"y":70,"z":-750}}`
	cases := map[string]string{
		"no plan":         `{"template":"repair_build",` + area + `}`,
		"other plan":      `{"template":"repair_build","plan":"labcourses",` + area + `}`,
		"no area":         `{"template":"repair_build","plan":"colosseum"}`,
		"half an area":    `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-320,"y":60,"z":-770}}}`,
		"inverted":        `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-300,"y":60,"z":-770},"max":{"x":-320,"y":70,"z":-750}}}`,
		"too wide":        `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-380,"y":60,"z":-770},"max":{"x":-300,"y":70,"z":-750}}}`,
		"outside arena":   `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":0,"y":60,"z":-770},"max":{"x":10,"y":70,"z":-750}}}`,
		"leaves one side": `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-190,"y":60,"z":-770},"max":{"x":-170,"y":70,"z":-750}}}`,
		"below the build": `{"template":"repair_build","plan":"colosseum","area":{"min":{"x":-320,"y":30,"z":-770},"max":{"x":-300,"y":70,"z":-750}}}`,
		"extra field":     `{"template":"repair_build","plan":"colosseum","item":"stone",` + area + `}`,
		"dry_run string":  `{"template":"repair_build","plan":"colosseum",` + area + `,"dry_run":"yes"}`,
		"trailing":        repairBody + `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTemplate([]byte(body))
			if err == nil {
				t.Fatal("accepted")
			}
			// Positions are lab locations and stay out of logs.
			for _, c := range []string{"-320", "-770", "-750", "-380", "-190"} {
				if strings.Contains(err.Error(), c) {
					t.Errorf("error %q quotes a coordinate", err)
				}
			}
		})
	}
}

type stateProbe struct{ probeWorld }

func (*stateProbe) State(protocol.BlockPos) (string, map[string]any, bool) { return "air", nil, true }

func TestRepairBuildExpandsToOneRepairSkill(t *testing.T) {
	tpl, _ := ParseTemplate([]byte(repairBody))
	skills, err := Expand(tpl, &stateProbe{}, func() belief.Snapshot { return belief.Snapshot{} })
	if err != nil || len(skills) != 1 || skills[0].Name() != "repair_build" {
		t.Fatalf("skills %v, err %v", skills, err)
	}
	if _, err := Expand(tpl, &probeWorld{}, func() belief.Snapshot { return belief.Snapshot{} }); err == nil {
		t.Error("a world with no block states was accepted")
	}
}

func TestOnlyAMenderTakesRepairBuild(t *testing.T) {
	for _, role := range []string{"", "vanguard", "striker", "ranger", "builder"} {
		h := newHarness(t, time.Minute, 1)
		h.r.SetRole(role)
		mux := http.NewServeMux()
		Routes(mux, h.r, "tok")
		rec := do(mux, "POST", "/v1/tasks", "Bearer tok", repairBody)
		if rec.Code != http.StatusConflict {
			t.Errorf("role %q: status %d body %q, want a conflict", role, rec.Code, rec.Body)
		}
		if len(h.r.List()) != 0 {
			t.Errorf("role %q: a task was queued", role)
		}
	}
}

type reporting struct {
	fakeSkill
	text string
}

func (r *reporting) Report() (string, bool) { return r.text, r.text != "" }

func TestAFinishedSkillsReportRidesTheDoneTask(t *testing.T) {
	h := newHarness(t, time.Minute, 1)
	sk := &reporting{fakeSkill: fakeSkill{name: "repair_build", steps: 1, ticks: true}, text: "repaired 2 cells"}
	h.r.newSkills = func(Template) ([]skill.Skill, error) { return []skill.Skill{sk}, nil }
	tk, err := h.r.Submit(tpl())
	if err != nil {
		t.Fatal(err)
	}
	h.tick(6)
	got := stateOf(t, h.r, tk.ID)
	if got.State != Done || got.Reason != "repaired 2 cells" || got.ReasonCode != "" {
		t.Errorf("task = %+v", got)
	}
}
