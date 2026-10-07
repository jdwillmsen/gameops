package structures

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
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
		if _, err := c.describe(t.Context(), chunks.Overworld, []Structure{{Kind: Monument, Box: Box{-64, -64, -64, 64, 320, 64}}}, nil, leveldat.Level{}, &allowance{names: 9, places: 9}); err != nil {
			t.Fatal(err)
		}
	})
}
