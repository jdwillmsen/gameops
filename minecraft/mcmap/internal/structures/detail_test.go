package structures

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
)

func nbtLong(name string, v int64) []byte {
	return nbtTag(tagLong, name, binary.LittleEndian.AppendUint64(nil, uint64(v)))
}

func nbtShort(name string, v int16) []byte {
	return nbtTag(tagShort, name, binary.LittleEndian.AppendUint16(nil, uint16(v)))
}

func nbtString(name, v string) []byte {
	return nbtTag(tagString, name, append(binary.LittleEndian.AppendUint16(nil, uint16(len(v))), v...))
}

func nbtFloats(name string, v ...float32) []byte {
	payload := binary.LittleEndian.AppendUint32([]byte{tagFloat}, uint32(len(v)))
	for _, f := range v {
		payload = binary.LittleEndian.AppendUint32(payload, math.Float32bits(f))
	}
	return nbtTag(tagList, name, payload)
}

// actors counts the actor records a test world holds, which is what gives
// each its storage key.
func (w *world) actors() int {
	n := 0
	for k := range w.records {
		if strings.HasPrefix(k, string(actorPrefix)) {
			n++
		}
	}
	return n
}

// mob saves a mob the way the game does: its own record, and its storage
// key in the list of the chunk it stands in. It returns the mob's UniqueID.
func (w *world) mob(d chunks.Dimension, kind string, x, y, z float32, more ...[]byte) int64 {
	id := int64(-4_000_000_000 - w.actors())
	key := binary.BigEndian.AppendUint64(nil, uint64(w.actors()+1))
	tags := append([][]byte{
		nbtString("identifier", "minecraft:"+kind), nbtFloats("Pos", x, y, z),
		nbtShort("HurtTime", 0), nbtLong("UniqueID", id),
	}, more...)
	w.records[string(actorPrefix)+string(key)] = nbtRecord(tags...)
	list := append([]byte("digp"), chunks.RecordKey(chunks.Pos{Dim: d, X: int32(math.Floor(float64(x))) >> 4, Z: int32(math.Floor(float64(z))) >> 4}, 0)...)
	list = list[:len(list)-1]
	w.records[string(list)] = append(w.records[string(list)], key...)
	return id
}

// blockEntity adds one to the chunk it lies in.
func (w *world) blockEntity(d chunks.Dimension, id string, x, y, z int32, more ...[]byte) *world {
	key := string(chunks.RecordKey(chunks.Pos{Dim: d, X: x >> 4, Z: z >> 4}, TagBlockEntities))
	tags := append([][]byte{nbtString("id", id), nbtInt("x", x), nbtInt("y", y), nbtInt("z", z)}, more...)
	w.records[key] = append(w.records[key], nbtRecord(tags...)...)
	return w
}

func items(n int) []byte {
	each := make([][]byte, n)
	for i := range each {
		each[i] = nbtCompound(nbtString("Name", "minecraft:made_up"))
	}
	return nbtList("Items", each...)
}

// detailOf is what the survey says the one structure of a kind holds.
func detailOf(t *testing.T, got Survey, d chunks.Dimension, kind Kind) (Structure, *Detail) {
	t.Helper()
	layer := got.Layers[d]
	if len(layer.Details) != len(layer.Recorded) {
		t.Fatalf("%d details for %d structures", len(layer.Details), len(layer.Recorded))
	}
	for i, s := range layer.Recorded {
		if s.Kind == kind {
			return s, layer.Details[i]
		}
	}
	t.Fatalf("no %s among %+v", kind, layer.Recorded)
	return Structure{}, nil
}

func TestDetail_CountsTheSavedMobsInsideABox(t *testing.T) {
	box := Box{0, 39, 0, 15, 61, 15}
	w := newWorld(t).structure(chunks.Overworld, 3, box)
	w.mob(chunks.Overworld, "elder_guardian", 4.5, 45, 4.5)
	w.mob(chunks.Overworld, "elder_guardian", 8.5, 50, 9.2)
	w.mob(chunks.Overworld, "guardian", 1, 40, 1)
	w.mob(chunks.Overworld, "guardian", 2, 40, 1, nbtByte("IsBaby", 1))
	w.mob(chunks.Overworld, "guardian", 3, 40, 1, nbtString("CustomName", "§aBubbles\n the  First"))
	// Above the box, beside it, in another dimension, dead, and not a mob
	// at all: none of these is in it.
	w.mob(chunks.Overworld, "guardian", 4, 62, 4)
	w.mob(chunks.Overworld, "guardian", 16.5, 40, 4)
	w.mob(chunks.Nether, "guardian", 4, 45, 4)
	w.mob(chunks.Overworld, "guardian", 4, 45, 4, nbtByte("Dead", 1))
	w.mob(chunks.Overworld, "armor_stand", 4, 45, 4)
	w.records[string(actorPrefix)+"itemitem"] = nbtRecord(nbtString("identifier", "minecraft:item"), nbtFloats("Pos", 4, 45, 4))
	got := take(t, surveyor(t, nil), w)

	_, detail := detailOf(t, got, chunks.Overworld, Monument)
	want := []MobCount{{Kind: "guardian", Count: 3, Babies: 1}, {Kind: "elder_guardian", Count: 2}}
	if detail.MobsTotal != 5 || !slices.Equal(detail.Mobs, want) {
		t.Errorf("mobs = %d %+v, want 5 %+v", detail.MobsTotal, detail.Mobs, want)
	}
	if detail.Elders == nil || *detail.Elders != 2 {
		t.Errorf("elders = %v, want 2", detail.Elders)
	}
	if want := []NamedMob{{Kind: "guardian", Name: "Bubbles the First"}}; !slices.Equal(detail.Named, want) {
		t.Errorf("named = %+v, want %+v", detail.Named, want)
	}
	if !got.Detailed || got.Contents.Mobs != 8 {
		t.Errorf("detailed %v, contents %+v, want eight mobs kept", got.Detailed, got.Contents)
	}
}

func TestDetail_ListsSpawnersAndSaysWhatContainersHold(t *testing.T) {
	box := Box{16, 48, 16, 31, 72, 31}
	w := newWorld(t).structure(chunks.Nether, 1, box)
	w.blockEntity(chunks.Nether, "MobSpawner", 20, 50, 20, nbtString("EntityIdentifier", "minecraft:blaze")).
		blockEntity(chunks.Nether, "MobSpawner", 18, 60, 30, nbtString("EntityIdentifier", "minecraft:blaze")).
		blockEntity(chunks.Nether, "MobSpawner", 19, 60, 30).
		blockEntity(chunks.Nether, "Chest", 21, 50, 20, nbtString("LootTable", "loot_tables/chests/made_up.json"), items(0)).
		blockEntity(chunks.Nether, "Chest", 22, 50, 20, items(3)).
		blockEntity(chunks.Nether, "Chest", 23, 50, 20, items(0)).
		// A large chest, with something in one half only: one chest.
		blockEntity(chunks.Nether, "Chest", 24, 50, 20, items(0), nbtInt("pairx", 25), nbtInt("pairz", 20)).
		blockEntity(chunks.Nether, "Chest", 25, 50, 20, items(2), nbtInt("pairx", 24), nbtInt("pairz", 20)).
		blockEntity(chunks.Nether, "Barrel", 26, 50, 20, items(1)).
		blockEntity(chunks.Nether, "Cauldron", 27, 50, 20).
		blockEntity(chunks.Nether, "SculkSensor", 28, 50, 20).
		// Outside the box, and one whose own position is another chunk's.
		blockEntity(chunks.Nether, "MobSpawner", 20, 80, 20, nbtString("EntityIdentifier", "minecraft:blaze"))
	key := string(chunks.RecordKey(chunks.Pos{Dim: chunks.Nether, X: 1, Z: 1}, TagBlockEntities))
	w.records[key] = append(w.records[key], nbtRecord(nbtString("id", "Chest"), nbtInt("x", 400), nbtInt("y", 50), nbtInt("z", 20))...)
	got := take(t, surveyor(t, nil), w)

	_, detail := detailOf(t, got, chunks.Nether, Fortress)
	if want := []SpawnerCount{{Mob: "blaze", Count: 2}, {Mob: "unknown", Count: 1}}; !slices.Equal(detail.SpawnerCounts, want) {
		t.Errorf("spawner counts = %+v, want %+v", detail.SpawnerCounts, want)
	}
	wantPlaces := []Spawner{{Mob: "blaze", X: 18, Y: 60, Z: 30}, {Mob: "unknown", X: 19, Y: 60, Z: 30}, {Mob: "blaze", X: 20, Y: 50, Z: 20}}
	if !slices.Equal(detail.Spawners, wantPlaces) || detail.SpawnersMore != 0 {
		t.Errorf("spawners = %+v (+%d), want %+v", detail.Spawners, detail.SpawnersMore, wantPlaces)
	}
	wantHeld := []ContainerCount{{Kind: "chest", Unopened: 1, Holding: 2, Empty: 1}, {Kind: "barrel", Holding: 1}}
	if !slices.Equal(detail.Containers, wantHeld) {
		t.Errorf("containers = %+v, want %+v", detail.Containers, wantHeld)
	}
	if len(detail.Blocks) != 1 || detail.Blocks["cauldron"] != 1 {
		t.Errorf("blocks = %v, want one cauldron", detail.Blocks)
	}
	if got.Contents.Skipped != 1 {
		t.Errorf("skipped = %d, want the one block entity outside its chunk", got.Contents.Skipped)
	}
	// What a container holds is never read: nothing of it is in an answer.
	body, _ := json.Marshal(detail)
	if strings.Contains(string(body), "made_up") {
		t.Errorf("the detail says what is in a container: %s", body)
	}
}

func TestDetail_SetsAVillagesRecordsBesideItsVillagers(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	w := newWorld(t)
	villager := func(profession string, tier int32, more ...[]byte) int64 {
		tags := append([][]byte{nbtInt("TradeTier", tier)}, more...)
		if profession != "" {
			tags = append(tags, nbtString("PreferredProfession", profession))
		}
		return w.mob(chunks.Overworld, "villager_v2", 10, 64, 10, tags...)
	}
	ids := []int64{
		villager("farmer", 0), villager("farmer", 4), villager("librarian", 2, nbtString("CustomName", "Made Up Name")),
		villager("", 0), villager("", 0, nbtByte("IsBaby", 1)),
		// One the village lists and the save does not hold.
		-77,
	}
	golem := w.mob(chunks.Overworld, "iron_golem", 12, 64, 12)
	list := func(ids ...int64) []byte {
		actors := make([][]byte, len(ids))
		for i, id := range ids {
			actors[i] = nbtCompound(nbtLong("ID", id))
		}
		return nbtCompound(nbtList("actors", actors...))
	}
	job := func(name string, x int32) []byte {
		return nbtCompound(nbtByte("Skip", 0), nbtInt("Type", 2), nbtInt("X", x), nbtInt("Y", 64), nbtInt("Z", 3), nbtString("Name", name))
	}
	info := nbtRecord(nbtByte("Initialized", 1), nbtLong("Tick", 4000),
		nbtInt("X0", box.MinX), nbtInt("X1", box.MaxX), nbtInt("Y0", box.MinY), nbtInt("Y1", box.MaxY), nbtInt("Z0", box.MinZ), nbtInt("Z1", box.MaxZ))
	w.village("Overworld", testVillageID, info,
		nbtRecord(nbtList("Dwellers", list(ids...), list(golem, -78), list(), list())),
		poiRecord([][]byte{job("farmer", 1), job("farmer", 2), job("<b>", 3)}, [][]byte{job("farmer", 1), job("librarian", 4)}))
	prefix := "VILLAGE_Overworld_" + testVillageID + "_"
	w.raw(prefix+"PLAYERS", nbtRecord(nbtList("Players",
		nbtCompound(nbtLong("ID", -9001), nbtInt("S", 7)), nbtCompound(nbtLong("ID", -9002), nbtInt("S", -4)))))
	w.raw(prefix+"RAID", nbtRecord(nbtTag(tagCompound, "Raid", nbtCompound(
		nbtByte("GroupNum", 2), nbtByte("NumGroups", 7), nbtByte("NumRaiders", 5), nbtLong("GameTick", 3000)))))
	dir := w.write()
	body := levelDat(levelSeed, 40, -72)
	body = append(body[:len(body)-1], nbtLong("currentTick", 5000)...)
	body = append(body, 0)
	binary.LittleEndian.PutUint32(body[4:], uint32(len(body)-8))
	if err := os.WriteFile(filepath.Join(dir, "level.dat"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := surveyor(t, nil).Take(t.Context(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}

	_, detail := detailOf(t, got, chunks.Overworld, Village)
	v := detail.Village
	if v == nil {
		t.Fatal("a counted village has no detail of its own")
	}
	wantProfessions := []Profession{
		{Profession: "farmer", Count: 2, Levels: [5]int{1, 0, 0, 0, 1}},
		{Profession: "librarian", Count: 1, Levels: [5]int{0, 0, 1, 0, 0}},
		{Profession: "", Count: 1},
	}
	if !slices.Equal(v.Professions, wantProfessions) || v.Babies != 1 || v.Missing != 1 || v.Golems != 1 || v.Cats != 0 {
		t.Errorf("village = %+v, want %+v, one baby, one missing, one golem", *v, wantProfessions)
	}
	if want := []JobSites{{"farmer", 2}, {"librarian", 1}, {"unknown", 1}}; !slices.Equal(v.JobSites, want) {
		t.Errorf("job sites = %+v, want %+v", v.JobSites, want)
	}
	if v.IdleSeconds == nil || *v.IdleSeconds != 50 {
		t.Errorf("idle = %v, want the 1,000 ticks since it was run as 50 seconds", v.IdleSeconds)
	}
	if v.Raid == nil || v.Raid.Wave != 2 || v.Raid.Waves != 7 || v.Raid.Raiders != 5 || v.Raid.IdleSeconds == nil || *v.Raid.IdleSeconds != 100 {
		t.Errorf("raid = %+v, want wave 2 of 7 with 5 raiders, 100 seconds back", v.Raid)
	}
	if s, met := v.Standing(-9002); v.Met != 2 || !met || s != -4 {
		t.Errorf("standing = %d (met %v) of %d, want -4 of two", s, met, v.Met)
	}
	if _, met := v.Standing(-1); met {
		t.Error("the village has a standing for a player it never met")
	}
	// Whose standing is whose is for the server to decide, and is in no
	// answer that every viewer is sent.
	sent, _ := json.Marshal(detail)
	for _, secret := range []string{"9001", "9002", "-4", "\"S\""} {
		if strings.Contains(string(sent), secret) {
			t.Errorf("the detail carries %s: %s", secret, sent)
		}
	}
	if want := []NamedMob{{Kind: "villager_v2", Name: "Made Up Name", Profession: "librarian", Level: 3}}; !slices.Equal(detail.Named, want) {
		t.Errorf("named = %+v, want %+v", detail.Named, want)
	}
}

// PLAYERS and RAID decorate a village. One that cannot be read is counted,
// and the village and everything else known of it stand.
func TestDetail_AVillageStandsWithoutTheRecordsThatDecorateIt(t *testing.T) {
	whole := nbtRecord(nbtList("Players", nbtCompound(nbtLong("ID", -9001), nbtInt("S", 7))))
	raid := nbtRecord(nbtTag(tagCompound, "Raid", nbtCompound(nbtByte("GroupNum", 2), nbtByte("NumGroups", 7), nbtByte("NumRaiders", 5))))
	for name, c := range map[string]struct {
		players, raid []byte
		skipped       int
	}{
		"sound records":             {whole, raid, 0},
		"players cut short":         {whole[:len(whole)-6], raid, 1},
		"players that are not NBT":  {[]byte("players"), raid, 1},
		"players that are no list":  {nbtRecord(nbtInt("Players", 3)), raid, 1},
		"a raid cut short":          {whole, raid[:len(raid)-3], 1},
		"a raid with a count short": {whole, nbtRecord(nbtTag(tagCompound, "Raid", nbtCompound(nbtByte("GroupNum", 2)))), 1},
		"both over the size limit":  {make([]byte, maxVillageRecord+1), make([]byte, maxVillageRecord+1), 2},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t).settled(3, Box{0, 60, 0, 64, 84, 64})
			for key := range w.records {
				if prefix, ok := strings.CutSuffix(key, "INFO"); ok {
					w.raw(prefix+"PLAYERS", c.players).raw(prefix+"RAID", c.raid)
				}
			}
			got := take(t, surveyor(t, nil), w)
			if want := (VillageStats{Found: 1}); got.Villages != want || got.Contents.Skipped != c.skipped {
				t.Errorf("stats = %+v, skipped %d, want %+v and %d", got.Villages, got.Contents.Skipped, want, c.skipped)
			}
			_, detail := detailOf(t, got, chunks.Overworld, Village)
			if detail.Village == nil || (c.skipped == 0) != (detail.Village.Met == 1 && detail.Village.Raid != nil) {
				t.Errorf("village detail = %+v", detail.Village)
			}
		})
	}
}

func TestDetail_FindsTrialChambersAndStrongholdsByTheirBlocks(t *testing.T) {
	w := newWorld(t).
		blockEntity(chunks.Overworld, "TrialSpawner", 100, -20, 100, nbtTag(tagCompound, "spawn_data", nbtCompound(nbtString("TypeId", "minecraft:breeze")))).
		blockEntity(chunks.Overworld, "TrialSpawner", 130, -24, 108).
		blockEntity(chunks.Overworld, "Vault", 150, -22, 140, nbtTag(tagCompound, "config", nbtCompound(nbtString("loot_table", "loot_tables/chests/made_up_ominous.json")))).
		blockEntity(chunks.Overworld, "Vault", 151, -22, 140, nbtTag(tagCompound, "config", nbtCompound(nbtString("loot_table", "loot_tables/chests/made_up.json")))).
		blockEntity(chunks.Overworld, "DecoratedPot", 120, -22, 105, nbtString("LootTable", "loot_tables/pots/made_up.json")).
		blockEntity(chunks.Overworld, "DecoratedPot", 121, -22, 105).
		blockEntity(chunks.Overworld, "Dispenser", 122, -22, 105, nbtString("LootTable", "loot_tables/dispensers/made_up.json")).
		blockEntity(chunks.Overworld, "Dispenser", 123, -22, 105, items(2)).
		blockEntity(chunks.Overworld, "Dropper", 124, -22, 105, items(0)).
		// Too far off to be the same chamber.
		blockEntity(chunks.Overworld, "Vault", 900, -22, 900).
		blockEntity(chunks.Overworld, "MobSpawner", 2000, 30, 2000, nbtString("EntityIdentifier", "minecraft:silverfish")).
		blockEntity(chunks.Overworld, "EndPortal", 2004, 30, 2001).
		blockEntity(chunks.Overworld, "EndPortal", 2005, 30, 2001).
		// Beside the portal room, and so not counted as in it: a chest and
		// a mob's spawner say nothing of the stronghold.
		blockEntity(chunks.Overworld, "Chest", 2003, 30, 2001, items(2)).
		// A room past the chamber's last spawner is the chamber's still,
		// as far as the stated reach and no further.
		blockEntity(chunks.Overworld, "Chest", 151+chamberSurround, -22, 140, items(1)).
		blockEntity(chunks.Overworld, "Chest", 100, -20+chamberSurround/2, 100-chamberSurround, items(0)).
		blockEntity(chunks.Overworld, "Chest", 152+chamberSurround, -22, 140, items(1)).
		blockEntity(chunks.Overworld, "Chest", 100, -19+chamberSurround/2, 100, items(1)).
		// A dungeon's spawner is no stronghold's, and the End's own portal
		// is in every End.
		blockEntity(chunks.Overworld, "MobSpawner", 3000, 30, 3000, nbtString("EntityIdentifier", "minecraft:zombie")).
		blockEntity(chunks.End, "EndPortal", 0, 60, 0).
		blockEntity(chunks.End, "TrialSpawner", 0, 60, 4)
	got := take(t, surveyor(t, nil), w)

	// The End's portal is its exit portal, and is no stronghold; a trial
	// spawner there is no chamber.
	if end := got.Layers[chunks.End].Recorded; len(end) != 1 || end[0].Kind != ExitPortal {
		t.Errorf("found in the End: %+v, want its exit portal and nothing else", end)
	}
	want := []Structure{
		{Kind: Stronghold, Box: Box{2000, 30, 2000, 2005, 30, 2001}, Evidence: 3},
		{Kind: TrialChamber, Box: Box{100, -24, 100, 151, -20, 140}, Evidence: 4, Partial: true},
		{Kind: TrialChamber, Box: Box{900, -22, 900, 900, -22, 900}, Evidence: 1, Partial: true},
	}
	if !slices.Equal(got.Layers[chunks.Overworld].Recorded, want) {
		t.Fatalf("found %+v\nwant  %+v", got.Layers[chunks.Overworld].Recorded, want)
	}
	chamber := got.Layers[chunks.Overworld].Details[1]
	if want := []SpawnerCount{{Mob: "breeze", Count: 1, Trial: true}, {Mob: "unknown", Count: 1, Trial: true}}; !slices.Equal(chamber.SpawnerCounts, want) {
		t.Errorf("chamber spawners = %+v, want %+v", chamber.SpawnerCounts, want)
	}
	if chamber.Blocks["vault"] != 1 || chamber.Blocks["ominous_vault"] != 1 {
		t.Errorf("chamber blocks = %v, want a vault and an ominous one", chamber.Blocks)
	}
	// A pot is a pot and a dispenser a dispenser: neither is a container
	// somebody has or has not opened.
	if want := []ContainerCount{{Kind: "chest", Holding: 1, Empty: 1}}; !slices.Equal(chamber.Containers, want) {
		t.Errorf("chamber containers = %+v, want the two within its reach: %+v", chamber.Containers, want)
	}
	if chamber.Blocks["unbroken_pot"] != 1 || chamber.Blocks["dispenser"] != 2 || chamber.Blocks["dropper"] != 1 {
		t.Errorf("chamber containers = %+v, blocks %v; want no containers, one unbroken pot, two dispensers and a dropper", chamber.Containers, chamber.Blocks)
	}
	// Of a stronghold only what it was found by is said, and not to the
	// block: one room of it is known, and nothing of where the rest is.
	hold := got.Layers[chunks.Overworld].Details[0]
	if want := []SpawnerCount{{Mob: "silverfish", Count: 1}}; !hold.Uncounted || hold.Blocks["end_portal"] != 2 || !slices.Equal(hold.SpawnerCounts, want) ||
		len(hold.Spawners) != 0 || hold.MobsTotal != 0 || len(hold.Containers) != 0 || len(hold.Blocks) != 1 {
		t.Errorf("stronghold = %+v, want it uncounted, with two portal blocks and a silverfish spawner", hold)
	}
	if sent, _ := json.Marshal(hold); strings.Contains(string(sent), "2000") {
		t.Errorf("the stronghold's detail says where its spawner is: %s", sent)
	}
	if chamber.Reach != chamberSurround || chamber.Uncounted || hold.Reach != 0 {
		t.Errorf("reach = %d and %d, want the chamber's contents counted %d blocks past its box", chamber.Reach, hold.Reach, chamberSurround)
	}
}

// Which chamber a block is part of is the square of the generator's grid
// it lies in, with the squares moved back to take in what reaches before
// them. These are the edges of that: the first and last chunk of a square
// on each side, in chunks from the square's own first.
// A chest that still carries the loot table it was generated with has not
// been opened, and the table names the structure it was generated in.
func TestDetail_FindsTheEndsAndTheNethersKindsByWhatIsLeftInThem(t *testing.T) {
	loot := func(name string) []byte { return nbtString("LootTable", "loot_tables/chests/"+name+".json") }
	w := newWorld(t).
		// An end city: two chests nobody has opened, its ship's dragon head
		// and the elytra in their frame, and three shulkers, one of them a
		// way off but in the city's square of the grid.
		blockEntity(chunks.End, "Chest", 1000, 80, 2000, loot("end_city_treasure")).
		blockEntity(chunks.End, "Chest", 1030, 96, 2010, loot("end_city_treasure")).
		blockEntity(chunks.End, "Skull", 1060, 110, 2040, nbtTag(tagByte, "SkullType", []byte{dragonHead})).
		blockEntity(chunks.End, "ItemFrame", 1062, 108, 2040, nbtTag(tagCompound, "Item", nbtCompound(nbtString("Name", "minecraft:elytra")))).
		// A chest somebody has opened, a skull that is no dragon's and a
		// frame holding something else are in it and are not what it is
		// found by.
		blockEntity(chunks.End, "Chest", 1001, 80, 2000, items(3)).
		blockEntity(chunks.End, "Skull", 1002, 80, 2000, nbtTag(tagByte, "SkullType", []byte{1})).
		blockEntity(chunks.End, "ItemFrame", 1003, 80, 2000, nbtTag(tagCompound, "Item", nbtCompound(nbtString("Name", "minecraft:paper")))).
		// A gateway, which nothing breaks, and the exit portal's blocks.
		blockEntity(chunks.End, "EndGateway", 3000, 75, -40).
		blockEntity(chunks.End, "EndPortal", 0, 62, 1).
		blockEntity(chunks.End, "EndPortal", 1, 62, 0).
		// A bastion: the treasure room's chest and spawner, and the chests
		// that any bastion has, three chunks off.
		blockEntity(chunks.Nether, "Chest", 500, 40, 500, loot("bastion_treasure")).
		blockEntity(chunks.Nether, "MobSpawner", 502, 36, 500, nbtString("EntityIdentifier", "minecraft:magma_cube")).
		blockEntity(chunks.Nether, "Chest", 540, 60, 530, loot("bastion_other")).
		// Another, too far off to be the same one, known by its stables.
		blockEntity(chunks.Nether, "Chest", 900, 50, 500, loot("bastion_hoglin_stable")).
		// A blaze spawner is a fortress's, and a fortress's chest no bastion's.
		blockEntity(chunks.Nether, "MobSpawner", 700, 60, 700, nbtString("EntityIdentifier", "minecraft:blaze")).
		blockEntity(chunks.Nether, "Chest", 702, 60, 700, loot("nether_bridge")).
		// A ruined portal in each of two dimensions, by its one chest.
		blockEntity(chunks.Nether, "Chest", -300, 70, 80, loot("ruined_portal")).
		blockEntity(chunks.Overworld, "Chest", -2400, 64, 640, loot("ruined_portal")).
		// And one whose chest has been opened, which is no longer found.
		blockEntity(chunks.Overworld, "Chest", -2500, 64, 640, items(1))
	for _, at := range [][3]float32{{1010.5, 82, 2004.5}, {1011.5, 82, 2004.5}, {1100.5, 120, 2060.5}} {
		w.mob(chunks.End, "shulker", at[0], at[1], at[2])
	}
	w.mob(chunks.Nether, "piglin_brute", 505.5, 40, 501.5)
	w.mob(chunks.Nether, "piglin", 506.5, 40, 501.5)
	got := take(t, surveyor(t, nil), w)

	want := map[chunks.Dimension][]Structure{
		chunks.End: {
			{Kind: EndCity, Box: Box{1000, 80, 2000, 1100, 120, 2060}, Evidence: 7},
			{Kind: EndGateway, Box: Box{3000, 75, -40, 3000, 75, -40}, Evidence: 1},
			{Kind: ExitPortal, Box: Box{0, 62, 0, 1, 62, 1}, Evidence: 2},
		},
		chunks.Nether: {
			{Kind: Bastion, Box: Box{500, 36, 500, 540, 60, 530}, Evidence: 3},
			{Kind: Bastion, Box: Box{900, 50, 500, 900, 50, 500}, Evidence: 1},
			{Kind: RuinedPortal, Box: Box{-300, 70, 80, -300, 70, 80}, Evidence: 1},
		},
		chunks.Overworld: {{Kind: RuinedPortal, Box: Box{-2400, 64, 640, -2400, 64, 640}, Evidence: 1}},
	}
	for d, list := range want {
		if !reflect.DeepEqual(got.Layers[d].Recorded, list) {
			t.Errorf("%s:\n got %+v\nwant %+v", d.Name(), got.Layers[d].Recorded, list)
		}
	}
	// A city says how many shulkers are left in it, and whether its ship's
	// head and elytra are; what a chest holds is never said, only that
	// two of the three have not been opened.
	_, city := detailOf(t, got, chunks.End, EndCity)
	if city.Reach != 32 || city.MobsTotal != 3 || city.Mobs[0] != (MobCount{Kind: "shulker", Count: 3}) || city.Blocks["dragon_head"] != 1 || city.Blocks["elytra"] != 1 ||
		len(city.Containers) != 1 || city.Containers[0] != (ContainerCount{Kind: "chest", Unopened: 2, Holding: 1}) {
		t.Errorf("city = %+v", city)
	}
	sent, _ := json.Marshal(city)
	if strings.Contains(string(sent), "end_city_treasure") || strings.Contains(string(sent), "loot") {
		t.Errorf("a city's details name a loot table: %s", sent)
	}
	// A bastion says which of the four it is, where its blocks still do.
	bastions := map[string]int{}
	for i, r := range got.Layers[chunks.Nether].Recorded {
		if r.Kind == Bastion {
			d := got.Layers[chunks.Nether].Details[i]
			bastions[d.Bastion]++
			if d.Bastion == "treasure" && (d.MobsTotal != 2 || len(d.SpawnerCounts) != 1 || d.SpawnerCounts[0].Mob != "magma_cube") {
				t.Errorf("the treasure bastion = %+v", d)
			}
		}
	}
	if bastions["treasure"] != 1 || bastions["stables"] != 1 {
		t.Errorf("bastions by kind = %v, want one with a treasure room and one with stables", bastions)
	}
}

// The overworld's kinds that leave no record are found the same way, each
// by the loot only it is generated with.
func TestDetail_FindsTheOverworldsKindsByWhatIsLeftInThem(t *testing.T) {
	chest := func(name string) []byte { return nbtString("LootTable", "loot_tables/chests/"+name+".json") }
	sand := func(name string) []byte {
		return nbtString("LootTable", "loot_tables/entities/"+name+"_brushable_block.json")
	}
	w := newWorld(t).
		// An ancient city: chests five chunks apart are one city still.
		blockEntity(chunks.Overworld, "Chest", 1000, -40, 1000, chest("ancient_city")).
		blockEntity(chunks.Overworld, "Chest", 1080, -44, 1010, chest("ancient_city_ice_box")).
		// A mansion, a shipwreck by two of its three chests, and a ruin by
		// a chest and by sand nobody has brushed.
		blockEntity(chunks.Overworld, "Chest", -3000, 70, 500, chest("woodland_mansion")).
		blockEntity(chunks.Overworld, "Chest", 2000, 50, -2000, chest("shipwrecksupply")).
		blockEntity(chunks.Overworld, "Chest", 2006, 50, -2000, chest("shipwrecktreasure")).
		blockEntity(chunks.Overworld, "Chest", 4000, 40, 4000, chest("underwater_ruin_big")).
		blockEntity(chunks.Overworld, "BrushableBlock", 4010, 39, 4020, sand("warm_ocean_ruins")).
		// Sand that has been brushed, or that a player put down, says nothing.
		blockEntity(chunks.Overworld, "BrushableBlock", 4500, 39, 4500).
		blockEntity(chunks.Overworld, "BrushableBlock", 4600, 39, 4500, nbtString("LootTable", "loot_tables/entities/made_up.json")).
		// Two buried treasures in neighbouring chunks are two.
		blockEntity(chunks.Overworld, "Chest", 5000+8, 60, 5000-8, chest("buriedtreasure")).
		blockEntity(chunks.Overworld, "Chest", 5000+24, 60, 5000-8, chest("buriedtreasure")).
		// A pyramid an older game left no record of, by its chests and its
		// sand; and a jungle temple by a chest and a trap.
		blockEntity(chunks.Overworld, "Chest", 6000+9, 52, 6000+10, chest("desert_pyramid")).
		blockEntity(chunks.Overworld, "BrushableBlock", 6000+11, 50, 6000+10, sand("desert_pyramid")).
		blockEntity(chunks.Overworld, "Chest", 7000+3, 60, 7000+8, chest("jungle_temple")).
		blockEntity(chunks.Overworld, "Dispenser", 7000+5, 61, 7000+2, chest("dispenser_trap")).
		blockEntity(chunks.Overworld, "BrushableBlock", 8000, 60, 8000, sand("trail_ruins")).
		// An igloo's chest under an igloo the world has recorded is that
		// igloo, and one under nothing recorded is an igloo found.
		blockEntity(chunks.Overworld, "Chest", 9600+2, 40, 9600+3, chest("igloo_chest")).
		blockEntity(chunks.Overworld, "Chest", 9920+2, 40, 9920+3, chest("igloo_chest")).
		put(chunks.Pos{Dim: chunks.Overworld, X: 600, Z: 600}, TagVolumes, volumes(volumeEntry{name: "minecraft:igloo", box: Box{9600, 69, 9600, 9606, 73, 9607}, scattered: true}))
	got := take(t, surveyor(t, nil), w)

	count := map[Kind]int{}
	evidence := map[Kind]int{}
	for _, r := range got.Layers[chunks.Overworld].Recorded {
		count[r.Kind]++
		evidence[r.Kind] += r.Evidence
	}
	for kind, want := range map[Kind][2]int{
		AncientCity: {1, 2}, Mansion: {1, 1}, Shipwreck: {1, 2}, OceanRuins: {1, 2}, BuriedTreasure: {2, 2},
		DesertPyramid: {1, 2}, JungleTemple: {1, 2}, TrailRuins: {1, 1},
		// One recorded, which has no evidence to its name, and one found.
		Igloo: {2, 1},
	} {
		if count[kind] != want[0] || evidence[kind] != want[1] {
			t.Errorf("%s: %d found by %d blocks, want %d by %d", kind, count[kind], evidence[kind], want[0], want[1])
		}
	}
	// What a kind found by its loot holds says how much of it is left, and
	// never what a chest holds.
	_, ruin := detailOf(t, got, chunks.Overworld, OceanRuins)
	if ruin.Reach != 8 || ruin.Blocks["unbrushed"] != 1 || len(ruin.Containers) != 1 || ruin.Containers[0].Unopened != 1 {
		t.Errorf("ruin = %+v", ruin)
	}
	for _, d := range chunks.Dimensions {
		for _, detail := range got.Layers[d].Details {
			if sent, _ := json.Marshal(detail); strings.Contains(string(sent), "loot") {
				t.Fatalf("a structure's details name a loot table: %s", sent)
			}
		}
	}
	sent, _ := json.Marshal(got.Layers[chunks.Overworld].Recorded)
	if strings.Contains(string(sent), "loot") || strings.Contains(string(sent), "treasure.json") {
		t.Errorf("the list names a loot table: %s", sent)
	}
}

// What a structure a newer game recorded is also found by is the same
// structure, and is left to its record.
func TestDetail_AStructureTheWorldRecordedIsNotAlsoFoundByItsBlocks(t *testing.T) {
	found := []Structure{
		{Kind: RuinedPortal, Box: Box{100, 64, 100, 100, 64, 100}, Evidence: 1},
		{Kind: Bastion, Box: Box{100, 64, 100, 139, 70, 141}, Evidence: 4},
		{Kind: RuinedPortal, Box: Box{100 + 21 + recordedPad, 64, 100, 100 + 21 + recordedPad, 64, 100}, Evidence: 1},
	}
	recorded := []Structure{{Kind: RuinedPortal, Box: Box{90, 60, 90, 120, 80, 110}, Areas: 2}}
	got := unrecorded(slices.Clone(found), recorded)
	if len(got) != 2 || got[0].Kind != Bastion || got[1].MinX != found[2].MinX {
		t.Errorf("kept %+v, want the bastion, which is another kind, and the portal past the recorded one's reach", got)
	}
	if got := unrecorded(slices.Clone(found), nil); len(got) != 3 {
		t.Errorf("with nothing recorded, kept %d of 3", len(got))
	}
}

func TestDetail_AChambersBlocksAreJoinedByTheGeneratorsGrid(t *testing.T) {
	at := func(chunkX, chunkZ int32) (x, z int32) { return chunkX*16 + 3, chunkZ*16 + 9 }
	for name, c := range map[string]struct {
		chunks [][2]int32
		want   int
	}{
		// Far apart, with nothing generated between: one chamber, which a
		// join by nearness would have cut in two.
		"the two ends of a square":         {[][2]int32{{-chamberReach, -chamberReach}, {chamberGrid - chamberReach - 1, chamberGrid - chamberReach - 1}}, 1},
		"a corridor of ungenerated chunks": {[][2]int32{{0, 0}, {12, 0}, {0, 25}}, 1},
		// Next to each other, and two chambers all the same.
		"either side of an edge, going east":  {[][2]int32{{chamberGrid - chamberReach - 1, 4}, {chamberGrid - chamberReach, 4}}, 2},
		"either side of an edge, going south": {[][2]int32{{4, chamberGrid - chamberReach - 1}, {4, chamberGrid - chamberReach}}, 2},
		"either side of the edge before":      {[][2]int32{{-chamberReach, 4}, {-chamberReach - 1, 4}}, 2},
		"across a corner":                     {[][2]int32{{-chamberReach - 1, -chamberReach - 1}, {-chamberReach, -chamberReach}}, 2},
		"a square on each side of the origin": {[][2]int32{{-2 * chamberGrid, 3}, {-chamberGrid, 3}, {0, 3}, {chamberGrid, 3}}, 4},
	} {
		c0 := newContents()
		for _, chunk := range c.chunks {
			x, z := at(chunk[0], chunk[1])
			c0.blocks[chunks.Overworld] = append(c0.blocks[chunks.Overworld], savedBlock{x: x, y: -20, z: z, sort: blockVault})
		}
		if got := locateAll(t, c0); len(got) != c.want {
			t.Errorf("%s: %d chambers %+v, want %d", name, len(got), got, c.want)
		}
	}
}

// A chamber found by fewer blocks than a finished one ever is, is said to
// be there in part, and one found by enough is not.
func TestDetail_AChamberFoundByLittleIsSaidToBeThereInPart(t *testing.T) {
	c := newContents()
	for i := range int32(wholeChamber - 1) {
		c.blocks[chunks.Overworld] = append(c.blocks[chunks.Overworld], savedBlock{x: 40 + i, y: -20, z: 40, sort: blockTrialSpawner})
	}
	for i := range int32(wholeChamber) {
		c.blocks[chunks.Overworld] = append(c.blocks[chunks.Overworld], savedBlock{x: 2000 + i, y: -20, z: 40, sort: blockVault})
	}
	c.blocks[chunks.Overworld] = append(c.blocks[chunks.Overworld], savedBlock{x: 4000, y: 30, z: 40, sort: blockPortal})
	got := locateAll(t, c)
	if len(got) != 3 || got[0].Kind != Stronghold || got[0].Partial ||
		got[1].Evidence != wholeChamber || got[1].Partial || got[2].Evidence != wholeChamber-1 || !got[2].Partial {
		t.Errorf("found %+v, want a stronghold, a whole chamber and one in part", got)
	}
}

// Blocks laid in a line join without end where nearness is what joins
// them. What they join into is no one structure's box, and is not drawn
// across the map. A chamber's cannot: its grid holds it to one square.
func TestDetail_ARunOfBlocksAcrossTheWorldIsNoStructure(t *testing.T) {
	w := newWorld(t)
	for x := int32(0); x <= maxLocatedSpan+64; x += 16 {
		w.blockEntity(chunks.Overworld, "EndPortal", x, 30, 8)
		w.blockEntity(chunks.Overworld, "Vault", x*4, -20, 4000)
	}
	w.blockEntity(chunks.Overworld, "EndPortal", -4000, 30, 40).blockEntity(chunks.Overworld, "EndPortal", -4001, 30, 40)
	got := take(t, surveyor(t, nil), w)
	found := got.Layers[chunks.Overworld].Recorded
	if len(found) == 0 || found[0] != (Structure{Kind: Stronghold, Box: Box{-4001, 30, 40, -4000, 30, 40}, Evidence: 2}) {
		t.Errorf("found %+v, want the one portal first", found)
	}
	for _, s := range found {
		if s.MaxX-s.MinX > maxLocatedSpan || (s.Kind == Stronghold && s.MinX >= 0) {
			t.Errorf("found %+v, which is a run of blocks and no structure", s)
		}
	}
	if got.Contents.Skipped != 1 {
		t.Errorf("skipped = %d, want the one run of portal blocks counted", got.Contents.Skipped)
	}
}

// A box is gone through by its squares or by what the world holds,
// whichever is fewer: a box across a whole world is found the same things
// in, and does not cost a walk of every square of it.
func TestDetail_ABoxAcrossAWorldCostsNoMoreThanWhatTheWorldHolds(t *testing.T) {
	c := newContents()
	c.blocks[chunks.Nether] = []savedBlock{{x: 5, y: 50, z: 5, sort: blockSpawner}, {x: maxCoordinate, y: 50, z: -maxCoordinate, sort: blockCauldron}, {x: 5, y: 500, z: 5, sort: blockBell}}
	c.mobs[chunks.Nether] = []savedMob{{x: -maxCoordinate, y: 50, z: maxCoordinate}, {x: 5, y: -500, z: 5}}
	wide := Box{-maxCoordinate, 0, -maxCoordinate, maxCoordinate, 127, maxCoordinate}
	started := time.Now()
	list, err := c.describe(t.Context(), chunks.Nether, []Structure{{Kind: Fortress, Box: wide}, {Kind: Fortress, Box: Box{0, 0, 0, 15, 127, 15}}}, nil, leveldat.Level{}, &allowance{names: 9, places: 9})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("took %s", took)
	}
	if d := list[0]; d.MobsTotal != 1 || len(d.Spawners) != 1 || d.Blocks["cauldron"] != 1 || d.Blocks["bell"] != 0 {
		t.Errorf("the wide box holds %+v", d)
	}
	if d := list[1]; d.MobsTotal != 0 || len(d.Spawners) != 1 || len(d.Blocks) != 0 {
		t.Errorf("the small box holds %+v", d)
	}
}

func TestDetail_BoundsWhatOneStructureAndOneSurveyList(t *testing.T) {
	box := Box{0, 48, 0, 15, 72, 15}
	w := newWorld(t).structure(chunks.Nether, 1, box).structure(chunks.Nether, 1, Box{320, 48, 320, 335, 72, 335})
	for i := range int32(MaxDetailSpawners + 5) {
		w.blockEntity(chunks.Nether, "MobSpawner", i%16, 48+i/16, 3, nbtString("EntityIdentifier", "minecraft:blaze"))
	}
	for i := range MaxDetailNamed + 3 {
		w.mob(chunks.Nether, "blaze", 4, 50, 4, nbtString("CustomName", "Made Up "+string(rune('A'+i))))
	}
	for i := range MaxDetailKinds + 2 {
		w.mob(chunks.Nether, "made_up_"+string(rune('a'+i%26))+string(rune('a'+i/26)), 5, 50, 5)
	}
	w.blockEntity(chunks.Nether, "MobSpawner", 321, 50, 321, nbtString("EntityIdentifier", strings.Repeat("x", maxTypeName+1)))
	got := take(t, surveyor(t, nil), w)

	detail := got.Layers[chunks.Nether].Details[0]
	if len(detail.Spawners) != MaxDetailSpawners || detail.SpawnersMore != 5 || detail.SpawnerCounts[0].Count != MaxDetailSpawners+5 {
		t.Errorf("%d spawners listed (+%d) of %+v", len(detail.Spawners), detail.SpawnersMore, detail.SpawnerCounts)
	}
	if len(detail.Named) != MaxDetailNamed || detail.NamedMore != 3 {
		t.Errorf("%d named (+%d), want %d (+3)", len(detail.Named), detail.NamedMore, MaxDetailNamed)
	}
	if len(detail.Mobs) != MaxDetailKinds || detail.MobKindsMore != 3 || detail.MobsTotal != MaxDetailNamed+3+MaxDetailKinds+2 {
		t.Errorf("%d kinds (+%d) of %d mobs", len(detail.Mobs), detail.MobKindsMore, detail.MobsTotal)
	}
	// A type that is not an id is not passed on as one.
	if other := got.Layers[chunks.Nether].Details[1]; len(other.Spawners) != 1 || other.Spawners[0].Mob != "unknown" {
		t.Errorf("spawners = %+v, want one of an unknown mob", other.Spawners)
	}

	// And across a survey: once its allowance is spent, the rest are
	// counted and not listed.
	c := newContents()
	c.blocks[chunks.Nether] = []savedBlock{{x: 1, y: 50, z: 1, sort: blockSpawner}, {x: 2, y: 50, z: 1, sort: blockSpawner}}
	c.mobs[chunks.Nether] = []savedMob{{x: 1, y: 50, z: 1, name: "Made Up"}, {x: 2, y: 50, z: 1, name: "Made Up Too"}}
	left := &allowance{names: 1, places: 1}
	list, err := c.describe(t.Context(), chunks.Nether, []Structure{{Kind: Fortress, Box: box}, {Kind: Fortress, Box: box}}, nil, got.Level, left)
	if err != nil {
		t.Fatal(err)
	}
	if len(list[0].Spawners) != 1 || list[0].SpawnersMore != 1 || len(list[0].Named) != 1 || list[0].NamedMore != 1 ||
		len(list[1].Spawners) != 0 || list[1].SpawnersMore != 2 || len(list[1].Named) != 0 || list[1].NamedMore != 2 {
		t.Errorf("first %+v\nsecond %+v", list[0], list[1])
	}
}

func locateAll(t testing.TB, c *contents) []Structure {
	t.Helper()
	found, err := c.locate(context.Background(), chunks.Overworld)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// Finding structures by their blocks is a few lookups a block. At the most
// blocks a dimension is kept, every one of them a kind's own and each in a
// chunk of its own, it is still done in well under the time it is given,
// and it stops when it is told to.
func TestDetail_FindingStructuresAtTheBoundsIsQuickAndCanBeStopped(t *testing.T) {
	c := newContents()
	for i := range int32(maxSavedBlocks) {
		sort := blockVault
		if i%2 == 1 {
			sort = blockPortal
		}
		// A square of chunks 640 a side, one block in each.
		c.blocks[chunks.Overworld] = append(c.blocks[chunks.Overworld], savedBlock{x: i % 640 * 16, y: 30, z: i / 640 * 16, sort: sort})
	}
	started := time.Now()
	found := locateAll(t, c)
	took := time.Since(started)
	t.Logf("%d blocks in %d chunks located as %d structures in %s", maxSavedBlocks, maxSavedBlocks, len(found), took.Round(time.Millisecond))
	if took > detailTimeout/2 {
		t.Errorf("took %s of the %s there is", took, detailTimeout)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := c.locate(stopped, chunks.Overworld); err == nil || got != nil {
		t.Errorf("locate with no time left = %d structures, %v", len(got), err)
	}
}

// Running out of time finding them costs the survey only them.
func TestTake_ThatCannotFindStructuresInTimeServesTheRest(t *testing.T) {
	w := newWorld(t).structure(chunks.Nether, fortressByte, Box{0, 48, 0, 15, 72, 15}).
		blockEntity(chunks.Overworld, "Vault", 40, -20, 40)
	s := surveyor(t, nil)
	if got := take(t, s, w); len(got.Layers[chunks.Overworld].Recorded) != 1 {
		t.Fatalf("with time: %+v", got.Layers[chunks.Overworld].Recorded)
	}
	s.DetailTimeout = time.Nanosecond
	got := take(t, s, w)
	if len(got.Layers[chunks.Overworld].Recorded) != 0 || len(got.Layers[chunks.Nether].Recorded) != 1 {
		t.Errorf("without: overworld %+v, nether %+v; want the fortress and no chamber", got.Layers[chunks.Overworld].Recorded, got.Layers[chunks.Nether].Recorded)
	}
}

func TestDetail_ThatRunsOutOfTimeLeavesTheStructuresWithoutIt(t *testing.T) {
	w := newWorld(t).structure(chunks.Nether, 1, Box{0, 48, 0, 15, 72, 15})
	s := surveyor(t, nil)
	s.DetailTimeout = time.Nanosecond
	got := take(t, s, w)
	if layer := got.Layers[chunks.Nether]; len(layer.Recorded) != 1 || layer.Details != nil || got.Detailed {
		t.Errorf("recorded %+v, details %v, detailed %v: want the structure and no details", layer.Recorded, layer.Details, got.Detailed)
	}
}

// Whatever a record is cut to or overwritten with, reading it is a count
// and never a panic, and nothing of a record that does not parse is kept.
func TestDetail_NoDamageToARecordPanics(t *testing.T) {
	actor := nbtRecord(
		nbtString("identifier", "minecraft:villager_v2"), nbtFloats("Pos", 1, 2, 3), nbtShort("HurtTime", 0),
		nbtLong("UniqueID", -5), nbtInt("TradeTier", 2), nbtString("PreferredProfession", "farmer"),
		nbtString("CustomName", "Made Up"), nbtByte("IsBaby", 1),
		nbtList("Armor", nbtCompound(nbtString("Name", "")), nbtCompound(nbtTag(tagCompound, "Block", nbtCompound(nbtInt("version", 1))))),
	)
	blocks := slices.Concat(
		nbtRecord(nbtString("id", "Chest"), nbtInt("x", 1), nbtInt("y", 2), nbtInt("z", 3), nbtString("LootTable", "made_up"), items(2), nbtInt("pairx", 2), nbtInt("pairz", 3)),
		nbtRecord(nbtString("id", "TrialSpawner"), nbtInt("x", 4), nbtInt("y", 2), nbtInt("z", 3), nbtTag(tagCompound, "spawn_data", nbtCompound(nbtString("TypeId", "minecraft:breeze")))),
		nbtRecord(nbtString("id", "Vault"), nbtInt("x", 5), nbtInt("y", 2), nbtInt("z", 3), nbtTag(tagCompound, "config", nbtCompound(nbtString("loot_table", "made_up")))),
	)
	key := append(slices.Clone(actorPrefix), 1, 2, 3, 4, 5, 6, 7, 8)
	read := func(a, b []byte) *contents {
		c := newContents()
		c.actor(key, a)
		c.place(append(slices.Clone(digpPrefix), 0, 0, 0, 0, 0, 0, 0, 0), key[len(actorPrefix):])
		c.blockEntities(chunks.Pos{}, b)
		return c
	}
	if c := read(actor, blocks); c.stats.Mobs != 1 || c.stats.Blocks != 3 || c.stats.Skipped != 0 {
		t.Fatalf("the whole records: %+v", c.stats)
	}
	for cut := range len(actor) {
		if c := read(actor[:cut], nil); c.stats.Mobs != 0 {
			t.Errorf("an actor cut to %d bytes was kept", cut)
		}
	}
	for cut := range len(blocks) {
		// Whatever is whole before the cut stands; nothing after it does.
		if c := read(nil, blocks[:cut]); c.stats.Blocks > 2 {
			t.Errorf("block entities cut to %d bytes gave %d", cut, c.stats.Blocks)
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20_000 {
		a, b := slices.Clone(actor), slices.Clone(blocks)
		for range 1 + rng.IntN(4) {
			a[rng.IntN(len(a))] = byte(rng.IntN(256))
			b[rng.IntN(len(b))] = byte(rng.IntN(256))
		}
		c := read(a, b)
		// And what was kept can be set inside a box without one either.
		for _, d := range chunks.Dimensions {
			if _, err := c.describe(t.Context(), d, []Structure{{Kind: Monument, Box: Box{-64, -64, -64, 64, 320, 64}}}, nil, leveldat.Level{}, &allowance{names: 9, places: 9}); err != nil {
				t.Fatal(err)
			}
		}
		locateAll(t, c)
	}
}

// A length written in a record is only ever a number to check against the
// bytes that are there.
func TestDetail_ARecordCannotClaimMoreThanItHolds(t *testing.T) {
	huge := binary.LittleEndian.AppendUint32([]byte{tagCompound}, 0x7fffffff)
	deep := nbtCompound()
	for range recordDepth + 1 {
		deep = nbtCompound(nbtTag(tagCompound, "c", deep))
	}
	for name, record := range map[string][]byte{
		"a list that claims a count":  nbtRecord(nbtString("id", "Chest"), nbtTag(tagList, "Items", huge)),
		"a list of nothing, counted":  nbtRecord(nbtString("id", "Chest"), nbtTag(tagList, "Items", binary.LittleEndian.AppendUint32([]byte{tagEnd}, 9))),
		"a string longer than it is":  append(nbtRecord()[:3], tagString, 2, 0, 'i', 'd', 0xff, 0xff, 'x'),
		"an array below nothing":      nbtRecord(nbtTag(tagIntArray, "a", binary.LittleEndian.AppendUint32(nil, 0xfffffff0))),
		"nested past the depth limit": nbtRecord(nbtTag(tagCompound, "c", deep)),
		"a tag of no known type":      nbtRecord(nbtTag(13, "x", nil)),
		"a record over the size limit": nbtRecord(nbtString("identifier", "minecraft:zombie"), nbtShort("HurtTime", 0),
			nbtTag(tagByteArray, "pad", append(binary.LittleEndian.AppendUint32(nil, maxContentRecord), make([]byte, maxContentRecord)...))),
	} {
		c := newContents()
		c.blockEntities(chunks.Pos{}, record)
		c.actor(append(slices.Clone(actorPrefix), 1, 2, 3, 4, 5, 6, 7, 8), append(slices.Clone(record), mobTag...))
		if c.stats.Blocks != 0 || len(c.unplaced) != 0 || c.stats.Skipped != 2 {
			t.Errorf("%s: %+v, %d mobs waiting; want both records skipped", name, c.stats, len(c.unplaced))
		}
	}
}

func TestDetail_BoundsHowMuchOfAWorldIsKept(t *testing.T) {
	c := newContents()
	record := nbtRecord(nbtString("identifier", "minecraft:zombie"), nbtFloats("Pos", 1, 2, 3), nbtShort("HurtTime", 0))
	for i := range maxSavedMobs + 7 {
		c.actor(binary.BigEndian.AppendUint64(slices.Clone(actorPrefix), uint64(i)), record)
	}
	if len(c.unplaced) != maxSavedMobs || c.stats.MobsOver != 7 {
		t.Errorf("%d mobs kept, %d over", len(c.unplaced), c.stats.MobsOver)
	}
	names := newTypeNames()
	for i := range maxTypeNames + 50 {
		names.of([]byte("made_up_" + strings.Repeat("a", i%40) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))))
	}
	if len(names.names) > maxTypeNames+1 || names.names[names.of([]byte("one_more"))] != "unknown" {
		t.Errorf("%d type names kept", len(names.names))
	}
}

func FuzzContents(f *testing.F) {
	f.Add(nbtRecord(nbtString("identifier", "minecraft:zombie"), nbtFloats("Pos", 1, 2, 3), nbtShort("HurtTime", 0), nbtLong("UniqueID", -5)))
	f.Add(nbtRecord(nbtString("id", "Chest"), nbtInt("x", 1), nbtInt("y", 2), nbtInt("z", 3), nbtString("LootTable", "made_up"), items(2)))
	f.Add(nbtRecord(nbtString("id", "Vault"), nbtInt("x", 5), nbtInt("y", 2), nbtInt("z", 3), nbtTag(tagCompound, "config", nbtCompound(nbtString("loot_table", "made_up")))))
	key := append(slices.Clone(actorPrefix), 1, 2, 3, 4, 5, 6, 7, 8)
	f.Fuzz(func(t *testing.T, record []byte) {
		c := newContents()
		c.actor(key, record)
		c.place(append(slices.Clone(digpPrefix), 0, 0, 0, 0, 0, 0, 0, 0), key[len(actorPrefix):])
		c.blockEntities(chunks.Pos{}, record)
		for _, b := range c.blocks[chunks.Overworld] {
			if b.x>>4 != 0 || b.z>>4 != 0 {
				t.Fatalf("kept a block entity at %d, %d from the chunk at the origin", b.x, b.z)
			}
		}
		for _, m := range c.mobs[chunks.Overworld] {
			if max(m.x, m.y, m.z) > maxCoordinate || min(m.x, m.y, m.z) < -maxCoordinate || m.tier < -1 || m.tier > 4 {
				t.Fatalf("kept a mob as %+v", m)
			}
		}
		locateAll(t, c)
		if _, err := c.describe(t.Context(), chunks.Overworld, []Structure{{Kind: Monument, Box: Box{-64, -64, -64, 64, 320, 64}}}, nil, leveldat.Level{}, &allowance{names: 9, places: 9}); err != nil {
			t.Fatal(err)
		}
	})
}
