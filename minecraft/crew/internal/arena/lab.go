package arena

import (
	"fmt"
	"strings"
)

// KnownLab is the one cluster lab the tools trust without being told: the
// crew lab's server pod. Any other target needs a confirmation repeating it
// exactly, so a mistyped pod cannot be commanded by accident. A local compose
// service is a lab by construction but still has to be confirmed unless it is
// the compose file's own `lab` service.
const KnownLab = "k8s:jdwillmsen-prd/jdwillmsen-crew-lab-prd-minecraft-bedrock-0"

// CheckLab refuses a target that is not the crew lab unless confirm repeats
// it. An empty lab passes: the caller decides whether one is required.
func CheckLab(lab, confirm string) error {
	if lab == "" {
		return nil
	}
	if lab == KnownLab || (!strings.HasPrefix(lab, "k8s:") && lab == "lab") || confirm == lab {
		return nil
	}
	return fmt.Errorf("--lab %q is not the crew lab (%s); if it really is a lab, repeat it as --confirm-lab %q", lab, KnownLab, lab)
}

// NewLabConsole drives a compose service, or k8s:<namespace>/<pod>.
func NewLabConsole(lab, compose string) (Console, error) {
	if target, ok := strings.CutPrefix(lab, "k8s:"); ok {
		ns, pod, ok := strings.Cut(target, "/")
		if !ok || ns == "" || pod == "" {
			return nil, fmt.Errorf("%q is not k8s:<namespace>/<pod>", lab)
		}
		return NewKubectlConsole(ns, pod), nil
	}
	return NewDockerConsole(compose, lab), nil
}
