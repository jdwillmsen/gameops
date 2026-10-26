package task

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/skill"
)

func TestReasonCode(t *testing.T) {
	cases := []struct{ reason, want string }{
		{"no axe in inventory", ReasonMissingTool},
		{"no pickaxe in inventory", ReasonMissingTool},
		{"the chest's chunk is not loaded", ReasonChunkNotLoaded},
		{"chest did not open within 300 ticks", ReasonKitChestTimeout},
		{"server rejected the deposit with status 6", ReasonDepositRejected},
		{"server rejected the deposit with status 12", ReasonDepositRejected},
		{"server rejected the withdrawal with status 3", ReasonDepositRejected},
		{"server rejected the source stack", ReasonDepositRejected},
		{"haul gave up after repeated failures: getting to the chest: walking to the goal: no path", ReasonNoPath},
		{"getting to the target: walking to the goal: no path", ReasonNoPath},
		{"no pickup after 3 breaks", ReasonNoPickup},
		{"climbing out of a trap: still no path after climbing 4 blocks", ReasonTrapClimbFailed},
		{"trapped with no walking path and out of blocks to climb with", ReasonTrapClimbFailed},
		{"inventory did not show the deposit within 40 ticks", ReasonInventoryDesync},
		{"inventory did not show the withdrawal within 40 ticks", ReasonInventoryDesync},
		{"no standing spot within reach of the chest", ReasonNoReachableTarget},
		{"cannot reach a spot to read the box from: no path", ReasonNoReachableTarget},
		{"wire down", ReasonSessionLost},
		{"session ended", ReasonSessionLost},
		{"block did not break", ReasonOther},
		{"no progress after 5 replans", ReasonOther},
		{"something nobody wrote a rule for", ReasonOther},
		{"", ""},
	}
	for _, c := range cases {
		if got := ReasonCode(c.reason); got != c.want {
			t.Errorf("ReasonCode(%q) = %q, want %q", c.reason, got, c.want)
		}
	}
}

func TestReasonCodeIgnoresEmbeddedNumbers(t *testing.T) {
	for _, n := range []int{0, 1, 6, 17, 4096} {
		if got := ReasonCode(fmt.Sprintf("server rejected the deposit with status %d", n)); got != ReasonDepositRejected {
			t.Errorf("status %d: %q", n, got)
		}
		if got := ReasonCode(fmt.Sprintf("no pickup after %d breaks", n)); got != ReasonNoPickup {
			t.Errorf("breaks %d: %q", n, got)
		}
	}
}

func TestReasonCodesAreClosed(t *testing.T) {
	known := map[string]bool{}
	for _, c := range ReasonCodes {
		known[c] = true
	}
	for _, r := range []string{"no axe in inventory", "wire down", "x"} {
		if c := ReasonCode(r); !known[c] {
			t.Errorf("%q maps to %q, outside ReasonCodes", r, c)
		}
	}
}

func TestKitReasonCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{skill.ErrChestChunkNotLoaded, ReasonKitChunkNotLoaded},
		{fmt.Errorf("wrapped: %w", skill.ErrChestNotOpened), ReasonKitChestTimeout},
		{errors.New("getting to the chest: walking to the goal: no path"), ReasonKitNoPath},
		{errors.New("no standing spot within reach of the chest"), ReasonKitNoPath},
		{errors.New("visit took too long"), ReasonKitVisitTimeout},
		{errors.New("server rejected the withdrawal with status 6"), ReasonDepositRejected},
		{errors.New("nothing recognisable"), ReasonOther},
	}
	for _, c := range cases {
		if got := KitReasonCode(c.err); got != c.want {
			t.Errorf("KitReasonCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}
