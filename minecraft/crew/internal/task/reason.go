package task

import (
	"errors"
	"regexp"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

// The closed set of failure reasons. A reason string carries numbers (a status,
// a break count) and wording that changes between releases, so anything that
// groups or chains failures keys on the code instead. Adding a code is a
// change here and in the docs catalogue; consumers treat an unknown one as
// other.
const (
	ReasonMissingTool       = "missing_tool"
	ReasonKitChunkNotLoaded = "kit_chunk_not_loaded"
	ReasonKitNoPath         = "kit_no_path"
	ReasonKitChestTimeout   = "kit_chest_timeout"
	ReasonKitVisitTimeout   = "kit_visit_timeout"
	ReasonSessionLost       = "session_lost"
	ReasonDepositRejected   = "deposit_rejected"
	ReasonNoPickup          = "no_pickup"
	ReasonNoReachableTarget = "no_reachable_target"
	ReasonTrapClimbFailed   = "trap_climb_failed"
	ReasonInventoryDesync   = "inventory_desync"
	ReasonChunkNotLoaded    = "chunk_not_loaded"
	ReasonNoPath            = "no_path"
	ReasonOther             = "other"
)

// ReasonCodes lists every code, for the docs test and for consumers that
// validate what a unit sent.
var ReasonCodes = []string{
	ReasonMissingTool, ReasonKitChunkNotLoaded, ReasonKitNoPath, ReasonKitChestTimeout,
	ReasonKitVisitTimeout, ReasonSessionLost, ReasonDepositRejected, ReasonNoPickup,
	ReasonNoReachableTarget, ReasonTrapClimbFailed, ReasonInventoryDesync,
	ReasonChunkNotLoaded, ReasonNoPath, ReasonOther,
}

// reasonRule is one row of the mapping. Rows are tried in order and the first
// match wins, so the specific phrase must precede the general one it contains.
type reasonRule struct {
	match *regexp.Regexp
	code  string
}

// Patterns tolerate embedded numbers (status 6, after 3 breaks) because the
// skills format them into the string.
var reasonRules = []reasonRule{
	{regexp.MustCompile(`^no [a-z_ ]+ in inventory$`), ReasonMissingTool},
	{regexp.MustCompile(`trapped with no walking path|climbing out of a trap|still no path after climbing|no way down from the pillar`), ReasonTrapClimbFailed},
	{regexp.MustCompile(`no pickup after \d+ breaks`), ReasonNoPickup},
	{regexp.MustCompile(`server rejected the (deposit|withdrawal|source stack)`), ReasonDepositRejected},
	{regexp.MustCompile(`inventory did not show`), ReasonInventoryDesync},
	{regexp.MustCompile(`wire down|session ended|connection (lost|closed)`), ReasonSessionLost},
	{regexp.MustCompile(`no standing spot|cannot reach|the entrance is outside|the plan reaches outside`), ReasonNoReachableTarget},
	{regexp.MustCompile(`chest did not open within \d+ ticks`), ReasonKitChestTimeout},
	{regexp.MustCompile(`chunk is not loaded`), ReasonChunkNotLoaded},
	{regexp.MustCompile(`\bno path\b|no walking path`), ReasonNoPath},
}

// ReasonCode maps a failure reason to its closed code. Typed errors are
// matched by the skill that raised them before this is reached; this is the
// single place that classifies the free text the rest produce.
func ReasonCode(reason string) string {
	r := strings.ToLower(strings.TrimSpace(reason))
	if r == "" {
		return ""
	}
	for _, rule := range reasonRules {
		if rule.match.MatchString(r) {
			return rule.code
		}
	}
	return ReasonOther
}

// KitReasonCode classifies a failed kit visit. The same text means something
// else here than in a task: a chest that will not open or a path that does not
// exist is a kit problem the unit retries, not a failed piece of work.
func KitReasonCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, skill.ErrChestChunkNotLoaded):
		return ReasonKitChunkNotLoaded
	case errors.Is(err, skill.ErrChestNotOpened):
		return ReasonKitChestTimeout
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "visit took too long"):
		return ReasonKitVisitTimeout
	case strings.Contains(text, "chunk is not loaded"):
		return ReasonKitChunkNotLoaded
	case strings.Contains(text, "did not open within"):
		return ReasonKitChestTimeout
	case strings.Contains(text, "no path"), strings.Contains(text, "no standing spot"):
		return ReasonKitNoPath
	}
	return ReasonCode(text)
}
