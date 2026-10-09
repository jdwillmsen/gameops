package structures

import (
	"context"
	"errors"
	"sync"
)

// scattered is a kind whose sites come from a spread alone, which is what
// lets a recorded structure be turned back into the numbers that placed it.
type scattered interface {
	Predictor
	scatter() spread
}

func (monument) scatter() spread { return monumentSpread }
func (outpost) scatter() spread  { return outpostSpread }
func (witchHut) scatter() spread { return witchHutSpread }

// Evidence is one recorded structure of a kind whose placement is exact.
type Evidence struct {
	Kind Kind
	Box  Box
}

// placement is where one piece of evidence allows its site to have been.
type placement struct {
	s       spread
	regionX int32
	regionZ int32
	// allowed is indexed by the site's offset in its region, x*span+z.
	allowed []bool
	chance  float64
}

var (
	// ErrNoAnchor: no piece of evidence pins its site to one region, so
	// there is nothing to search from.
	ErrNoAnchor = errors.New("structures: no recorded structure pins a site to one region")
	// ErrAmbiguous: more than one seed explains the evidence equally well.
	ErrAmbiguous = errors.New("structures: more than one seed explains the recorded structures")
	// ErrNoSeed: no 32-bit seed puts enough of the evidence where it is.
	ErrNoSeed = errors.New("structures: no seed explains the recorded structures")
)

// place works out the offsets a record allows. ok is false for a record
// that could belong to a site in more than one region, which says too
// little to be worth the cases.
func place(p scattered, box Box) (placement, bool) {
	s := p.scatter()
	span := s.spacing - s.separation
	out := placement{s: s, allowed: make([]bool, span*span)}
	// No kind's footprint reaches further from its site than this.
	const reach = 4
	found := 0
	for cx := box.MinX>>4 - reach; cx <= box.MaxX>>4+reach; cx++ {
		for cz := box.MinZ>>4 - reach; cz <= box.MaxZ>>4+reach; cz++ {
			if !p.Explains(Site{cx, cz}, box) {
				continue
			}
			rx, rz := floorDiv(cx, s.spacing), floorDiv(cz, s.spacing)
			ox, oz := cx-rx*s.spacing, cz-rz*s.spacing
			if ox >= span || oz >= span {
				continue
			}
			if found > 0 && (rx != out.regionX || rz != out.regionZ) {
				return placement{}, false
			}
			out.regionX, out.regionZ = rx, rz
			out.allowed[ox*span+oz] = true
			found++
		}
	}
	out.chance = float64(found) / float64(span*span)
	return out, found > 0
}

func (p placement) explainedBy(seed uint32) bool {
	site, _ := p.s.site(seed, p.regionX, p.regionZ)
	span := p.s.spacing - p.s.separation
	ox, oz := site.ChunkX-p.regionX*p.s.spacing, site.ChunkZ-p.regionZ*p.s.spacing
	return p.allowed[ox*span+oz]
}

// offsets is spread.site for a generator seeded with m, without the 624
// words of state: the four draws a site takes read only words 0 to 4 and
// 397 to 400, and the search seeds the generator four thousand million
// times.
func (s spread) offsets(m uint32) (x, z uint32) {
	const last = twisterShift + 3
	var low [5]uint32
	var high [4]uint32
	prev := m
	low[0] = m
	for i := uint32(1); i <= last; i++ {
		prev = 1812433253*(prev^(prev>>30)) + i
		if i < 5 {
			low[i] = prev
		} else if i >= twisterShift {
			high[i-twisterShift] = prev
		}
	}
	draw := func(k int) uint32 {
		y := (low[k] & 0x80000000) | (low[k+1] & 0x7fffffff)
		v := high[k] ^ (y >> 1)
		if y&1 != 0 {
			v ^= 0x9908b0df
		}
		v ^= v >> 11
		v ^= (v << 7) & 0x9d2c5680
		v ^= (v << 15) & 0xefc60000
		v ^= v >> 18
		return v
	}
	span := uint32(s.spacing - s.separation)
	if !s.triangular {
		return draw(0) % span, draw(1) % span
	}
	return (draw(0)%span + draw(1)%span) / 2, (draw(2)%span + draw(3)%span) / 2
}

// Solve finds the 32 bits that put the evidence where the world recorded
// it, by trying all of them on workers CPUs: about fifty minutes of one.
//
// One record, the anchor, is tested against each seeding of the generator;
// the few million that pass are set against the rest. A seed is the answer
// when it explains the anchor and at least need others and no other seed
// explains as many. agree is how many records it explains in all.
func Solve(ctx context.Context, evidence []Evidence, need, workers int) (seed uint32, agree int, err error) {
	return solve(ctx, evidence, need, workers, 0, 1<<32)
}

// solve searches the generator seedings from, inclusive, to to, exclusive.
func solve(ctx context.Context, evidence []Evidence, need, count int, from, to uint64) (uint32, int, error) {
	var placed []placement
	for _, e := range evidence {
		for _, p := range Predictors {
			s, exact := p.(scattered)
			if !exact || !p.Exact() || p.Kind() != e.Kind {
				continue
			}
			if at, ok := place(s, e.Box); ok {
				placed = append(placed, at)
			}
		}
	}
	anchor := -1
	for i, p := range placed {
		if anchor < 0 || p.chance < placed[anchor].chance {
			anchor = i
		}
	}
	if anchor < 0 {
		return 0, 0, ErrNoAnchor
	}
	a := placed[anchor]
	span := uint32(a.s.spacing - a.s.separation)
	shift := uint32(a.regionX)*regionFactorX + uint32(a.regionZ)*regionFactorZ + a.s.salt

	type best struct {
		seed  uint32
		agree int
		ties  int
	}
	workers := uint64(max(count, 1))
	results := make([]best, workers)
	var wg sync.WaitGroup
	for w := uint64(0); w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lo, hi := from+(to-from)*w/workers, from+(to-from)*(w+1)/workers
			var b best
			for m := lo; m < hi; m++ {
				if m&0xffff == 0 && ctx.Err() != nil {
					return
				}
				x, z := a.s.offsets(uint32(m))
				if !a.allowed[x*span+z] {
					continue
				}
				seed, agree := uint32(m)-shift, 1
				for i, p := range placed {
					if i != anchor && p.explainedBy(seed) {
						agree++
					}
				}
				switch {
				case agree > b.agree:
					b = best{seed, agree, 0}
				case agree == b.agree:
					b.ties++
				}
			}
			results[w] = b
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	var b best
	for _, r := range results {
		switch {
		case r.agree > b.agree:
			b = r
		case r.agree == b.agree:
			b.ties += r.ties + 1
		}
	}
	switch {
	case b.agree < need+1:
		return 0, b.agree, ErrNoSeed
	case b.ties > 0:
		return 0, b.agree, ErrAmbiguous
	}
	return b.seed, b.agree, nil
}
