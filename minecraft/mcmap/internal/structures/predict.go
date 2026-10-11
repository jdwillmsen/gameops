package structures

import "github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"

// Area is a rectangle of chunks, both corners inclusive.
type Area struct {
	MinX, MinZ, MaxX, MaxZ int32
}

// Site is where the generator starts a structure.
type Site struct {
	ChunkX, ChunkZ int32
}

// Predictor works out where the generator puts one kind of structure. Each
// kind is an implementation of its own, so one can be corrected, or a new
// one added, without touching the others.
type Predictor interface {
	Kind() Kind
	Dimension() chunks.Dimension
	// Sites lists the kind's sites in area, at most limit of them, and how
	// many it left out.
	Sites(seed uint32, area Area, limit int) (sites []Site, more int)
	// Explains reports whether a structure the world recorded is the one
	// this site would have produced.
	Explains(site Site, real Box) bool
	// Exact reports whether Explains is tight enough that a match is
	// evidence the seed is right, rather than something a wrong seed would
	// often produce by chance.
	Exact() bool
	// Certain reports whether the generator builds at every site. Where it
	// only builds if the biome suits, a site is a place it will try, and
	// only a chunk that exists has a biome to ask about.
	Certain() bool
	// Allows reports whether the generator builds this kind at a site in
	// the biome with this id.
	Allows(biome uint32) bool
	// Founded reports whether the world also records structures of this
	// kind that the generator never placed, so that one with no site is
	// nothing against the rule.
	Founded() bool
	// Centre is the block a site is drawn at.
	Centre(site Site) (x, z int32)
}

// Traits is what else is true of how a kind is known, for the kinds it is
// true of.
type Traits struct {
	// Quiet is set where nothing the kind is known by is sure to be there:
	// a chest somebody has opened no longer says what it was. A finished
	// site with nothing found is then no disagreement with the world.
	Quiet bool
	// DropEmpty is set where a finished site with nothing found is not
	// offered at all, because such a site is empty more often than not.
	DropEmpty bool
	// FinishedOnly is set for a kind that is offered only in finished
	// chunks, where the world can be asked: one with a site every few
	// chunks, or one that shares its sites with other kinds, so that
	// before the biome is known each site would be a mark for every one
	// of them.
	FinishedOnly bool
}

// A predictor that says more of its kind than every kind has to.
type traited interface{ Traits() Traits }

func traitsOf(p Predictor) Traits {
	if t, ok := p.(traited); ok {
		return t.Traits()
	}
	return Traits{}
}

// whole is a kind the game places with all 64 bits of the world seed,
// which Sites, taking the low half, is then never asked for.
type whole interface {
	WholeSites(seed int64, area Area, limit int) (sites []Site, more int)
}

// dense is a kind whose sites are asked for a region at a time.
type dense interface {
	// Region is the side of its regions, in chunks.
	Region() int32
	SiteIn(seed uint32, regionX, regionZ int32) Site
}

// Predictors is every kind there is a predictor for. Each is served only
// while the world's own records of that kind bear its rule out.
var Predictors = []Predictor{fortress{}, monument{}, outpost{}, villageSite{}, witchHut{}, desertPyramid{}, jungleTemple{}, igloo{},
	bastion{}, endCity{}, ruinedPortal{chunks.Overworld}, ruinedPortal{chunks.Nether},
	mansion{}, trialChamber{}, trailRuins{}, oceanRuins{}, buriedTreasure{}}

// The game's ids for the biomes a kind is only built in.
const (
	biomeOcean           = 0
	biomePlains          = 1
	biomeDesert          = 2
	biomeTaiga           = 5
	biomeSwamp           = 6
	biomeSnowyPlains     = 12
	biomeDesertHills     = 17
	biomeJungle          = 21
	biomeJungleHills     = 22
	biomeTheEnd          = 9
	biomeLegacyFrozen    = 10
	biomeMushroomShore   = 15
	biomeBeach           = 16
	biomeDeepOcean       = 24
	biomeStonyShore      = 25
	biomeSnowyBeach      = 26
	biomeDarkForest      = 29
	biomeSnowyTaiga      = 30
	biomeOldPineTaiga    = 32
	biomeSavanna         = 35
	biomeWarmOcean       = 40
	biomeDeepWarmOcean   = 41
	biomeLukewarmOcean   = 42
	biomeDeepLukewarm    = 43
	biomeColdOcean       = 44
	biomeDeepColdOcean   = 45
	biomeFrozenOcean     = 46
	biomeDeepFrozenOcean = 47
	biomeSunflowerPlains = 129
	biomeOldBirchForest  = 155
	biomeDarkForestHills = 157
	biomeOldSpruceTaiga  = 160
	biomeBasaltDeltas    = 181
	biomeJaggedPeaks     = 182
	biomeFrozenPeaks     = 183
	biomeSnowySlopes     = 184
	biomeGrove           = 185
	biomeMeadow          = 186
	biomeStonyPeaks      = 189
	biomeCherryGrove     = 192
	biomePaleGarden      = 193
)

func oneOf(ids ...uint32) map[uint32]bool {
	set := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// spread is how the generator scatters a kind: the world is cut into
// regions of spacing chunks a side, and each region gets one site, at an
// offset below spacing minus separation on each axis.
//
// The offset comes from a Mersenne Twister seeded with the region's
// position, the low 32 bits of the world seed and a number of the kind's
// own. A triangular kind averages two draws per axis, which bunches its
// sites towards the middle of the range.
type spread struct {
	spacing, separation int32
	salt                uint32
	triangular          bool
}

const (
	regionFactorX = 2570712328 // 341873128712 mod 2^32
	regionFactorZ = 4048968661 // 132897987541 mod 2^32
)

func floorDiv(a, b int32) int32 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// site is a region's site, and the generator in the state the game leaves
// it in, for a kind that draws again to decide what the site becomes.
func (s spread) site(seed uint32, regionX, regionZ int32) (Site, *twister) {
	rng := newTwister(uint32(regionX)*regionFactorX + uint32(regionZ)*regionFactorZ + seed + s.salt)
	span := uint32(s.spacing - s.separation)
	// Each draw advances the generator, so the order they are taken in is
	// part of the rule: both of x's before either of z's.
	x := rng.next() % span
	if s.triangular {
		second := rng.next() % span
		x = (x + second) / 2
	}
	z := rng.next() % span
	if s.triangular {
		second := rng.next() % span
		z = (z + second) / 2
	}
	return Site{regionX*s.spacing + int32(x), regionZ*s.spacing + int32(z)}, rng
}

// maxRegions bounds one walk, whatever area it is handed. The survey keeps
// its areas well inside this, so that the bound never decides which
// regions are looked at.
const maxRegions = 1 << 16

// sites walks the regions touching area, up to maxRegions of them. keep
// decides, from the generator state after the offset, whether the region's
// site is one of this kind. more is how many sites past limit were found,
// which falls short of all there are only where the walk was cut off.
func (s spread) sites(seed uint32, area Area, limit int, keep func(*twister) bool) (sites []Site, more int) {
	walked := 0
	for rx := floorDiv(area.MinX, s.spacing); rx <= floorDiv(area.MaxX, s.spacing); rx++ {
		for rz := floorDiv(area.MinZ, s.spacing); rz <= floorDiv(area.MaxZ, s.spacing); rz++ {
			if walked++; walked > maxRegions {
				return sites, more
			}
			site, rng := s.site(seed, rx, rz)
			if site.ChunkX < area.MinX || site.ChunkX > area.MaxX || site.ChunkZ < area.MinZ || site.ChunkZ > area.MaxZ {
				continue
			}
			if keep != nil && !keep(rng) {
				continue
			}
			if len(sites) >= limit {
				more++
				continue
			}
			sites = append(sites, site)
		}
	}
	return sites, more
}

// fortress: the nether's regions are shared by fortresses and bastion
// remnants. A third draw decides which a region gets, and a fortress is
// built whatever the biome.
type fortress struct{}

var fortressSpread = spread{spacing: 30, separation: 4, salt: 30084232}

func (fortress) Kind() Kind                  { return Fortress }
func (fortress) Dimension() chunks.Dimension { return chunks.Nether }
func (fortress) Exact() bool                 { return false }
func (fortress) Certain() bool               { return true }
func (fortress) Allows(uint32) bool          { return true }
func (fortress) Founded() bool               { return false }

func (fortress) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return fortressSpread.sites(seed, area, limit, func(rng *twister) bool { return rng.next()%6 < 2 })
}

// fortressReach is how far from its start a fortress's recorded areas have
// been found, in blocks. A fortress grows outward from the site in a shape
// of its own, so all this can ask is that the site is in or near the box,
// and a wrong seed's site would be that about one time in four.
const fortressReach = 160

func (fortress) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16+8, site.ChunkZ*16+8
	return x >= real.MinX-fortressReach && x <= real.MaxX+fortressReach &&
		z >= real.MinZ-fortressReach && z <= real.MaxZ+fortressReach
}

func (fortress) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// monument: 58 blocks square, centred on the middle of its site's chunk,
// and only in a deep ocean. The game also wants water all round the site,
// which is not asked here: where the country round a site is only part
// generated there is nothing to ask it of.
type monument struct{}

var (
	monumentSpread = spread{spacing: 32, separation: 5, salt: 10387313, triangular: true}
	monumentBiomes = oneOf(biomeDeepOcean, biomeDeepWarmOcean, biomeDeepLukewarm, biomeDeepColdOcean, biomeDeepFrozenOcean)
)

func (monument) Kind() Kind                  { return Monument }
func (monument) Dimension() chunks.Dimension { return chunks.Overworld }
func (monument) Exact() bool                 { return true }
func (monument) Certain() bool               { return false }
func (monument) Allows(biome uint32) bool    { return monumentBiomes[biome] }
func (monument) Founded() bool               { return false }

func (monument) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return monumentSpread.sites(seed, area, limit, nil)
}

// A monument only part generated is recorded as the part there is, so the
// test is that the box lies inside the footprint and holds its centre.
func (monument) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16+8, site.ChunkZ*16+8
	return real.within(x-29, z-29, x+28, z+28) && real.MinX <= x && real.MaxX >= x && real.MinZ <= z && real.MaxZ >= z
}

func (monument) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// outpost: the watchtower's area is 16 blocks square with one corner on the
// site chunk's first block; which corner depends on the way it is turned.
// The tents and cages round it are recorded too by a newer game, as far as
// outpostReach from that block.
//
// Its biomes are the game's list for it. The FWB world has outposts in
// plains, snowy plains, desert and meadow, and no generated site in the
// others.
type outpost struct{}

var (
	outpostSpread = spread{spacing: 80, separation: 24, salt: 165745296, triangular: true}
	outpostBiomes = oneOf(biomePlains, biomeSunflowerPlains, biomeDesert, biomeSavanna, biomeTaiga, biomeSnowyPlains,
		biomeMeadow, biomeGrove, biomeSnowySlopes, biomeJaggedPeaks, biomeFrozenPeaks, biomeStonyPeaks, biomeCherryGrove)
)

func (outpost) Kind() Kind                  { return Outpost }
func (outpost) Dimension() chunks.Dimension { return chunks.Overworld }
func (outpost) Exact() bool                 { return true }
func (outpost) Certain() bool               { return false }
func (outpost) Allows(biome uint32) bool    { return outpostBiomes[biome] }
func (outpost) Founded() bool               { return false }

func (outpost) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return outpostSpread.sites(seed, area, limit, nil)
}

// outpostReach is how far from the tower's corner an outpost's recorded
// boxes have been found, in blocks: 58 in the FWB world. A wrong seed's
// site is inside one outpost's box in a hundred.
const outpostReach = 80

func (outpost) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x-outpostReach, z-outpostReach, x+outpostReach, z+outpostReach) && real.MinX <= x && real.MaxX >= x && real.MinZ <= z && real.MaxZ >= z
}

func (outpost) Centre(site Site) (int32, int32) { return site.ChunkX * 16, site.ChunkZ * 16 }

// villageSite: the game keeps a record for a village it is running, whose box
// grows and shrinks with the beds and bells its villagers claim, and makes
// one for any bed a villager claims anywhere. So a generated village's box
// is only somewhere about its site, a village players founded has no site
// at all, and one nobody has been near has no record.
type villageSite struct{}

var (
	villageSpread = spread{spacing: 34, separation: 8, salt: 10387312, triangular: true}
	// Every biome a village on a site has been recorded in. The game's
	// list has snowy taiga as well, where this world has three generated
	// sites and no village.
	villageBiomes = oneOf(biomePlains, biomeSunflowerPlains, biomeDesert, biomeSavanna, biomeTaiga, biomeSnowyPlains, biomeMeadow)
)

// villageReach is how far outside a village's recorded box, in blocks, the
// middle of its site's chunk may be. A wrong seed's site is that near one
// village in eleven.
const villageReach = 16

func (villageSite) Kind() Kind                  { return Village }
func (villageSite) Dimension() chunks.Dimension { return chunks.Overworld }
func (villageSite) Exact() bool                 { return false }
func (villageSite) Certain() bool               { return false }
func (villageSite) Allows(biome uint32) bool    { return villageBiomes[biome] }
func (villageSite) Founded() bool               { return true }

func (villageSite) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return villageSpread.sites(seed, area, limit, nil)
}

func (villageSite) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16+8, site.ChunkZ*16+8
	return x >= real.MinX-villageReach && x <= real.MaxX+villageReach &&
		z >= real.MinZ-villageReach && z <= real.MaxZ+villageReach
}

func (villageSite) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// witchHut: the site is shared with desert and jungle temples and igloos,
// and the biome picks which of them, if any, is built. A hut lies inside
// the site's chunk, in a swamp.
type witchHut struct{}

var witchHutSpread = spread{spacing: 32, separation: 8, salt: 14357617}

func (witchHut) Kind() Kind                  { return WitchHut }
func (witchHut) Dimension() chunks.Dimension { return chunks.Overworld }
func (witchHut) Exact() bool                 { return true }
func (witchHut) Certain() bool               { return false }
func (witchHut) Allows(biome uint32) bool    { return biome == biomeSwamp }
func (witchHut) Founded() bool               { return false }

func (witchHut) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return witchHutSpread.sites(seed, area, limit, nil)
}

func (witchHut) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x, z, x+15, z+15)
}

func (witchHut) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// The witch hut's sites are also those of desert pyramids, jungle temples
// and igloos: the biome under a site says which of the four is built, and
// each is built from the first block of the site's chunk.

// desertPyramid: 21 blocks square, so it runs into the chunks beyond.
type desertPyramid struct{}

func (desertPyramid) Kind() Kind                  { return DesertPyramid }
func (desertPyramid) Dimension() chunks.Dimension { return chunks.Overworld }
func (desertPyramid) Exact() bool                 { return true }
func (desertPyramid) Certain() bool               { return false }
func (desertPyramid) Allows(biome uint32) bool {
	return biome == biomeDesert || biome == biomeDesertHills
}
func (desertPyramid) Founded() bool  { return false }
func (desertPyramid) Traits() Traits { return Traits{Quiet: true, FinishedOnly: true} }

func (desertPyramid) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return witchHutSpread.sites(seed, area, limit, nil)
}

func (desertPyramid) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x, z, x+20, z+20)
}

func (desertPyramid) Centre(site Site) (int32, int32) {
	return site.ChunkX*16 + 10, site.ChunkZ*16 + 10
}

// jungleTemple: 12 blocks by 15, inside the site's chunk.
type jungleTemple struct{}

func (jungleTemple) Kind() Kind                  { return JungleTemple }
func (jungleTemple) Dimension() chunks.Dimension { return chunks.Overworld }
func (jungleTemple) Exact() bool                 { return true }
func (jungleTemple) Certain() bool               { return false }
func (jungleTemple) Allows(biome uint32) bool {
	return biome == biomeJungle || biome == biomeJungleHills
}
func (jungleTemple) Founded() bool  { return false }
func (jungleTemple) Traits() Traits { return Traits{Quiet: true, FinishedOnly: true} }

func (jungleTemple) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return witchHutSpread.sites(seed, area, limit, nil)
}

func (jungleTemple) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x, z, x+15, z+15)
}

func (jungleTemple) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// igloo: 7 blocks by 8 inside the site's chunk. One in two has a basement
// under it, which is where its chest is and which reaches a little past
// the chunk.
type igloo struct{}

var iglooBiomes = oneOf(biomeSnowyPlains, biomeSnowyTaiga, biomeSnowySlopes)

// iglooReach is how far outside the site's chunk an igloo's basement has
// been found, in blocks.
const iglooReach = 8

func (igloo) Kind() Kind                  { return Igloo }
func (igloo) Dimension() chunks.Dimension { return chunks.Overworld }
func (igloo) Exact() bool                 { return true }
func (igloo) Certain() bool               { return false }
func (igloo) Allows(biome uint32) bool    { return iglooBiomes[biome] }
func (igloo) Founded() bool               { return false }
func (igloo) Traits() Traits              { return Traits{Quiet: true, FinishedOnly: true} }

func (igloo) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return witchHutSpread.sites(seed, area, limit, nil)
}

func (igloo) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x-iglooReach, z-iglooReach, x+15+iglooReach, z+15+iglooReach)
}

func (igloo) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 4, site.ChunkZ*16 + 4 }

// bastion: the nether's regions are the fortress's, and the draw that
// gives a region no fortress gives it a bastion, four times in six. One is
// not built in basalt deltas. It is known by its chests, which are found
// within bastionReach of the middle of its site's chunk.
type bastion struct{}

// bastionReach is how far from the middle of its site's chunk a bastion's
// chests have been found, in blocks: 32 in the FWB world, and looking
// half as far again finds no more. A wrong seed's site is that near one
// bastion in sixteen.
const bastionReach = 64

func (bastion) Kind() Kind                  { return Bastion }
func (bastion) Dimension() chunks.Dimension { return chunks.Nether }
func (bastion) Exact() bool                 { return false }
func (bastion) Certain() bool               { return false }
func (bastion) Allows(biome uint32) bool    { return biome != biomeBasaltDeltas }
func (bastion) Founded() bool               { return false }
func (bastion) Traits() Traits              { return Traits{Quiet: true} }

func (bastion) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return fortressSpread.sites(seed, area, limit, func(rng *twister) bool { return rng.next()%6 >= 2 })
}

// near reports whether the middle of a site's chunk is within reach blocks
// of a box, across the map.
func near(site Site, real Box, reach int32) bool {
	x, z := site.ChunkX*16+8, site.ChunkZ*16+8
	return x >= real.MinX-reach && x <= real.MaxX+reach && z >= real.MinZ-reach && z <= real.MaxZ+reach
}

func (bastion) Explains(site Site, real Box) bool { return near(site, real, bastionReach) }
func (bastion) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// endCity: the tower a city grows from stands on the middle of its site's
// chunk, and only where the End's outer islands give it ground to stand
// on, which nothing here can ask until the chunk is generated. Every city
// found in the FWB world has a chest or a shulker within four blocks of
// that middle.
type endCity struct{}

var endCitySpread = spread{spacing: 20, separation: 11, salt: 10387313, triangular: true}

// cityNear is how far from the middle of its site's chunk the nearest of a
// city's chests and shulkers may be.
const cityNear = 24

func (endCity) Kind() Kind                  { return EndCity }
func (endCity) Dimension() chunks.Dimension { return chunks.End }
func (endCity) Exact() bool                 { return false }
func (endCity) Certain() bool               { return false }
func (endCity) Allows(biome uint32) bool    { return biome == biomeTheEnd }
func (endCity) Founded() bool               { return false }
func (endCity) Traits() Traits              { return Traits{Quiet: true} }

func (endCity) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return endCitySpread.sites(seed, area, limit, nil)
}

func (endCity) Explains(site Site, real Box) bool { return near(site, real, cityNear) }
func (endCity) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// ruinedPortal: one rule in the overworld and another, with smaller
// regions, in the Nether. A portal is built whatever the biome, and is
// known by its one chest, which is within portalReach of the middle of its
// site's chunk. Four sites in five in finished chunks hold such a chest;
// in the overworld as many portals again are at no site, placed by a rule
// that was not found, and those are known by their chests alone.
type ruinedPortal struct{ in chunks.Dimension }

var (
	portalSpread       = spread{spacing: 40, separation: 15, salt: 40552231}
	netherPortalSpread = spread{spacing: 25, separation: 10, salt: 40552231}
)

// portalReach is how far from the middle of its site's chunk a portal's
// chest has been found, in blocks: 24 in the FWB world.
const portalReach = 24

func (p ruinedPortal) Kind() Kind                  { return RuinedPortal }
func (p ruinedPortal) Dimension() chunks.Dimension { return p.in }
func (ruinedPortal) Exact() bool                   { return false }
func (ruinedPortal) Certain() bool                 { return true }
func (ruinedPortal) Allows(uint32) bool            { return true }
func (ruinedPortal) Founded() bool                 { return false }

// A portal is slight and common: it is offered where the world can be
// asked, and not as hundreds of marks over country nobody has seen.
func (ruinedPortal) Traits() Traits { return Traits{Quiet: true, FinishedOnly: true} }

func (p ruinedPortal) spread() spread {
	if p.in == chunks.Nether {
		return netherPortalSpread
	}
	return portalSpread
}

func (p ruinedPortal) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return p.spread().sites(seed, area, limit, nil)
}

func (ruinedPortal) Explains(site Site, real Box) bool { return near(site, real, portalReach) }
func (ruinedPortal) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// mansion: a woodland mansion is built on the middle of its site's chunk,
// in a dark forest or a pale garden. Its regions are the outpost's size,
// and it is rare enough that a world may hold none: the rule is then
// checked against where the world's woodland explorer maps point, which
// is the game's own working out of the same thing.
type mansion struct{}

var (
	mansionSpread = spread{spacing: 80, separation: 20, salt: 10387319, triangular: true}
	mansionBiomes = oneOf(biomeDarkForest, biomeDarkForestHills, biomePaleGarden)
)

// mansionReach is how far from the middle of its site's chunk a mansion's
// chests may be, in blocks: it is some 60 blocks across.
const mansionReach = 64

func (mansion) Kind() Kind                  { return Mansion }
func (mansion) Dimension() chunks.Dimension { return chunks.Overworld }
func (mansion) Exact() bool                 { return false }
func (mansion) Certain() bool               { return false }
func (mansion) Allows(biome uint32) bool    { return mansionBiomes[biome] }
func (mansion) Founded() bool               { return false }

// A site is offered where a map points or a finished chunk shows a dark
// forest, and nowhere else: of the sites in country not generated the
// biome will allow one in twenty.
func (mansion) Traits() Traits { return Traits{Quiet: true, FinishedOnly: true} }

func (mansion) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return mansionSpread.sites(seed, area, limit, nil)
}

func (mansion) Explains(site Site, real Box) bool { return near(site, real, mansionReach) }
func (mansion) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// The kinds the game builds from data files are not placed as the older
// ones are. Their regions are the same squares, but the offset is drawn
// from Java's generator, a 48-bit one, seeded with the region, a number
// per kind and all 64 bits of the world seed: the game places these where
// Java Edition does. Under the older kinds' generator neither a trial
// chamber nor a trail ruin is at a site more often than a wrong seed puts
// one there.
type javaSpread struct {
	spacing, separation int32
	salt                int64
}

const (
	javaRegionX    = 341873128712
	javaRegionZ    = 132897987541
	javaMultiplier = 0x5DEECE66D
	javaMask       = 1<<48 - 1
)

// javaRandom is java.util.Random.
type javaRandom struct{ state uint64 }

func (r *javaRandom) next(bits uint) int32 {
	r.state = (r.state*javaMultiplier + 0xB) & javaMask
	return int32(int64(r.state) >> (48 - bits))
}

// below is nextInt(bound), for a bound above nought.
func (r *javaRandom) below(bound int32) int32 {
	if bound&-bound == bound {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := r.next(31)
		if value := bits % bound; bits-value+(bound-1) >= 0 {
			return value
		}
	}
}

func (s javaSpread) site(seed int64, regionX, regionZ int32) Site {
	rng := javaRandom{(uint64(int64(regionX)*javaRegionX+int64(regionZ)*javaRegionZ+seed+s.salt) ^ javaMultiplier) & javaMask}
	// x is drawn before z.
	x := rng.below(s.spacing - s.separation)
	z := rng.below(s.spacing - s.separation)
	return Site{regionX*s.spacing + x, regionZ*s.spacing + z}
}

func (s javaSpread) sites(seed int64, area Area, limit int) (sites []Site, more int) {
	walked := 0
	for rx := floorDiv(area.MinX, s.spacing); rx <= floorDiv(area.MaxX, s.spacing); rx++ {
		for rz := floorDiv(area.MinZ, s.spacing); rz <= floorDiv(area.MaxZ, s.spacing); rz++ {
			if walked++; walked > maxRegions {
				return sites, more
			}
			site := s.site(seed, rx, rz)
			if site.ChunkX < area.MinX || site.ChunkX > area.MaxX || site.ChunkZ < area.MinZ || site.ChunkZ > area.MaxZ {
				continue
			}
			if len(sites) >= limit {
				more++
				continue
			}
			sites = append(sites, site)
		}
	}
	return sites, more
}

// trialChamber: one to a square of the grid its blocks are joined by,
// whatever the biome above. A chamber's spawners cannot be taken away, so
// a finished site with none has no chamber: a chunk generated before the
// game had chambers, or the deep dark, which has none. Such a site is not
// offered. Nine sites in ten in chunks the current game generated hold
// one.
type trialChamber struct{}

var chamberSpread = javaSpread{spacing: chamberGrid, separation: 12, salt: 94251327}

// chamberNear is how far from the middle of its site's chunk the nearest
// of a chamber's spawners and vaults may be, in blocks.
const chamberNear = 32

func (trialChamber) Kind() Kind                  { return TrialChamber }
func (trialChamber) Dimension() chunks.Dimension { return chunks.Overworld }
func (trialChamber) Exact() bool                 { return false }
func (trialChamber) Certain() bool               { return true }
func (trialChamber) Allows(uint32) bool          { return true }
func (trialChamber) Founded() bool               { return false }
func (trialChamber) Traits() Traits              { return Traits{Quiet: true, DropEmpty: true} }

// The low half of the seed is not enough to place one.
func (trialChamber) Sites(uint32, Area, int) ([]Site, int) { return nil, 0 }

func (trialChamber) WholeSites(seed int64, area Area, limit int) ([]Site, int) {
	return chamberSpread.sites(seed, area, limit)
}

func (trialChamber) Explains(site Site, real Box) bool { return near(site, real, chamberNear) }
func (trialChamber) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// trailRuins: in the taigas, the old growth birch forest and the jungle.
// The current game places them so; the chunks of the FWB world's older
// seed hold ruins the rule does not explain, and it is set aside there.
type trailRuins struct{}

var (
	trailSpread = javaSpread{spacing: 34, separation: 8, salt: 83469867}
	trailBiomes = oneOf(biomeTaiga, biomeSnowyTaiga, biomeOldPineTaiga, biomeOldSpruceTaiga, biomeOldBirchForest, biomeJungle)
)

// trailReach is how far from the middle of its site's chunk a ruin's
// recorded box, or what it was found by, may be, in blocks.
const trailReach = 32

func (trailRuins) Kind() Kind                  { return TrailRuins }
func (trailRuins) Dimension() chunks.Dimension { return chunks.Overworld }
func (trailRuins) Exact() bool                 { return false }
func (trailRuins) Certain() bool               { return false }
func (trailRuins) Allows(biome uint32) bool    { return trailBiomes[biome] }
func (trailRuins) Founded() bool               { return false }
func (trailRuins) Traits() Traits              { return Traits{Quiet: true, FinishedOnly: true} }

func (trailRuins) Sites(uint32, Area, int) ([]Site, int) { return nil, 0 }

func (trailRuins) WholeSites(seed int64, area Area, limit int) ([]Site, int) {
	return trailSpread.sites(seed, area, limit)
}

func (trailRuins) Explains(site Site, real Box) bool { return near(site, real, trailReach) }
func (trailRuins) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// oceanRuins: a cluster of ruins round the middle of its site's chunk, in
// any ocean, known by its chests and by the suspicious sand and gravel
// nobody has brushed. Nearly every finished site in an ocean holds one.
// It is slight and common, and is offered only in finished chunks.
type oceanRuins struct{}

var (
	ruinsSpread = spread{spacing: 20, separation: 8, salt: 14357621}
	oceanBiomes = oneOf(biomeOcean, biomeLegacyFrozen, biomeDeepOcean, biomeWarmOcean, biomeDeepWarmOcean, biomeLukewarmOcean, biomeDeepLukewarm,
		biomeColdOcean, biomeDeepColdOcean, biomeFrozenOcean, biomeDeepFrozenOcean)
)

// ruinsReach is how far from the middle of its site's chunk the nearest
// of a ruin's chests and suspicious blocks has been found: 8 blocks in
// the FWB world.
const ruinsReach = 24

func (oceanRuins) Kind() Kind                  { return OceanRuins }
func (oceanRuins) Dimension() chunks.Dimension { return chunks.Overworld }
func (oceanRuins) Exact() bool                 { return false }
func (oceanRuins) Certain() bool               { return false }
func (oceanRuins) Allows(biome uint32) bool    { return oceanBiomes[biome] }
func (oceanRuins) Founded() bool               { return false }
func (oceanRuins) Traits() Traits              { return Traits{Quiet: true, FinishedOnly: true} }

func (oceanRuins) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return ruinsSpread.sites(seed, area, limit, nil)
}

func (oceanRuins) Explains(site Site, real Box) bool { return near(site, real, ruinsReach) }
func (oceanRuins) Centre(site Site) (int32, int32)   { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }

// buriedTreasure: one chest, on block 8, 8 of its site's chunk, under a
// beach. A site is one chunk in sixteen, so before the biome is known it
// says next to nothing: it is asked for only in the regions the world has
// chunks in, and offered only in finished chunks.
type buriedTreasure struct{}

var (
	treasureSpread = spread{spacing: 4, separation: 2, salt: 16842397, triangular: true}
	treasureBiomes = oneOf(biomeBeach, biomeSnowyBeach, biomeStonyShore, biomeMushroomShore)
)

func (buriedTreasure) Kind() Kind                  { return BuriedTreasure }
func (buriedTreasure) Dimension() chunks.Dimension { return chunks.Overworld }
func (buriedTreasure) Exact() bool                 { return false }
func (buriedTreasure) Certain() bool               { return false }
func (buriedTreasure) Allows(biome uint32) bool    { return treasureBiomes[biome] }
func (buriedTreasure) Founded() bool               { return false }
func (buriedTreasure) Traits() Traits              { return Traits{Quiet: true, FinishedOnly: true} }
func (buriedTreasure) Region() int32               { return treasureSpread.spacing }

func (buriedTreasure) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return treasureSpread.sites(seed, area, limit, nil)
}

func (buriedTreasure) SiteIn(seed uint32, regionX, regionZ int32) Site {
	site, _ := treasureSpread.site(seed, regionX, regionZ)
	return site
}

func (buriedTreasure) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x, z, x+15, z+15)
}

func (buriedTreasure) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }
