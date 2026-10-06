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

// Predictors is every kind there is a predictor for. Each is served only
// while the world's own records of that kind bear its rule out.
var Predictors = []Predictor{fortress{}, monument{}, outpost{}, villageSite{}, witchHut{}}

// The game's ids for the biomes a kind is only built in.
const (
	biomePlains          = 1
	biomeDesert          = 2
	biomeTaiga           = 5
	biomeSwamp           = 6
	biomeSnowyPlains     = 12
	biomeDeepOcean       = 24
	biomeSavanna         = 35
	biomeDeepWarmOcean   = 41
	biomeDeepLukewarm    = 43
	biomeDeepColdOcean   = 45
	biomeDeepFrozenOcean = 47
	biomeSunflowerPlains = 129
	biomeJaggedPeaks     = 182
	biomeFrozenPeaks     = 183
	biomeSnowySlopes     = 184
	biomeGrove           = 185
	biomeMeadow          = 186
	biomeStonyPeaks      = 189
	biomeCherryGrove     = 192
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

func (outpost) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x-15, z-15, x+15, z+15) && real.MinX <= x && real.MaxX >= x && real.MinZ <= z && real.MaxZ >= z
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
