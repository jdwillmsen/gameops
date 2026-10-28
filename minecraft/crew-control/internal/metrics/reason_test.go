package metrics

import (
	"slices"
	"strings"
	"testing"
	"testing/quick"
)

// The phrases are the ones crew's skills and runner produce today, including
// the three seen most in production.
func TestReasonTable(t *testing.T) {
	for text, want := range map[string]string{
		"no axe in inventory":        ReasonMissingItem,
		"no pickaxe in inventory":    ReasonMissingItem,
		"have 3 oak_log, need 8":     ReasonMissingItem,
		"no oak_log left to deposit": ReasonMissingItem,
		"a stair floor is missing and the unit is out of blocks to bridge it": ReasonMissingItem,
		"no path":                      ReasonNoPath,
		"walking to the goal: no path": ReasonNoPath,
		"getting to the chest: walking to the goal: no path":           ReasonNoPath,
		"no standing spot within reach of the chest":                   ReasonNoPath,
		"trapped with no walking path and out of blocks to climb with": ReasonNoPath,
		"still no path after climbing 4 blocks":                        ReasonNoPath,
		"visit took too long":                                          ReasonTimeout,
		"timed out":                                                    ReasonTimeout,
		"inventory did not show the deposit within 40 ticks":           ReasonTimeout,
		"sticks unavailable":                                           ReasonOther,
		"no answer to the move within 40 ticks":                        ReasonTimeout,
		"the chest's chunk is not loaded":                              ReasonContainer,
		"chest did not open within 300 ticks":                          ReasonContainer,
		"chest is full":                                                ReasonContainer,
		"no chest at the target":                                       ReasonContainer,
		"needs crafting table":                                         ReasonContainer,
		"no pickup after 3 breaks":                                     ReasonNoPickup,
		"no progress after 3 replans":                                  ReasonNoProgress,
		"server rejected the deposit with status 4":                    ReasonServerRejected,
		"the server did not place the block":                           ReasonServerRejected,
		"the unit is not in creative mode":                             ReasonInvalidTask,
		"repair needs 2000 cells changed, more than a unit walks in one task (1000); use arenarepair for this damage": ReasonInvalidTask,
		"repair needs 12 block types and the hotbar has room for 9; narrow the area or use arenarepair":               ReasonInvalidTask,
		"block did not break":                ReasonActionFailed,
		"jump did not land":                  ReasonActionFailed,
		"invalid task: qty must be 1 to 256": ReasonInvalidTask,
		"session ended":                      ReasonSession,
		"unit died":                          ReasonDied,
		"skill failed":                       ReasonOther,
		"":                                   ReasonOther,
	} {
		if got := Reason(text); got != want {
			t.Errorf("Reason(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestReasonIsCaseInsensitive(t *testing.T) {
	if got := Reason("NO AXE IN INVENTORY"); got != ReasonMissingItem {
		t.Fatalf("got %q", got)
	}
}

func TestReasonIsAlwaysInTheClosedSet(t *testing.T) {
	for _, r := range reasonRules {
		if !slices.Contains(Reasons, r.reason) {
			t.Errorf("rule reason %q is not listed in Reasons", r.reason)
		}
	}
	if err := quick.Check(func(s string) bool { return slices.Contains(Reasons, Reason(s)) }, nil); err != nil {
		t.Fatal(err)
	}
	// Free text with the shape of a real reason but arbitrary parts.
	for _, s := range []string{"no " + strings.Repeat("x", 4000), "\x00\xff", "chest\n\tnumber 12345"} {
		if r := Reason(s); !slices.Contains(Reasons, r) {
			t.Errorf("Reason(%q) = %q, outside the closed set", s, r)
		}
	}
}

func TestTemplateIsClosed(t *testing.T) {
	if Template("haul") != "haul" || Template("gather_and_deposit") != "gather_and_deposit" {
		t.Fatal("known templates must pass through")
	}
	if Template("repair_build") != "repair_build" || Template("build_blueprint") != "build_blueprint" {
		t.Fatal("the mender and builder templates must pass through")
	}
	for _, s := range []string{"", "HAUL", "haul ", "build_castle", strings.Repeat("a", 1000)} {
		if got := Template(s); got != TemplateOther {
			t.Errorf("Template(%q) = %q, want %q", s, got, TemplateOther)
		}
	}
}
