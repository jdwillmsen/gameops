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

// trustOwnerUID is the only account whose ownership makes a trust directory
// or an approver key believable. Root is the one account the agents' own
// processes cannot become, so a directory root owns and nobody else can write
// proves a human installed it. Only tests replace it, because they cannot
// chown to root.
var trustOwnerUID uint32

// resolve makes a path absolute and follows symlinks as far as it exists, so
// a link cannot make the store directory look like it lives elsewhere.
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

// checkTrust refuses a trust directory that agents could write and returns
// its fully resolved path. Approvals are only evidence if the public keys that
// verify them cannot be swapped by the same actors who could forge an
// approval file, so the directory must be named explicitly, sit outside the
// state store, be owned by root and closed to everyone else, and so must every
// directory above it: a writable parent lets its writer rename the trust
// directory away and put their own in its place. Callers must read keys from
// the returned path and not from s.TrustDir, so the path that was checked is
// the path that is used.
func (s *Store) checkTrust() (string, error) {
	if s.trustDefaulted {
		return "", errors.New("tacticsctl: an explicit trust directory is needed (--trust or TACTICSCTL_TRUST): the default <dir>/trusted is inside the state agents write to")
	}
	loose, err := resolve(s.TrustDir)
	if err != nil {
		return "", err
	}
	root, err := resolve(s.Root)
	if err != nil {
		return "", err
	}
	if inside(root, loose) {
		return "", fmt.Errorf("tacticsctl: trust directory %s lives under the store %s, where agents can write", s.TrustDir, s.Root)
	}
	trust, err := filepath.EvalSymlinks(loose)
	if err != nil {
		return "", fmt.Errorf("tacticsctl: trust directory %s: %w", s.TrustDir, err)
	}
	fi, err := os.Lstat(trust)
	if err != nil {
		return "", fmt.Errorf("tacticsctl: trust directory %s: %w", s.TrustDir, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("tacticsctl: trust path %s is not a directory", s.TrustDir)
	}
	if fi.Mode().Perm()&groupOtherWrite != 0 {
		return "", fmt.Errorf("tacticsctl: trust directory %s is writable by group or others (mode %04o); chmod go-w it", trust, fi.Mode().Perm())
	}
	if err := trustedOwner(fi, trust, "trust directory"); err != nil {
		return "", err
	}
	for cur := filepath.Dir(trust); ; cur = filepath.Dir(cur) {
		pfi, err := os.Lstat(cur)
		if err != nil {
			return "", fmt.Errorf("tacticsctl: trust directory parent %s: %w", cur, err)
		}
		uid, known := FileOwner(pfi)
		if !known || (uid != 0 && uid != trustOwnerUID) {
			return "", fmt.Errorf("tacticsctl: %s above the trust directory is not owned by root; one-time setup: sudo install -d -o root -m 0755 <trust-dir>", cur)
		}
		// A sticky directory lets only a file's owner rename it, so the
		// world-writable /tmp style stays safe for a root-owned child.
		if pfi.Mode().Perm()&groupOtherWrite != 0 && pfi.Mode()&os.ModeSticky == 0 {
			return "", fmt.Errorf("tacticsctl: %s above the trust directory is writable by group or others (mode %04o)", cur, pfi.Mode().Perm())
		}
		if filepath.Dir(cur) == cur {
			break
		}
	}
	return trust, nil
}

// trustedOwner refuses fi unless the trust owner owns it.
func trustedOwner(fi os.FileInfo, path, what string) error {
	uid, known := FileOwner(fi)
	if !known || uid != trustOwnerUID {
		return fmt.Errorf("tacticsctl: %s %s is not owned by root; agents could replace it. One-time setup, as a human: sudo install -d -o root -m 0755 /etc/crew-tactics-trust; sudo install -m 0644 <name>.pub /etc/crew-tactics-trust/", what, path)
	}
	return nil
}

// evidenceRE is the layout the trainer files scorecards under:
// <family>/scorecards/<profile>.jsonl, relative to its store.
var evidenceRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}/scorecards/([A-Za-z0-9][A-Za-z0-9._-]{0,63})\.jsonl$`)

// checkEvidence requires the profile to name the scorecards behind it, and to
// name them under its own name, so a profile cannot borrow another's fights.
// When the trainer's store is known the file must also exist there and hold
// data: a well-formed path to nothing is not evidence. Without a store only
// the path shape can be checked, and the command says so.
func (s *Store) checkEvidence(pr tactics.Profile) error {
	if pr.Evidence == "" {
		return fmt.Errorf("tacticsctl: profile %q carries no scorecards evidence path; only trainer output with recorded fights can go to production", pr.Name)
	}
	m := evidenceRE.FindStringSubmatch(pr.Evidence)
	if m == nil || m[1] != pr.Name {
		return fmt.Errorf("tacticsctl: profile %q evidence %q is not <family>/scorecards/%s.jsonl", pr.Name, pr.Evidence, pr.Name)
	}
	if s.EvidenceStore == "" {
		return nil
	}
	path := filepath.Join(s.EvidenceStore, filepath.FromSlash(pr.Evidence))
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("tacticsctl: profile %q names evidence %s, which does not exist in the trainer store: %w", pr.Name, pr.Evidence, err)
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 {
		return fmt.Errorf("tacticsctl: evidence %s for profile %q is empty or not a regular file", path, pr.Name)
	}
	return nil
}
