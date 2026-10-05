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
	// nothing here can say what the biome of an unvisited chunk will be.
	Certain() bool
	// Centre is the block a site is drawn at.
	Centre(site Site) (x, z int32)
}

// Predictors is every kind there is a predictor for. Only those that are
// Certain reach the page; the others are what the seed is checked with.
var Predictors = []Predictor{fortress{}, monument{}, outpost{}, witchHut{}}

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

// monument: 58 blocks square, centred on the middle of its site's chunk.
type monument struct{}

var monumentSpread = spread{spacing: 32, separation: 5, salt: 10387313, triangular: true}

func (monument) Kind() Kind                  { return Monument }
func (monument) Dimension() chunks.Dimension { return chunks.Overworld }
func (monument) Exact() bool                 { return true }
func (monument) Certain() bool               { return false }

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
type outpost struct{}

var outpostSpread = spread{spacing: 80, separation: 24, salt: 165745296, triangular: true}

func (outpost) Kind() Kind                  { return Outpost }
func (outpost) Dimension() chunks.Dimension { return chunks.Overworld }
func (outpost) Exact() bool                 { return true }
func (outpost) Certain() bool               { return false }

func (outpost) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return outpostSpread.sites(seed, area, limit, nil)
}

func (outpost) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x-15, z-15, x+15, z+15) && real.MinX <= x && real.MaxX >= x && real.MinZ <= z && real.MaxZ >= z
}

func (outpost) Centre(site Site) (int32, int32) { return site.ChunkX * 16, site.ChunkZ * 16 }

// witchHut: the site is shared with desert and jungle temples and igloos,
// and the biome picks which of them, if any, is built. A hut lies inside
// the site's chunk.
type witchHut struct{}

var witchHutSpread = spread{spacing: 32, separation: 8, salt: 14357617}

func (witchHut) Kind() Kind                  { return WitchHut }
func (witchHut) Dimension() chunks.Dimension { return chunks.Overworld }
func (witchHut) Exact() bool                 { return true }
func (witchHut) Certain() bool               { return false }

func (witchHut) Sites(seed uint32, area Area, limit int) ([]Site, int) {
	return witchHutSpread.sites(seed, area, limit, nil)
}

func (witchHut) Explains(site Site, real Box) bool {
	x, z := site.ChunkX*16, site.ChunkZ*16
	return real.within(x, z, x+15, z+15)
}

func (witchHut) Centre(site Site) (int32, int32) { return site.ChunkX*16 + 8, site.ChunkZ*16 + 8 }
