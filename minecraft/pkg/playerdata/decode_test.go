package playerdata

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

type c = map[string]any

func item(name string, count uint8, slot uint8, tag c) c {
	it := c{"Name": name, "Count": count, "Damage": int16(0), "Slot": slot, "WasPickedUp": uint8(0)}
	if tag != nil {
		it["tag"] = tag
	}
	return it
}

func attribute(name string, cur, max float32) c {
	return c{"Name": name, "Current": cur, "Max": max, "Base": cur, "DefaultMax": max, "DefaultMin": float32(0), "Min": float32(0)}
}

// fixture is an invented player: every position, name and number is made up.
func fixture(id int64) c {
	return c{
		"UniqueID": id,
		"Attributes": []any{
			attribute("minecraft:health", 17, 20),
			attribute("minecraft:player.hunger", 14, 20),
			attribute("minecraft:player.saturation", 3.5, 20),
			attribute("minecraft:absorption", 4, 16),
		},
		"Air":                 int16(280),
		"PlayerLevel":         int32(12),
		"PlayerLevelProgress": float32(0.25),
		"PlayerGameMode":      int32(0),
		"DimensionId":         int32(1),
		"Pos":                 []any{float32(10.5), float32(70), float32(-20.25)},
		"Armor": []any{
			item("minecraft:iron_helmet", 1, 0, c{"Damage": int32(33), "ench": []any{c{"id": int16(0), "lvl": int16(4)}, c{"id": int16(99), "lvl": int16(1)}}}),
			item("", 0, 0, nil),
			item("minecraft:diamond_leggings", 1, 0, nil),
			item("", 0, 0, nil),
		},
		"Mainhand": []any{item("minecraft:diamond_sword", 1, 0, c{"Damage": int32(10), "display": c{"Name": "Sting"}, "ench": []any{c{"id": int16(9), "lvl": int16(5)}}})},
		"Offhand":  []any{item("", 0, 0, nil)},
		"Inventory": []any{
			item("minecraft:cobblestone", 64, 0, nil),
			item("minecraft:bread", 3, 11, nil),
			item("", 0, 12, nil),
		},
		"EnderChestInventory": []any{item("minecraft:chest", 2, 4, nil)},
		"HasDiedBefore":       uint8(1),
		"DeathPositionX":      int32(100),
		"DeathPositionY":      int32(64),
		"DeathPositionZ":      int32(-300),
		"DeathDimension":      int32(0),
		"SpawnBlockPositionX": int32(-5),
		"SpawnBlockPositionY": int32(65),
		"SpawnBlockPositionZ": int32(8),
		"SpawnDimension":      int32(0),
		"recipe_unlocking":    c{"unlocked_recipes": []any{"a", "b", "c"}},
		"HasSeenCredits":      uint8(1),
		"WardenThreatLevel":   int32(2),
	}
}

func encode(t testing.TB, m c) []byte {
	t.Helper()
	b, err := nbt.MarshalEncoding(m, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeReadsEverythingTheSaveHoldsOfAPlayer(t *testing.T) {
	id, r, err := Decode(encode(t, fixture(-4294967290)))
	if err != nil || id != -4294967290 {
		t.Fatalf("id = %d, %v", id, err)
	}
	if r.Health == nil || *r.Health != (Gauge{17, 20}) || *r.Hunger != (Gauge{14, 20}) || r.Saturation.Current != 3.5 || r.Absorption.Current != 4 {
		t.Errorf("vitals = %+v %+v %+v %+v", r.Health, r.Hunger, r.Saturation, r.Absorption)
	}
	if *r.Air != 280 || *r.Level != 12 || *r.Progress != 0.25 || r.GameMode != "survival" || r.Dimension != "nether" {
		t.Errorf("air %d level %d progress %v mode %q dimension %q", *r.Air, *r.Level, *r.Progress, r.GameMode, r.Dimension)
	}
	if *r.Saved != (Place{10, 70, -21, "nether"}) {
		t.Errorf("saved = %+v", *r.Saved)
	}
	if len(r.Armor) != 2 || r.Armor[0].Slot != 0 || r.Armor[1].Slot != 2 || r.Armor[1].Name != "diamond_leggings" {
		t.Errorf("armor = %+v", r.Armor)
	}
	helmet := r.Armor[0]
	if helmet.Damage != 33 || helmet.MaxDamage != 165 || len(helmet.Enchants) != 2 ||
		helmet.Enchants[0] != (Enchantment{0, "Protection", 4}) || helmet.Enchants[1] != (Enchantment{99, "", 1}) {
		t.Errorf("helmet = %+v", helmet)
	}
	if r.Mainhand == nil || r.Mainhand.Custom != "Sting" || r.Mainhand.Enchants[0].Name != "Sharpness" || r.Offhand != nil {
		t.Errorf("hands = %+v %+v", r.Mainhand, r.Offhand)
	}
	if len(r.Inventory) != 2 || r.Inventory[1].Slot != 11 || r.Inventory[1].Count != 3 {
		t.Errorf("inventory = %+v", r.Inventory)
	}
	if len(r.EnderChest) != 1 || r.EnderChest[0].Slot != 4 {
		t.Errorf("ender chest = %+v", r.EnderChest)
	}
	if *r.Death != (Place{100, 64, -300, "overworld"}) || *r.Spawn != (Place{-5, 65, 8, "overworld"}) {
		t.Errorf("death %+v spawn %+v", r.Death, r.Spawn)
	}
	if r.RecipesUnlocked != 3 || !r.SeenCredits || *r.WardenThreat != 2 {
		t.Errorf("recipes %d credits %v warden %v", r.RecipesUnlocked, r.SeenCredits, r.WardenThreat)
	}
}

func TestDecodeLeavesOutWhatThePlayerHasNot(t *testing.T) {
	m := fixture(7)
	m["HasDiedBefore"] = uint8(0)
	for _, axis := range []string{"X", "Y", "Z"} {
		m["SpawnBlockPosition"+axis] = int32(math.MinInt32)
	}
	m["PlayerGameMode"] = int32(5)
	delete(m, "Attributes")
	delete(m, "Mainhand")
	_, r, err := Decode(encode(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if r.Death != nil || r.Spawn != nil || r.Health != nil || r.Mainhand != nil {
		t.Errorf("record = %+v", r)
	}
	if r.GameMode != "default" {
		t.Errorf("game mode = %q", r.GameMode)
	}
}

func TestDecodeFallsBackToTheExperienceAttributes(t *testing.T) {
	m := fixture(7)
	delete(m, "PlayerLevel")
	delete(m, "PlayerLevelProgress")
	m["Attributes"] = []any{attribute("minecraft:player.level", 30, 24791), attribute("minecraft:player.experience", 0.5, 1)}
	_, r, _ := Decode(encode(t, m))
	if r.Level == nil || *r.Level != 30 || r.Progress == nil || *r.Progress != 0.5 {
		t.Errorf("level %v progress %v", r.Level, r.Progress)
	}
}

func TestDecodeRefusesWhatIsNotAPlayer(t *testing.T) {
	m := fixture(1)
	delete(m, "UniqueID")
	for name, data := range map[string][]byte{"no id": encode(t, m), "garbage": []byte("not nbt"), "empty": nil} {
		if _, _, err := Decode(data); err == nil {
			t.Errorf("%s: read as a player", name)
		}
	}
}

func TestDecodeBoundsWhatOneRecordMayHold(t *testing.T) {
	m := fixture(1)
	var many []any
	for i := range 500 {
		many = append(many, item("minecraft:stick", 1, uint8(i), c{"ench": []any{}}))
	}
	m["Inventory"] = many
	_, r, err := Decode(encode(t, m))
	if err != nil || len(r.Inventory) != maxItems {
		t.Errorf("%d items, %v", len(r.Inventory), err)
	}
}

func TestLookupFindsAPlayerByEitherReadingOfTheirId(t *testing.T) {
	var s Store
	at := time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC)
	if _, ok := s.Scanned(); ok {
		t.Error("scanned before any scan")
	}
	s.Set(at, map[int64]Record{-4294967290: {RecipesUnlocked: 1}, 42: {RecipesUnlocked: 2}})
	for id, want := range map[string]int{"-4294967290": 1, "18446744069414584326": 1, "42": 2} {
		r, when, ok := s.Lookup(id)
		if !ok || r.RecipesUnlocked != want || !when.Equal(at) {
			t.Errorf("Lookup(%s) = %+v, %v, %v", id, r, when, ok)
		}
	}
	for _, id := range []string{"", "43", "abc", "1e3", "99999999999999999999999"} {
		if _, _, ok := s.Lookup(id); ok {
			t.Errorf("Lookup(%q) found a player", id)
		}
	}
}

// world builds a LevelDB holding entries, closed and without a LOCK file,
// the way a world sits on disk for a reader.
func world(t testing.TB, entries map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range entries {
		if err := db.Put([]byte(k), v, nil); err != nil {
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

func TestReadReadsOnlyPlayerRecords(t *testing.T) {
	dir := world(t, map[string][]byte{
		"player_server_aaaa":   encode(t, fixture(11)),
		"player_server_bbbb":   encode(t, fixture(-12)),
		"player_server_broken": []byte("garbage"),
		"player_0000-0000":     encode(t, c{"ServerId": "player_server_aaaa", "MsaId": "x"}),
		"~local_player":        encode(t, fixture(13)),
		"unrelated":            []byte("x"),
	})
	db, err := leveldb.OpenFile(dir, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	found, stats, err := Read(context.Background(), db)
	if err != nil || stats.Players != 2 || stats.Skipped != 1 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if _, ok := found[11]; !ok {
		t.Error("player 11 not read")
	}
	if _, ok := found[-12]; !ok {
		t.Error("player -12 not read")
	}
	if _, ok := found[13]; ok {
		t.Error("the local player's record was read")
	}
}

func TestReadStopsWhenCancelled(t *testing.T) {
	dir := world(t, map[string][]byte{"player_server_aaaa": encode(t, fixture(11))})
	db, err := leveldb.OpenFile(dir, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Read(ctx, db); err == nil {
		t.Error("a cancelled read succeeded")
	}
}

func TestDurabilityIsLookedUpByMaterialAndKind(t *testing.T) {
	for name, n := range map[string]int{"netherite_boots": 481, "stone_axe": 131, "golden_hoe": 32, "bow": 384} {
		if maxDamage[name] != n {
			t.Errorf("%s = %d, want %d", name, maxDamage[name], n)
		}
	}
}

// The decoder makes a slice of whatever length a list header gives, so a
// few bytes that claim billions of elements must be refused before it does.
func TestDecodeRefusesAListThatClaimsMoreThanTheRecordHolds(t *testing.T) {
	record := func(elem byte, length uint32) []byte {
		b := []byte{tagCompound, 0, 0}
		b = append(b, tagList, 9, 0)
		b = append(b, "Inventory"...)
		b = append(b, elem, byte(length), byte(length>>8), byte(length>>16), byte(length>>24))
		return append(b, tagEnd)
	}
	for name, data := range map[string][]byte{
		"compounds":      record(tagCompound, 0x7fffffff),
		"longs":          record(tagLong, 1<<20),
		"bounded":        record(tagInt, 1<<16+1),
		"negative":       record(tagInt, 0xffffffff),
		"more than left": record(tagCompound, 50),
		"of nothing":     record(tagEnd, 3),
	} {
		if _, _, err := Decode(data); err == nil {
			t.Errorf("%s: a hostile list header was decoded", name)
		}
	}
	deep := []byte{tagCompound, 0, 0}
	for range 100 {
		deep = append(deep, tagCompound, 1, 0, 'a')
	}
	if _, _, err := Decode(deep); err == nil {
		t.Error("a record nested past any the game writes was decoded")
	}
}

func TestLookupAndSetAgreeOnBothReadingsOfEveryId(t *testing.T) {
	var s Store
	ids := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	byID := map[int64]Record{}
	for i, id := range ids {
		byID[id] = Record{RecipesUnlocked: i + 1}
	}
	s.Set(time.Now(), byID)
	for i, id := range ids {
		for _, text := range []string{strconv.FormatInt(id, 10), strconv.FormatUint(uint64(id), 10)} {
			if r, _, ok := s.Lookup(text); !ok || r.RecipesUnlocked != i+1 {
				t.Errorf("Lookup(%s) = %+v, %v", text, r, ok)
			}
		}
	}
}

// box is a shulker box stack carrying the given stacks.
func box(name string, slot uint8, inside ...any) c {
	return item(name, 1, slot, c{"Items": inside})
}

func TestDecodeReadsWhatAShulkerBoxCarries(t *testing.T) {
	m := fixture(1)
	m["Inventory"] = []any{
		box("minecraft:blue_shulker_box", 2,
			item("minecraft:diamond", 12, 0, nil),
			item("minecraft:iron_pickaxe", 1, 5, c{"Damage": int32(40), "ench": []any{c{"id": int16(15), "lvl": int16(3)}}}),
			item("", 0, 6, nil)),
		box("minecraft:undyed_shulker_box", 3),
		item("minecraft:chest", 1, 4, c{"Items": []any{item("minecraft:stick", 1, 0, nil)}}),
	}
	_, r, err := Decode(encode(t, m))
	if err != nil || len(r.Inventory) != 3 {
		t.Fatalf("%d items, %v", len(r.Inventory), err)
	}
	blue := r.Inventory[0]
	if len(blue.Contents) != 2 || blue.Contents[0].Name != "diamond" || blue.Contents[0].Count != 12 || blue.Contents[1].Slot != 5 {
		t.Fatalf("blue box = %+v", blue.Contents)
	}
	if pick := blue.Contents[1]; pick.Damage != 40 || pick.MaxDamage == 0 || len(pick.Enchants) != 1 || pick.Enchants[0].Level != 3 {
		t.Errorf("pickaxe in the box = %+v", pick)
	}
	if r.Inventory[1].Contents != nil {
		t.Errorf("an empty box has contents %+v", r.Inventory[1].Contents)
	}
	if r.Inventory[2].Contents != nil {
		t.Errorf("a chest item, which keeps nothing in its stack, has contents %+v", r.Inventory[2].Contents)
	}
}

func depthOf(it Item) int {
	deepest := 0
	for _, in := range it.Contents {
		deepest = max(deepest, 1+depthOf(in))
	}
	return deepest
}

func TestDecodeStopsReadingBoxesInBoxesAtTheNestingLimit(t *testing.T) {
	nested := func(levels int) c {
		inner := item("minecraft:diamond", 1, 0, nil)
		for range levels {
			inner = box("minecraft:red_shulker_box", 0, inner)
		}
		return inner
	}
	m := fixture(1)
	m["Inventory"] = []any{nested(5)}
	_, r, err := Decode(encode(t, m))
	if err != nil || len(r.Inventory) != 1 {
		t.Fatalf("%d items, %v", len(r.Inventory), err)
	}
	if got := depthOf(r.Inventory[0]); got != maxNesting {
		t.Errorf("read %d boxes deep, want %d", got, maxNesting)
	}
	// The box at the limit is still an item, only without what is in it.
	deepest := r.Inventory[0]
	for range maxNesting {
		deepest = deepest.Contents[0]
	}
	if deepest.Name != "red_shulker_box" || deepest.Contents != nil {
		t.Errorf("box at the limit = %+v", deepest)
	}
}

// This is the record guard's depth limit at work, not the nesting limit of
// the box reader, which only a record that gets past the guard reaches.
func TestDecodeRefusesBoxesNestedPastAnythingTheGameWrites(t *testing.T) {
	inner := item("minecraft:diamond", 1, 0, nil)
	for range 60 {
		inner = box("minecraft:shulker_box", 0, inner)
	}
	m := fixture(1)
	m["Inventory"] = []any{inner}
	if _, _, err := Decode(encode(t, m)); err == nil {
		t.Error("a record nested 60 boxes deep was decoded")
	}
}

func TestDecodeBoundsABoxAndTheWholeRecordsBoxes(t *testing.T) {
	var full []any
	for i := range 500 {
		full = append(full, item("minecraft:stick", 1, uint8(i), nil))
	}
	m := fixture(1)
	m["Inventory"] = []any{box("minecraft:shulker_box", 0, full...)}
	_, r, err := Decode(encode(t, m))
	if err != nil || len(r.Inventory[0].Contents) != maxItems {
		t.Fatalf("%d items in one box, %v", len(r.Inventory[0].Contents), err)
	}

	var boxes []any
	for i := range 40 {
		boxes = append(boxes, box("minecraft:shulker_box", uint8(i), full[:maxItems+6]...))
	}
	m["Inventory"], m["EnderChestInventory"] = boxes, boxes
	data := encode(t, m)
	if len(data) > maxRecord {
		t.Fatalf("fixture is %d bytes, over the limit before any box is read", len(data))
	}
	_, r, err = Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, list := range [][]Item{r.Inventory, r.EnderChest} {
		for _, b := range list {
			held += len(b.Contents)
		}
	}
	if held != maxContained {
		t.Errorf("%d stacks read out of boxes, want %d", held, maxContained)
	}
}

// This is the size limit on a whole record, which was there before boxes
// were read; it does not bound what the box reader returns.
func TestDecodeRefusesARecordOverTheSizeLimit(t *testing.T) {
	m := fixture(1)
	var big []any
	for i := range 40 {
		big = append(big, item("minecraft:stick", 1, uint8(i), c{"display": c{"Name": strings.Repeat("x", 30000)}}))
	}
	m["Inventory"] = []any{box("minecraft:shulker_box", 0, big...)}
	data := encode(t, m)
	if len(data) <= maxRecord {
		t.Fatalf("fixture is %d bytes, under the limit", len(data))
	}
	if _, _, err := Decode(data); err == nil {
		t.Error("a record over the limit was decoded")
	}
}

func TestDecodeReadsNoBoxesOutOfWhatIsWornOrHeld(t *testing.T) {
	inside := item("minecraft:diamond", 1, 0, nil)
	m := fixture(1)
	m["Armor"] = []any{box("minecraft:shulker_box", 0, inside)}
	m["Mainhand"] = []any{box("minecraft:shulker_box", 0, inside)}
	m["Offhand"] = []any{box("minecraft:shulker_box", 0, inside)}
	m["Inventory"] = []any{box("minecraft:shulker_box", 0, inside)}
	m["EnderChestInventory"] = []any{box("minecraft:shulker_box", 0, inside)}
	_, r, err := Decode(encode(t, m))
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]Item{"armor": r.Armor, "mainhand": {*r.Mainhand}, "offhand": {*r.Offhand}} {
		if len(got) != 1 || got[0].Name != "shulker_box" || got[0].Contents != nil {
			t.Errorf("%s = %+v", name, got)
		}
	}
	if len(r.Inventory[0].Contents) != 1 || len(r.EnderChest[0].Contents) != 1 {
		t.Errorf("inventory %+v, ender chest %+v", r.Inventory[0].Contents, r.EnderChest[0].Contents)
	}
}
