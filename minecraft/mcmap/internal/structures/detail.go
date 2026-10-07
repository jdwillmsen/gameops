package structures

import (
	"cmp"
	"context"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
)

// Bounds on what is said of one structure, and of all of them together. A
// fortress in the FWB world holds four spawners and a village 58 villagers;
// these are what keep a world that is all spawners, or all name tags, from
// becoming an answer of that size.
const (
	// MaxDetailKinds is how many types of mob one structure's are counted
	// by; the rest are counted together.
	MaxDetailKinds = 40
	// MaxDetailNamed and MaxDetailSpawners are how many named mobs and
	// spawners one structure lists; the rest are counted.
	MaxDetailNamed    = 20
	MaxDetailSpawners = 64
	// maxDetailNames and maxDetailPlaces are how many of each a whole
	// survey lists.
	maxDetailNames  = 5_000
	maxDetailPlaces = 20_000
	// cellShift makes the squares mobs and block entities are sorted
	// into, 64 blocks a side, so that a structure looks only at what is
	// near it.
	cellShift = 6
)

// MobCount is how many mobs of one type the save holds in a structure.
type MobCount struct {
	// Kind is the mob's type without the minecraft: prefix.
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	// Babies and Captains are how many of them are not grown, and how
	// many are raid captains.
	Babies   int `json:"babies,omitempty"`
	Captains int `json:"captains,omitempty"`
}

// NamedMob is one mob with a name tag. The name is cleaned and cut, and is
// still a player's text: nothing may treat it as markup.
type NamedMob struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Baby bool   `json:"baby,omitempty"`
	// Profession and Level are a villager's, where its record has them.
	Profession string `json:"profession,omitempty"`
	Level      int    `json:"level,omitempty"`
}

// SpawnerCount is how many spawners of one mob a structure holds.
type SpawnerCount struct {
	// Mob is the type spawned, or "unknown" where the record names none.
	Mob   string `json:"mob"`
	Count int    `json:"count"`
	// Trial is set for trial spawners, which are counted apart.
	Trial bool `json:"trial,omitempty"`
}

// Spawner is one spawner and the block it is.
type Spawner struct {
	Mob   string `json:"mob"`
	X     int32  `json:"x"`
	Y     int32  `json:"y"`
	Z     int32  `json:"z"`
	Trial bool   `json:"trial,omitempty"`
}

// ContainerCount is the containers of one kind in a structure, by what
// their records say is in them. What that is, is never read.
type ContainerCount struct {
	// Kind is chest, barrel, shulker, dispenser, dropper or pot.
	Kind string `json:"kind"`
	// Unopened still carry the loot table they were generated with: the
	// game rolls it the first time one is opened, so nothing has been.
	Unopened int `json:"unopened"`
	// Holding have something in them and Empty nothing. Either was opened
	// once, or was put there by a player; the record does not say which.
	Holding int `json:"holding"`
	Empty   int `json:"empty"`
}

// Detail is what the world's save holds inside one structure's box: the
// mobs it has saved there, and the block entities that say something of
// the place. It is as old as the snapshot it was read from.
type Detail struct {
	// MobsTotal is every saved mob in the box. Mobs is them by type, the
	// most first, and MobKindsMore how many types were left out of it.
	MobsTotal    int        `json:"mobsTotal"`
	Mobs         []MobCount `json:"mobs"`
	MobKindsMore int        `json:"mobKindsMore,omitempty"`
	Named        []NamedMob `json:"named"`
	NamedMore    int        `json:"namedMore,omitempty"`
	// SpawnerCounts is every spawner by what it spawns; Spawners lists
	// them, and SpawnersMore is how many it left out.
	SpawnerCounts []SpawnerCount   `json:"spawnerCounts"`
	Spawners      []Spawner        `json:"spawners"`
	SpawnersMore  int              `json:"spawnersMore,omitempty"`
	Containers    []ContainerCount `json:"containers"`
	// Blocks counts the other block entities that say something of a
	// structure: cauldron, bell, vault, ominous_vault, end_portal. One
	// with none is left out.
	Blocks map[string]int `json:"blocks,omitempty"`
	// Elders is, for a monument, how many elder guardians the save holds
	// in it. One is generated with three and the game never adds another.
	Elders *int `json:"elders,omitempty"`
	// Village is set for a village the game has counted.
	Village *VillageDetail `json:"village,omitempty"`
}

// Profession is the grown villagers of one profession.
type Profession struct {
	// Profession is the game's word, or empty for those whose records
	// name none: the unemployed and the nitwits, which are not told apart.
	Profession string `json:"profession"`
	Count      int    `json:"count"`
	// Levels is how many are at each trade level, novice to master. It is
	// all noughts for those with no profession.
	Levels [5]int `json:"levels"`
}

// RaidFacts is what the game last wrote of a village's raid. The record
// outlives the raid, so this is how far one got and not that one is on.
type RaidFacts struct {
	Wave    int `json:"wave"`
	Waves   int `json:"waves"`
	Raiders int `json:"raiders"`
	// IdleSeconds is how long before the snapshot the game last ran the
	// raid, in seconds of game time; left out where that is not known.
	IdleSeconds *int64 `json:"idleSeconds,omitempty"`
}

// VillageDetail is what a village's own records say beyond its counts,
// with its villagers looked up among the mobs the save holds.
type VillageDetail struct {
	// Professions is the grown villagers found in the save, by profession.
	Professions []Profession `json:"professions"`
	Babies      int          `json:"babies"`
	// Missing is villagers the village lists and the save has no record
	// of; NotLookedUp is those past the bound, which were not looked for.
	Missing     int `json:"missing"`
	NotLookedUp int `json:"notLookedUp,omitempty"`
	// Golems and Cats are how many of each the village lists that the
	// save still holds.
	Golems int `json:"golems"`
	Cats   int `json:"cats"`
	// JobSites is the claimed job sites by the profession each gives.
	JobSites []JobSites `json:"jobSites"`
	// IdleSeconds is how long before the snapshot the game last ran the
	// village, in seconds of game time; left out where that is not known.
	IdleSeconds *int64 `json:"idleSeconds,omitempty"`
	// Raid is left out for a village with no raid record.
	Raid *RaidFacts `json:"raid,omitempty"`
	// Met is how many players the village has a standing for.
	Met int `json:"met"`
	// Standings is what the village thinks of each of them. It is never
	// part of an answer: a standing is sent only to the player it is of,
	// by whoever knows which player is asking.
	Standings []Standing `json:"-"`
}

// Standing is what one village thinks of one player: a whole number that
// starts at nought and that the game moves as the player trades with its
// villagers or hurts them.
type Standing struct {
	// Player is the player's UniqueID in the world's own records.
	Player int64
	Value  int32
}

// Standing is what the village thinks of the player with this UniqueID, if
// it has met them.
func (v *VillageDetail) Standing(player int64) (int32, bool) {
	for _, s := range v.Standings {
		if s.Player == player {
			return s.Value, true
		}
	}
	return 0, false
}

// allowance is what one survey may still list across all its structures.
type allowance struct {
	names, places int
}

func cellOf(x, z int32) uint64 { return chunkKey(x>>cellShift, z>>cellShift) }

// describe says what the save holds in each structure of one dimension, in
// the order they are given.
func (c *contents) describe(ctx context.Context, d chunks.Dimension, list []Structure, records map[*VillageFacts]*villageRecords, level leveldat.Level, left *allowance) ([]*Detail, error) {
	mobs, blocks := c.mobs[d], c.blocks[d]
	mobCells, blockCells := map[uint64][]int32{}, map[uint64][]int32{}
	for i, m := range mobs {
		mobCells[cellOf(m.x, m.z)] = append(mobCells[cellOf(m.x, m.z)], int32(i))
	}
	for i, b := range blocks {
		blockCells[cellOf(b.x, b.z)] = append(blockCells[cellOf(b.x, b.z)], int32(i))
	}
	out := make([]*Detail, len(list))
	for i, s := range list {
		// A hostile world can put everything it holds inside every box,
		// and going through one box is then all of it.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var inMobs []savedMob
		var inBlocks []savedBlock
		inside := func(x, y, z int32) bool {
			return x >= s.MinX && x <= s.MaxX && y >= s.MinY && y <= s.MaxY && z >= s.MinZ && z <= s.MaxZ
		}
		// A box is gone through by its squares only while that is the
		// shorter way. Nothing bounds how far a fortress's rooms can be
		// joined, and a box across a whole world is more squares than the
		// world holds things.
		across := int64(s.MaxX>>cellShift) - int64(s.MinX>>cellShift) + 1
		down := int64(s.MaxZ>>cellShift) - int64(s.MinZ>>cellShift) + 1
		if across*down > int64(len(mobs)+len(blocks)) {
			for _, m := range mobs {
				if inside(m.x, m.y, m.z) {
					inMobs = append(inMobs, m)
				}
			}
			for _, b := range blocks {
				if inside(b.x, b.y, b.z) {
					inBlocks = append(inBlocks, b)
				}
			}
		} else {
			for cx := s.MinX >> cellShift; cx <= s.MaxX>>cellShift; cx++ {
				for cz := s.MinZ >> cellShift; cz <= s.MaxZ>>cellShift; cz++ {
					for _, at := range mobCells[chunkKey(cx, cz)] {
						if m := mobs[at]; inside(m.x, m.y, m.z) {
							inMobs = append(inMobs, m)
						}
					}
					for _, at := range blockCells[chunkKey(cx, cz)] {
						if b := blocks[at]; inside(b.x, b.y, b.z) {
							inBlocks = append(inBlocks, b)
						}
					}
				}
			}
		}
		detail := &Detail{}
		c.mobsIn(detail, inMobs, left)
		c.blocksIn(detail, inBlocks, left)
		if s.Kind == Monument {
			n := 0
			for _, m := range inMobs {
				if c.types.names[m.kind] == "elder_guardian" {
					n++
				}
			}
			detail.Elders = &n
		}
		if s.Village != nil && s.Village.Counted {
			if r := records[s.Village]; r != nil {
				detail.Village = c.village(r, level)
			}
		}
		out[i] = detail
	}
	return out, nil
}

// compareNamed puts named mobs in an order that is the same every time.
func compareNamed(a, b NamedMob) int {
	return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Profession, b.Profession), cmp.Compare(a.Level, b.Level))
}

func (c *contents) named(m savedMob) NamedMob {
	n := NamedMob{Kind: c.types.names[m.kind], Name: m.name, Baby: m.baby, Profession: c.types.names[m.profession]}
	if m.profession != 0 && m.tier >= 0 {
		n.Level = int(m.tier) + 1
	}
	return n
}

// keepNamed cuts a list of named mobs to what one structure and the whole
// survey may still list, and returns how many it left out.
func keepNamed(named []NamedMob, left *allowance) ([]NamedMob, int) {
	slices.SortFunc(named, compareNamed)
	keep := min(len(named), MaxDetailNamed, left.names)
	left.names -= keep
	return slices.Clip(named[:keep]), len(named) - keep
}

func (c *contents) mobsIn(detail *Detail, in []savedMob, left *allowance) {
	counts := map[uint16]*MobCount{}
	named := []NamedMob{}
	for _, m := range in {
		n, seen := counts[m.kind]
		if !seen {
			n = &MobCount{Kind: c.types.names[m.kind]}
			counts[m.kind] = n
		}
		n.Count++
		if m.baby {
			n.Babies++
		}
		if m.captain {
			n.Captains++
		}
		if m.name != "" {
			named = append(named, c.named(m))
		}
	}
	detail.MobsTotal = len(in)
	detail.Mobs = make([]MobCount, 0, len(counts))
	for _, n := range counts {
		detail.Mobs = append(detail.Mobs, *n)
	}
	slices.SortFunc(detail.Mobs, func(a, b MobCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Kind, b.Kind))
	})
	if over := len(detail.Mobs) - MaxDetailKinds; over > 0 {
		detail.Mobs, detail.MobKindsMore = detail.Mobs[:MaxDetailKinds], over
	}
	detail.Named, detail.NamedMore = keepNamed(named, left)
}

func (c *contents) blocksIn(detail *Detail, in []savedBlock, left *allowance) {
	type at struct{ x, y, z int32 }
	type spawns struct {
		mob   uint16
		trial bool
	}
	// A large chest is two block entities, each naming the other. It is
	// counted once, as the half with the lower x or z, and by the more
	// telling of what its halves say: a lower state is the more so.
	halves := map[at]int{}
	for i, b := range in {
		if b.paired {
			halves[at{b.x, b.y, b.z}] = i
		}
	}
	whole := in[:0:0]
	for _, b := range in {
		first, held := halves[at{b.pairX, b.y, b.pairZ}]
		if b.paired && held && (b.pairX < b.x || (b.pairX == b.x && b.pairZ < b.z)) {
			in[first].state = min(in[first].state, b.state)
		}
	}
	for _, b := range in {
		_, held := halves[at{b.pairX, b.y, b.pairZ}]
		if b.paired && held && (b.pairX < b.x || (b.pairX == b.x && b.pairZ < b.z)) {
			continue
		}
		whole = append(whole, b)
	}
	in = whole
	containers := map[blockSort]*ContainerCount{}
	spawners := map[spawns]int{}
	others := map[string]int{}
	detail.Spawners = []Spawner{}
	slices.SortFunc(in, func(a, b savedBlock) int {
		return cmp.Or(cmp.Compare(a.x, b.x), cmp.Compare(a.z, b.z), cmp.Compare(a.y, b.y))
	})
	for _, b := range in {
		switch b.sort {
		case blockSpawner, blockTrialSpawner:
			trial := b.sort == blockTrialSpawner
			spawners[spawns{b.mob, trial}]++
			if len(detail.Spawners) >= MaxDetailSpawners || left.places <= 0 {
				detail.SpawnersMore++
				continue
			}
			left.places--
			detail.Spawners = append(detail.Spawners, Spawner{Mob: c.types.names[b.mob], X: b.x, Y: b.y, Z: b.z, Trial: trial})
		case blockVault:
			if b.state == 1 {
				others["ominous_vault"]++
			} else {
				others["vault"]++
			}
		case blockCauldron:
			others["cauldron"]++
		case blockBell:
			others["bell"]++
		case blockPortal:
			others["end_portal"]++
		default:
			n, seen := containers[b.sort]
			if !seen {
				n = &ContainerCount{}
				containers[b.sort] = n
			}
			switch b.state {
			case holdsLoot:
				n.Unopened++
			case holdsItems:
				n.Holding++
			default:
				n.Empty++
			}
		}
	}
	detail.Containers = []ContainerCount{}
	for _, k := range containerKinds {
		if n := containers[k.sort]; n != nil && n.Unopened+n.Holding+n.Empty > 0 {
			n.Kind = k.kind
			detail.Containers = append(detail.Containers, *n)
		}
	}
	detail.SpawnerCounts = make([]SpawnerCount, 0, len(spawners))
	for s, n := range spawners {
		detail.SpawnerCounts = append(detail.SpawnerCounts, SpawnerCount{Mob: c.types.names[s.mob], Count: n, Trial: s.trial})
	}
	slices.SortFunc(detail.SpawnerCounts, func(a, b SpawnerCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Mob, b.Mob))
	})
	if len(others) > 0 {
		detail.Blocks = others
	}
}

// since is how long before the world was saved a tick was, in seconds of
// game time, where both are known and the tick is not after the save.
func since(level leveldat.Level, tick int64, known bool) *int64 {
	if !known || !level.TickKnown || tick < 0 || tick > level.Tick {
		return nil
	}
	seconds := (level.Tick - tick) / ticksPerSecond
	return &seconds
}

// village sets a village's own records beside the mobs the save holds.
func (c *contents) village(r *villageRecords, level leveldat.Level) *VillageDetail {
	v := &VillageDetail{
		Professions: []Profession{},
		NotLookedUp: r.dwellersOver,
		JobSites:    slices.Clone(r.jobSites),
		IdleSeconds: since(level, r.tick, r.hasTick),
		Met:         len(r.standings),
		Standings:   r.standings,
	}
	if v.JobSites == nil {
		v.JobSites = []JobSites{}
	}
	slices.SortFunc(v.JobSites, func(a, b JobSites) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Profession, b.Profession))
	})
	if r.raid != nil {
		v.Raid = &RaidFacts{Wave: r.raid.wave, Waves: r.raid.waves, Raiders: r.raid.raiders, IdleSeconds: since(level, r.raid.tick, r.raid.hasTick)}
	}
	held := func(id int64) (savedMob, bool) {
		at, ok := c.byID[id]
		if !ok {
			return savedMob{}, false
		}
		return c.mobs[at.dim][at.i], true
	}
	for _, id := range r.dwellers[roleGolem] {
		if _, ok := held(id); ok {
			v.Golems++
		}
	}
	for _, id := range r.dwellers[roleCat] {
		if _, ok := held(id); ok {
			v.Cats++
		}
	}
	by := map[uint16]*Profession{}
	for _, id := range r.dwellers[roleVillager] {
		m, ok := held(id)
		switch {
		case !ok:
			v.Missing++
			continue
		case m.baby:
			v.Babies++
		default:
			p, seen := by[m.profession]
			if !seen {
				p = &Profession{Profession: c.types.names[m.profession]}
				by[m.profession] = p
			}
			p.Count++
			if m.profession != 0 && m.tier >= 0 {
				p.Levels[m.tier]++
			}
		}
	}
	for _, p := range by {
		v.Professions = append(v.Professions, *p)
	}
	// Those with a profession first, the most of one first.
	slices.SortFunc(v.Professions, func(a, b Profession) int {
		if none := a.Profession == ""; none != (b.Profession == "") {
			if none {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Profession, b.Profession))
	})
	return v
}
