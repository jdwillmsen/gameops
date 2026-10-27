package tacticsctl

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Stage is a directory of the store. Candidates is the trainer's output; Lab
// and Production each hold the Profiles that stage has used plus a LIVE
// pointer to the one in force.
type Stage string

const (
	Candidates Stage = "candidates"
	Lab        Stage = "lab"
	Production Stage = "production"
)

const (
	livePointer = "LIVE"
	historyFile = "HISTORY.jsonl"
	// DefaultName rolls a stage back to the shipped behaviour; it is not a
	// storable profile name.
	DefaultName = "default"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Store is the on-disk control state. TrustDir holds the approvers' public
// keys; point it somewhere agents cannot write, or approvals prove nothing.
// Promote enforces that: see checkTrust.
type Store struct {
	Root     string
	TrustDir string
	// trustDefaulted records that the caller named no trust directory. The
	// fallback sits inside the state agents write to, so it is good enough to
	// browse a store but never to promote from.
	trustDefaulted bool
	now            func() time.Time
}

// New returns a Store rooted at dir. An empty trustDir falls back to
// <dir>/trusted, which Promote refuses to use.
func New(dir, trustDir string) *Store {
	defaulted := trustDir == ""
	if defaulted {
		trustDir = filepath.Join(dir, "trusted")
	}
	return &Store{Root: dir, TrustDir: trustDir, trustDefaulted: defaulted, now: time.Now}
}

// Pointer says which Profile a stage has in force.
type Pointer struct {
	Name  string `json:"name"`
	Hash  string `json:"hash,omitempty"`
	Since string `json:"since"`
}

type historyEntry struct {
	TS     string `json:"ts"`
	Action string `json:"action"`
	Name   string `json:"name"`
	Hash   string `json:"hash,omitempty"`
}

func (s *Store) stageDir(st Stage) string { return filepath.Join(s.Root, string(st)) }

func (s *Store) ts() string { return s.now().UTC().Format(time.RFC3339) }

// TrainingPath is the kill-switch file.
func (s *Store) TrainingPath() string { return filepath.Join(s.Root, TrainingFile) }

// SetTraining writes the kill-switch file. Disabling writes "off" rather than
// deleting, so the file's presence records that a human decided.
func (s *Store) SetTraining(on bool) error {
	v := "off\n"
	if on {
		v = "on\n"
	}
	return writeAtomic(s.TrainingPath(), []byte(v), 0o644)
}

// Hash is the identity an approval names: the SHA-256 of the validated
// profile's canonical JSON, lineage included.
func Hash(pr tactics.Profile) (string, error) {
	pr, err := pr.Validate()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(pr)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func checkName(name string) error {
	if name == DefaultName || !nameRE.MatchString(name) {
		return fmt.Errorf("tacticsctl: %q is not a valid profile name", name)
	}
	return nil
}

func encode(pr tactics.Profile) ([]byte, error) {
	return json.MarshalIndent(pr, "", "  ")
}

func (s *Store) read(st Stage, name string) (tactics.Profile, error) {
	var pr tactics.Profile
	if err := checkName(name); err != nil {
		return pr, err
	}
	b, err := os.ReadFile(filepath.Join(s.stageDir(st), name+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pr, fmt.Errorf("tacticsctl: no profile %q in %s", name, st)
		}
		return pr, err
	}
	pr, err = tactics.Parse(b)
	if err != nil {
		return pr, err
	}
	if pr.Name != name {
		return pr, fmt.Errorf("tacticsctl: %s/%s.json holds profile %q", st, name, pr.Name)
	}
	return pr, nil
}

// put stores a profile under a stage. Names are immutable: a different
// profile under an existing name is refused, so a pointer's hash always
// resolves to the bytes that were reviewed.
func (s *Store) put(st Stage, pr tactics.Profile) error {
	if err := checkName(pr.Name); err != nil {
		return err
	}
	pr, err := pr.Validate()
	if err != nil {
		return err
	}
	if old, err := s.read(st, pr.Name); err == nil {
		oh, _ := Hash(old)
		nh, _ := Hash(pr)
		if oh == nh {
			return nil
		}
		return fmt.Errorf("tacticsctl: %s already holds a different profile named %q", st, pr.Name)
	}
	b, err := encode(pr)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.stageDir(st), pr.Name+".json"), append(b, '\n'), 0o644)
}

// AddCandidate files a trainer-proposed profile.
func (s *Store) AddCandidate(pr tactics.Profile) error { return s.put(Candidates, pr) }

// Candidate loads a candidate by name.
func (s *Store) Candidate(name string) (tactics.Profile, error) { return s.read(Candidates, name) }

// Candidates lists candidate names.
func (s *Store) Candidates() ([]string, error) {
	ents, err := os.ReadDir(s.stageDir(Candidates))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok && nameRE.MatchString(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func isShipped(parent string) bool { return parent == "" || parent == DefaultName }

// copyLineage stores pr and every ancestor into st, taking each from the
// candidates or from the stage itself. An unresolvable ancestor is an error:
// rollback has to be able to walk the whole chain.
func (s *Store) copyLineage(st Stage, pr tactics.Profile) error {
	seen := map[string]bool{}
	for {
		if seen[pr.Name] {
			return fmt.Errorf("tacticsctl: lineage of %q loops through %q", pr.Name, pr.Name)
		}
		seen[pr.Name] = true
		if err := s.put(st, pr); err != nil {
			return err
		}
		if isShipped(pr.Parent) {
			return nil
		}
		next, err := s.read(Candidates, pr.Parent)
		if err != nil {
			if next, err = s.read(st, pr.Parent); err != nil {
				return fmt.Errorf("tacticsctl: ancestor %q of %q not found: %w", pr.Parent, pr.Name, err)
			}
		}
		pr = next
	}
}

func (s *Store) point(st Stage, action, name, hash string) error {
	p := Pointer{Name: name, Hash: hash, Since: s.ts()}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.stageDir(st), livePointer), append(b, '\n'), 0o644); err != nil {
		return err
	}
	line, _ := json.Marshal(historyEntry{TS: p.Since, Action: action, Name: name, Hash: hash})
	f, err := os.OpenFile(filepath.Join(s.stageDir(st), historyFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *Store) history(st Stage) []historyEntry {
	f, err := os.Open(filepath.Join(s.stageDir(st), historyFile))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []historyEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e historyEntry
		// A torn last line from a crash is skipped, not fatal.
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Pointer returns what a stage has in force; ok is false when nothing was
// ever set.
func (s *Store) Pointer(st Stage) (Pointer, bool, error) {
	var p Pointer
	b, err := os.ReadFile(filepath.Join(s.stageDir(st), livePointer))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return p, false, nil
		}
		return p, false, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, false, fmt.Errorf("tacticsctl: %s pointer unreadable: %w", st, err)
	}
	return p, true, nil
}

// Live loads the Profile a stage has in force, verified against the pointer's
// hash. A stage with nothing set, or set to default, yields the shipped
// behaviour.
func (s *Store) Live(st Stage) (tactics.Profile, error) {
	p, ok, err := s.Pointer(st)
	if err != nil {
		return tactics.NewProfile(DefaultName), err
	}
	if !ok || p.Name == DefaultName {
		return tactics.NewProfile(DefaultName), nil
	}
	pr, err := s.read(st, p.Name)
	if err != nil {
		return tactics.NewProfile(DefaultName), err
	}
	if h, _ := Hash(pr); h != p.Hash {
		return tactics.NewProfile(DefaultName), fmt.Errorf("tacticsctl: %s/%s does not match its pointer hash", st, p.Name)
	}
	return pr, nil
}

// Effective is what a unit should fight with: the stage's live Profile, or the
// shipped default on any problem. The lab additionally falls back to default
// while training is disabled, which is how the kill switch reaches units.
func (s *Store) Effective(st Stage) tactics.Profile {
	if st == Lab && !TrainingEnabled(s.Root) {
		return tactics.NewProfile(DefaultName)
	}
	pr, err := s.Live(st)
	if err != nil {
		return tactics.NewProfile(DefaultName)
	}
	return pr
}

// LabSet makes a candidate the lab's live Profile. Allowed at any time; the
// lab is where Profiles are meant to be tried.
func (s *Store) LabSet(name string) error {
	pr, err := s.read(Candidates, name)
	if err != nil {
		return err
	}
	if err := s.copyLineage(Lab, pr); err != nil {
		return err
	}
	h, err := Hash(pr)
	if err != nil {
		return err
	}
	return s.point(Lab, "set", name, h)
}

// Chain returns the live Profile of a stage followed by its ancestors, newest
// first, stopping where the lineage leaves the stage.
func (s *Store) Chain(st Stage) ([]tactics.Profile, error) {
	p, ok, err := s.Pointer(st)
	if err != nil || !ok || p.Name == DefaultName {
		return nil, err
	}
	var chain []tactics.Profile
	seen := map[string]bool{}
	for name := p.Name; !isShipped(name) && !seen[name]; {
		seen[name] = true
		pr, err := s.read(st, name)
		if err != nil {
			return chain, err
		}
		chain = append(chain, pr)
		name = pr.Parent
	}
	return chain, nil
}

// Rollback restores an ancestor of the stage's live Profile (its parent when
// to is empty), or the shipped default when to is "default" or the chain ends.
// Production only returns to a Profile it has already run: that was approved
// then, so a rollback cannot become a way around the promotion gate.
func (s *Store) Rollback(st Stage, to string) error {
	if st != Lab && st != Production {
		return fmt.Errorf("tacticsctl: cannot roll back %s", st)
	}
	chain, err := s.Chain(st)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return fmt.Errorf("tacticsctl: %s has no live profile to roll back", st)
	}
	target := to
	if target == "" {
		target = chain[0].Parent
	}
	if isShipped(target) {
		return s.point(st, "rollback", DefaultName, "")
	}
	var found *tactics.Profile
	for i := 1; i < len(chain); i++ {
		if chain[i].Name == target {
			found = &chain[i]
		}
	}
	if found == nil {
		return fmt.Errorf("tacticsctl: %q is not an ancestor of %s's live profile %q", target, st, chain[0].Name)
	}
	h, err := Hash(*found)
	if err != nil {
		return err
	}
	if st == Production && !s.ranBefore(st, target, h) {
		return fmt.Errorf("tacticsctl: production never ran %q; promote it with an approval instead", target)
	}
	return s.point(st, "rollback", target, h)
}

func (s *Store) ranBefore(st Stage, name, hash string) bool {
	for _, e := range s.history(st) {
		if e.Name == name && e.Hash == hash {
			return true
		}
	}
	return false
}

// StageStatus is one stage's line in Status.
type StageStatus struct {
	Stage Stage    `json:"stage"`
	Live  string   `json:"live"`
	Hash  string   `json:"hash,omitempty"`
	Since string   `json:"since,omitempty"`
	Chain []string `json:"chain,omitempty"`
	Error string   `json:"error,omitempty"`
}

// Status is everything `tacticsctl status` reports.
type Status struct {
	Training   bool          `json:"training"`
	Candidates []string      `json:"candidates"`
	Stages     []StageStatus `json:"stages"`
}

// Status reports what is live per stage with its parent chain.
func (s *Store) Status() Status {
	st := Status{Training: TrainingEnabled(s.Root)}
	st.Candidates, _ = s.Candidates()
	for _, stage := range []Stage{Lab, Production} {
		ss := StageStatus{Stage: stage, Live: DefaultName}
		p, ok, err := s.Pointer(stage)
		switch {
		case err != nil:
			ss.Error = err.Error()
		case ok:
			ss.Live, ss.Hash, ss.Since = p.Name, p.Hash, p.Since
			chain, err := s.Chain(stage)
			if err != nil {
				ss.Error = err.Error()
			}
			for _, c := range chain {
				ss.Chain = append(ss.Chain, c.Name)
			}
			if _, err := s.Live(stage); err != nil && ss.Error == "" {
				ss.Error = err.Error()
			}
		}
		st.Stages = append(st.Stages, ss)
	}
	return st
}
