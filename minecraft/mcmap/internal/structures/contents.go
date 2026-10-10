package structures

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/markers"
)

// What a structure holds is not in any record of the structure. It is in
// the records of the things themselves: each saved mob is an actor record,
// and each spawner, chest and bell is a block entity of its chunk. The
// survey already passes every key of the world once, so it keeps a few
// words about each of those as it goes, and sets them inside the boxes once
// it knows where the boxes are.
//
// The bounds are for a damaged or hostile world. The FWB world holds 22,000
// actors, 2,900 of them mobs a chunk still lists, and 110,000 block
// entities, 45,000 of them of a sort kept here.
const (
	// TagBlockEntities is the chunk record listing its block entities:
	// NBT compounds one after another.
	TagBlockEntities = 0x31

	maxSavedMobs   = 200_000
	maxSavedBlocks = 400_000
	// maxContentRecord is the largest actor or block entity record walked.
	// The largest in the FWB world is 212 KB, a chunk of full chests.
	maxContentRecord = 8 << 20
	// maxTypeNames is how many different mob types and professions are
	// told apart. The game has about 130; past this they are all "unknown".
	maxTypeNames = 2048
	maxTypeName  = 64
)

var (
	// Each actor is one record under this prefix and its eight-byte
	// storage key; a chunk's digp record is the storage keys of the actors
	// in it, and the only place an actor's dimension is written.
	actorPrefix = []byte("actorprefix")
	digpPrefix  = []byte("digp")
	// Every mob's record has this tag and no item's, arrow's or boat's
	// does, so a record without the word is passed over unread.
	mobTag = []byte("HurtTime")
)

// notMobs is what the game stores as a mob and nobody would count as one.
var notMobs = map[string]bool{"armor_stand": true}

// blockSort is which of the block entities worth keeping one is.
type blockSort uint8

const (
	blockChest blockSort = iota + 1
	blockBarrel
	blockShulker
	blockDispenser
	blockDropper
	blockPot
	blockSpawner
	blockTrialSpawner
	blockVault
	blockCauldron
	blockBell
	blockPortal
	// Suspicious sand or gravel nobody has brushed, a dragon's head, an
	// item frame holding elytra, and an end gateway: each says something
	// of the structure it is in and is kept for nothing else.
	blockBrushable
	blockDragonHead
	blockElytra
	blockGateway
)

// The block entity ids kept, by what they are kept as.
var blockSorts = map[string]blockSort{
	"Chest": blockChest, "Barrel": blockBarrel, "ShulkerBox": blockShulker,
	"Dispenser": blockDispenser, "Dropper": blockDropper, "DecoratedPot": blockPot,
	"MobSpawner": blockSpawner, "TrialSpawner": blockTrialSpawner, "Vault": blockVault,
	"Cauldron": blockCauldron, "Bell": blockBell, "EndPortal": blockPortal,
	"BrushableBlock": blockBrushable, "Skull": blockDragonHead, "ItemFrame": blockElytra, "GlowItemFrame": blockElytra,
	"EndGateway": blockGateway,
}

// origin is the kind of structure a block was generated as part of, where
// its loot table says. The game names a structure's loot after it, and
// drops the name the first time the chest is opened or the sand brushed,
// so a block that still carries one is where the generator put it and has
// not been touched.
type origin uint8

const (
	originEndCity origin = iota + 1
	originBastion
	originBastionTreasure
	originBastionStables
	originBastionBridge
	originRuinedPortal
	originShipwreck
	originOceanRuins
	originBuriedTreasure
	originAncientCity
	originMansion
	originDesertPyramid
	originJungleTemple
	originIgloo
	originTrailRuins
)

// lootOrigins is the part of a loot table's path that names each origin.
// The first that is found in a path is the block's, so a bastion's
// treasure room is asked about before a bastion.
var lootOrigins = []struct {
	part []byte
	of   origin
}{
	{[]byte("chests/end_city"), originEndCity},
	{[]byte("chests/bastion_treasure"), originBastionTreasure},
	{[]byte("chests/bastion_hoglin_stable"), originBastionStables},
	{[]byte("chests/bastion_bridge"), originBastionBridge},
	{[]byte("chests/bastion_"), originBastion},
	{[]byte("chests/ruined_portal"), originRuinedPortal},
	{[]byte("chests/shipwreck"), originShipwreck},
	{[]byte("chests/underwater_ruin"), originOceanRuins},
	{[]byte("ocean_ruins_brushable"), originOceanRuins},
	{[]byte("chests/buriedtreasure"), originBuriedTreasure},
	{[]byte("chests/ancient_city"), originAncientCity},
	{[]byte("chests/woodland_mansion"), originMansion},
	{[]byte("chests/desert_pyramid"), originDesertPyramid},
	{[]byte("desert_pyramid_brushable"), originDesertPyramid},
	{[]byte("chests/jungle_temple"), originJungleTemple},
	{[]byte("chests/dispenser_trap"), originJungleTemple},
	{[]byte("chests/igloo_chest"), originIgloo},
	{[]byte("trail_ruins_brushable"), originTrailRuins},
}

func originOf(loot []byte) origin {
	for _, o := range lootOrigins {
		if bytes.Contains(loot, o.part) {
			return o.of
		}
	}
	return 0
}

// dragonHead is the game's number for a dragon's head among its skulls.
const dragonHead = 5

// containerKinds is the containers a player opens, by what each is called
// in an answer and in the order they are listed. A dispenser, a dropper and
// a pot hold things too, and are counted as the blocks they are: nobody
// opens a pot, and a dispenser's load says nothing of who has been by.
var containerKinds = []struct {
	sort blockSort
	kind string
}{
	{blockChest, "chest"}, {blockBarrel, "barrel"}, {blockShulker, "shulker"},
}

// What a container's record says of what is in it.
const (
	// holdsLoot: it still carries the loot table it was generated with.
	// The game rolls the table and drops the tag the first time anything
	// opens the container, so nothing has.
	holdsLoot uint8 = iota + 1
	holdsItems
	holdsNothing
)

type savedMob struct {
	x, y, z int32
	// kind and profession index the survey's type names; profession 0 is
	// none recorded.
	kind, profession uint16
	// tier is a villager's trade tier, 0 to 4, or -1 where there is none.
	tier          int8
	baby, captain bool
	// name is the mob's name tag, cleaned; it is still a player's text.
	name string
}

type savedBlock struct {
	x, y, z int32
	sort    blockSort
	// state is a container's holds value, and for a vault 1 if ominous.
	state uint8
	// origin is the structure the block's loot table names, if it still
	// carries one.
	origin origin
	// mob indexes the type a spawner spawns.
	mob uint16
	// paired is set on half of a large chest, with where the other half is.
	paired       bool
	pairX, pairZ int32
}

// typeNames gives each mob type and profession a small number, so that ten
// thousand zombies are not ten thousand strings, nor ten thousand strings
// made and thrown away to find that out.
type typeNames struct {
	// raw is the number of a type by what the game wrote; ids by the
	// plain id that was reduced to.
	raw, ids map[string]uint16
	names    []string
}

// unknownType is what a type that is not an id, or is one too many, is
// called. It is in the table from the start, so it is always there to fall
// back on.
const unknownType = "unknown"

func newTypeNames() *typeNames {
	return &typeNames{raw: map[string]uint16{}, ids: map[string]uint16{"": 0, unknownType: 1}, names: []string{"", unknownType}}
}

// of is the number of a type as the game wrote it, which is reduced to its
// plain id first. One that is not an id, or one too many, is "unknown".
func (t *typeNames) of(raw []byte) uint16 {
	if n, ok := t.raw[string(raw)]; ok {
		return n
	}
	id := cleanID(string(raw))
	n, ok := t.ids[id]
	switch {
	case ok:
	case len(t.names) >= maxTypeNames:
		return t.ids[unknownType]
	default:
		n = uint16(len(t.names))
		t.ids[id] = n
		t.names = append(t.names, id)
	}
	// Bounded with the names: a world cannot fill this with spellings.
	if len(t.raw) < 2*maxTypeNames {
		t.raw[string(raw)] = n
	}
	return n
}

// cleanID reduces an identifier to what follows minecraft:. Whatever is
// not an identifier is not passed on: this text reaches a browser.
func cleanID(id string) string {
	id = strings.TrimPrefix(id, "minecraft:")
	if id == "" || len(id) > maxTypeName {
		return unknownType
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != ':' && r != '.' && r != '-' {
			return unknownType
		}
	}
	return id
}

// ContentStats is what a survey read of what the world holds.
type ContentStats struct {
	// Mobs and Blocks are how many saved mobs and block entities were kept.
	Mobs, Blocks int
	// Skipped counts records that could not be read or made no sense;
	// MobsOver and BlocksOver those past the bounds.
	Skipped, MobsOver, BlocksOver int
	// Targets is how many places the world's explorer maps point to, of
	// the kinds they are read for, and TargetsOver those past the bound.
	Targets, TargetsOver int
}

// contents is every saved mob and every block entity worth keeping.
type contents struct {
	types *typeNames
	// unplaced is the mobs no chunk has listed yet, by storage key.
	unplaced map[[8]byte]placing
	mobs     map[chunks.Dimension][]savedMob
	// byID is where each mob is, by the id the village records know it by.
	byID   map[int64]mobAt
	blocks map[chunks.Dimension][]savedBlock
	// targets is where the world's explorer maps point.
	targets map[target]struct{}
	stats   ContentStats
}

type placing struct {
	mob   savedMob
	id    int64
	hasID bool
}

type mobAt struct {
	dim chunks.Dimension
	i   int32
}

func newContents() *contents {
	return &contents{
		types:    newTypeNames(),
		unplaced: map[[8]byte]placing{},
		mobs:     map[chunks.Dimension][]savedMob{},
		byID:     map[int64]mobAt{},
		blocks:   map[chunks.Dimension][]savedBlock{},
		targets:  map[target]struct{}{},
	}
}

// actor keeps one saved mob. It is not placed until its chunk's list says
// which dimension it is in; every actor's key sorts before every list's.
func (c *contents) actor(k, v []byte) {
	if len(k) != len(actorPrefix)+8 || !bytes.Contains(v, mobTag) {
		return
	}
	if len(v) > maxContentRecord {
		c.stats.Skipped++
		return
	}
	var (
		p                         = placing{mob: savedMob{tier: -1}}
		kind, profession, name    []byte
		isMob, placed, dead, tier bool
	)
	if _, err := fieldsAt(v, func(field []byte, tag byte, payload []byte) {
		switch string(field) {
		case "HurtTime":
			isMob = true
		case "identifier":
			kind, _ = bytesOf(tag, payload)
		case "Pos":
			p.mob.x, p.mob.y, p.mob.z, placed = placeOf(tag, payload)
		case "Dead":
			n, _ := wholeOf(tag, payload)
			dead = n != 0
		case "IsBaby":
			n, _ := wholeOf(tag, payload)
			p.mob.baby = n != 0
		case "IsIllagerCaptain":
			n, _ := wholeOf(tag, payload)
			p.mob.captain = n != 0
		case "PreferredProfession":
			profession, _ = bytesOf(tag, payload)
		case "TradeTier":
			if n, ok := wholeOf(tag, payload); ok && n >= 0 && n <= 4 {
				p.mob.tier, tier = int8(n), true
			}
		case "CustomName":
			name, _ = bytesOf(tag, payload)
		case "UniqueID":
			if tag == tagLong {
				p.id, p.hasID = int64(binary.LittleEndian.Uint64(payload)), true
			}
		}
	}); err != nil {
		c.stats.Skipped++
		return
	}
	if !isMob || dead {
		return
	}
	if !placed {
		c.stats.Skipped++
		return
	}
	p.mob.kind = c.types.of(kind)
	if notMobs[c.types.names[p.mob.kind]] {
		return
	}
	if len(c.unplaced) >= maxSavedMobs {
		c.stats.MobsOver++
		return
	}
	if len(profession) > 0 {
		p.mob.profession = c.types.of(profession)
	} else if tier {
		// Every mob's record has a trade tier, and it means something only
		// beside a profession.
		p.mob.tier = -1
	}
	if len(name) > 0 {
		p.mob.name = markers.CleanName(string(name))
	}
	c.unplaced[[8]byte(k[len(actorPrefix):])] = p
}

// place gives the mobs a chunk lists their dimension. One no chunk lists
// is a leftover the game itself never loads, and is not counted anywhere.
func (c *contents) place(k, v []byte) {
	if len(c.unplaced) == 0 {
		return
	}
	var dim chunks.Dimension
	switch len(k) - len(digpPrefix) {
	case 8:
	case 12:
		dim = chunks.Dimension(int32(binary.LittleEndian.Uint32(k[len(digpPrefix)+8:])))
	default:
		return
	}
	if !slices.Contains(chunks.Dimensions, dim) || len(v)%8 != 0 {
		return
	}
	for ; len(v) >= 8; v = v[8:] {
		key := [8]byte(v[:8])
		p, ok := c.unplaced[key]
		if !ok {
			continue
		}
		delete(c.unplaced, key)
		if p.hasID {
			c.byID[p.id] = mobAt{dim, int32(len(c.mobs[dim]))}
		}
		c.mobs[dim] = append(c.mobs[dim], p.mob)
		c.stats.Mobs++
	}
}

// blockEntities keeps what is worth keeping of one chunk's block entities.
func (c *contents) blockEntities(pos chunks.Pos, v []byte) {
	if len(v) > maxContentRecord {
		c.stats.Skipped++
		return
	}
	for len(v) > 0 {
		var (
			b                         savedBlock
			id, mob, loot             []byte
			hasX, hasY, hasZ, hasLoot bool
			hasPairX, hasPairZ        bool
			items, skull              int64
			elytra                    bool
		)
		rest, err := fieldsAt(v, func(field []byte, tag byte, payload []byte) {
			switch string(field) {
			case "id":
				id, _ = bytesOf(tag, payload)
			case "x":
				b.x, hasX = intOf(tag, payload)
			case "y":
				b.y, hasY = intOf(tag, payload)
			case "z":
				b.z, hasZ = intOf(tag, payload)
			case "pairx":
				b.pairX, hasPairX = intOf(tag, payload)
			case "pairz":
				b.pairZ, hasPairZ = intOf(tag, payload)
			case "Items":
				if tag == tagList {
					items = int64(int32(binary.LittleEndian.Uint32(payload[1:5])))
				}
			case "LootTable":
				loot, hasLoot = bytesOf(tag, payload)
			case "EntityIdentifier":
				mob, _ = bytesOf(tag, payload)
			case "SkullType":
				skull, _ = wholeOf(tag, payload)
			case "Item":
				// What an item frame holds.
				if tag == tagCompound {
					_ = eachField(payload, func(field []byte, tag byte, payload []byte) error {
						if name, ok := bytesOf(tag, payload); ok && string(field) == "Name" {
							elytra = string(name) == "minecraft:elytra"
						}
						return nil
					})
				}
			case "spawn_data":
				// A trial spawner's mob, one level down.
				if tag == tagCompound {
					_ = eachField(payload, func(field []byte, tag byte, payload []byte) error {
						if string(field) == "TypeId" {
							mob, _ = bytesOf(tag, payload)
						}
						return nil
					})
				}
			case "config":
				// A vault's loot table, which is all that says an ominous
				// one from a plain one.
				if tag == tagCompound {
					_ = eachField(payload, func(field []byte, tag byte, payload []byte) error {
						if string(field) == "loot_table" {
							loot, _ = bytesOf(tag, payload)
						}
						return nil
					})
				}
			}
		})
		if err != nil {
			// Nothing says where the next one starts.
			c.stats.Skipped++
			return
		}
		v = rest
		sort, kept := blockSorts[string(id)]
		if !kept {
			continue
		}
		// A block entity lies in the chunk whose record holds it. One that
		// says otherwise is not somewhere to count anything.
		if !hasX || !hasY || !hasZ || b.x>>4 != pos.X || b.z>>4 != pos.Z || b.y < -maxCoordinate || b.y > maxCoordinate {
			c.stats.Skipped++
			continue
		}
		b.sort = sort
		b.origin = originOf(loot)
		switch sort {
		case blockBrushable:
			// One that has been brushed, or that a player made, says
			// nothing of any structure.
			if b.origin == 0 {
				continue
			}
		case blockDragonHead:
			if skull != dragonHead {
				continue
			}
		case blockElytra:
			if !elytra {
				continue
			}
		case blockPot:
			// A pot the generator placed carries a loot table until it is
			// broken. One without is a player's, or shards put back.
			if !hasLoot || len(loot) == 0 {
				continue
			}
		case blockChest, blockBarrel, blockShulker:
			switch {
			case hasLoot && len(loot) > 0:
				b.state = holdsLoot
			case items > 0:
				b.state = holdsItems
			default:
				b.state = holdsNothing
			}
			b.paired = sort == blockChest && hasPairX && hasPairZ
		case blockSpawner, blockTrialSpawner:
			b.mob = c.types.of(mob)
		case blockVault:
			if bytes.Contains(loot, []byte("ominous")) {
				b.state = 1
			}
		}
		if len(c.blocks[pos.Dim]) >= maxSavedBlocks {
			c.stats.BlocksOver++
			continue
		}
		c.blocks[pos.Dim] = append(c.blocks[pos.Dim], b)
		c.stats.Blocks++
	}
}
