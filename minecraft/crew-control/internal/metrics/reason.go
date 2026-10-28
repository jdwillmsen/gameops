package metrics

import "strings"

// Closed set of failure reasons. A unit reports a failure as free text that
// can embed numbers and item names, so exporting it raw would mint a series
// per phrasing; Reason maps every text onto one of these instead.
const (
	ReasonNoPath         = "no_path"
	ReasonMissingItem    = "missing_item"
	ReasonNoPickup       = "no_pickup"
	ReasonNoProgress     = "no_progress"
	ReasonContainer      = "container"
	ReasonServerRejected = "server_rejected"
	ReasonTimeout        = "timeout"
	ReasonActionFailed   = "action_failed"
	ReasonInvalidTask    = "invalid_task"
	ReasonSession        = "session"
	ReasonDied           = "died"
	ReasonOther          = "other"
)

// Reasons lists every value Reason can return.
var Reasons = []string{
	ReasonNoPath, ReasonMissingItem, ReasonNoPickup, ReasonNoProgress,
	ReasonContainer, ReasonServerRejected, ReasonTimeout, ReasonActionFailed,
	ReasonInvalidTask, ReasonSession, ReasonDied, ReasonOther,
}

// Most specific first: a unit that cannot reach a chest reports both the
// chest and the missing path, and the path is the cause worth counting. The
// first rule with a matching phrase wins.
var reasonRules = []struct {
	reason  string
	phrases []string
}{
	{ReasonSession, []string{"session ended"}},
	{ReasonDied, []string{"unit died"}},
	{ReasonNoPath, []string{"no path", "no walking path", "no standing spot", "cannot reach", "no way down", "trapped"}},
	{ReasonMissingItem, []string{" in inventory", "left to deposit", ", need ", "out of blocks", "nothing to haul"}},
	{ReasonNoPickup, []string{"no pickup"}},
	{ReasonNoProgress, []string{"no progress after"}},
	{ReasonContainer, []string{"chest", "crafting table"}},
	{ReasonServerRejected, []string{"server rejected", "server did not", "did not confirm"}},
	{ReasonTimeout, []string{"took too long", "timed out", " ticks"}},
	{ReasonActionFailed, []string{"did not break", "did not land", "never settled", "drifted", "did not come down", "is blocked at its first step"}},
	{ReasonInvalidTask, []string{"invalid task", "outside the allowed", "outside the area", "unknown template", "not in creative mode", "more than a unit walks", "hotbar has room"}},
}

// maxReasonText bounds the text matched: a unit's reason is its own words,
// and nothing past the first sentence changes which row it falls in.
const maxReasonText = 256

// Reason classifies a unit's failure text. Text that matches no rule is
// ReasonOther, so a new failure shows up in the catch-all until someone
// adds a row for it.
func Reason(text string) string {
	if len(text) > maxReasonText {
		text = text[:maxReasonText]
	}
	text = strings.ToLower(text)
	for _, r := range reasonRules {
		for _, p := range r.phrases {
			if strings.Contains(text, p) {
				return r.reason
			}
		}
	}
	return ReasonOther
}

// Task templates crew accepts. Unit-reported templates outside this list
// are exported as TemplateOther: the label must not grow with whatever a
// unit or a future version sends.
const TemplateOther = "other"

var Templates = []string{
	"gather_and_deposit", "haul", "withdraw", "deposit", "mine_shaft",
	"goto", "inspect", "observe_blocks", "place_block", "craft",
	"repair_build", "build_blueprint",
}

// Template maps a reported template onto the closed set.
func Template(s string) string {
	for _, t := range Templates {
		if s == t {
			return t
		}
	}
	return TemplateOther
}
