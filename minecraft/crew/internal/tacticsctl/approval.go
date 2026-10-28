package tacticsctl

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Approval is the owner's signed sign-off for one exact profile hash. Nothing
// in this package creates one on its own: NewApproval needs the approver's
// private key, which only the `tacticsctl approve` command (run by a human at
// a terminal) is given. Promote only ever reads approvals.
type Approval struct {
	Profile  string `json:"profile"`
	Hash     string `json:"hash"`
	Approver string `json:"approver"`
	TS       string `json:"ts"`
	Sig      string `json:"sig"`
}

func approvalMessage(hash, approver, ts string) []byte {
	return []byte("tacticsctl-approval-v1\n" + hash + "\n" + approver + "\n" + ts)
}

// NewApproval signs an approval of pr by approver.
func NewApproval(priv ed25519.PrivateKey, pr string, hash, approver, ts string) Approval {
	return Approval{
		Profile: pr, Hash: hash, Approver: approver, TS: ts,
		Sig: hex.EncodeToString(ed25519.Sign(priv, approvalMessage(hash, approver, ts))),
	}
}

func (s *Store) approvalPath(hash string) string {
	return filepath.Join(s.Root, "approvals", hash+".json")
}

// SaveApproval files a signed approval. The signature is checked first, so a
// bad key fails at approve time and not at promote time. This is a sanity
// check only: it does not demand the root-owned trust directory, which
// Promote does, so a human can approve before the directory is installed.
func (s *Store) SaveApproval(a Approval) error {
	if err := s.verify(a, false); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.approvalPath(a.Hash), append(b, '\n'), 0o644)
}

// trustedKey loads an approver's public key. Strict is the promotion gate: the
// trust directory and the key file must be root's. Lenient is for the
// sanity check at approve time.
func (s *Store) trustedKey(approver string, strict bool) (ed25519.PublicKey, error) {
	if !nameRE.MatchString(approver) {
		return nil, fmt.Errorf("tacticsctl: %q is not a valid approver name", approver)
	}
	trust := s.TrustDir
	if strict {
		var err error
		if trust, err = s.checkTrust(); err != nil {
			return nil, err
		}
	}
	keyPath := filepath.Join(trust, approver+".pub")
	f, err := os.OpenFile(keyPath, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return nil, fmt.Errorf("tacticsctl: approver %q is not trusted (no public key in %s)", approver, trust)
	}
	defer f.Close()
	// Checked on the open descriptor, so the file that is read is the file
	// that was checked even if the name is swapped meanwhile.
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("tacticsctl: public key %s is not a regular file", keyPath)
	}
	if strict {
		if err := trustedOwner(fi, keyPath, "public key"); err != nil {
			return nil, err
		}
		if fi.Mode().Perm()&groupOtherWrite != 0 {
			return nil, fmt.Errorf("tacticsctl: public key %s is writable by group or others; chmod go-w it", keyPath)
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("tacticsctl: trusted key for %q is not a hex ed25519 public key", approver)
	}
	return ed25519.PublicKey(raw), nil
}

func (s *Store) verify(a Approval, strict bool) error {
	pub, err := s.trustedKey(a.Approver, strict)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(a.Sig)
	if err != nil || !ed25519.Verify(pub, approvalMessage(a.Hash, a.Approver, a.TS), sig) {
		return errors.New("tacticsctl: approval signature does not verify")
	}
	return nil
}

// Approval loads and verifies the approval for hash.
func (s *Store) Approval(hash string) (Approval, error) {
	var a Approval
	b, err := os.ReadFile(s.approvalPath(hash))
	if err != nil {
		return a, fmt.Errorf("tacticsctl: no approval for profile hash %s", hash)
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return a, fmt.Errorf("tacticsctl: approval for %s unreadable: %w", hash, err)
	}
	if a.Hash != hash {
		return a, fmt.Errorf("tacticsctl: approval filed under %s names hash %s", hash, a.Hash)
	}
	return a, s.verify(a, true)
}

// Promote makes a profile the production Profile. It refuses unless
//   - the trust directory is explicit, outside the store, owned by root and
//     not writable by anyone else, as is every directory above it and each
//     approver's key file;
//   - the profile has run in the lab, byte for byte;
//   - the profile names the scorecards that justified it;
//   - a signed approval from a trusted approver names this profile's exact
//     hash, so editing the profile after approval, or approving a different
//     one, does not pass. The approval is spent by the promotion.
//
// This is the only command that writes production, besides rollback.
func (s *Store) Promote(name string) error {
	if _, err := s.checkTrust(); err != nil {
		return err
	}
	pr, err := s.read(Candidates, name)
	if err != nil {
		if pr, err = s.read(Lab, name); err != nil {
			return fmt.Errorf("tacticsctl: no candidate or lab profile %q", name)
		}
	}
	h, err := Hash(pr)
	if err != nil {
		return err
	}
	if !s.ranBefore(Lab, name, h) {
		return fmt.Errorf("tacticsctl: %q has not run in the lab; lab-set it first", name)
	}
	if err := s.checkEvidence(pr); err != nil {
		return err
	}
	a, err := s.Approval(h)
	if err != nil {
		return err
	}
	if a.Profile != pr.Name {
		return fmt.Errorf("tacticsctl: approval %s is for %q, not %q", h, a.Profile, pr.Name)
	}
	// Spent before production changes, and not given back if a later step
	// fails: an approval that might still be usable after the profile went
	// live is worse than asking the human to approve again.
	if err := s.consumeApproval(h); err != nil {
		return err
	}
	if err := s.copyLineage(Production, pr); err != nil {
		return err
	}
	return s.point(Production, "promote", name, h)
}

// consumeApproval moves the approval for hash out of the live approvals, so it
// can sign off exactly one promotion. A rollback does not bring it back, and
// promoting the same profile again needs a fresh human approval. The rename is
// atomic, so two promotions racing on one approval cannot both win.
func (s *Store) consumeApproval(hash string) error {
	usedDir := filepath.Join(s.Root, "approvals", "used")
	if err := os.MkdirAll(usedDir, 0o755); err != nil {
		return err
	}
	used := filepath.Join(usedDir, fmt.Sprintf("%s-%d.json", hash, s.now().UnixNano()))
	if err := os.Rename(s.approvalPath(hash), used); err != nil {
		return fmt.Errorf("tacticsctl: approval for %s was already used or removed; approve it again", hash)
	}
	return nil
}
