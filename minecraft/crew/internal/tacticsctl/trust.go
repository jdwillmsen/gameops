package tacticsctl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// groupOtherWrite is the permission bits that let someone other than the
// owner replace a file or, on a directory, add one.
const groupOtherWrite = 0o022

// resolve makes a path absolute and follows symlinks as far as it exists, so
// a link cannot make the trust directory look like it lives elsewhere.
func resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Resolve the deepest ancestor that exists and re-append the rest.
	rest := ""
	for cur := abs; ; cur = filepath.Dir(cur) {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest), nil
		}
		if filepath.Dir(cur) == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
	}
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkTrust refuses a trust directory that agents could write. Approvals are
// only evidence if the public keys that verify them cannot be swapped by the
// same actors who could forge an approval file, so the directory must be named
// explicitly, sit outside the state store, and be closed to group and others.
// Ownership by a different account than the agents' is the real protection;
// this can only reject the setups that are provably open.
func (s *Store) checkTrust() error {
	if s.trustDefaulted {
		return errors.New("tacticsctl: promote needs an explicit trust directory (--trust or TACTICSCTL_TRUST): the default <dir>/trusted is inside the state agents write to")
	}
	trust, err := resolve(s.TrustDir)
	if err != nil {
		return err
	}
	root, err := resolve(s.Root)
	if err != nil {
		return err
	}
	if inside(root, trust) {
		return fmt.Errorf("tacticsctl: trust directory %s lives under the store %s, where agents can write", s.TrustDir, s.Root)
	}
	fi, err := os.Stat(trust)
	if err != nil {
		return fmt.Errorf("tacticsctl: trust directory %s: %w", s.TrustDir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("tacticsctl: trust path %s is not a directory", s.TrustDir)
	}
	if fi.Mode().Perm()&groupOtherWrite != 0 {
		return fmt.Errorf("tacticsctl: trust directory %s is writable by group or others (mode %04o); chmod go-w it", s.TrustDir, fi.Mode().Perm())
	}
	return nil
}

// evidenceRE is the layout the trainer files scorecards under:
// <family>/scorecards/<profile>.jsonl, relative to its store.
var evidenceRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}/scorecards/([A-Za-z0-9][A-Za-z0-9._-]{0,63})\.jsonl$`)

// checkEvidence requires the profile to name the scorecards behind it, and to
// name them under its own name, so a profile cannot borrow another's fights.
func checkEvidence(pr tactics.Profile) error {
	if pr.Evidence == "" {
		return fmt.Errorf("tacticsctl: profile %q carries no scorecards evidence path; only trainer output with recorded fights can go to production", pr.Name)
	}
	m := evidenceRE.FindStringSubmatch(pr.Evidence)
	if m == nil || m[1] != pr.Name {
		return fmt.Errorf("tacticsctl: profile %q evidence %q is not <family>/scorecards/%s.jsonl", pr.Name, pr.Evidence, pr.Name)
	}
	return nil
}
