package tacticsctl

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
// bad key fails at approve time and not at promote time.
func (s *Store) SaveApproval(a Approval) error {
	if err := s.verify(a); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.approvalPath(a.Hash), append(b, '\n'), 0o644)
}

func (s *Store) trustedKey(approver string) (ed25519.PublicKey, error) {
	if !nameRE.MatchString(approver) {
		return nil, fmt.Errorf("tacticsctl: %q is not a valid approver name", approver)
	}
	keyPath := filepath.Join(s.TrustDir, approver+".pub")
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tacticsctl: approver %q is not trusted (no public key in %s)", approver, s.TrustDir)
	}
	if fi, err := os.Stat(keyPath); err == nil && fi.Mode().Perm()&groupOtherWrite != 0 {
		return nil, fmt.Errorf("tacticsctl: public key %s is writable by group or others; chmod go-w it", keyPath)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("tacticsctl: trusted key for %q is not a hex ed25519 public key", approver)
	}
	return ed25519.PublicKey(raw), nil
}

func (s *Store) verify(a Approval) error {
	pub, err := s.trustedKey(a.Approver)
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
	return a, s.verify(a)
}

// Promote makes a profile the production Profile. It refuses unless
//   - the trust directory is explicit, outside the store and not writable by
//     group or others;
//   - the profile has run in the lab, byte for byte;
//   - the profile names the scorecards that justified it;
//   - a signed approval from a trusted approver names this profile's exact
//     hash, so editing the profile after approval, or approving a different
//     one, does not pass.
//
// This is the only command that writes production, besides rollback.
func (s *Store) Promote(name string) error {
	if err := s.checkTrust(); err != nil {
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
	if err := checkEvidence(pr); err != nil {
		return err
	}
	a, err := s.Approval(h)
	if err != nil {
		return err
	}
	if a.Profile != pr.Name {
		return fmt.Errorf("tacticsctl: approval %s is for %q, not %q", h, a.Profile, pr.Name)
	}
	if err := s.copyLineage(Production, pr); err != nil {
		return err
	}
	return s.point(Production, "promote", name, h)
}
