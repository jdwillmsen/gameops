package structures

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/fnv"
	"os"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// searchTimeout is how long one search for the seed may take. It is about
// fifty minutes of one CPU, and the pod it runs in may be given less.
const searchTimeout = 6 * time.Hour

// kept is what SeedFile holds: the outcome of the last search and the
// records it was made from, so that a restart neither searches again nor
// forgets that a search found nothing.
type kept struct {
	Seed     uint32 `json:"seed"`
	Found    bool   `json:"found"`
	Evidence uint64 `json:"evidence"`
}

// exactEvidence is the recorded structures a seed can be worked out from,
// and a number that changes when they do.
func exactEvidence(known map[chunks.Dimension][]Structure) ([]Evidence, uint64) {
	exact := map[Kind]bool{}
	for _, p := range Predictors {
		if _, ok := p.(scattered); ok && p.Exact() {
			exact[p.Kind()] = true
		}
	}
	var out []Evidence
	sum := fnv.New64a()
	for _, d := range chunks.Dimensions {
		for _, r := range known[d] {
			if !exact[r.Kind] {
				continue
			}
			out = append(out, Evidence{Kind: r.Kind, Box: r.Box})
			sum.Write([]byte(r.Kind))
			_ = binary.Write(sum, binary.LittleEndian, []int32{r.MinX, r.MinZ, r.MaxX, r.MaxZ})
		}
	}
	// Never zero, which is what "no search yet" is.
	return out, sum.Sum64() | 1
}

// worked is the seed a search found, if one has. The file is read once.
func (s *Surveyor) worked() (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.restored {
		s.restored = true
		var k kept
		if raw, err := os.ReadFile(s.SeedFile); err == nil && json.Unmarshal(raw, &k) == nil {
			s.searched = k.Evidence
			if k.Found {
				s.found = &k.Seed
			}
		}
	}
	if s.found == nil {
		return 0, false
	}
	return *s.found, true
}

// keep writes the outcome of a search beside the mirror, which already
// holds level.dat and so is no worse a place for a seed.
func (s *Surveyor) keep(k kept) {
	raw, err := json.Marshal(k)
	if err == nil {
		tmp := s.SeedFile + ".tmp"
		if err = os.WriteFile(tmp, raw, 0o600); err == nil {
			err = os.Rename(tmp, s.SeedFile)
		}
	}
	if err != nil {
		s.Logger.Warn("the outcome of the search for the structure seed was not kept; a restart searches again", "error", err)
	}
}

// reconsider starts a search for the seed when the one in hand has been
// refuted, unless these same records have been searched already. The seed
// it finds is used from the next survey, and checked there like any other.
//
// A seed a search found can be refuted by the very records it was found
// from: it explained enough of them to be the best there was and too few
// to be believed. It is dropped then, in favour of level.dat's, and the
// records are remembered as ones that have no answer.
func (s *Surveyor) reconsider(check Check, known map[chunks.Dimension][]Structure) {
	if s.SeedFile == "" || s.StructureSeed != nil || check.State != SeedRefuted {
		return
	}
	evidence, sum := exactEvidence(known)
	s.mu.Lock()
	if s.searching {
		s.mu.Unlock()
		return
	}
	if s.searched == sum {
		dropped := s.found != nil
		s.found = nil
		s.mu.Unlock()
		if dropped {
			s.keep(kept{Evidence: sum})
		}
		return
	}
	s.searching, s.searched = true, sum
	s.mu.Unlock()

	search, parent := s.search, s.Background
	if search == nil {
		// One CPU: the search is in no hurry and shares a node with the game.
		search = func(ctx context.Context, e []Evidence, need int) (uint32, int, error) { return Solve(ctx, e, need, 1) }
	}
	if parent == nil {
		parent = context.Background()
	}
	s.Logger.Info("working the structure seed out from the world's recorded structures, since the seed in hand does not place them; it takes about an hour of one CPU and nothing is predicted meanwhile",
		"records", len(evidence))
	go func() {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		started := time.Now()
		seed, agree, err := search(ctx, evidence, minEvidence-1)
		took := time.Since(started).Round(time.Second)

		s.mu.Lock()
		s.searching = false
		switch {
		case err == nil:
			s.found = &seed
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// Not an answer about these records, so they are searched again.
			s.searched = 0
		default:
			// The seed the last search found is refuted too by now.
			s.found = nil
		}
		s.mu.Unlock()

		switch {
		case err == nil:
			s.keep(kept{Seed: seed, Found: true, Evidence: sum})
			s.Logger.Info("structure seed worked out from the world's records; it is used from the next survey", "explains", agree, "records", len(evidence), "took", took.String())
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			s.Logger.Warn("the search for the structure seed was cut short and starts again at the next survey", "error", err, "took", took.String())
		default:
			s.keep(kept{Evidence: sum})
			s.Logger.Warn("no structure seed explains the world's recorded structures, so nothing is predicted; the search runs again when the world records more",
				"error", err, "explains", agree, "records", len(evidence), "took", took.String())
		}
	}()
}
