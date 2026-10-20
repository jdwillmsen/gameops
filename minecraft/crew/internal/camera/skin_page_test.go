package camera

import (
	"strconv"
	"strings"
	"testing"
)

// The page is embedded and not type-checked, so the frame numbers it switches
// on are checked here.
func TestPageHandlesTheSkinAndArmourFrames(t *testing.T) {
	for _, s := range []string{
		"case " + strconv.Itoa(int(frameSkin)) + ":",
		"case " + strconv.Itoa(int(frameArmour)) + ":",
		"ents.setSkin(",
		"ents.setArmour(",
	} {
		if !strings.Contains(string(page), s) {
			t.Errorf("page does not contain %q", s)
		}
	}
}
