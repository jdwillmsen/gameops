// Package tacticsctl is the human control surface over warrior Profiles: the
// training kill switch, the lab-only guard, the staged profile store and the
// approval-gated promotion to production.
package tacticsctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// TrainingFile is the kill-switch file inside the control directory.
// Training is enabled only while it exists and holds exactly "on"; a missing,
// empty, unreadable or garbled file means disabled, so a wiped volume or a
// half-written file can never start a trainer by accident.
const TrainingFile = "training"

// TrainingEnabled is the one question trainers and lab units ask before every
// run and every fight.
func TrainingEnabled(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, TrainingFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "on"
}

// ErrNotLab is returned for any target that is not positively the lab.
var ErrNotLab = errors.New("tacticsctl: target is not the crew lab")

// forbidden words mark a target as a real-player server even if it also
// mentions "lab".
var forbidden = map[string]bool{
	"fwb": true, "prd": true, "prod": true, "production": true, "live": true,
}

// LabOnly accepts a lab target (a compose service or k8s:<namespace>/<pod>)
// only when one of its words is "lab" and none names a real-player server.
// The allowlist is the rule; the forbidden list only catches names like
// "fwb-lab" that would otherwise slip through it.
func LabOnly(target string) error {
	words := strings.FieldsFunc(strings.ToLower(target), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	isLab := false
	for _, w := range words {
		if forbidden[w] {
			return fmt.Errorf("%w: %q names a real-player server", ErrNotLab, target)
		}
		if w == "lab" {
			isLab = true
		}
	}
	if !isLab {
		return fmt.Errorf("%w: %q", ErrNotLab, target)
	}
	return nil
}
