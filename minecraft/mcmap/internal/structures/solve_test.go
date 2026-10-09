package structures

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
)

// recordedAt is the box the world would record for a kind built at a site.
func recordedAt(t *testing.T, p Predictor, site Site) Evidence {
	t.Helper()
	var box Box
	switch p.(type) {
	case monument:
		x, z := site.ChunkX*16+8, site.ChunkZ*16+8
		box = Box{MinX: x - 29, MinZ: z - 29, MaxX: x + 28, MaxZ: z + 28}
	case outpost:
		x, z := site.ChunkX*16, site.ChunkZ*16
		box = Box{MinX: x, MinZ: z, MaxX: x + 15, MaxZ: z + 15}
	case witchHut:
		x, z := site.ChunkX*16, site.ChunkZ*16
		box = Box{MinX: x + 2, MinZ: z + 3, MaxX: x + 8, MaxZ: z + 11}
	default:
		t.Fatalf("no box for %s", p.Kind())
	}
	if !p.Explains(site, box) {
		t.Fatalf("%s at %v does not explain its own box", p.Kind(), site)
	}
	return Evidence{Kind: p.Kind(), Box: box}
}

// evidenceFor is a handful of structures a world with this seed records.
func evidenceFor(t *testing.T, seed uint32) []Evidence {
	t.Helper()
	area := Area{-200, -200, 200, 200}
	var out []Evidence
	for _, p := range []Predictor{outpost{}, monument{}, witchHut{}} {
		sites, _ := p.Sites(seed, area, 4)
		for _, site := range sites {
			out = append(out, recordedAt(t, p, site))
		}
	}
	return out
}

func TestOffsetsMatchTheTwister(t *testing.T) {
	for _, s := range []spread{monumentSpread, outpostSpread, witchHutSpread, villageSpread} {
		for _, m := range []uint32{0, 1, 0x55667788, 0xffffffff, 2463534242} {
			site, _ := s.site(m-s.salt, 0, 0)
			x, z := s.offsets(m)
			if int32(x) != site.ChunkX || int32(z) != site.ChunkZ {
				t.Errorf("spread %+v seeded %d: offsets %d, %d, site %v", s, m, x, z, site)
			}
		}
	}
}

// anchorSeeding is the generator seeding the search finds the seed at, so
// a test can search a window round it instead of every seeding.
func anchorSeeding(t *testing.T, evidence []Evidence, seed uint32) uint64 {
	t.Helper()
	best := placement{chance: 2}
	for _, e := range evidence {
		for _, p := range Predictors {
			if s, ok := p.(scattered); ok && p.Kind() == e.Kind {
				if at, ok := place(s, e.Box); ok && at.chance < best.chance {
					best = at
				}
			}
		}
	}
	return uint64(seed + uint32(best.regionX)*regionFactorX + uint32(best.regionZ)*regionFactorZ + best.s.salt)
}

func TestSolveFindsTheSeed(t *testing.T) {
	for _, want := range []uint32{0x55667788, 7, 0xfffffff0} {
		evidence := evidenceFor(t, want)
		at := anchorSeeding(t, evidence, want)
		from, to := uint64(0), uint64(1<<32)
		if at > 1<<21 {
			from = at - 1<<21
		}
		if at+1<<21 < to {
			to = at + 1<<21
		}
		got, agree, err := solve(context.Background(), evidence, minEvidence, 2, from, to)
		if err != nil || got != want || agree != len(evidence) {
			t.Errorf("seed %d: got %d explaining %d of %d, %v", want, got, agree, len(evidence), err)
		}
	}
}

func TestSolveRefusesWhatItCannotTell(t *testing.T) {
	evidence := evidenceFor(t, 0x55667788)
	at := anchorSeeding(t, evidence, 0x55667788)
	// A window the seed is not in holds nothing that explains four records.
	if _, _, err := solve(context.Background(), evidence, minEvidence, 2, at+1, at+1<<20); !errors.Is(err, ErrNoSeed) {
		t.Errorf("a window without the seed: %v", err)
	}
	// One record alone is explained by millions of seeds.
	if _, _, err := solve(context.Background(), evidence[:1], 0, 2, at-1<<20, at+1<<20); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("one record: %v", err)
	}
	if _, _, err := Solve(context.Background(), []Evidence{{Kind: Fortress}}, 0, 1); !errors.Is(err, ErrNoAnchor) {
		t.Errorf("nothing exact: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Solve(ctx, evidence, minEvidence, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// The whole search, which is minutes of every CPU:
//
//	MCMAP_SOLVE_ALL=1 go test -run SolveEverySeeding -v -timeout 2h ./minecraft/mcmap/internal/structures/
func TestSolveEverySeeding(t *testing.T) {
	if os.Getenv("MCMAP_SOLVE_ALL") == "" {
		t.Skip("MCMAP_SOLVE_ALL is not set")
	}
	const want = 0x9abcdef1
	evidence := evidenceFor(t, want)
	started := time.Now()
	got, agree, err := Solve(context.Background(), evidence, minEvidence, runtime.GOMAXPROCS(0))
	t.Logf("searched in %s", time.Since(started).Round(time.Second))
	if err != nil || got != want {
		t.Errorf("got %d explaining %d of %d, %v", got, agree, len(evidence), err)
	}
}
