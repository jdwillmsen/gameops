package arena

import (
	"fmt"
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
