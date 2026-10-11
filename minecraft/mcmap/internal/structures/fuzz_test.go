package structures

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// The three records read for the newer kinds are whatever a world's files
// hold: a damaged or hostile one must cost a count and never a panic, a
// box outside its own chunk or a place past the edge of the world. Each
// target starts from records built here the way the game writes them.
//
//	go test -run '^$' -fuzz FuzzVolumes -fuzztime 1m ./minecraft/mcmap/internal/structures/

func FuzzVolumes(f *testing.F) {
	f.Add(volumes(
		volumeEntry{name: "minecraft:trail_ruins", box: inChunk(2, 61, 7, 15, 82, 15), whole: true},
		volumeEntry{name: "minecraft:trail_ruins", box: inChunk(5, 76, 11, 9, 80, 15)},
		volumeEntry{name: "minecraft:igloo", box: inChunk(0, 69, 0, 6, 73, 7), scattered: true},
		volumeEntry{name: "minecraft:abandoned_camp_taiga", box: inChunk(6, 63, 0, 15, 70, 8), whole: true}))
	f.Add(volumes())
	f.Add([]byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0x7f})
	f.Fuzz(func(t *testing.T, raw []byte) {
		found, unknown, malformed, err := decodeVolumes(volumeChunk, raw)
		if err != nil && (len(found) != 0 || unknown != 0 || malformed != 0) {
			t.Fatalf("a record refused still gave %d boxes, %d unknown, %d malformed", len(found), unknown, malformed)
		}
		if len(found) > maxVolumeBoxes*2 {
			t.Fatalf("%d boxes from one record", len(found))
		}
		for _, p := range found {
			if !p.box.within(80, -48, 95, -33) || p.box.MinY > p.box.MaxY || p.kind == "" || len(p.variant) > maxVolumeName {
				t.Fatalf("kept %+v", p)
			}
		}
	})
}

func FuzzSeedBook(f *testing.F) {
	f.Add(dictionary(seedEntry{olderHash, ptr(olderSeed)}, seedEntry{newerHash, ptr(levelSeed)}, seedEntry{silentHash, nil}))
	f.Add(dictionary())
	f.Add([]byte{0xff, 0xff, 0xff, 0x7f, 1, 2, 3})
	f.Fuzz(func(t *testing.T, raw []byte) {
		book, err := readSeedBook(raw)
		if err != nil {
			if book != nil {
				t.Fatal("a dictionary refused still gave seeds")
			}
			return
		}
		if len(book.seeds) > maxSeeds || len(book.byHash) > maxDictionaryEntries {
			t.Fatalf("%d seeds over %d entries", len(book.seeds), len(book.byHash))
		}
		for hash, at := range book.byHash {
			if at != seedUnknown && (at < 0 || int(at) >= len(book.seeds)) {
				t.Fatalf("entry %x is of seed %d of %d", hash, at, len(book.seeds))
			}
		}
	})
}

func FuzzMapRecord(f *testing.F) {
	f.Add(mapRecord(0, mark(0, 10, 10), mark(markMansion, 4008, -2296)))
	f.Add(mapRecord(7, mark(markMansion, 96, 96)))
	f.Add(nbtRecord(nbtList("decorations", mark(markMansion, 4008, -2296))))
	f.Add(mapRecord(0))
	f.Fuzz(func(t *testing.T, raw []byte) {
		c := newContents()
		c.mapRecord(raw)
		if len(c.targets) > maxMapTargets {
			t.Fatalf("%d places from one map", len(c.targets))
		}
		for at := range c.targets {
			if at.kind != Mansion || at.dim < chunks.Overworld || at.dim > chunks.End ||
				at.site.ChunkX < -maxCoordinate/16 || at.site.ChunkX > maxCoordinate/16 || at.site.ChunkZ < -maxCoordinate/16 || at.site.ChunkZ > maxCoordinate/16 {
				t.Fatalf("a map points at %+v", at)
			}
		}
	})
}
