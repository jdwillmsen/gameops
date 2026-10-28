package trainer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// Record is one champion with the evidence behind it. Profile.Parent links it
// to the champion it replaced; ScorecardsPath (relative to the store) holds
// the fights that justified the replacement.
type Record struct {
	Profile        tactics.Profile `json:"profile"`
	Family         tactics.Family  `json:"family"`
	Seq            int             `json:"seq"`
	Generation     int             `json:"generation"`
	RunID          string          `json:"runId"`
	ScorecardsPath string          `json:"scorecardsPath,omitempty"`
	Pairs          int             `json:"pairs,omitempty"`
	Advantage      float64         `json:"advantage,omitempty"`
	P              float64         `json:"p,omitempty"`
	CreatedAt      string          `json:"createdAt"`
}

// Params returns the tuned parameter set for the record's family.
func (r Record) Params() tactics.Params { return r.Profile.For(r.Family) }

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// Store keeps champions as JSON files:
//
//	<dir>/<family>/champion.json         current champion
//	<dir>/<family>/history/NNNN-name.json every champion ever promoted
//	<dir>/<family>/scorecards/name.jsonl  the evidence per promotion
//
// History is append-only, so a rollback only moves the champion pointer.
type Store struct {
	dir  string
	mu   sync.Mutex
	held map[tactics.Family]int // families whose training lock this Store holds
}

// NewStore opens (and creates) a store directory.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("trainer: store directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the store root.
func (s *Store) Dir() string { return s.dir }

func (s *Store) familyDir(f tactics.Family) (string, error) {
	if !nameRE.MatchString(string(f)) {
		return "", fmt.Errorf("trainer: family %q is not a safe name", f)
	}
	return filepath.Join(s.dir, string(f)), nil
}

// Champion returns the current champion, if any. The profile is validated, so
// values from disk are clamped into bounds.
func (s *Store) Champion(f tactics.Family) (Record, bool, error) {
	fd, err := s.familyDir(f)
	if err != nil {
		return Record{}, false, err
	}
	rec, err := readRecord(filepath.Join(fd, "champion.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	return rec, err == nil, err
}

// History lists every promoted champion, oldest first.
func (s *Store) History(f tactics.Family) ([]Record, error) {
	fd, err := s.familyDir(f)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(fd, "history"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := readRecord(filepath.Join(fd, "history", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Append promotes rec to champion under the family's training lock. It
// assigns the next sequence number and name, writes the evidence first and the
// champion pointer last, so a crash leaves the previous champion intact.
func (s *Store) Append(rec Record, cards []tactics.Scorecard) (out Record, err error) {
	err = s.withLock(rec.Family, func() error {
		out, err = s.appendLocked(rec, cards)
		return err
	})
	return out, err
}

func (s *Store) appendLocked(rec Record, cards []tactics.Scorecard) (Record, error) {
	fd, err := s.familyDir(rec.Family)
	if err != nil {
		return Record{}, err
	}
	hist, err := s.History(rec.Family)
	if err != nil {
		return Record{}, err
	}
	rec.Seq = 0
	if n := len(hist); n > 0 {
		rec.Seq = hist[n-1].Seq + 1
	}
	rec.Profile.Name = fmt.Sprintf("%s-v%03d", rec.Family, rec.Seq)
	if len(cards) > 0 {
		rec.ScorecardsPath = filepath.Join(string(rec.Family), "scorecards", rec.Profile.Name+".jsonl")
		rec.Profile.Evidence = filepath.ToSlash(rec.ScorecardsPath)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, c := range cards {
			if err := enc.Encode(c); err != nil {
				return Record{}, err
			}
		}
		if err := atomicWrite(filepath.Join(s.dir, rec.ScorecardsPath), buf.Bytes()); err != nil {
			return Record{}, err
		}
	}
	b, err := marshalRecord(rec)
	if err != nil {
		return Record{}, err
	}
	hname := fmt.Sprintf("%04d-%s.json", rec.Seq, rec.Profile.Name)
	if err := atomicWrite(filepath.Join(fd, "history", hname), b); err != nil {
		return Record{}, err
	}
	return rec, atomicWrite(filepath.Join(fd, "champion.json"), b)
}

// Rollback makes the named historical champion current again, under the
// family's training lock.
func (s *Store) Rollback(f tactics.Family, name string) (out Record, err error) {
	err = s.withLock(f, func() error {
		out, err = s.rollbackLocked(f, name)
		return err
	})
	return out, err
}

func (s *Store) rollbackLocked(f tactics.Family, name string) (Record, error) {
	hist, err := s.History(f)
	if err != nil {
		return Record{}, err
	}
	for _, rec := range hist {
		if rec.Profile.Name != name {
			continue
		}
		b, err := marshalRecord(rec)
		if err != nil {
			return Record{}, err
		}
		fd, _ := s.familyDir(f)
		return rec, atomicWrite(filepath.Join(fd, "champion.json"), b)
	}
	return Record{}, fmt.Errorf("trainer: no champion %q in the history of %s", name, f)
}

// SaveRun writes a run summary beside the champions.
func (s *Store) SaveRun(f tactics.Family, runID string, v any) error {
	fd, err := s.familyDir(f)
	if err != nil {
		return err
	}
	if !nameRE.MatchString(strings.ToLower(runID)) {
		return fmt.Errorf("trainer: run id %q is not a safe name", runID)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(fd, "runs", runID+".json"), append(b, '\n'))
}

func marshalRecord(rec Record) ([]byte, error) {
	b, err := json.MarshalIndent(rec, "", "  ")
	return append(b, '\n'), err
}

func readRecord(path string) (Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("trainer: %s: %w", path, err)
	}
	pr, err := rec.Profile.Validate()
	if err != nil {
		return Record{}, fmt.Errorf("trainer: %s: %w", path, err)
	}
	rec.Profile = pr
	return rec, nil
}

// atomicWrite replaces path by renaming a synced temp file over it, so a
// reader sees the old or the new content and never a torn file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
