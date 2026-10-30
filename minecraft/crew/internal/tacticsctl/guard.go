// Package tacticsctl is the human control surface over warrior Profiles: the
// training kill switch, the lab-only guard, the staged profile store and the
// approval-gated promotion to production.
package tacticsctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// TrainingFile is the kill-switch file inside the control directory.
// Training is enabled only while it exists and holds exactly "on"; a missing,
// empty, unreadable or garbled file means disabled, so a wiped volume or a
// half-written file can never start a trainer by accident.
const TrainingFile = "training"

// DefaultDir is the control state directory every tool falls back to, so the
// trainer and tacticsctl agree on where the switch lives without being told.
func DefaultDir() string {
	if d := os.Getenv("TACTICSCTL_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "crew-tactics")
}

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

var (
	// crewLabToken is the lab's own name as a whole token, so "crew-lab-0"
	// counts and "crew-labx" or "acrew-lab" do not.
	crewLabToken = regexp.MustCompile(`(^|[^a-z0-9])crew-lab([^a-z0-9]|$)`)
	labSquadID   = regexp.MustCompile(`^lab-[a-z0-9-]+$`)
)

// KnownLab is the one cluster pod the tools trust as the crew lab. Its
// namespace and pod name contain "prd" because the lab runs in the production
// cluster, so the word check below would refuse it; only this exact string is
// let through, never a lookalike. It must equal arena.KnownLab (a test in
// cmd/trainbridge holds the two together; this package cannot import arena).
const KnownLab = "k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-0"

// LabOnly accepts a target only when it is positively the crew lab: it
// carries the token "crew-lab" (a compose service, or the pod of a
// k8s:<namespace>/<pod>), or it is a lab squad id, "lab-" and a name. A bare
// word "lab" is not enough, because any namespace can be called that. The
// allowlist is the rule; the forbidden list only catches names like
// "crew-lab-prod" that would otherwise slip through it.
func LabOnly(target string) error {
	if target == KnownLab {
		return nil
	}
	lower := strings.ToLower(target)
	if strings.Contains(lower, "fwb") {
		return fmt.Errorf("%w: %q names a real-player server", ErrNotLab, target)
	}
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if forbidden[w] {
			return fmt.Errorf("%w: %q names a real-player server", ErrNotLab, target)
		}
	}
	if !crewLabToken.MatchString(lower) && !labSquadID.MatchString(lower) {
		return fmt.Errorf("%w: %q", ErrNotLab, target)
	}
	return nil
}
