package structures

import (
	"reflect"
	"testing"
)

// The reference outputs of MT19937 seeded with 5489, from its authors.
func TestTwister_MatchesTheReferenceGenerator(t *testing.T) {
	rng := newTwister(5489)
	for i, want := range []uint32{3499211612, 581869302, 3890346734, 3586334585, 545404204} {
		if got := rng.next(); got != want {
			t.Errorf("output %d = %d, want %d", i, got, want)
		}
	}
}

// These pin the arithmetic, for a seed that is nobody's world. They were
// worked out by a second implementation of the same rules, written
// separately, so they catch a slip in this one; what shows the rules are
// the game's is the check against a real world's records on every survey,
// which on 2026-10-05 (game 1.26.52.3) placed all 11 monuments, 7 outposts
// and 1 witch hut the FWB world had recorded.
func TestSpread_SitesForAKnownSeed(t *testing.T) {
	const seed = 20261005
	regions := [][2]int32{{0, 0}, {-1, 0}, {3, -2}, {-7, -5}}
	for name, c := range map[string]struct {
		spread spread
		want   []Site
	}{
		"fortress": {fortressSpread, []Site{{2, 12}, {-25, 15}, {91, -58}, {-195, -144}}},
		"monument": {monumentSpread, []Site{{10, 14}, {-26, 19}, {105, -58}, {-207, -147}}},
		"outpost":  {outpostSpread, []Site{{31, 21}, {-75, 33}, {251, -113}, {-552, -372}}},
		"hut":      {witchHutSpread, []Site{{18, 6}, {-10, 19}, {101, -49}, {-221, -140}}},
	} {
		for i, r := range regions {
			if got, _ := c.spread.site(seed, r[0], r[1]); got != c.want[i] {
				t.Errorf("%s region %v: site %+v, want %+v", name, r, got, c.want[i])
			}
		}
	}
}

// A third of the nether's regions hold a fortress; the rest are left to
// bastions, and must not be listed.
func TestFortress_ListsOnlyTheRegionsThatGetAFortress(t *testing.T) {
	got, more := fortress{}.Sites(20261005, Area{-64, -64, 63, 63}, 100)
	want := []Site{{-47, 20}, {-40, 49}, {-12, -51}, {-15, -24}, {21, -5}, {12, 50}}
	if !reflect.DeepEqual(got, want) || more != 0 {
		t.Errorf("Sites = %+v (+%d)\nwant   %+v", got, more, want)
	}
}

func TestSites_StayInsideTheAreaAndTheLimit(t *testing.T) {
	area := Area{-500, -300, 700, 900}
	for _, p := range Predictors {
		all, more := p.Sites(7, area, 1_000_000)
		if more != 0 || len(all) == 0 {
			t.Fatalf("%s: %d sites, %d left out", p.Kind(), len(all), more)
		}
		seen := map[Site]bool{}
		for _, s := range all {
			if s.ChunkX < area.MinX || s.ChunkX > area.MaxX || s.ChunkZ < area.MinZ || s.ChunkZ > area.MaxZ {
				t.Errorf("%s: site %+v is outside %+v", p.Kind(), s, area)
			}
			if seen[s] {
				t.Errorf("%s: site %+v listed twice", p.Kind(), s)
			}
			seen[s] = true
		}
		// A stray chunk far from the rest must not become a walk over
		// everything in between.
		if far, _ := p.Sites(7, Area{0, 0, 1 << 26, 1 << 26}, 10); len(far) != 10 {
			t.Errorf("%s: %d sites from a vast area, want the 10 asked for", p.Kind(), len(far))
		}
		some, more := p.Sites(7, area, 5)
		if len(some) != 5 || more != len(all)-5 {
			t.Errorf("%s: a limit of 5 gave %d sites and %d left out, of %d", p.Kind(), len(some), more, len(all))
		}
	}
}

func TestExplains(t *testing.T) {
	site := Site{-10, 6} // first block -160, 96; centre -152, 104
	for name, c := range map[string]struct {
		p    Predictor
		real Box
		want bool
	}{
		"monument whole":                 {monument{}, Box{-181, 39, 75, -124, 61, 132}, true},
		"monument part generated":        {monument{}, Box{-176, 39, 75, -124, 61, 127}, true},
		"monument one chunk over":        {monument{}, Box{-165, 39, 75, -108, 61, 132}, false},
		"monument part without centre":   {monument{}, Box{-181, 39, 75, -161, 61, 95}, false},
		"outpost at the first block":     {outpost{}, Box{-160, 64, 96, -145, 85, 111}, true},
		"outpost turned to end there":    {outpost{}, Box{-175, 64, 81, -160, 85, 96}, true},
		"outpost a chunk away":           {outpost{}, Box{-144, 64, 96, -129, 85, 111}, false},
		"hut inside the chunk":           {witchHut{}, Box{-160, 85, 96, -154, 91, 104}, true},
		"hut in the next chunk":          {witchHut{}, Box{-144, 85, 96, -138, 91, 104}, false},
		"fortress around the site":       {fortress{}, Box{-250, 48, 20, -100, 80, 150}, true},
		"fortress fragment within reach": {fortress{}, Box{-10, 48, 100, -5, 57, 104}, true},
		"fortress out of reach":          {fortress{}, Box{20, 48, 100, 25, 57, 104}, false},
	} {
		if got := c.p.Explains(site, c.real); got != c.want {
			t.Errorf("%s: Explains = %v, want %v", name, got, c.want)
		}
	}
}

func TestFloorDiv(t *testing.T) {
	for _, c := range [][3]int32{{0, 32, 0}, {31, 32, 0}, {32, 32, 1}, {-1, 32, -1}, {-32, 32, -1}, {-33, 32, -2}} {
		if got := floorDiv(c[0], c[1]); got != c[2] {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}
