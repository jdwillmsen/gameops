package structures

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// seedEntry is one entry of the dictionary: the hash chunks name it by and
// the seed it says they were generated from, or nil for one that says none.
type seedEntry struct {
	hash uint64
	seed *int64
}

func dictionary(entries ...seedEntry) []byte {
	out := binary.LittleEndian.AppendUint32(nil, uint32(len(entries)))
	for _, e := range entries {
		out = binary.LittleEndian.AppendUint64(out, e.hash)
		out = append(out, 10, 0, 0)
		out = append(out, nbtString("DimensionName", "Overworld")...)
		if e.seed != nil {
			out = append(out, nbtLong("GenerationSeed", *e.seed)...)
		}
		out = append(out, 0)
	}
	return out
}

// The two seeds of a world that was given a new one part way: what its
// older chunks were generated from, and what level.dat now holds.
const (
	olderLow   = 777
	olderSeed  = int64(0x0bad<<32 | olderLow)
	olderHash  = uint64(0x1111)
	newerHash  = uint64(0x2222)
	silentHash = uint64(0x3333)
)

func ptr(v int64) *int64 { return &v }

func TestSeedBook_SaysWhichSeedEachEntrysChunksAreOf(t *testing.T) {
	book, err := readSeedBook(dictionary(
		seedEntry{olderHash, ptr(olderSeed)}, seedEntry{newerHash, ptr(levelSeed)},
		seedEntry{0x4444, ptr(olderSeed)}, seedEntry{silentHash, nil}))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.seeds) != 2 || book.seeds[0] != olderSeed || book.seeds[1] != levelSeed {
		t.Errorf("seeds = %v", book.seeds)
	}
	for hash, want := range map[uint64]int8{olderHash: 0, newerHash: 1, 0x4444: 0, silentHash: seedUnknown} {
		if got, held := book.byHash[hash]; !held || got != want {
			t.Errorf("entry %x is of seed %d (held %v), want %d", hash, got, held, want)
		}
	}
}

// Half a dictionary would give some chunks a seed and leave others that
// have one looking as though they had none.
func TestSeedBook_RefusesADictionaryThatIsNotWhole(t *testing.T) {
	sound := dictionary(seedEntry{olderHash, ptr(olderSeed)}, seedEntry{newerHash, ptr(levelSeed)})
	for cut := range len(sound) {
		if book, err := readSeedBook(sound[:cut]); err == nil {
			t.Errorf("cut to %d of %d bytes: read as %+v", cut, len(sound), book)
		}
	}
	for name, value := range map[string][]byte{
		"a byte too many":   append(append([]byte{}, sound...), 0),
		"more than it has":  append([]byte{9, 0, 0, 0}, sound[4:]...),
		"a count past sane": append([]byte{0xff, 0xff, 0xff, 0x7f}, sound[4:]...),
	} {
		if book, err := readSeedBook(value); err == nil {
			t.Errorf("%s: read as %+v", name, book)
		}
	}
	for i := range sound {
		changed := append([]byte{}, sound...)
		changed[i] ^= 0xff
		_, _ = readSeedBook(changed)
	}
}

func TestSeedBook_TellsApartOnlySoManySeeds(t *testing.T) {
	var entries []seedEntry
	for i := range maxSeeds + 3 {
		entries = append(entries, seedEntry{uint64(100 + i), ptr(int64(i))})
	}
	book, err := readSeedBook(dictionary(entries...))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.seeds) != maxSeeds || book.over != 3 || book.byHash[uint64(100+maxSeeds)] != seedUnknown {
		t.Errorf("%d seeds kept and %d over, the next of seed %d", len(book.seeds), book.over, book.byHash[uint64(100+maxSeeds)])
	}
}

// seeded marks a chunk as complete and as generated from the seed of one
// of the dictionary's entries.
func (w *world) seeded(d chunks.Dimension, x, z int32, hash uint64) *world {
	return w.generated(d, x, z).put(chunks.Pos{Dim: d, X: x, Z: z}, TagGeneration, binary.LittleEndian.AppendUint64(nil, hash))
}

// twoSeeds is a world whose older chunks, west of the line x = 0, were
// generated from one seed and whose newer ones from the seed in level.dat,
// each holding three monuments where its own seed puts them.
func twoSeeds(t *testing.T) (w *world, older, newer []Site) {
	w = newWorld(t)
	w.records[dictionaryKey] = dictionary(seedEntry{olderHash, ptr(olderSeed)}, seedEntry{newerHash, ptr(levelSeed)}, seedEntry{silentHash, nil})
	for rz := int32(-1); rz <= 1; rz++ {
		o, _ := monumentSpread.site(olderLow, -2, rz)
		n, _ := monumentSpread.site(testSeed, 1, rz)
		older, newer = append(older, o), append(newer, n)
		w.seeded(chunks.Overworld, o.ChunkX, o.ChunkZ, olderHash).structure(chunks.Overworld, monumentByte, monumentAt(o))
		w.seeded(chunks.Overworld, n.ChunkX, n.ChunkZ, newerHash).structure(chunks.Overworld, monumentByte, monumentAt(n))
	}
	return w, older, newer
}

// A chunk is what its own seed made it. One seed for the whole world puts
// half of this world's monuments nowhere, and the other half's sites over
// country that has none.
func TestTake_WorksASiteOutFromTheSeedOfItsOwnChunk(t *testing.T) {
	w, older, newer := twoSeeds(t)
	// Where each seed would put a monument in the other's country, both
	// chunks finished, in deep ocean, and holding none.
	stray, _ := monumentSpread.site(testSeed, -3, 0)
	other, _ := monumentSpread.site(olderLow, 2, 0)
	w.seeded(chunks.Overworld, stray.ChunkX, stray.ChunkZ, olderHash).seeded(chunks.Overworld, other.ChunkX, other.ChunkZ, newerHash)
	// And a site of each in its own country with none: those the world
	// does disagree with.
	bare, _ := monumentSpread.site(olderLow, -3, 1)
	w.seeded(chunks.Overworld, bare.ChunkX, bare.ChunkZ, olderHash)
	// A chunk that names no seed says nothing either way, of the site in
	// it or of the monument recorded in it.
	silent, _ := monumentSpread.site(testSeed, 2, 1)
	w.seeded(chunks.Overworld, silent.ChunkX, silent.ChunkZ, silentHash)
	w.seeded(chunks.Overworld, silent.ChunkX+25, silent.ChunkZ, silentHash).structure(chunks.Overworld, monumentByte, monumentAt(Site{silent.ChunkX + 25, silent.ChunkZ}))

	s := surveyor(t, nil)
	s.Biomes = func(chunks.Dimension, int32, int32) (uint32, bool) { return biomeDeepOcean, true }
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]
	if got.Check.State != SeedVerified || k.State != SeedVerified || k.Agree != 6 || k.Disagree != 0 || k.SetAside != 0 {
		t.Fatalf("check = %s, monuments %+v; want all six on a site of their own chunks' seed", got.Check.State, k)
	}
	if got.Seeds != 2 || got.Seedless != 2 || got.StructureSeed != testSeed {
		t.Errorf("%d seeds, %d chunks naming none, new chunks from %d", got.Seeds, got.Seedless, got.StructureSeed)
	}
	at := map[Site]Prediction{}
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		if p.Kind == Monument {
			at[siteOf(p)] = p
		}
	}
	for _, site := range append(append([]Site{}, older...), newer...) {
		if _, offered := at[site]; offered {
			t.Errorf("a site with its monument recorded is predicted as well: %+v", site)
		}
	}
	if p, offered := at[bare]; !offered || !p.Generated {
		t.Errorf("the older seed's empty site in its own finished chunk = %+v (offered %v), want it predicted", p, offered)
	}
	for name, site := range map[string]Site{"the newer seed's site in an older chunk": stray, "the older seed's site in a newer chunk": other, "a site in a chunk that names no seed": silent} {
		if p, offered := at[site]; offered {
			t.Errorf("%s is predicted: %+v", name, p)
		}
	}
	// Country nobody has generated will be made by the seed in level.dat.
	ahead, _ := monumentSpread.site(testSeed, 0, 0)
	behind, _ := monumentSpread.site(olderLow, 0, 0)
	if p, offered := at[ahead]; !offered || !p.Candidate {
		t.Errorf("the current seed's site in country not generated = %+v (offered %v), want a candidate", p, offered)
	}
	if _, offered := at[behind]; offered && behind != ahead {
		t.Error("the older seed's site is offered in country it will never generate")
	}
}

// A game that placed a kind another way leaves the chunks of its seed
// disagreeing with the rule. The rule is not used for those chunks, and is
// neither refuted by them nor borne out by them elsewhere.
func TestTake_SetsARuleAsideForTheSeedWhoseChunksContradictIt(t *testing.T) {
	w, _, _ := twoSeeds(t)
	// Four monuments in the older country where its seed puts none.
	for i := int32(0); i < 4; i++ {
		x, z := int32(-200-40*i), int32(-180)
		for {
			near, _ := monumentSpread.site(olderLow, floorDiv(x, 32), floorDiv(z, 32))
			if near != (Site{x, z}) {
				break
			}
			x++
		}
		w.seeded(chunks.Overworld, x, z, olderHash).structure(chunks.Overworld, monumentByte, monumentAt(Site{x, z}))
	}
	bare, _ := monumentSpread.site(olderLow, -3, 1)
	w.seeded(chunks.Overworld, bare.ChunkX, bare.ChunkZ, olderHash)
	s := surveyor(t, nil)
	s.Biomes = func(chunks.Dimension, int32, int32) (uint32, bool) { return biomeDeepOcean, true }
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]
	if k.State != SeedVerified || k.Agree != 6 || k.Disagree != 4 || k.SetAside != 1 {
		t.Fatalf("monuments = %+v, want verified by the newer chunks with the older seed set aside", k)
	}
	// What an older game did otherwise says nothing of whether the seeds
	// are right: the three monuments the newer seed explains are what the
	// seed is believed by, and the seven of the seed set aside are not
	// counted for it or against it.
	if got.Check.State != SeedVerified || got.Check.Agree != 3 || got.Check.Disagree != 0 {
		t.Errorf("the seeds are %s by %d agreeing and %d not, want verified by the three the seed set aside has no part in", got.Check.State, got.Check.Agree, got.Check.Disagree)
	}
	candidates := 0
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		if siteOf(p) == bare {
			t.Errorf("a site of the seed set aside is predicted: %+v", p)
		}
		if p.Candidate {
			candidates++
		}
	}
	if candidates == 0 {
		t.Error("nothing is offered in country not generated, which the seed that is borne out will make")
	}
}

// An operator's seed is used for every chunk, whatever the world says.
func TestTake_AnOperatorsSeedIsUsedWhateverTheChunksSay(t *testing.T) {
	w, _, _ := twoSeeds(t)
	s := surveyor(t, nil)
	seed := uint32(testSeed)
	s.StructureSeed = &seed
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]; k.Agree != 3 || k.Disagree != 3 || got.Seeds != 0 {
		t.Errorf("monuments = %+v under %d seeds, want the three of the operator's seed on a site and the rest not", k, got.Seeds)
	}
}

// A dictionary that cannot be read is a world that does not say, which is
// what every world was before the game kept one.
func TestTake_AWorldWhoseDictionaryIsDamagedIsAllOfOneSeed(t *testing.T) {
	w, _, _ := twoSeeds(t)
	w.records[dictionaryKey] = w.records[dictionaryKey][:20]
	got, err := surveyor(t, nil).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if k := got.Check.Kinds[Rule{Monument, chunks.Overworld}]; k.Agree != 3 || k.Disagree != 3 || got.Seeds != 0 || got.Malformed != 1 {
		t.Errorf("monuments = %+v under %d seeds with %d malformed", k, got.Seeds, got.Malformed)
	}
}

// A world of more seeds than are told apart says so, since the chunks of
// the rest are then treated as naming none.
func TestTake_SaysWhenTheWorldNamesMoreSeedsThanAreToldApart(t *testing.T) {
	w := newWorld(t)
	var entries []seedEntry
	for i := range maxSeeds + 2 {
		entries = append(entries, seedEntry{uint64(100 + i), ptr(int64(i + 1))})
		w.seeded(chunks.Overworld, int32(i), 0, uint64(100+i))
	}
	w.records[dictionaryKey] = dictionary(entries...)
	log := &bytes.Buffer{}
	got, err := surveyor(t, log).Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got.SeedsOver != 2 || got.Seedless != 2 || !strings.Contains(log.String(), "more seeds than are told apart") || !strings.Contains(log.String(), "entries_over=2") {
		t.Errorf("%d entries over and %d chunks naming none; log:\n%s", got.SeedsOver, got.Seedless, log)
	}
}

// A kind the owner withholds is not recorded, found or predicted, so that
// nothing downstream of a survey has one to give away.
func TestTake_LeavesOutEveryKindTheOwnerWithholds(t *testing.T) {
	build := func() *world {
		w, _, _ := twoSeeds(t)
		w.blockEntity(chunks.Overworld, "Chest", 5000+8, 60, 5000-8, nbtString("LootTable", "loot_tables/chests/buriedtreasure.json"))
		w.put(chunks.Pos{Dim: chunks.Overworld, X: 600, Z: 600}, TagVolumes, volumes(volumeEntry{name: "minecraft:igloo", box: Box{9600, 69, 9600, 9606, 73, 9607}, scattered: true}))
		return w
	}
	kinds := func(got Survey) map[Kind]int {
		out := map[Kind]int{}
		for _, r := range got.Layers[chunks.Overworld].Recorded {
			out[r.Kind]++
		}
		for _, p := range got.Layers[chunks.Overworld].Predicted {
			out[p.Kind]++
		}
		return out
	}
	open := take(t, surveyor(t, nil), build())
	if got := kinds(open); got[BuriedTreasure] != 1 || got[Igloo] != 1 || got[Monument] == 0 {
		t.Fatalf("with nothing withheld: %v", got)
	}
	s := surveyor(t, nil)
	s.Withheld = []Kind{BuriedTreasure, Igloo, Monument}
	held := take(t, s, build())
	if got := kinds(held); got[BuriedTreasure] != 0 || got[Igloo] != 0 || got[Monument] != 0 {
		t.Errorf("withheld kinds are in the survey: %v", got)
	}
	for _, kind := range s.Withheld {
		if k, judged := held.Check.Kinds[Rule{kind, chunks.Overworld}]; judged {
			t.Errorf("%s is withheld and its rule was worked out all the same: %+v", kind, k)
		}
	}
}
