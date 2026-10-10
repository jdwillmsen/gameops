package structures

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

const (
	testDeepOcean = biomeDeepOcean
	testForest    = 4
)

// biomesOf answers from a list of chunks, as a reading of the world's
// biomes does: nothing for a chunk it does not hold.
func biomesOf(held map[Site]uint32) BiomeAt {
	return func(d chunks.Dimension, x, z int32) (uint32, bool) {
		id, ok := held[Site{x >> 4, z >> 4}]
		return id, ok && d == chunks.Overworld
	}
}

func siteOf(p Prediction) Site { return Site{p.X >> 4, p.Z >> 4} }

// The fortresses a world recorded where the rule puts none are a reason to
// stop predicting fortresses, and no reason to stop predicting monuments.
func TestTake_AKindTheWorldContradictsIsWithheldAlone(t *testing.T) {
	w := evidence(t)
	w.fortresses()
	// More fortresses than the rule explains, each far from any site.
	all, _ := fortress{}.Sites(testSeed, Area{-200, -200, 250, 250}, 1000)
	isNear := func(x, z int32) bool {
		for _, site := range all {
			if (fortress{}).Explains(site, Box{x, 48, z, x + 5, 57, z + 5}) {
				return true
			}
		}
		return false
	}
	stray := 0
	for x := int32(-1000); x < 2000 && stray < 4; x += 160 {
		for z := int32(-1000); z < 2000 && stray < 4; z += 160 {
			if !isNear(x, z) {
				w.structure(chunks.Nether, fortressByte, Box{x, 48, z, x + 5, 57, z + 5})
				stray++
			}
		}
	}
	if stray != 4 {
		t.Fatalf("found room for only %d stray fortresses", stray)
	}
	log := &bytes.Buffer{}
	s, dir := surveyor(t, log), w.write()
	got, err := s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Check.State != SeedVerified {
		t.Fatalf("check = %+v, want the seed still verified by the monuments", got.Check)
	}
	// Said once, as a warning: the seed is right, so it is the rule that
	// is wrong.
	if _, err := s.Take(context.Background(), dir, surveyedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(log.String(), "kind=fortress state=refuted"); n != 1 || !strings.Contains(log.String(), "level=WARN msg=\"structure kind checked") {
		t.Errorf("the fortress rule's refusal was logged %d times, want once and as a warning:\n%s", n, log)
	}
	if n := strings.Count(log.String(), "kind=monument state=verified"); n != 1 {
		t.Errorf("the monument rule's standing was logged %d times, want once", n)
	}
	if k := got.Check.Kinds[Rule{Fortress, chunks.Nether}]; k.State != SeedRefuted || k.Agree != 3 || k.Disagree != 4 || k.Findings != 4 {
		t.Errorf("fortress = %+v, want refuted with 3 explained, 4 not", k)
	}
	if n := len(got.Layers[chunks.Nether].Predicted); n != 0 {
		t.Errorf("%d fortresses predicted by a rule the world contradicts", n)
	}
	if k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]; k.State != SeedVerified {
		t.Errorf("monument = %+v, want verified", k)
	}
	if len(got.Layers[chunks.Overworld].Predicted) == 0 {
		t.Error("monuments are withheld because fortresses are")
	}
	if v := testutil.ToFloat64(metricKindVerified.WithLabelValues("nether", string(Fortress))); v != 0 {
		t.Errorf("mcmap_structures_kind_verified{fortress} = %v, want 0", v)
	}
	if v := testutil.ToFloat64(metricKindVerified.WithLabelValues("overworld", string(Monument))); v != 1 {
		t.Errorf("mcmap_structures_kind_verified{monument} = %v, want 1", v)
	}
	if v := testutil.ToFloat64(metricDisagreements.WithLabelValues("nether", string(Fortress))); v != 4 {
		t.Errorf("mcmap_structures_prediction_disagreements{fortress} = %v, want 4", v)
	}
	if v := testutil.ToFloat64(metricSeedVerified); v != 1 {
		t.Errorf("mcmap_structures_seed_verified = %v, want 1", v)
	}
}

// What the biome of a site's chunk makes of it: nothing where the kind is
// not built there, a prediction the world has not borne out where the
// chunk is finished, and a candidate where it is not.
func TestTake_TheBiomeDecidesWhatASiteIs(t *testing.T) {
	w := evidence(t)
	var free []Site
	recorded := map[Site]bool{}
	for _, region := range [][2]int32{{0, 0}, {1, 0}, {0, 1}} {
		site, _ := monumentSpread.site(testSeed, region[0], region[1])
		recorded[site] = true
	}
	all, _ := monument{}.Sites(testSeed, evidenceArea, 100)
	for _, site := range all {
		if !recorded[site] {
			free = append(free, site)
		}
	}
	if len(free) < 5 {
		t.Fatalf("only %d monument sites to test with", len(free))
	}
	finishedOcean, finishedForest, finishedUnread, startedForest, startedOcean := free[0], free[1], free[2], free[3], free[4]
	held := []Site{{0, 0}, {63, 63}}
	biome := map[Site]uint32{
		finishedOcean: testDeepOcean, finishedForest: testForest,
		startedForest: testForest, startedOcean: testDeepOcean,
	}
	for _, site := range []Site{finishedOcean, finishedForest, finishedUnread} {
		w.generated(chunks.Overworld, site.ChunkX, site.ChunkZ)
		held = append(held, site)
	}
	for site := range recorded {
		w.generated(chunks.Overworld, site.ChunkX, site.ChunkZ)
		held = append(held, site)
		biome[site] = testDeepOcean
	}
	// How far a site is from the nearest chunk the world holds.
	away := func(site Site) int32 {
		best := int32(1 << 30)
		for _, h := range held {
			best = min(best, max(site.ChunkX-h.ChunkX, h.ChunkX-site.ChunkX, site.ChunkZ-h.ChunkZ, h.ChunkZ-site.ChunkZ))
		}
		return best
	}
	s := surveyor(t, nil)
	s.Biomes = biomesOf(biome)
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	found := map[Site]Prediction{}
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		found[siteOf(p)] = p
	}
	if p, ok := found[finishedOcean]; !ok || !p.Generated || p.Candidate {
		t.Errorf("a finished site in a deep ocean with no monument = %+v (offered %v), want it marked generated", p, ok)
	}
	if p, ok := found[startedOcean]; !ok || !p.Candidate || p.Generated {
		t.Errorf("an unfinished site in a deep ocean = %+v (offered %v), want a candidate", p, ok)
	}
	for name, site := range map[string]Site{"finished, in a forest": finishedForest, "unfinished, in a forest": startedForest, "finished, biome not read": finishedUnread} {
		if p, ok := found[site]; ok {
			t.Errorf("a site %s is offered: %+v", name, p)
		}
	}
	// Beyond the world, a site is a candidate while it is within reach of
	// a chunk and nothing past that; the reach is kept to the nearest
	// sixteen chunks.
	beside := 0
	for _, site := range free[5:] {
		p, ok := found[site]
		switch d := away(site); {
		case d <= predictionMargin-cellChunks:
			beside++
			if !ok || !p.Candidate {
				t.Errorf("a site %d chunks from the world = %+v (offered %v), want a candidate", d, p, ok)
			}
		case d > predictionMargin+2*cellChunks:
			if ok {
				t.Errorf("a site %d chunks from the world is offered: %+v", d, p)
			}
		}
	}
	if beside == 0 {
		t.Error("no site beside the world to test with")
	}
	k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]
	if k.Built != 3 || k.Empty != 1 || k.Findings != 1 {
		t.Errorf("monument = %+v, want 3 built, 1 empty and that one a finding", k)
	}
	if len(got.Check.Findings) != 1 || !strings.Contains(got.Check.Findings[0], "monument predicted at overworld") {
		t.Errorf("findings = %v", got.Check.Findings)
	}
	if v := testutil.ToFloat64(metricPredicted.WithLabelValues("overworld", string(Monument), "predicted")); v != 1 {
		t.Errorf("mcmap_structures_predicted{monument,predicted} = %v, want 1", v)
	}
	candidates := 0
	for _, p := range found {
		if p.Candidate {
			candidates++
		}
	}
	if v := testutil.ToFloat64(metricPredicted.WithLabelValues("overworld", string(Monument), "candidate")); int(v) != candidates || candidates < len(free)-4 {
		t.Errorf("mcmap_structures_predicted{monument,candidate} = %v, want the %d candidates offered", v, candidates)
	}

	// With no reading of the biomes there is nothing to say a finished
	// site should have held a monument, and eleven in twelve should not.
	got, err = surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	offered := map[Site]Prediction{}
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		offered[siteOf(p)] = p
	}
	for _, site := range []Site{finishedOcean, finishedForest, finishedUnread} {
		if p, ok := offered[site]; ok {
			t.Errorf("without biomes a finished site is offered: %+v", p)
		}
	}
	for _, site := range free[3:] {
		if p, ok := offered[site]; away(site) <= predictionMargin-cellChunks && (!ok || !p.Candidate) {
			t.Errorf("without biomes an unfinished site = %+v (offered %v), want a candidate", p, ok)
		}
	}
	if got.Check.Total != 0 {
		t.Errorf("findings without biomes = %v", got.Check.Findings)
	}
}

// villageAt is a village's box as the game might have grown it round the
// site it was generated at.
func villageAt(s Site) Box {
	return Box{s.ChunkX*16 - 30, 60, s.ChunkZ*16 - 20, s.ChunkX*16 + 40, 90, s.ChunkZ*16 + 50}
}

// Players found villages wherever they put a bed and a villager, so most
// of a world's villages may have no site, and the rule is still borne out
// by the ones that do. A site with no village on record is no finding
// either: the game keeps no record of a village nobody has been near.
func TestTake_VillagesPlayersFoundedAreNothingAgainstTheRule(t *testing.T) {
	w := evidence(t)
	sites, _ := villageSite{}.Sites(testSeed, evidenceArea, 100)
	if len(sites) < 6 {
		t.Fatalf("only %d village sites to test with", len(sites))
	}
	for _, site := range sites[:3] {
		w.settled(4, villageAt(site))
	}
	unrecorded := sites[3]
	w.generated(chunks.Overworld, unrecorded.ChunkX, unrecorded.ChunkZ)
	founded := 0
	for x := int32(-900); x < 1900 && founded < 9; x += 130 {
		box := Box{x, 60, x, x + 40, 80, x + 40}
		near := false
		for _, site := range sites {
			near = near || (villageSite{}).Explains(site, box)
		}
		if !near {
			w.settled(2, box)
			founded++
		}
	}
	if founded != 9 {
		t.Fatalf("found room for only %d founded villages", founded)
	}
	s := surveyor(t, nil)
	s.Biomes = biomesOf(map[Site]uint32{unrecorded: biomePlains})
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	k := got.Check.Kinds[Rule{Village, chunks.Overworld}]
	if k.State != SeedVerified || k.Agree != 3 || k.Disagree != 9 || k.Findings != 0 {
		t.Errorf("village = %+v, want verified by 3 of 12 and no findings", k)
	}
	if got.Check.Agree != 3 || got.Check.Disagree != 0 {
		t.Errorf("check = %+v: villages are no evidence about the seed", got.Check)
	}
	villages := map[Site]Prediction{}
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		if p.Kind == Village {
			villages[siteOf(p)] = p
		}
	}
	for _, site := range sites[:3] {
		if p, ok := villages[site]; ok {
			t.Errorf("%+v is predicted where the world has a village", p)
		}
	}
	if p, ok := villages[unrecorded]; !ok || !p.Generated {
		t.Errorf("a finished site in plains with no village on record = %+v (offered %v), want it marked generated", p, ok)
	}
	if p, ok := villages[sites[4]]; !ok || !p.Candidate {
		t.Errorf("a site in country not generated = %+v (offered %v), want a candidate", p, ok)
	}
}

func TestKindCheck_Settle(t *testing.T) {
	for name, c := range map[string]struct {
		seed            string
		founded         bool
		agree, disagree int
		want            string
	}{
		"nothing recorded":                      {SeedVerified, false, 0, 0, SeedUnverified},
		"two on their sites":                    {SeedVerified, false, 2, 0, SeedUnverified},
		"three on their sites":                  {SeedVerified, false, 3, 0, SeedVerified},
		"as many off as on":                     {SeedVerified, false, 3, 3, SeedRefuted},
		"three off":                             {SeedVerified, false, 0, 3, SeedRefuted},
		"two on, one off":                       {SeedVerified, false, 2, 1, SeedUnverified},
		"founded, one in five on a site":        {SeedVerified, true, 3, 12, SeedVerified},
		"founded, fewer than one in five":       {SeedVerified, true, 3, 13, SeedRefuted},
		"founded, two on a site":                {SeedVerified, true, 2, 1, SeedUnverified},
		"founded, none on a site":               {SeedVerified, true, 0, 3, SeedRefuted},
		"a seed not verified judges no rule":    {SeedUnverified, false, 9, 0, SeedUnverified},
		"a refuted seed refutes every kind":     {SeedRefuted, false, 9, 0, SeedRefuted},
		"an unread seed leaves every kind open": {SeedUnknown, true, 9, 0, SeedUnknown},
	} {
		k := KindCheck{Agree: c.agree, Disagree: c.disagree, borneAgree: c.agree, borneDisagree: c.disagree}
		if k.settle(c.seed, c.founded); k.State != c.want {
			t.Errorf("%s: %s, want %s", name, k.State, c.want)
		}
	}
	// A rule is judged by the chunks of the seeds it has not been set
	// aside for: those of a game version that placed the kind another way
	// neither refute it elsewhere nor are counted for it.
	for name, c := range map[string]struct {
		k    KindCheck
		want string
	}{
		"borne out where it is still used": {KindCheck{Agree: 9, Disagree: 20, SetAside: 1, borneAgree: 6, borneDisagree: 2}, SeedVerified},
		"too few where it is still used":   {KindCheck{Agree: 5, Disagree: 20, SetAside: 1, borneAgree: 2}, SeedUnverified},
		"set aside for every seed":         {KindCheck{Agree: 3, Disagree: 20, SetAside: 2}, SeedRefuted},
	} {
		if c.k.settle(SeedVerified, false); c.k.State != c.want {
			t.Errorf("%s: %s, want %s", name, c.k.State, c.want)
		}
	}
}

// One kind has a site every 32 chunks and another every 80. Each is cut to
// its own bound, nearest the middle of the world first, so that the common
// one cannot leave the rare one no room.
func TestCompare_BoundsEachKindAndKeepsTheNearest(t *testing.T) {
	// A chunk every 64 each way, so that all of it is beside the world.
	e := newExtent(chunks.Pos{})
	for x := int32(-2000); x <= 2000; x += 64 {
		for z := int32(-2000); z <= 2000; z += 64 {
			e.add(chunks.Pos{X: x, Z: z})
		}
	}
	e.single = true
	extents := map[chunks.Dimension]*extent{chunks.Overworld: e}
	var recorded []Structure
	for _, region := range [][2]int32{{0, 0}, {1, 0}, {0, 1}} {
		m, _ := monumentSpread.site(testSeed, region[0], region[1])
		recorded = append(recorded, Structure{Kind: Monument, Box: monumentAt(m)})
		o, _ := outpostSpread.site(testSeed, region[0], region[1])
		recorded = append(recorded, Structure{Kind: Outpost, Box: Box{o.ChunkX * 16, 64, o.ChunkZ * 16, o.ChunkX*16 + 15, 85, o.ChunkZ*16 + 15}})
	}
	check, predicted, more := compare([]Predictor{monument{}, outpost{}}, []worldSeed{{whole: int64(testSeed), narrow: true}}, 0, MaxPerLayer,
		map[chunks.Dimension][]Structure{chunks.Overworld: recorded}, nil, extents, nil)
	if check.State != SeedVerified {
		t.Fatalf("check = %+v", check)
	}
	count := map[Kind]int{}
	furthest := map[Kind]int32{}
	for _, p := range predicted[chunks.Overworld] {
		count[p.Kind]++
		furthest[p.Kind] = max(furthest[p.Kind], max(p.X, -p.X, p.Z, -p.Z)>>4)
	}
	if count[Monument] != MaxPerKind || count[Outpost] != MaxPerKind {
		t.Errorf("kept %d monuments and %d outposts, want %d of each", count[Monument], count[Outpost], MaxPerKind)
	}
	// 500 sites, one to a region 32 chunks a side, are within some 13
	// regions of the middle if the nearest were kept, and the area is 64.
	if furthest[Monument] > 20*32 {
		t.Errorf("a monument %d chunks from the middle was kept over nearer ones", furthest[Monument])
	}
	if more[chunks.Overworld] < 10_000 {
		t.Errorf("%d left out, want the thousands there were", more[chunks.Overworld])
	}
}
