package tactics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
)

func TestHashIsSHA256OfValidatedJSON(t *testing.T) {
	pr := NewProfile("cand")
	pr.Overrides = map[Family]Params{FamilyZombie: Defaults()}
	got, err := Hash(pr)
	if err != nil {
		t.Fatal(err)
	}
	v, err := pr.Validate()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hash %s, want %s", got, want)
	}
}

func TestHashChangesWithTheProfile(t *testing.T) {
	a := NewProfile("a")
	same, _ := Hash(a)
	if again, _ := Hash(NewProfile("a")); again != same {
		t.Fatal("equal profiles hash differently")
	}
	renamed, _ := Hash(NewProfile("b"))
	tuned := NewProfile("a")
	tuned.Default.ShotEvery = 40
	retuned, _ := Hash(tuned)
	withTactics := NewProfile("a")
	withTactics.Tactics = map[Family][]string{FamilyZombie: {"x"}}
	tacticked, _ := Hash(withTactics)
	seen := map[string]bool{same: true}
	for _, h := range []string{renamed, retuned, tacticked} {
		if seen[h] {
			t.Fatalf("hash %s collides", h)
		}
		seen[h] = true
	}
}

func TestHashSeesTheClampedProfile(t *testing.T) {
	loose := NewProfile("p")
	loose.Default.ShotEvery = 1000
	clamped := NewProfile("p")
	clamped.Default.ShotEvery = Bounds()["shotEvery"].Max
	a, _ := Hash(loose)
	b, _ := Hash(clamped)
	if a != b {
		t.Fatal("an out-of-range value must hash as the bound it is held to")
	}
}

func TestHashRefusesAWrongVersion(t *testing.T) {
	pr := NewProfile("p")
	pr.Version = Version + 1
	if _, err := Hash(pr); err == nil {
		t.Fatal("a profile the engine cannot read must not hash")
	}
}

func TestHashLeavesTheOverridesAlone(t *testing.T) {
	over := Defaults()
	over.ShotEvery = 1000
	pr := NewProfile("p")
	pr.Overrides = map[Family]Params{FamilyZombie: over}
	if _, err := Hash(pr); err != nil {
		t.Fatal(err)
	}
	if pr.Overrides[FamilyZombie].ShotEvery != 1000 {
		t.Fatal("Hash clamped the caller's map")
	}
}

func TestHashIsSafeAgainstAConcurrentReader(t *testing.T) {
	pr := NewProfile("p")
	pr.Overrides = map[Family]Params{FamilyZombie: Defaults()}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = Hash(pr)
				_ = pr.For(FamilyZombie)
			}
		}()
	}
	wg.Wait()
}
