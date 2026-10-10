package structures

import (
	"reflect"
	"slices"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
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
// and 1 witch hut the FWB world had recorded, and 31 of its 70 villages.
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
		"village":  {villageSpread, []Site{{12, 9}, {-18, 8}, {111, -48}, {-219, -156}}},
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
		"monument whole":                  {monument{}, Box{-181, 39, 75, -124, 61, 132}, true},
		"monument part generated":         {monument{}, Box{-176, 39, 75, -124, 61, 127}, true},
		"monument one chunk over":         {monument{}, Box{-165, 39, 75, -108, 61, 132}, false},
		"monument part without centre":    {monument{}, Box{-181, 39, 75, -161, 61, 95}, false},
		"outpost at the first block":      {outpost{}, Box{-160, 64, 96, -145, 85, 111}, true},
		"outpost turned to end there":     {outpost{}, Box{-175, 64, 81, -160, 85, 96}, true},
		"outpost a chunk away":            {outpost{}, Box{-144, 64, 96, -129, 85, 111}, false},
		"outpost with its tents":          {outpost{}, Box{-203, 64, 81, -145, 85, 150}, true},
		"outpost's tents past any reach":  {outpost{}, Box{-260, 64, 81, -145, 85, 150}, false},
		"bastion's chests round the site": {bastion{}, Box{-190, 40, 80, -130, 70, 140}, true},
		"bastion's corner, in reach":      {bastion{}, Box{-90, 40, 104, -88, 44, 110}, true},
		"bastion out of reach":            {bastion{}, Box{-87, 40, 104, -80, 44, 110}, false},
		"city's tower on the site":        {endCity{}, Box{-153, 70, 100, -150, 90, 106}, true},
		"city reaching the site":          {endCity{}, Box{-240, 70, 100, -176, 120, 180}, true},
		"city short of the site":          {endCity{}, Box{-240, 70, 100, -177, 120, 180}, false},
		"portal's chest by the site":      {ruinedPortal{chunks.Overworld}, Box{-140, 64, 120, -140, 64, 120}, true},
		"portal's chest out of reach":     {ruinedPortal{chunks.Overworld}, Box{-127, 64, 120, -127, 64, 120}, false},
		"pyramid from the first block":    {desertPyramid{}, Box{-160, 62, 96, -140, 76, 116}, true},
		"pyramid known by its chests":     {desertPyramid{}, Box{-151, 51, 105, -149, 51, 107}, true},
		"pyramid a chunk over":            {desertPyramid{}, Box{-144, 62, 96, -124, 76, 116}, false},
		"temple inside the chunk":         {jungleTemple{}, Box{-160, 64, 96, -149, 73, 110}, true},
		"temple in the next chunk":        {jungleTemple{}, Box{-144, 64, 96, -133, 73, 110}, false},
		"igloo inside the chunk":          {igloo{}, Box{-160, 69, 96, -154, 73, 103}, true},
		"igloo known by its basement":     {igloo{}, Box{-164, 40, 100, -164, 40, 100}, true},
		"igloo two chunks over":           {igloo{}, Box{-128, 69, 96, -122, 73, 103}, false},
		"hut inside the chunk":            {witchHut{}, Box{-160, 85, 96, -154, 91, 104}, true},
		"hut in the next chunk":           {witchHut{}, Box{-144, 85, 96, -138, 91, 104}, false},
		"village round the site":          {villageSite{}, Box{-190, 60, 70, -120, 80, 140}, true},
		"village grown away, in reach":    {villageSite{}, Box{-136, 60, 70, -80, 80, 140}, true},
		"village out of reach":            {villageSite{}, Box{-135, 60, 70, -80, 80, 140}, false},
		"fortress around the site":        {fortress{}, Box{-250, 48, 20, -100, 80, 150}, true},
		"fortress fragment within reach":  {fortress{}, Box{-10, 48, 100, -5, 57, 104}, true},
		"fortress out of reach":           {fortress{}, Box{20, 48, 100, 25, 57, 104}, false},
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

// A kind the biome decides must say no to most biomes, or the filter that
// keeps its sites out of generated country filters nothing.
func TestAllows(t *testing.T) {
	const forest, river = 4, 7
	for _, p := range Predictors {
		if p.Certain() {
			if !p.Allows(forest) {
				t.Errorf("%s is built at every site and refuses a biome", p.Kind())
			}
			continue
		}
		// The Nether and the End have biomes of their own to refuse.
		switch p.Dimension() {
		case chunks.Overworld:
			if p.Allows(forest) || p.Allows(river) {
				t.Errorf("%s allows a forest or a river", p.Kind())
			}
		case chunks.Nether:
			if p.Allows(biomeBasaltDeltas) {
				t.Errorf("%s allows basalt deltas", p.Kind())
			}
		case chunks.End:
			if p.Allows(forest) {
				t.Errorf("%s allows a forest", p.Kind())
			}
		}
	}
	for name, c := range map[string]struct {
		p     Predictor
		biome uint32
		want  bool
	}{
		"monument in a deep ocean":      {monument{}, biomeDeepOcean, true},
		"monument in a deep cold ocean": {monument{}, biomeDeepColdOcean, true},
		"monument in a shallow ocean":   {monument{}, 0, false},
		"outpost in a desert":           {outpost{}, biomeDesert, true},
		"outpost in a swamp":            {outpost{}, biomeSwamp, false},
		"village in a savanna":          {villageSite{}, biomeSavanna, true},
		"village in a grove":            {villageSite{}, biomeGrove, false},
		"hut in a swamp":                {witchHut{}, biomeSwamp, true},
		"hut in a mangrove swamp":       {witchHut{}, 191, false},
		"bastion in a crimson forest":   {bastion{}, 179, true},
		"bastion in basalt deltas":      {bastion{}, biomeBasaltDeltas, false},
		"city in the End":               {endCity{}, biomeTheEnd, true},
		"portal anywhere":               {ruinedPortal{chunks.Nether}, biomeBasaltDeltas, true},
		"pyramid in a desert":           {desertPyramid{}, biomeDesert, true},
		"pyramid in a swamp":            {desertPyramid{}, biomeSwamp, false},
		"temple in a jungle":            {jungleTemple{}, biomeJungle, true},
		"temple in a desert":            {jungleTemple{}, biomeDesert, false},
		"igloo in snowy taiga":          {igloo{}, biomeSnowyTaiga, true},
		"igloo in plain taiga":          {igloo{}, biomeTaiga, false},
	} {
		if got := c.p.Allows(c.biome); got != c.want {
			t.Errorf("%s: Allows = %v, want %v", name, got, c.want)
		}
	}
}

// Four kinds share the witch hut's sites, and the biome under a site says
// which is built there. Until a chunk is finished nothing says which, so a
// site in country not generated is offered as one of them and not as all.
func TestKindsThatShareSitesAreOfferedOnlyWhereTheBiomeIsKnown(t *testing.T) {
	for _, p := range []Predictor{desertPyramid{}, jungleTemple{}, igloo{}} {
		if got := traitsOf(p); !got.FinishedOnly || !got.Quiet {
			t.Errorf("%s: %+v, want offered in finished chunks only, and an empty site no finding", p.Kind(), got)
		}
		hut, _ := witchHut{}.Sites(7, Area{-200, -200, 200, 200}, 1000)
		own, _ := p.Sites(7, Area{-200, -200, 200, 200}, 1000)
		if len(own) == 0 || !slices.Equal(hut, own) {
			t.Errorf("%s: %d sites, not the witch hut's %d", p.Kind(), len(own), len(hut))
		}
	}
	if got := traitsOf(monument{}); got != (Traits{}) {
		t.Errorf("a monument, which is recorded where it is built, has %+v", got)
	}
	// Three pyramids on their sites bear the rule out. A finished desert
	// site with none is offered, plainly; one not generated is not.
	e := newExtent(chunks.Pos{})
	e.single = true
	sites, _ := desertPyramid{}.Sites(7, Area{-200, -200, 200, 200}, 1000)
	var recorded []Structure
	for _, site := range sites[:3] {
		e.add(chunks.Pos{X: site.ChunkX, Z: site.ChunkZ})
		e.finish(chunks.Pos{X: site.ChunkX, Z: site.ChunkZ})
		recorded = append(recorded, Structure{Kind: DesertPyramid, Box: Box{site.ChunkX * 16, 62, site.ChunkZ * 16, site.ChunkX*16 + 20, 76, site.ChunkZ*16 + 20}})
	}
	bare := sites[3]
	e.add(chunks.Pos{X: bare.ChunkX, Z: bare.ChunkZ})
	e.finish(chunks.Pos{X: bare.ChunkX, Z: bare.ChunkZ})
	check, predicted, _ := compare([]Predictor{desertPyramid{}}, []worldSeed{{whole: 7, narrow: true}}, 0, MaxPerLayer,
		map[chunks.Dimension][]Structure{chunks.Overworld: recorded}, map[chunks.Dimension]*extent{chunks.Overworld: e},
		func(chunks.Dimension, int32, int32) (uint32, bool) { return biomeDesert, true })
	k := check.Kinds[Rule{DesertPyramid, chunks.Overworld}]
	if k.State != SeedVerified || k.Agree != 3 || k.Empty != 1 || k.Findings != 0 || check.Total != 0 {
		t.Fatalf("pyramids = %+v with %d findings, want verified and the empty site no finding", k, check.Total)
	}
	got := predicted[chunks.Overworld]
	if len(got) != 1 || siteOf(got[0]) != bare || !got[0].Generated || got[0].Candidate {
		t.Errorf("predicted %+v, want the one finished site with none and no site in country not generated", got)
	}
}

// A bastion is built where the fortress's draw gives no fortress: the two
// between them take every site of the Nether's grid, and share none.
func TestBastionsAndFortressesShareOutTheNethersSites(t *testing.T) {
	area := Area{-400, -400, 400, 400}
	all, _ := fortressSpread.sites(7, area, 10_000, nil)
	forts, _ := fortress{}.Sites(7, area, 10_000)
	bastions, _ := bastion{}.Sites(7, area, 10_000)
	if len(forts) == 0 || len(bastions) <= len(forts) || len(forts)+len(bastions) != len(all) {
		t.Fatalf("%d fortresses and %d bastions on %d sites, want about two bastions to a fortress and every site taken", len(forts), len(bastions), len(all))
	}
	for _, site := range forts {
		if slices.Contains(bastions, site) {
			t.Errorf("%+v is both a fortress's site and a bastion's", site)
		}
	}
}

// The overworld's portals and the Nether's are one kind on two grids, and
// each dimension's rule is judged by that dimension's portals.
func TestRuinedPortalsHaveARuleInEachDimension(t *testing.T) {
	over, _ := ruinedPortal{chunks.Overworld}.Sites(7, Area{-200, -200, 200, 200}, 10_000)
	under, _ := ruinedPortal{chunks.Nether}.Sites(7, Area{-200, -200, 200, 200}, 10_000)
	if len(over) == 0 || len(under) <= len(over) || slices.Equal(over, under[:len(over)]) {
		t.Fatalf("%d sites in the overworld and %d in the Nether, want the Nether's the closer set", len(over), len(under))
	}
	extents := map[chunks.Dimension]*extent{}
	recorded := map[chunks.Dimension][]Structure{}
	for d, sites := range map[chunks.Dimension][]Site{chunks.Overworld: over, chunks.Nether: under} {
		e := newExtent(chunks.Pos{Dim: d})
		e.single = true
		// Three chests where the overworld's rule puts a portal, in each
		// dimension: the Nether's rule explains none of them there.
		for _, site := range over[:3] {
			e.add(chunks.Pos{Dim: d, X: site.ChunkX, Z: site.ChunkZ})
			e.finish(chunks.Pos{Dim: d, X: site.ChunkX, Z: site.ChunkZ})
			at := Box{site.ChunkX*16 + 12, 64, site.ChunkZ*16 + 3, site.ChunkX*16 + 12, 64, site.ChunkZ*16 + 3}
			recorded[d] = append(recorded[d], Structure{Kind: RuinedPortal, Box: at, Evidence: 1})
		}
		bare := sites[len(sites)-1]
		e.add(chunks.Pos{Dim: d, X: bare.ChunkX, Z: bare.ChunkZ})
		e.finish(chunks.Pos{Dim: d, X: bare.ChunkX, Z: bare.ChunkZ})
		extents[d] = e
	}
	// A monument on its site, three times, is what the seed is believed by.
	for _, region := range [][2]int32{{0, 0}, {1, 0}, {0, 1}} {
		m, _ := monumentSpread.site(7, region[0], region[1])
		recorded[chunks.Overworld] = append(recorded[chunks.Overworld], Structure{Kind: Monument, Box: monumentAt(m)})
	}
	check, predicted, _ := compare([]Predictor{monument{}, ruinedPortal{chunks.Overworld}, ruinedPortal{chunks.Nether}},
		[]worldSeed{{whole: 7, narrow: true}}, 0, MaxPerLayer, recorded, extents, nil)
	here, there := check.Kinds[Rule{RuinedPortal, chunks.Overworld}], check.Kinds[Rule{RuinedPortal, chunks.Nether}]
	if here.State != SeedVerified || here.Agree != 3 || here.Disagree != 0 {
		t.Errorf("the overworld's rule = %+v, want borne out by its three", here)
	}
	if there.State != SeedRefuted || there.Agree != 0 || there.Disagree != 3 {
		t.Errorf("the Nether's rule = %+v, want refuted by three chests it puts no portal at", there)
	}
	// A chest somebody has opened no longer says what it was, so a
	// finished site with none is offered, plainly, and is no finding; and
	// a portal is not offered where nothing is generated.
	if check.Total != 0 {
		t.Errorf("%d findings: %v", check.Total, check.Findings)
	}
	portals := slices.DeleteFunc(slices.Clone(predicted[chunks.Overworld]), func(p Prediction) bool { return p.Kind != RuinedPortal })
	if len(portals) != 1 || !portals[0].Generated || portals[0].Candidate {
		t.Errorf("overworld portals predicted %+v, want the one finished site with no chest", portals)
	}
	if got := predicted[chunks.Nether]; len(got) != 0 {
		t.Errorf("the Nether is predicted %+v by a rule its portals refute", got)
	}
}
