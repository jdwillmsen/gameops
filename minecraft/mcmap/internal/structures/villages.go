package structures

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/util"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Village is the one kind that is not read from spawn areas. The game keeps
// each village it is running as a handful of records of its own, outside
// any chunk:
//
//	VILLAGE_<dimension>_<id>_INFO      the box the village covers
//	VILLAGE_<dimension>_<id>_DWELLERS  the mobs that belong to it
//	VILLAGE_<dimension>_<id>_POI       each villager's bed, bell and job site
//	VILLAGE_<dimension>_<id>_PLAYERS   what it thinks of each player
//	VILLAGE_<dimension>_<id>_RAID      a raid in progress
//
// Each is one unnamed NBT compound. INFO holds the box as six ints, X0, Y0,
// Z0 to X1, Y1, Z1, and a byte, Initialized. DWELLERS holds a list,
// Dwellers, of four compounds each with a list, actors: the first is the
// village's villagers, the second its iron golems and the fourth its cats.
// The third has been empty in every village looked at. POI holds a
// list, POI, with a compound per villager, whose instances list has a
// compound for each thing that villager has claimed: Type 0 a bed, 1 a
// bell, 2 a job site, at X, Y, Z, or only Skip set where it has claimed
// none. A job site's Name is the profession it gives, and so which block it
// is: a farmer's is a composter.
//
// INFO also holds Tick, the game tick the village was last run at. An actor
// in DWELLERS is an ID, the UniqueID of the mob's own record. PLAYERS holds
// a list, Players, of an ID and S for each player the village has a
// standing for: the player's own UniqueID, and a whole number that starts
// at nought. RAID is there only for a village that has had a raid, and
// holds a compound, Raid, with GroupNum and NumGroups for the wave reached
// and how many there are, NumRaiders, and GameTick, when it was last run;
// the game does not take the record away when the raid is over.
//
// PLAYERS is the only one that says anything of a player, and none of it
// is in what every viewer is sent: a standing goes only to the player it
// is of. PLAYERS and RAID decorate a village and never decide whether
// there is one, so one that does not parse is counted and the village
// stands.
const Village Kind = "village"

// VillageFacts is what a village's records say about it besides where it is.
type VillageFacts struct {
	// Counted is false for a village the game has made a record for and
	// not yet run: its box is a first guess, 64 blocks square, and nothing
	// in it has been counted, so the numbers below are left at zero and
	// mean "not known", not "none".
	Counted   bool `json:"counted"`
	Villagers int  `json:"villagers"`
	Golems    int  `json:"golems"`
	Cats      int  `json:"cats"`
	// Beds, Bells and JobSites are the ones a villager has claimed, each
	// counted once however many share it.
	Beds     int `json:"beds"`
	Bells    int `json:"bells"`
	JobSites int `json:"jobSites"`
}

// VillageStats is what one reading of the village records came to.
type VillageStats struct {
	// Found is how many villages are worth drawing, before any layer's
	// limit.
	Found int
	// Empty is villages the game has counted and found no villager in: a
	// record still, and no longer somewhere to go looking for one.
	Empty int
	// Malformed is villages whose records do not parse, lack a box, or
	// give one no village could have.
	Malformed int
	// Unknown is villages under a key this version does not know: another
	// dimension's name, or the form older game versions wrote.
	Unknown int
	// OverLimit is villages past maxVillages, which were not read.
	OverLimit int
	// Stale is set when the records could not be read this time and the
	// villages are those of the survey before.
	Stale bool
}

// Bounds on the village records. The FWB world has 70 villages in 281
// records, the largest 36 KB, the widest box 159 blocks.
const (
	// maxVillages is how many villages are read; the rest are counted.
	maxVillages = 4096
	// keysPerVillage is how many keys under the village prefix the read
	// allows for each village it may read, before it gives the world up as
	// one whose village records cannot be trusted. A village has five.
	keysPerVillage   = 16
	maxVillageKey    = 128
	maxVillageRecord = 1 << 20
	// maxVillageSpan is the widest and tallest box a village is drawn
	// with. The game grows a village to take in claimed blocks near its
	// edge, so there is no fixed size, but one this wide is damage, and
	// would be drawn across the map.
	maxVillageSpan = 1024
	// maxCoordinate is past the edge of any Bedrock world.
	maxCoordinate = 32_000_000
	// maxCount is the most of anything one village is said to hold. A
	// megabyte of record can claim a million villagers.
	maxCount = 10_000
	// maxDwellerIDs is how many of each sort of dweller are looked up in
	// the mobs the save holds; the busiest village in the FWB world has 58
	// villagers. The rest are counted as not looked up.
	maxDwellerIDs = 512
	// maxProfessions is how many different job sites a village is told
	// apart by; the game has thirteen.
	maxProfessions = 32
	// maxStandings is how many players' standings are kept for a village.
	maxStandings = 256
	// ticksPerSecond is the game's own clock.
	ticksPerSecond = 20
)

var villagePrefix = []byte("VILLAGE_")

// The names the game gives the dimensions in keys. Only the overworld's has
// been seen in a real world; the other two are the names of the game's own
// per-dimension records.
var villageDimensions = map[string]chunks.Dimension{
	"Overworld": chunks.Overworld,
	"Nether":    chunks.Nether,
	"TheEnd":    chunks.End,
}

// villageKey takes a village record's key apart. The id is not kept: it is
// only what says which records belong together, and a village's records are
// next to each other in the database's order.
func villageKey(k []byte) (dim chunks.Dimension, part []byte, ok bool) {
	if len(k) > maxVillageKey || !bytes.HasPrefix(k, villagePrefix) {
		return 0, nil, false
	}
	rest := k[len(villagePrefix):]
	first, last := bytes.IndexByte(rest, '_'), bytes.LastIndexByte(rest, '_')
	if first <= 0 || last <= first+1 {
		return 0, nil, false
	}
	dim, known := villageDimensions[string(rest[:first])]
	for _, c := range rest[first+1 : last] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && c != '-' {
			return 0, nil, false
		}
	}
	return dim, rest[last+1:], known
}

// village is the records of one village as they are read.
type village struct {
	dim     chunks.Dimension
	box     Box
	hasBox  bool
	facts   VillageFacts
	damaged bool
	more    villageRecords
}

// The lists of a DWELLERS record that are read, by where they are in it.
const (
	roleVillager = iota
	roleGolem
	roleCat
	roles
)

// villageRecords is what a village's records hold that is not sent with
// every village: it is set beside the mobs the save holds when one
// village's details are put together.
type villageRecords struct {
	// dwellers is the UniqueID of each mob of each role, up to
	// maxDwellerIDs; dwellersOver is how many more there were.
	dwellers     [roles][]int64
	dwellersOver int
	// jobSites is the claimed job sites by the profession each gives.
	jobSites []JobSites
	// tick is the game tick the village was last run at.
	tick    int64
	hasTick bool
	raid    *raidRecord
	// standings is what the village thinks of each player it has met.
	standings []Standing
	// skipped is how many of the PLAYERS and RAID records did not parse.
	skipped int
}

type raidRecord struct {
	wave, waves, raiders int
	tick                 int64
	hasTick              bool
}

// JobSites is how many job sites of one profession a village's villagers
// have claimed.
type JobSites struct {
	// Profession is the game's word: farmer, toolsmith.
	Profession string `json:"profession"`
	Count      int    `json:"count"`
}

var errVillageRecords = errors.New("more records under the village prefix than villages could account for")

// readVillages reads every village in the world. It reads only the keys
// under the village prefix, which is a few hundred records in a world of
// millions. A prefix holding far more keys than villages have is refused
// whole: stopping part way would report whichever villages sorted first as
// all there are.
func readVillages(ctx context.Context, db *leveldb.DB, limit int) (map[chunks.Dimension][]Structure, map[*VillageFacts]*villageRecords, VillageStats, error) {
	found := map[chunks.Dimension][]Structure{}
	records := map[*VillageFacts]*villageRecords{}
	var stats VillageStats
	var (
		current []byte
		v       *village
	)
	finish := func() {
		if v == nil {
			return
		}
		switch {
		case v.damaged || !v.hasBox:
			stats.Malformed++
		case v.facts.Counted && v.facts.Villagers == 0:
			stats.Empty++
		default:
			facts, more := v.facts, v.more
			if !facts.Counted {
				// Whatever an unrun village's lists hold, the game has
				// not counted it, and a number here would say it had.
				facts, more = VillageFacts{}, villageRecords{skipped: more.skipped}
			}
			found[v.dim] = append(found[v.dim], Structure{Kind: Village, Box: v.box, Village: &facts})
			records[&facts] = &more
			stats.Found++
		}
		v = nil
	}

	it := db.NewIterator(util.BytesPrefix(villagePrefix), nil)
	defer it.Release()
	for n := 0; it.Next(); n++ {
		if n%64 == 0 && ctx.Err() != nil {
			return nil, nil, VillageStats{}, ctx.Err()
		}
		if n >= keysPerVillage*limit {
			return nil, nil, VillageStats{}, errVillageRecords
		}
		k := it.Key()
		// Every village has one INFO record, so that is the one counted
		// where a village is passed over without being read.
		isInfo := bytes.HasSuffix(k, []byte("_INFO"))
		dim, part, ok := villageKey(k)
		if !ok {
			if isInfo {
				stats.Unknown++
			}
			continue
		}
		var (
			read func(*village, []byte) error
			// decorates is set for a record a village stands without.
			decorates bool
		)
		switch string(part) {
		case "INFO":
			read = (*village).info
		case "DWELLERS":
			read = (*village).dwellers
		case "POI":
			read = (*village).claims
		case "PLAYERS":
			read, decorates = (*village).players, true
		case "RAID":
			read, decorates = (*village).raid, true
		default:
			// Not a record of any village, whatever its key looks like.
			continue
		}
		// The dimension is part of what says which village this is.
		same := k[:len(k)-len(part)]
		if !bytes.Equal(same, current) {
			finish()
			current = append(current[:0], same...)
			if stats.Found+stats.Empty+stats.Malformed >= limit {
				if isInfo {
					stats.OverLimit++
				}
				continue
			}
			v = &village{dim: dim}
		}
		if v == nil {
			if isInfo {
				stats.OverLimit++
			}
			continue
		}
		value := it.Value()
		switch {
		case len(value) <= maxVillageRecord && read(v, value) == nil:
		case decorates:
			v.more.skipped++
		default:
			v.damaged = true
		}
	}
	finish()
	if err := it.Error(); err != nil {
		return nil, nil, VillageStats{}, fmt.Errorf("read villages: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, VillageStats{}, err
	}
	for _, list := range found {
		slices.SortFunc(list, compareVillages)
	}
	return found, records, stats, nil
}

// The most lived-in first, so that a list cut short keeps the villages a
// player is likeliest to be looking for; then by position, so the order is
// the same every time.
func compareVillages(a, b Structure) int {
	return cmp.Or(
		cmp.Compare(b.Village.Villagers, a.Village.Villagers),
		cmp.Compare(a.MinX, b.MinX), cmp.Compare(a.MinZ, b.MinZ), cmp.Compare(a.MinY, b.MinY),
		cmp.Compare(a.MaxX, b.MaxX), cmp.Compare(a.MaxZ, b.MaxZ), cmp.Compare(a.MaxY, b.MaxY),
	)
}

func (v *village) info(record []byte) error {
	b, err := rootCompound(record)
	if err != nil {
		return err
	}
	var (
		corner [6]int32
		seen   [6]bool
	)
	// A record without the flag is taken as counted, and is then judged by
	// its villagers like any other.
	v.facts.Counted = true
	err = eachField(b, func(name []byte, tag byte, payload []byte) error {
		i := -1
		switch string(name) {
		case "X0":
			i = 0
		case "Y0":
			i = 1
		case "Z0":
			i = 2
		case "X1":
			i = 3
		case "Y1":
			i = 4
		case "Z1":
			i = 5
		case "Initialized":
			if tag != tagByte {
				return errNBT
			}
			v.facts.Counted = payload[0] != 0
			return nil
		case "Tick":
			if tag == tagLong {
				v.more.tick, v.more.hasTick = int64(binary.LittleEndian.Uint64(payload)), true
			}
			return nil
		default:
			return nil
		}
		corner[i], seen[i] = intOf(tag, payload)
		return nil
	})
	if err != nil {
		return err
	}
	if seen != [6]bool{true, true, true, true, true, true} {
		return errNBT
	}
	box := Box{corner[0], corner[1], corner[2], corner[3], corner[4], corner[5]}
	for _, c := range corner {
		if c < -maxCoordinate || c > maxCoordinate {
			return errNBT
		}
	}
	// An inside-out box is damage, and so is one wider than any village:
	// either would be drawn as something that is not there.
	if box.MinX > box.MaxX || box.MinY > box.MaxY || box.MinZ > box.MaxZ ||
		box.MaxX-box.MinX > maxVillageSpan || box.MaxY-box.MinY > maxVillageSpan || box.MaxZ-box.MinZ > maxVillageSpan {
		return errNBT
	}
	v.box, v.hasBox = box, true
	return nil
}

func (v *village) dwellers(record []byte) error {
	b, err := rootCompound(record)
	if err != nil {
		return err
	}
	return eachField(b, func(name []byte, tag byte, payload []byte) error {
		if string(name) != "Dwellers" {
			return nil
		}
		role := 0
		_, err := eachCompound(tag, payload, func(list []byte) error {
			defer func() { role++ }()
			return eachField(list, func(name []byte, tag byte, payload []byte) error {
				if string(name) != "actors" {
					return nil
				}
				// What the third list holds has not been seen.
				kept, known := map[int]int{0: roleVillager, 1: roleGolem, 3: roleCat}[role]
				n, err := eachCompound(tag, payload, func(actor []byte) error {
					if !known {
						return nil
					}
					return eachField(actor, func(name []byte, tag byte, payload []byte) error {
						if string(name) != "ID" || tag != tagLong {
							return nil
						}
						if len(v.more.dwellers[kept]) >= maxDwellerIDs {
							v.more.dwellersOver++
							return nil
						}
						v.more.dwellers[kept] = append(v.more.dwellers[kept], int64(binary.LittleEndian.Uint64(payload)))
						return nil
					})
				})
				if err != nil || !known {
					return err
				}
				switch n = min(n, maxCount); kept {
				case roleVillager:
					v.facts.Villagers = n
				case roleGolem:
					v.facts.Golems = n
				case roleCat:
					v.facts.Cats = n
				}
				return nil
			})
		})
		return err
	})
}

// claims counts the beds, bells and job sites the village's villagers have
// claimed. A bell is claimed by every villager who gathers at it, so each
// block is counted once.
func (v *village) claims(record []byte) error {
	b, err := rootCompound(record)
	if err != nil {
		return err
	}
	type claim struct {
		kind    int32
		x, y, z int32
	}
	seen := map[claim]struct{}{}
	return eachField(b, func(name []byte, tag byte, payload []byte) error {
		if string(name) != "POI" {
			return nil
		}
		_, err := eachCompound(tag, payload, func(villager []byte) error {
			return eachField(villager, func(name []byte, tag byte, payload []byte) error {
				if string(name) != "instances" {
					return nil
				}
				_, err := eachCompound(tag, payload, func(instance []byte) error {
					var (
						c    claim
						has  [4]bool
						skip bool
						job  string
					)
					if err := eachField(instance, func(name []byte, tag byte, payload []byte) error {
						switch string(name) {
						case "Type":
							c.kind, has[0] = intOf(tag, payload)
						case "X":
							c.x, has[1] = intOf(tag, payload)
						case "Y":
							c.y, has[2] = intOf(tag, payload)
						case "Z":
							c.z, has[3] = intOf(tag, payload)
						case "Skip":
							skip = tag == tagByte && payload[0] != 0
						case "Name":
							job, _ = textOf(tag, payload)
						}
						return nil
					}); err != nil {
						return err
					}
					// A kind of claim this version does not know is not
					// counted, and takes none of the room for those that are.
					if skip || has != [4]bool{true, true, true, true} || c.kind < 0 || c.kind > 2 || len(seen) >= maxCount {
						return nil
					}
					if _, dup := seen[c]; dup {
						return nil
					}
					seen[c] = struct{}{}
					switch c.kind {
					case 0:
						v.facts.Beds++
					case 1:
						v.facts.Bells++
					case 2:
						v.facts.JobSites++
						v.more.jobSite(job)
					}
					return nil
				})
				return err
			})
		})
		return err
	})
}

// jobSite counts one claimed job site under the profession it gives.
func (r *villageRecords) jobSite(name string) {
	profession := cleanID(name)
	for i := range r.jobSites {
		if r.jobSites[i].Profession == profession {
			r.jobSites[i].Count++
			return
		}
	}
	if len(r.jobSites) >= maxProfessions {
		return
	}
	r.jobSites = append(r.jobSites, JobSites{Profession: profession, Count: 1})
}

// players reads what the village thinks of each player it has met. Who a
// number is of is the player's UniqueID, which is kept and never sent.
func (v *village) players(record []byte) error {
	b, err := rootCompound(record)
	if err != nil {
		return err
	}
	var found []Standing
	if err := eachField(b, func(name []byte, tag byte, payload []byte) error {
		if string(name) != "Players" {
			return nil
		}
		_, err := eachCompound(tag, payload, func(player []byte) error {
			var (
				s            Standing
				hasID, hasIt bool
			)
			if err := eachField(player, func(name []byte, tag byte, payload []byte) error {
				switch string(name) {
				case "ID":
					if tag == tagLong {
						s.Player, hasID = int64(binary.LittleEndian.Uint64(payload)), true
					}
				case "S":
					s.Value, hasIt = intOf(tag, payload)
				}
				return nil
			}); err != nil {
				return err
			}
			if hasID && hasIt && len(found) < maxStandings {
				found = append(found, s)
			}
			return nil
		})
		return err
	}); err != nil {
		return err
	}
	v.more.standings = found
	return nil
}

// raid reads how far the village's raid got.
func (v *village) raid(record []byte) error {
	b, err := rootCompound(record)
	if err != nil {
		return err
	}
	var (
		raid raidRecord
		has  [3]bool
	)
	if err := eachField(b, func(name []byte, tag byte, payload []byte) error {
		if string(name) != "Raid" || tag != tagCompound {
			return nil
		}
		return eachField(payload, func(name []byte, tag byte, payload []byte) error {
			count := func(i int, into *int) {
				// Each is a byte in the game, read as it counts: unsigned.
				if tag == tagByte {
					*into, has[i] = int(payload[0]), true
				}
			}
			switch string(name) {
			case "GroupNum":
				count(0, &raid.wave)
			case "NumGroups":
				count(1, &raid.waves)
			case "NumRaiders":
				count(2, &raid.raiders)
			case "GameTick":
				if tag == tagLong {
					raid.tick, raid.hasTick = int64(binary.LittleEndian.Uint64(payload)), true
				}
			}
			return nil
		})
	}); err != nil {
		return err
	}
	if has != [3]bool{true, true, true} {
		return errNBT
	}
	v.more.raid = &raid
	return nil
}
