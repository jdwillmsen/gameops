package arena

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	composeLab = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	k8sLab     = regexp.MustCompile(`^k8s:([a-z0-9][a-z0-9.-]*)/([a-z0-9][a-z0-9.-]*)$`)
)

// ValidateLabTarget accepts only the crew lab: a compose service, or a
// k8s:<namespace>/<pod> whose pod is the crew lab's. Anything naming FWB is
// refused outright, so a mistyped target can never aim a duel at the players'
// world. A duel teleports units and clears their inventories, which is only
// acceptable in the lab.
func ValidateLabTarget(lab string) error {
	if lab == "" {
		return fmt.Errorf("arena: --lab is required")
	}
	if strings.Contains(strings.ToLower(lab), "fwb") {
		return fmt.Errorf("arena: %q names FWB; duels run on the crew lab only", lab)
	}
	if composeLab.MatchString(lab) {
		return nil
	}
	m := k8sLab.FindStringSubmatch(lab)
	if m == nil {
		return fmt.Errorf("arena: %q is not a compose service or k8s:<namespace>/<pod>", lab)
	}
	if !strings.Contains(m[2], "crew-lab") {
		return fmt.Errorf("arena: pod %q is not a crew lab pod; duels run on the crew lab only", m[2])
	}
	return nil
}

// DefaultDuelists are the crew warriors a duel may put through its clears and
// teleports unless the operator names others.
var DefaultDuelists = []string{"Rusty", "Slate", "Tinder"}

// ValidateComposeFile refuses a compose file that is plainly not the crew
// lab's: the lab target is only as safe as the file the console drives, so
// one that names FWB in its file or directory name is as wrong as a lab
// service that does.
func ValidateComposeFile(path string) error {
	if path == "" {
		return fmt.Errorf("arena: --compose is required for a compose lab")
	}
	for _, part := range []string{filepath.Base(path), filepath.Base(filepath.Dir(path))} {
		if strings.Contains(strings.ToLower(part), "fwb") {
			return fmt.Errorf("arena: compose file %q names FWB; this tool runs on the crew lab only", path)
		}
	}
	return nil
}

// ValidateLabConsole applies the lab rules to both halves of a console
// target: the lab, and for a compose lab the compose file it runs from.
func ValidateLabConsole(lab, compose string) error {
	if err := ValidateLabTarget(lab); err != nil {
		return err
	}
	if strings.HasPrefix(lab, "k8s:") {
		return nil
	}
	return ValidateComposeFile(compose)
}
