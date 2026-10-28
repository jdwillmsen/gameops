package trainer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestAppendAndRollbackRefuseWhileAnotherTrainerHoldsTheFamily(t *testing.T) {
	holder := newStore(t)
	other, err := NewStore(holder.Dir())
	if err != nil {
		t.Fatal(err)
	}
	f := tactics.FamilyZombie
	rec, err := other.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil)
	if err != nil {
		t.Fatalf("append with no one training: %v", err)
	}
	release, err := holder.Lock(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil); !errors.Is(err, ErrLocked) {
		t.Errorf("append against a held family = %v, want ErrLocked", err)
	}
	if _, err := other.Rollback(f, rec.Profile.Name); !errors.Is(err, ErrLocked) {
		t.Errorf("rollback against a held family = %v, want ErrLocked", err)
	}
	if hist, _ := holder.History(f); len(hist) != 1 {
		t.Errorf("history has %d entries, want 1: the refused append wrote", len(hist))
	}
	if _, err := holder.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil); err != nil {
		t.Errorf("the holder's own append: %v", err)
	}
	release()
	if _, err := other.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil); err != nil {
		t.Errorf("append after release: %v", err)
	}
	if _, err := other.Append(Record{Profile: tactics.NewProfile(""), Family: f}, nil); err != nil {
		t.Errorf("append leaves the family locked: %v", err)
	}
}

func TestLockDoesNotFollowASymlinkedLockFile(t *testing.T) {
	s := newStore(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Dir(), string(tactics.FamilyZombie))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := s.Lock(tactics.FamilyZombie); err == nil {
		release()
		t.Fatal("lock followed a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Errorf("symlink target was changed to %q", b)
	}
}
