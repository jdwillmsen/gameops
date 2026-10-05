package markers

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/df-mc/goleveldb/leveldb"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

type entry struct{ k, v []byte }

func blockEntities(t testing.TB, dim, cx, cz int32, records ...map[string]any) entry {
	t.Helper()
	k := binary.LittleEndian.AppendUint32(nil, uint32(cx))
	k = binary.LittleEndian.AppendUint32(k, uint32(cz))
	if dim != 0 {
		k = binary.LittleEndian.AppendUint32(k, uint32(dim))
	}
	var v []byte
	for _, r := range records {
		v = append(v, record(t, r)...)
	}
	return entry{append(k, tagBlockEntities), v}
}

func storageKey(id int64) []byte { return binary.LittleEndian.AppendUint64(nil, uint64(id)) }

func actor(t testing.TB, id int64, r map[string]any) entry {
	t.Helper()
	return entry{append([]byte("actorprefix"), storageKey(id)...), record(t, r)}
}

// digp is a chunk's list of the actors in it.
func digp(dim, cx, cz int32, ids ...int64) entry {
	k := binary.LittleEndian.AppendUint32([]byte("digp"), uint32(cx))
	k = binary.LittleEndian.AppendUint32(k, uint32(cz))
	if dim != 0 {
		k = binary.LittleEndian.AppendUint32(k, uint32(dim))
	}
	var v []byte
	for _, id := range ids {
		v = append(v, storageKey(id)...)
	}
	return entry{k, v}
}

// world builds a LevelDB holding entries, closed and without a LOCK file,
// the way a mirrored world sits on disk.
func world(t testing.TB, entries ...entry) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := db.Put(e.k, e.v, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "LOCK")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scanOf(t testing.TB, entries ...entry) (World, Stats) {
	t.Helper()
	db, done, err := chunks.OpenView(world(t, entries...), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	w, stats, err := Scan(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return w, stats
}

func bedAt(x, y, z int32, color uint8) map[string]any {
	return map[string]any{"id": "Bed", "x": x, "y": y, "z": z, "color": color}
}

var stack = []any{map[string]any{"Name": "minecraft:dirt", "Count": uint8(64), "Slot": uint8(0)}}

func chestAt(x, y, z int32, more map[string]any) map[string]any {
	m := map[string]any{"id": "Chest", "x": x, "y": y, "z": z, "Items": stack}
	for k, v := range more {
		m[k] = v
	}
	return m
}

func TestScan_FindsBedsContainersAndNamedMobsByDimension(t *testing.T) {
	w, stats := scanOf(t,
		blockEntities(t, 0, 0, 0,
			bedAt(3, 64, 5, 14), bedAt(3, 64, 6, 14),
			map[string]any{"id": "MobSpawner", "x": int32(1), "y": int32(10), "z": int32(1)},
			chestAt(8, 70, 9, map[string]any{"CustomName": "Diamonds"}),
		),
		blockEntities(t, 0, -1, -2,
			map[string]any{"id": "Barrel", "x": int32(-2), "y": int32(60), "z": int32(-20), "Items": stack},
		),
		blockEntities(t, 1, 2, 2,
			map[string]any{"id": "ShulkerBox", "x": int32(40), "y": int32(33), "z": int32(41), "Items": stack},
			bedAt(33, 90, 34, 0),
		),
		actor(t, 101, map[string]any{"identifier": "minecraft:cat", "CustomName": "OJ", "Pos": []any{float32(173.76), float32(66), float32(263.24)}}),
		actor(t, 102, map[string]any{"identifier": "minecraft:strider", "CustomName": "Lava Lad", "Pos": []any{float32(-10.5), float32(31), float32(-0.5)}}),
		actor(t, 103, map[string]any{"identifier": "minecraft:cow", "Pos": []any{float32(1), float32(2), float32(3)}}),
		digp(0, 10, 16, 101, 103),
		digp(1, -1, -1, 102),
	)

	ow, nether, end := w[chunks.Overworld], w[chunks.Nether], w[chunks.End]
	if want := []Marker{{X: 3, Y: 64, Z: 5}}; !reflect.DeepEqual(ow.Beds, want) {
		t.Errorf("overworld beds = %v, want one bed for its two halves: %v", ow.Beds, want)
	}
	if want := []Marker{{X: 8, Y: 70, Z: 9, Kind: "chest", Name: "Diamonds"}, {X: -2, Y: 60, Z: -20, Kind: "barrel"}}; !reflect.DeepEqual(ow.Containers, want) {
		t.Errorf("overworld containers = %v, want %v", ow.Containers, want)
	}
	if want := []Marker{{X: 173, Y: 66, Z: 263, Kind: "cat", Name: "OJ"}}; !reflect.DeepEqual(ow.Mobs, want) {
		t.Errorf("overworld mobs = %v, want %v", ow.Mobs, want)
	}
	if want := []Marker{{X: 33, Y: 90, Z: 34}}; !reflect.DeepEqual(nether.Beds, want) {
		t.Errorf("nether beds = %v, want %v", nether.Beds, want)
	}
	if want := []Marker{{X: 40, Y: 33, Z: 41, Kind: "shulker"}}; !reflect.DeepEqual(nether.Containers, want) {
		t.Errorf("nether containers = %v, want %v", nether.Containers, want)
	}
	// Negative positions round down, as the game shows them.
	if want := []Marker{{X: -11, Y: 31, Z: -1, Kind: "strider", Name: "Lava Lad"}}; !reflect.DeepEqual(nether.Mobs, want) {
		t.Errorf("nether mobs = %v, want %v", nether.Mobs, want)
	}
	if len(end.Beds)+len(end.Containers)+len(end.Mobs) != 0 {
		t.Errorf("end = %+v, want nothing", end)
	}
	if stats.Beds != 2 || stats.Containers != 3 || stats.Mobs != 2 || stats.Skipped != 0 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestScan_LeavesOutContainersThatHoldNothingOfAPlayers(t *testing.T) {
	w, _ := scanOf(t, blockEntities(t, 0, 0, 0,
		// Never opened: the contents are still a loot table.
		map[string]any{"id": "Chest", "x": int32(1), "y": int32(1), "z": int32(1), "Items": []any{}, "LootTable": "loot_tables/chests/village.json"},
		map[string]any{"id": "Chest", "x": int32(2), "y": int32(1), "z": int32(1), "Items": []any{}},
		map[string]any{"id": "Barrel", "x": int32(3), "y": int32(1), "z": int32(1)},
		// Storage of other kinds is not what a player looks for on a map.
		map[string]any{"id": "Hopper", "x": int32(4), "y": int32(1), "z": int32(1), "Items": stack},
		map[string]any{"id": "EnderChest", "x": int32(5), "y": int32(1), "z": int32(1), "Items": stack},
		chestAt(6, 1, 1, nil),
	))
	if got, want := w[chunks.Overworld].Containers, []Marker{{X: 6, Y: 1, Z: 1, Kind: "chest"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("containers = %v, want %v", got, want)
	}
}

func TestScan_MarksALargeChestOnce(t *testing.T) {
	w, _ := scanOf(t,
		blockEntities(t, 0, 0, 0,
			chestAt(15, 64, 3, map[string]any{"pairx": int32(16), "pairz": int32(3), "pairlead": uint8(1)}),
			// A half whose other half is empty is still the chest.
			chestAt(5, 64, 3, map[string]any{"pairx": int32(4), "pairz": int32(3)}),
			map[string]any{"id": "Chest", "x": int32(4), "y": int32(64), "z": int32(3), "Items": []any{}, "pairx": int32(5), "pairz": int32(3), "pairlead": uint8(1)},
		),
		// The other half lies across the chunk border.
		blockEntities(t, 0, 1, 0, chestAt(16, 64, 3, map[string]any{"pairx": int32(15), "pairz": int32(3)})),
	)
	want := []Marker{{X: 5, Y: 64, Z: 3, Kind: "chest"}, {X: 15, Y: 64, Z: 3, Kind: "chest"}}
	if got := w[chunks.Overworld].Containers; !reflect.DeepEqual(got, want) {
		t.Errorf("containers = %v, want %v", got, want)
	}
}

func TestScan_PairsBedHalvesWithoutMergingNeighbours(t *testing.T) {
	w, _ := scanOf(t, blockEntities(t, 0, 0, 0,
		// Two beds side by side, each running along z.
		bedAt(1, 64, 1, 14), bedAt(1, 64, 2, 14), bedAt(2, 64, 1, 14), bedAt(2, 64, 2, 14),
		// Different colours touching are two beds' ends, not one bed.
		bedAt(8, 64, 8, 1), bedAt(9, 64, 8, 2),
		// One above the other is two beds.
		bedAt(12, 64, 12, 3), bedAt(12, 65, 12, 3),
	))
	if got := len(w[chunks.Overworld].Beds); got != 6 {
		t.Errorf("found %d beds, want 6: %v", got, w[chunks.Overworld].Beds)
	}
}

func TestScan_DoesNotDrawWhatItCannotPlace(t *testing.T) {
	w, stats := scanOf(t,
		// Named, and in no chunk's list: a leftover the game never loads.
		actor(t, 1, map[string]any{"identifier": "minecraft:cat", "CustomName": "Ghost", "Pos": []any{float32(1), float32(2), float32(3)}}),
		actor(t, 2, map[string]any{"identifier": "minecraft:cat", "CustomName": "Nowhere"}),
		actor(t, 3, map[string]any{"identifier": "minecraft:cat", "CustomName": "Far", "Pos": []any{float32(1e30), float32(2), float32(3)}}),
		actor(t, 4, map[string]any{"identifier": "minecraft:cat", "CustomName": "Unknown realm", "Pos": []any{float32(1), float32(2), float32(3)}}),
		actor(t, 5, map[string]any{"identifier": "<script>alert(1)</script>", "CustomName": "Odd", "Pos": []any{float32(1), float32(2), float32(3)}}),
		digp(0, 0, 0, 2, 3, 5),
		digp(7, 0, 0, 4),
		// A bed that claims to be in another chunk than the one holding it.
		blockEntities(t, 0, 0, 0, bedAt(400, 64, 400, 0)),
		entry{append(binary.LittleEndian.AppendUint64(nil, 5), tagBlockEntities), []byte("Bed, but not a record")},
	)
	if got, want := w[chunks.Overworld].Mobs, []Marker{{X: 1, Y: 2, Z: 3, Kind: "unknown", Name: "Odd"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("mobs = %v, want %v", got, want)
	}
	if len(w[chunks.Overworld].Beds) != 0 {
		t.Errorf("beds = %v, want none", w[chunks.Overworld].Beds)
	}
	if stats.Skipped != 4 {
		t.Errorf("skipped = %d, want 4 (two unplaceable actors, the stray bed, the broken record)", stats.Skipped)
	}
}

// A world can hold more of anything than a browser should be sent. What is
// kept is what is nearest the origin, and the rest is counted.
func TestScan_KeepsNoMoreThanTheLimitOfEachKind(t *testing.T) {
	var entries []entry
	const extra = 7
	for i := range int32(MaxContainers + extra) {
		// One per chunk along x, so the nearest are the lowest numbered.
		entries = append(entries, blockEntities(t, 0, i, 0, chestAt(i*16, 64, 0, nil), bedAt(i*16+2, 64, 0, 0)))
	}
	var ids []int64
	for i := range int64(MaxMobs + extra) {
		entries = append(entries, actor(t, i+1, map[string]any{"identifier": "minecraft:sheep", "CustomName": fmt.Sprintf("s%d", i), "Pos": []any{float32(i), float32(64), float32(0)}}))
		ids = append(ids, i+1)
	}
	entries = append(entries, digp(2, 0, 0, ids...))
	w, _ := scanOf(t, entries...)

	ow, end := w[chunks.Overworld], w[chunks.End]
	if len(ow.Containers) != MaxContainers || ow.More.Containers != extra {
		t.Errorf("containers: kept %d, %d more; want %d and %d", len(ow.Containers), ow.More.Containers, MaxContainers, extra)
	}
	if len(ow.Beds) != MaxBeds || ow.More.Beds != extra {
		t.Errorf("beds: kept %d, %d more; want %d and %d", len(ow.Beds), ow.More.Beds, MaxBeds, extra)
	}
	if len(end.Mobs) != MaxMobs || end.More.Mobs != extra {
		t.Errorf("mobs: kept %d, %d more; want %d and %d", len(end.Mobs), end.More.Mobs, MaxMobs, extra)
	}
	if far := ow.Containers[len(ow.Containers)-1]; far.X != (MaxContainers-1)*16 {
		t.Errorf("the farthest container kept is at x=%d, want the nearest %d kept", far.X, MaxContainers)
	}
}

// Past a few times the limit the scan stops holding what it finds, so a
// world built to exhaust memory costs a count and nothing else.
func TestScan_StopsCollectingWellPastTheLimit(t *testing.T) {
	const chunksOfBeds, perChunk = 100, 256
	var entries []entry
	for c := range int32(chunksOfBeds) {
		var beds []map[string]any
		for i := range int32(perChunk) {
			// Alternating colours, so that no two halves pair up.
			beds = append(beds, bedAt(c*16+i%16, i/16, 0, uint8(i%2)))
		}
		entries = append(entries, blockEntities(t, 0, c, 0, beds...))
	}
	s := &scan{beds: map[chunks.Dimension][]bed{}, more: map[chunks.Dimension]*More{chunks.Overworld: {}}}
	for _, e := range entries {
		s.blockEntities(chunks.Pos{X: int32(binary.LittleEndian.Uint32(e.k)), Z: 0}, e.v)
	}
	if held := len(s.beds[chunks.Overworld]); held != collectFactor*MaxBeds {
		t.Errorf("held %d bed halves, want no more than %d", held, collectFactor*MaxBeds)
	}
	if more := s.more[chunks.Overworld].Beds; more != chunksOfBeds*perChunk-collectFactor*MaxBeds {
		t.Errorf("counted %d left out, want %d", more, chunksOfBeds*perChunk-collectFactor*MaxBeds)
	}
}

func TestScan_StopsWhenToldTo(t *testing.T) {
	db, done, err := chunks.OpenView(world(t, blockEntities(t, 0, 0, 0, bedAt(1, 1, 1, 0))), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Scan(ctx, db); err == nil {
		t.Error("a cancelled scan reported a result")
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "",
		"Diamonds":                     "Diamonds",
		"  two\n lines\tand  gaps ":    "two lines and gaps",
		"§l§4Bold Red§r":               "Bold Red",
		"§":                            "",
		"bad\xffbytes\x00here":         "badbytes here",
		"<img src=x onerror=alert(1)>": "<img src=x onerror=alert(1)>",
		strings.Repeat("é", 200):       strings.Repeat("é", MaxName),
		strings.Repeat("a ", 200):      strings.TrimSpace(strings.Repeat("a ", MaxName/2)),
	} {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}
