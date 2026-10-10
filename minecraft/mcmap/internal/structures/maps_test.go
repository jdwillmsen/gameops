package structures

import (
	"fmt"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// mark is one mark on a map as the game writes it: what it is drawn as,
// where on the map, and the block it stands for.
func mark(sort, x, z int32) []byte {
	return nbtCompound(
		nbtTag(tagCompound, "data", nbtCompound(nbtInt("rot", 8), nbtInt("type", sort), nbtInt("x", 40), nbtInt("y", -12))),
		nbtTag(tagCompound, "key", nbtCompound(nbtInt("blockX", x), nbtInt("blockY", -32768), nbtInt("blockZ", z), nbtInt("type", 1))),
	)
}

func mapRecord(dimension byte, marks ...[]byte) []byte {
	return nbtRecord(
		nbtTag(tagByteArray, "colors", []byte{4, 0, 0, 0, 1, 2, 3, 4}),
		nbtList("decorations", marks...),
		nbtByte("dimension", dimension),
		nbtLong("mapId", 77),
	)
}

func targetsOf(c *contents) map[target]struct{} { return c.targets }

func TestMaps_KeepWhereAWoodlandExplorerMapPoints(t *testing.T) {
	c := newContents()
	// The mansion's mark, with the player's own beside it, and the same
	// map again at another scale, as the game keeps five of each.
	c.mapRecord(mapRecord(0, mark(0, 10, 10), mark(markMansion, 4008, -2296)))
	c.mapRecord(mapRecord(0, mark(markMansion, 4008, -2296)))
	// A monument's mark, a mansion's on a map of nowhere there is, and a
	// map a player drew, which points nowhere.
	c.mapRecord(mapRecord(0, mark(15, 800, 800)))
	c.mapRecord(mapRecord(7, mark(markMansion, 96, 96)))
	c.mapRecord(mapRecord(0))
	want := target{Mansion, chunks.Overworld, Site{250, -144}}
	if got := targetsOf(c); len(got) != 1 || c.stats.Skipped != 0 {
		t.Fatalf("targets %+v with %d skipped, want the one mansion", got, c.stats.Skipped)
	}
	if _, held := c.targets[want]; !held {
		t.Errorf("targets %+v, want %+v", c.targets, want)
	}
}

// A map is somebody's drawing as well as the game's, and a damaged one
// must cost a count and no more.
func TestMaps_ADamagedMapIsSkippedAndCounted(t *testing.T) {
	sound := mapRecord(0, mark(markMansion, 4008, -2296))
	for cut := 1; cut < len(sound); cut++ {
		c := newContents()
		c.mapRecord(sound[:cut])
		if len(c.targets) != 0 {
			t.Fatalf("a map cut to %d of %d bytes points somewhere: %+v", cut, len(sound), c.targets)
		}
	}
	for i := range sound {
		for _, v := range []byte{0, 1, 9, 10, 0x7f, 0xff} {
			changed := append([]byte{}, sound...)
			changed[i] = v
			newContents().mapRecord(changed)
		}
	}
	// A place past the edge of any world is not a place.
	c := newContents()
	c.mapRecord(mapRecord(0, mark(markMansion, 2_000_000_000, 0)))
	if len(c.targets) != 0 || c.stats.Skipped != 1 {
		t.Errorf("a map pointing past the edge of the world: %+v, %d skipped", c.targets, c.stats.Skipped)
	}
}

func TestMaps_KeepOnlySoManyPlaces(t *testing.T) {
	c := newContents()
	for i := range int32(maxMapTargets + 5) {
		c.mapRecord(mapRecord(0, mark(markMansion, i*16, 0)))
	}
	if len(c.targets) != maxMapTargets || c.stats.TargetsOver != 5 {
		t.Errorf("%d places kept and %d over, want %d and 5", len(c.targets), c.stats.TargetsOver, maxMapTargets)
	}
}

// A world with no mansion built, whose maps say where the game would put
// three: the rule is checked by them, and the one still to be built is on
// the map as more than a possible site.
func TestTake_ChecksTheMansionRuleByTheWorldsOwnMaps(t *testing.T) {
	w, _, newer := twoSeeds(t)
	_ = newer
	for i, region := range [][2]int32{{2, 2}, {3, 2}, {2, 3}} {
		site, _ := mansionSpread.site(testSeed, region[0], region[1])
		w.records[fmt.Sprintf("map_%d", i)] = mapRecord(0, mark(markMansion, site.ChunkX*16+8, site.ChunkZ*16+8))
	}
	got, err := surveyor(t, nil).Take(t.Context(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	k := got.Check.Kinds[Rule{Mansion, chunks.Overworld}]
	if k.State != SeedVerified || k.Agree != 3 || got.Contents.Targets != 3 {
		t.Fatalf("mansions = %+v from %d map targets", k, got.Contents.Targets)
	}
	mapped := 0
	for _, p := range got.Layers[chunks.Overworld].Predicted {
		if p.Kind == Mansion && p.Mapped && !p.Candidate {
			mapped++
		}
	}
	if mapped != 3 {
		t.Errorf("%d mansions offered as mapped, want the three the maps point at", mapped)
	}
}
