package structures

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/leveldat"
)

// The records of two villages, with every tag game version 1.26.52 writes
// in each and in the order it writes them. The first is a generated village
// down to its last villager, with a golem, three cats, one claimed bed and
// its bell. The second is one the game has made a record for and never run.
// Every value in them is made up: see the note in structures_test.go.
var (
	livedVillageBox = Box{96, 60, -176, 150, 78, -112}
	newVillageBox   = Box{320, 58, 400, 384, 82, 464}

	livedVillageInfo     = gameInfo(livedVillageBox, 1, 4_000_000)
	livedVillageDwellers = nbtRecord(nbtList("Dwellers",
		gameDwellers(4_000_000, dweller{-101, 110, 64, -150}),
		gameDwellers(4_000_000, dweller{-102, 120, 63, -140}),
		gameDwellers(4_000_000),
		gameDwellers(4_000_000, dweller{-103, 100, 66, -120}, dweller{-104, 112, 65, -170}, dweller{-105, 98, 67, -160}),
	))
	livedVillagePOI = nbtRecord(nbtList("POI", nbtCompound(
		nbtLong("VillagerID", -101),
		nbtList("instances",
			gameClaim(0, "villager", 0.75, 1, 104, 64, -148),
			gameClaim(1, "villager", 7, 20, 111, 66, -176),
			nbtCompound(nbtByte("Skip", 1)),
		),
	)))

	newVillageInfo     = gameInfo(newVillageBox, 0, 0)
	newVillageDwellers = nbtRecord(nbtList("Dwellers", gameDwellers(0), gameDwellers(0), gameDwellers(0), gameDwellers(0)))
	newVillagePOI      = nbtRecord(nbtList("POI"))
)

type dweller struct {
	id      int64
	x, y, z int32
}

// gameInfo is an INFO record as the game writes one: its timers, the flag,
// the box its raid is fought in, the tick it was last run at, and its own
// box. A village never run has every timer at nought.
func gameInfo(box Box, counted byte, tick int64) []byte {
	after := func(ticks int64) int64 {
		if tick == 0 {
			return 0
		}
		return tick + ticks
	}
	return nbtRecord(
		nbtLong("BDTime", after(8_000)), nbtLong("GDTime", after(100_000)), nbtByte("Initialized", counted),
		nbtLong("MTick", 0), nbtLong("PDTick", after(300)),
		nbtInt("RX0", 0), nbtInt("RX1", 1), nbtInt("RY0", 0), nbtInt("RY1", 1), nbtInt("RZ0", 0), nbtInt("RZ1", 1),
		nbtLong("Tick", tick), nbtByte("Version", 1),
		nbtInt("X0", box.MinX), nbtInt("X1", box.MaxX), nbtInt("Y0", box.MinY), nbtInt("Y1", box.MaxY), nbtInt("Z0", box.MinZ), nbtInt("Z1", box.MaxZ),
	)
}

// gameDwellers is one of the four lists of a DWELLERS record: each mob's
// id, when the village last saw it and where.
func gameDwellers(tick int64, mobs ...dweller) []byte {
	actors := make([][]byte, len(mobs))
	for i, m := range mobs {
		at := binary.LittleEndian.AppendUint32([]byte{tagInt}, 3)
		for _, v := range []int32{m.x, m.y, m.z} {
			at = binary.LittleEndian.AppendUint32(at, uint32(v))
		}
		actors[i] = nbtCompound(nbtLong("ID", m.id), nbtLong("TS", tick-int64(i)*20), nbtTag(tagList, "last_saved_pos", at))
	}
	return nbtCompound(nbtList("actors", actors...))
}

// gameClaim is one claimed block of a POI record, with every tag the game
// writes for one.
func gameClaim(kind int32, name string, radius float32, capacity int64, x, y, z int32) []byte {
	return nbtCompound(
		nbtLong("Capacity", capacity), nbtString("InitEvent", ""), nbtString("Name", name), nbtLong("OwnerCount", 1),
		nbtTag(tagFloat, "Radius", binary.LittleEndian.AppendUint32(nil, math.Float32bits(radius))),
		nbtByte("Skip", 0), nbtString("SoundEvent", "undefined"), nbtInt("Type", kind), nbtByte("UseAABB", byte(1-kind)), nbtLong("Weight", 1),
		nbtInt("X", x), nbtInt("Y", y), nbtInt("Z", z),
	)
}

// NBT, built the way the game writes it.

func nbtTag(kind byte, name string, payload []byte) []byte {
	out := binary.LittleEndian.AppendUint16([]byte{kind}, uint16(len(name)))
	return append(append(out, name...), payload...)
}

func nbtInt(name string, v int32) []byte {
	return nbtTag(tagInt, name, binary.LittleEndian.AppendUint32(nil, uint32(v)))
}

func nbtByte(name string, v byte) []byte { return nbtTag(tagByte, name, []byte{v}) }

// nbtCompound is a compound's payload: its tags and the end.
func nbtCompound(tags ...[]byte) []byte {
	return append(bytes.Join(tags, nil), tagEnd)
}

// nbtList is a named list of compounds.
func nbtList(name string, compounds ...[]byte) []byte {
	kind := tagCompound
	if len(compounds) == 0 {
		kind = tagEnd
	}
	payload := binary.LittleEndian.AppendUint32([]byte{kind}, uint32(len(compounds)))
	return nbtTag(tagList, name, append(payload, bytes.Join(compounds, nil)...))
}

// nbtRecord is a whole record: one unnamed compound.
func nbtRecord(tags ...[]byte) []byte {
	return nbtTag(tagCompound, "", nbtCompound(tags...))
}

func infoRecord(box Box, counted byte) []byte {
	return nbtRecord(
		nbtByte("Initialized", counted),
		nbtInt("X0", box.MinX), nbtInt("X1", box.MaxX),
		nbtInt("Y0", box.MinY), nbtInt("Y1", box.MaxY),
		nbtInt("Z0", box.MinZ), nbtInt("Z1", box.MaxZ),
	)
}

// dwellersRecord has a list of this many actors for each count: villagers,
// golems, the list nothing has been seen in, and cats.
func dwellersRecord(counts ...int) []byte {
	var lists [][]byte
	for _, n := range counts {
		actors := make([][]byte, n)
		for i := range actors {
			actors[i] = nbtCompound(nbtTag(tagLong, "ID", make([]byte, 8)))
		}
		lists = append(lists, nbtCompound(nbtList("actors", actors...)))
	}
	return nbtRecord(nbtList("Dwellers", lists...))
}

func claimed(kind, x, y, z int32) []byte {
	return nbtCompound(nbtByte("Skip", 0), nbtInt("Type", kind), nbtInt("X", x), nbtInt("Y", y), nbtInt("Z", z))
}

// poiRecord has one villager for each list of claims.
func poiRecord(villagers ...[][]byte) []byte {
	var each [][]byte
	for _, instances := range villagers {
		each = append(each, nbtCompound(nbtList("instances", instances...)))
	}
	return nbtRecord(nbtList("POI", each...))
}

const testVillageID = "474bb2d7-a3a0-49f3-89ed-7ce946650bee"

func (w *world) raw(key string, value []byte) *world {
	w.records[key] = value
	return w
}

// village writes a village's records under the key the game uses.
func (w *world) village(dimension, id string, info, dwellers, poi []byte) *world {
	prefix := "VILLAGE_" + dimension + "_" + id + "_"
	for part, value := range map[string][]byte{"INFO": info, "DWELLERS": dwellers, "POI": poi} {
		if value != nil {
			w.raw(prefix+part, value)
		}
	}
	return w
}

// settled is a village with this many villagers and nothing else.
func (w *world) settled(n int, box Box) *world {
	return w.village("Overworld", fmt.Sprintf("%08x-0000-0000-0000-000000000000", len(w.records)), infoRecord(box, 1), dwellersRecord(n, 0, 0, 0), poiRecord())
}

func villagesOf(layer Layer) []Structure {
	var out []Structure
	for _, s := range layer.Recorded {
		if s.Kind == Village {
			out = append(out, s)
		}
	}
	return out
}

func take(t *testing.T, s *Surveyor, w *world) Survey {
	t.Helper()
	got, err := s.Take(context.Background(), w.write(), surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestVillages_ReadsRecordsAsTheGameWritesThem(t *testing.T) {
	w := newWorld(t).
		village("Overworld", testVillageID, livedVillageInfo, livedVillageDwellers, livedVillagePOI).
		village("Overworld", "238407a1-d860-4020-80a6-578192adfcbb", newVillageInfo, newVillageDwellers, newVillagePOI).
		// The records that are never read are there all the same, and
		// could hold anything.
		raw("VILLAGE_Overworld_"+testVillageID+"_PLAYERS", []byte("not NBT at all")).
		raw("VILLAGE_Overworld_"+testVillageID+"_RAID", []byte{tagCompound})
	got := take(t, surveyor(t, nil), w)

	if want := (VillageStats{Found: 2}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	villages := villagesOf(got.Layers[chunks.Overworld])
	if len(villages) != 2 {
		t.Fatalf("villages = %+v, want two", villages)
	}
	lived, fresh := villages[0], villages[1]
	if lived.Box != livedVillageBox || lived.Areas != 0 {
		t.Errorf("box = %+v (areas %d), want %+v", lived.Box, lived.Areas, livedVillageBox)
	}
	if want := (VillageFacts{Counted: true, Villagers: 1, Golems: 1, Cats: 3, Beds: 1, Bells: 1}); *lived.Village != want {
		t.Errorf("facts = %+v, want %+v", *lived.Village, want)
	}
	// One the game has not run is still where a village is, and says
	// nothing has been counted rather than that nothing is there.
	if fresh.Box != newVillageBox || *fresh.Village != (VillageFacts{}) {
		t.Errorf("uncounted village = %+v %+v", fresh.Box, *fresh.Village)
	}
	for _, d := range []chunks.Dimension{chunks.Nether, chunks.End} {
		if n := len(got.Layers[d].Recorded); n != 0 {
			t.Errorf("%d structures in %s", n, d.Name())
		}
	}
}

func TestVillages_AreListedAfterTheStructuresTheSeedIsCheckedAgainst(t *testing.T) {
	small, large := Box{0, 60, 0, 64, 84, 64}, Box{1000, 60, 1000, 1100, 84, 1100}
	w := evidence(t).settled(2, small).settled(9, large).
		village("Nether", testVillageID, infoRecord(small, 1), dwellersRecord(1), nil).
		village("TheEnd", testVillageID, infoRecord(large, 1), dwellersRecord(3, 2, 0, 1), nil)
	got := take(t, surveyor(t, nil), w)

	overworld := got.Layers[chunks.Overworld].Recorded
	if len(overworld) != 5 {
		t.Fatalf("overworld = %+v, want three monuments and two villages", overworld)
	}
	for _, s := range overworld[:3] {
		if s.Kind != Monument || s.Village != nil {
			t.Errorf("%+v ahead of the villages", s)
		}
	}
	// The most lived-in village first.
	if overworld[3].Box != large || overworld[3].Village.Villagers != 9 || overworld[4].Box != small {
		t.Errorf("villages = %+v, %+v", overworld[3], overworld[4])
	}
	if got.Check.State != SeedVerified {
		t.Errorf("check = %+v: villages cost the seed its evidence", got.Check)
	}
	if nether := villagesOf(got.Layers[chunks.Nether]); len(nether) != 1 || nether[0].Box != small {
		t.Errorf("nether = %+v", nether)
	}
	end := villagesOf(got.Layers[chunks.End])
	if len(end) != 1 || *end[0].Village != (VillageFacts{Counted: true, Villagers: 3, Golems: 2, Cats: 1}) {
		t.Errorf("end = %+v", end)
	}
	for kind, want := range map[Kind]float64{Village: 2, Monument: 3} {
		if n := testutil.ToFloat64(metricRecorded.WithLabelValues("overworld", string(kind))); n != want {
			t.Errorf("mcmap_structures_recorded{overworld,%s} = %v, want %v", kind, n, want)
		}
	}
}

func TestVillages_CountEachClaimedBlockOnce(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	bell := claimed(1, 10, 64, 10)
	unclaimed := nbtCompound(nbtByte("Skip", 1))
	poi := poiRecord(
		[][]byte{claimed(0, 1, 64, 1), bell, claimed(2, 5, 64, 5)},
		[][]byte{claimed(0, 2, 64, 2), bell, unclaimed},
		[][]byte{unclaimed, bell, claimed(2, 5, 64, 5)},
		// A kind of claim this version does not know, and one with no place.
		[][]byte{claimed(7, 3, 64, 3), nbtCompound(nbtByte("Skip", 0), nbtInt("Type", 0))},
	)
	got := take(t, surveyor(t, nil), newWorld(t).village("Overworld", testVillageID, infoRecord(box, 1), dwellersRecord(4, 0, 0, 0), poi))
	villages := villagesOf(got.Layers[chunks.Overworld])
	if len(villages) != 1 {
		t.Fatalf("villages = %+v", villages)
	}
	if want := (VillageFacts{Counted: true, Villagers: 4, Beds: 2, Bells: 1, JobSites: 1}); *villages[0].Village != want {
		t.Errorf("facts = %+v, want %+v", *villages[0].Village, want)
	}
}

// A village the game has counted and found nobody in is a record and no
// longer a village; one it has not counted yet may be full.
func TestVillages_LeaveOutTheOnesWithNobodyInThem(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	w := newWorld(t).
		village("Overworld", "0000", infoRecord(box, 1), dwellersRecord(0, 1, 0, 2), poiRecord()).
		village("Overworld", "0001", infoRecord(box, 1), nil, nil).
		village("Overworld", "0002", infoRecord(Box{640, 60, -800, 704, 84, -736}, 0), dwellersRecord(0, 0, 0, 0), poiRecord())
	got := take(t, surveyor(t, nil), w)
	if want := (VillageStats{Found: 1, Empty: 2}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	if villages := villagesOf(got.Layers[chunks.Overworld]); len(villages) != 1 || villages[0].Village.Counted {
		t.Errorf("villages = %+v, want only the one not yet counted", villages)
	}
	if n := testutil.ToFloat64(metricVillagesSkipped.WithLabelValues("empty")); n != 2 {
		t.Errorf("mcmap_structures_villages_skipped{empty} = %v, want 2", n)
	}
}

func TestVillages_LeaveOutWhatCannotBeRead(t *testing.T) {
	good := Box{0, 60, 0, 64, 84, 64}
	whole := infoRecord(good, 1)
	deep := nbtCompound()
	for range maxDepth + 1 {
		deep = nbtCompound(nbtTag(tagCompound, "c", deep))
	}
	// A list that claims two thousand million compounds and holds none.
	hollow := nbtRecord(nbtTag(tagList, "Dwellers", binary.LittleEndian.AppendUint32([]byte{tagCompound}, 0x7fffffff)))
	missing := nbtRecord(nbtByte("Initialized", 1), nbtInt("X0", 0), nbtInt("X1", 64), nbtInt("Y0", 60), nbtInt("Y1", 84), nbtInt("Z0", 0))

	for name, c := range map[string]struct{ info, dwellers, poi []byte }{
		"an INFO record cut short":         {whole[:len(whole)-5], dwellersRecord(1), poiRecord()},
		"an INFO record with more after":   {append(slices.Clone(whole), 0), dwellersRecord(1), poiRecord()},
		"an INFO record that is not NBT":   {[]byte("village"), dwellersRecord(1), poiRecord()},
		"an empty INFO record":             {[]byte{}, dwellersRecord(1), poiRecord()},
		"no INFO record":                   {nil, dwellersRecord(1), poiRecord()},
		"a box with a corner missing":      {missing, dwellersRecord(1), poiRecord()},
		"a box inside out":                 {infoRecord(Box{64, 60, 0, 0, 84, 64}, 1), dwellersRecord(1), poiRecord()},
		"a box wider than any village":     {infoRecord(Box{0, 60, 0, maxVillageSpan + 1, 84, 64}, 1), dwellersRecord(1), poiRecord()},
		"a box taller than any village":    {infoRecord(Box{0, -2000, 0, 64, 2000, 64}, 1), dwellersRecord(1), poiRecord()},
		"a box past the edge of the world": {infoRecord(Box{maxCoordinate + 1, 60, 0, maxCoordinate + 60, 84, 64}, 1), dwellersRecord(1), poiRecord()},
		"dwellers cut short":               {whole, dwellersRecord(1)[:20], poiRecord()},
		"dwellers nested without end":      {whole, nbtRecord(nbtTag(tagCompound, "c", deep)), poiRecord()},
		"dwellers that claim a count":      {whole, hollow, poiRecord()},
		"dwellers with a count below none": {whole, nbtRecord(nbtTag(tagList, "Dwellers", binary.LittleEndian.AppendUint32([]byte{tagCompound}, 0xfffffff0))), poiRecord()},
		"dwellers listed as nothing":       {whole, nbtRecord(nbtTag(tagList, "Dwellers", binary.LittleEndian.AppendUint32([]byte{tagEnd}, 3))), poiRecord()},
		"dwellers that are not compounds":  {whole, nbtRecord(nbtTag(tagList, "Dwellers", append(binary.LittleEndian.AppendUint32([]byte{tagByte}, 3), 0, 0, 0))), poiRecord()},
		"a tag of no known type":           {whole, nbtRecord(nbtTag(13, "x", nil), nbtList("Dwellers", nbtCompound(nbtList("actors", nbtCompound())))), poiRecord()},
		"dwellers over the size limit":     {whole, nbtRecord(nbtTag(tagByteArray, "pad", append(binary.LittleEndian.AppendUint32(nil, maxVillageRecord), make([]byte, maxVillageRecord)...)), nbtList("Dwellers", nbtCompound(nbtList("actors", nbtCompound())))), poiRecord()},
		"claims cut short":                 {whole, dwellersRecord(1), poiRecord([][]byte{claimed(0, 1, 2, 3)})[:30]},
		"claims that are not a list":       {whole, dwellersRecord(1), nbtRecord(nbtInt("POI", 4))},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t).
				village("Overworld", "0bad", c.info, c.dwellers, c.poi).
				settled(3, Box{500, 60, 500, 564, 84, 564})
			got := take(t, surveyor(t, nil), w)
			if want := (VillageStats{Found: 1, Malformed: 1}); got.Villages != want {
				t.Errorf("stats = %+v, want %+v", got.Villages, want)
			}
			villages := villagesOf(got.Layers[chunks.Overworld])
			if len(villages) != 1 || villages[0].Village.Villagers != 3 {
				t.Errorf("villages = %+v, want only the sound one", villages)
			}
		})
	}
}

// What a record is nested with is walked once per level, so the depth is
// what bounds the work. A record as the game writes one is five deep, and
// is read by the tests of those; this is where the bound falls.
func TestVillages_ARecordIsReadToTheDepthLimitAndNoDeeper(t *testing.T) {
	deep := nbtCompound()
	for range maxDepth - 2 {
		deep = nbtCompound(nbtTag(tagCompound, "c", deep))
	}
	if _, err := rootCompound(nbtRecord(nbtTag(tagCompound, "c", deep))); err != nil {
		t.Errorf("a record nested %d deep was refused: %v", maxDepth, err)
	}
	deep = nbtCompound(nbtTag(tagCompound, "c", deep))
	if _, err := rootCompound(nbtRecord(nbtTag(tagCompound, "c", deep))); err == nil {
		t.Errorf("a record nested %d deep was read", maxDepth+1)
	}
}

func TestVillages_HoldACountToWhatAVillageCouldHave(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	// Claims of a kind this version does not know come first, and must
	// not use up the room for the beds after them.
	var claims [][]byte
	for i := range int32(maxCount) {
		claims = append(claims, claimed(9, i, 64, 0))
	}
	for i := range int32(maxCount + 50) {
		claims = append(claims, claimed(0, i, 64, 0))
	}
	w := newWorld(t).village("Overworld", testVillageID, infoRecord(box, 1), dwellersRecord(maxCount+50, maxCount+7, 0, maxCount+1), poiRecord(claims))
	got := take(t, surveyor(t, nil), w)
	villages := villagesOf(got.Layers[chunks.Overworld])
	if len(villages) != 1 {
		t.Fatalf("villages = %+v (%+v)", villages, got.Villages)
	}
	if f := *villages[0].Village; f.Villagers != maxCount || f.Golems != maxCount || f.Cats != maxCount || f.Beds != maxCount {
		t.Errorf("facts = %+v, want each held to %d", f, maxCount)
	}
}

func TestVillages_PassOverKeysThisVersionDoesNotKnow(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	info := infoRecord(box, 1)
	w := newWorld(t).
		settled(1, box).
		// The form older versions of the game wrote, with no dimension.
		raw("VILLAGE_"+testVillageID+"_INFO", info).
		raw("VILLAGE_"+testVillageID+"_DWELLERS", dwellersRecord(5)).
		raw("VILLAGE_Aether_"+testVillageID+"_INFO", info).
		raw("VILLAGE_Overworld_not a village id_INFO", info).
		raw("VILLAGE_Overworld__INFO", info).
		raw("VILLAGE_Overworld_"+strings.Repeat("a", maxVillageKey)+"_INFO", info).
		raw("VILLAGE_INFO", info)
	got := take(t, surveyor(t, nil), w)
	if want := (VillageStats{Found: 1, Unknown: 6}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	for _, d := range chunks.Dimensions {
		for _, v := range villagesOf(got.Layers[d]) {
			if v.Village.Villagers != 1 {
				t.Errorf("a village was read from a key that is not one: %+v in %s", v, d.Name())
			}
		}
	}
}

func TestVillages_BoundHowManyAreRead(t *testing.T) {
	w := evidence(t)
	for i := range int32(10) {
		w.settled(int(i)+1, Box{i * 100, 60, 0, i*100 + 64, 84, 64})
	}
	dir := w.write()

	s := surveyor(t, nil)
	s.villageLimit = 4
	got, err := s.Take(context.Background(), dir, surveyedAt)
	if err != nil {
		t.Fatal(err)
	}
	if want := (VillageStats{Found: 4, OverLimit: 6}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	if n := testutil.ToFloat64(metricVillagesSkipped.WithLabelValues("limit")); n != 6 {
		t.Errorf("mcmap_structures_villages_skipped{limit} = %v, want 6", n)
	}

	// The layer's own limit covers villages with everything else on it.
	s = surveyor(t, nil)
	s.layerLimit = 5
	if got, err = s.Take(context.Background(), dir, surveyedAt); err != nil {
		t.Fatal(err)
	}
	layer := got.Layers[chunks.Overworld]
	if len(layer.Recorded) != 5 || layer.RecordedMore != 8 || len(villagesOf(layer)) != 2 {
		t.Errorf("recorded %d (+%d) with %d villages; want 5 (+8) with 2", len(layer.Recorded), layer.RecordedMore, len(villagesOf(layer)))
	}
	if top := villagesOf(layer)[0]; top.Village.Villagers != 10 {
		t.Errorf("the layer kept a village of %d ahead of the one of 10", top.Village.Villagers)
	}
}

// Keys that are no village's record, however like one they look, open no
// village and take none of the room for real ones.
func TestVillages_KeysThatAreNoVillagesRecordCostNothing(t *testing.T) {
	w := newWorld(t)
	for i := range 20 {
		w.raw(fmt.Sprintf("VILLAGE_Overworld_0000-%04d_JUNK", i), []byte{1})
		w.raw(fmt.Sprintf("VILLAGE_Overworld_0000-%04d_", i), []byte{1})
	}
	w.village("Overworld", "ffff-ffff", infoRecord(Box{900, 60, 0, 964, 84, 64}, 1), dwellersRecord(7), nil)
	s := surveyor(t, nil)
	s.villageLimit = 4
	got := take(t, s, w)
	if want := (VillageStats{Found: 1}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
}

// A world can hold records under the village prefix without end. Stopping
// part way would show the villages that sorted first as all there are, so
// such a world's villages are not read, and that is a failure to be seen.
func TestVillages_AWorldOfNothingButVillageRecordsIsAFailure(t *testing.T) {
	var log bytes.Buffer
	s := surveyor(t, &log)
	s.villageLimit = 8
	before := testutil.ToFloat64(metricVillageFailures)
	real := func() *world { return newWorld(t).settled(2, Box{0, 60, 0, 64, 84, 64}) }
	if got := take(t, s, real()); got.Villages.Found != 1 {
		t.Fatalf("first survey = %+v", got.Villages)
	}
	flooded := real()
	// One key more than the read allows, with the real village's three.
	for i := range keysPerVillage*8 - 2 {
		flooded.raw(fmt.Sprintf("VILLAGE_Overworld_ffff-%04d_JUNK", i), []byte{1})
	}
	got := take(t, s, flooded)
	if want := (VillageStats{Found: 1, Stale: true}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	if n := testutil.ToFloat64(metricVillageFailures) - before; n != 1 {
		t.Errorf("mcmap_structures_village_read_failures_total rose by %v, want 1", n)
	}
	if !strings.Contains(log.String(), "more records under the village prefix") {
		t.Errorf("the log does not say why:\n%s", log.String())
	}
	// One key fewer is a world that is read.
	delete(flooded.records, "VILLAGE_Overworld_ffff-0000_JUNK")
	if got := take(t, s, flooded); got.Villages.Stale || got.Villages.Found != 1 {
		t.Errorf("a world under the cap: %+v", got.Villages)
	}
}

// A village the game has not run is not known to hold anything, whatever
// is in its lists.
func TestVillages_AnUncountedVillageCarriesNoCounts(t *testing.T) {
	box := Box{0, 60, 0, 64, 84, 64}
	w := newWorld(t).village("Overworld", testVillageID, infoRecord(box, 0), dwellersRecord(5, 1, 0, 2), poiRecord([][]byte{claimed(0, 1, 64, 1)}))
	got := take(t, surveyor(t, nil), w)
	villages := villagesOf(got.Layers[chunks.Overworld])
	if len(villages) != 1 || *villages[0].Village != (VillageFacts{}) {
		t.Errorf("villages = %+v (%+v), want one with nothing counted", villages, got.Villages)
	}
}

func TestVillages_ThatCannotBeReadInTimeKeepTheLastOnes(t *testing.T) {
	var log bytes.Buffer
	s := surveyor(t, &log)
	before := testutil.ToFloat64(metricVillageFailures)
	first := take(t, s, evidence(t).settled(6, Box{0, 60, 0, 64, 84, 64}))
	if first.Villages.Stale || len(villagesOf(first.Layers[chunks.Overworld])) != 1 {
		t.Fatalf("first survey = %+v", first.Villages)
	}

	// The world has since gained a village and a monument. The villages
	// run out of time, which a time already past does without a race; the
	// rest of the survey does not.
	s.VillageTimeout = -time.Second
	later := evidence(t).settled(6, Box{0, 60, 0, 64, 84, 64}).settled(4, Box{300, 60, 0, 364, 84, 64})
	site, _ := monumentSpread.site(testSeed, 1, 1)
	later.structure(chunks.Overworld, monumentByte, monumentAt(site))
	got, err := s.Take(context.Background(), later.write(), surveyedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("the survey failed with its villages: %v", err)
	}
	if want := (VillageStats{Found: 1, Stale: true}); got.Villages != want {
		t.Errorf("stats = %+v, want %+v", got.Villages, want)
	}
	layer := got.Layers[chunks.Overworld]
	if villages := villagesOf(layer); len(villages) != 1 || villages[0].Village.Villagers != 6 {
		t.Errorf("villages = %+v, want the one from the survey before", villages)
	}
	if len(layer.Recorded) != 5 {
		t.Errorf("recorded = %d, want the four monuments now there and the village kept", len(layer.Recorded))
	}
	if n := testutil.ToFloat64(metricVillageFailures) - before; n != 1 {
		t.Errorf("mcmap_structures_village_read_failures_total rose by %v, want 1", n)
	}
	// The villages on the page are an hour older than the rest of it, and
	// that is what an alert can see.
	if at := testutil.ToFloat64(metricVillagesAt); at != float64(surveyedAt.Unix()) {
		t.Errorf("villages last read at %v, want %v", at, surveyedAt.Unix())
	}
	if !strings.Contains(log.String(), "villages not read") {
		t.Errorf("nothing logged about the villages:\n%s", log.String())
	}
	if last, _ := s.Last(); !last.At.Equal(surveyedAt.Add(time.Hour)) {
		t.Errorf("Last is from %v: the survey was not kept", last.At)
	}

	// And they are read again as soon as they can be.
	s.VillageTimeout = 0
	if got = take(t, s, later); got.Villages.Stale || got.Villages.Found != 2 {
		t.Errorf("after the read recovered: %+v", got.Villages)
	}
}

// With no survey before, a read that fails leaves no villages and still a
// survey.
// The villages of the survey before are fallen back on only in the world
// they were read from. In a world with another seed, or with no level to
// tell by, they are villages that are not there.
func TestVillages_ThatCannotBeReadAreNotKeptFromAnotherWorld(t *testing.T) {
	for name, change := range map[string]func(*world){
		"another seed":      func(w *world) { other := levelSeed + 1<<40; w.seed = &other },
		"no level to go by": func(w *world) { w.seed = nil },
	} {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			s := surveyor(t, &log)
			s.Predictors = nil
			box := Box{0, 60, 0, 64, 84, 64}
			if first := take(t, s, newWorld(t).settled(6, box)); len(villagesOf(first.Layers[chunks.Overworld])) != 1 {
				t.Fatalf("first survey = %+v", first.Villages)
			}
			s.VillageTimeout = -time.Second
			// The same world an hour on keeps them.
			if same := take(t, s, newWorld(t).settled(6, box)); len(villagesOf(same.Layers[chunks.Overworld])) != 1 || !same.Villages.Stale {
				t.Fatalf("in the same world: %+v", same.Villages)
			}
			w := newWorld(t).settled(6, box)
			change(w)
			got := take(t, s, w)
			if want := (VillageStats{Stale: true}); got.Villages != want || len(got.Layers[chunks.Overworld].Recorded) != 0 {
				t.Errorf("stats = %+v with %d recorded, want %+v and none", got.Villages, len(got.Layers[chunks.Overworld].Recorded), want)
			}
			if !strings.Contains(log.String(), "another world's") {
				t.Errorf("nothing logged of why:\n%s", log.String())
			}
		})
	}
}

// A world put back to an earlier copy of itself has gone back in its own
// time, which its game tick says.
func TestSameWorld(t *testing.T) {
	at := func(seed, tick int64, known bool) Survey {
		return Survey{HasLevel: true, Level: leveldat.Level{Seed: seed, Tick: tick, TickKnown: known}}
	}
	for name, c := range map[string]struct {
		before, now Survey
		want        bool
	}{
		"a snapshot later":        {at(7, 100, true), at(7, 400, true), true},
		"the same snapshot again": {at(7, 100, true), at(7, 100, true), true},
		"an earlier copy":         {at(7, 400, true), at(7, 399, true), false},
		"another seed":            {at(7, 100, true), at(8, 400, true), false},
		"no tick to go by":        {at(7, 0, false), at(7, 0, false), true},
		"no level before":         {Survey{}, at(7, 100, true), false},
		"no level now":            {at(7, 100, true), Survey{}, false},
	} {
		if got := sameWorld(c.before, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestVillages_ThatCannotBeReadTheFirstTimeAreNone(t *testing.T) {
	s := surveyor(t, nil)
	s.VillageTimeout = -time.Second
	got := take(t, s, evidence(t).settled(6, Box{0, 60, 0, 64, 84, 64}))
	if !got.Villages.Stale || len(got.Layers[chunks.Overworld].Recorded) != 3 {
		t.Errorf("stats %+v, recorded %+v", got.Villages, got.Layers[chunks.Overworld].Recorded)
	}
}

func TestVillageKey(t *testing.T) {
	for key, want := range map[string]struct {
		dim  chunks.Dimension
		part string
		ok   bool
	}{
		"VILLAGE_Overworld_" + testVillageID + "_INFO":     {chunks.Overworld, "INFO", true},
		"VILLAGE_Nether_" + testVillageID + "_DWELLERS":    {chunks.Nether, "DWELLERS", true},
		"VILLAGE_TheEnd_" + testVillageID + "_POI":         {chunks.End, "POI", true},
		"VILLAGE_Overworld_" + testVillageID + "_":         {chunks.Overworld, "", true},
		"VILLAGE_overworld_" + testVillageID + "_INFO":     {},
		"VILLAGE_" + testVillageID + "_INFO":               {},
		"VILLAGE_Overworld_" + testVillageID:               {},
		"VILLAGE_Overworld_474BB2D7_INFO":                  {},
		"VILLAGE_Overworld_../../etc_INFO":                 {},
		"VILLAGE_":                                         {},
		"village_Overworld_" + testVillageID + "_INFO":     {},
		"VILLAGE_Overworld_" + testVillageID + "_INFO\x00": {chunks.Overworld, "INFO\x00", true},
	} {
		dim, part, ok := villageKey([]byte(key))
		if ok != want.ok || (ok && (dim != want.dim || string(part) != want.part)) {
			t.Errorf("villageKey(%q) = %v, %q, %v; want %v, %q, %v", key, dim, part, ok, want.dim, want.part, want.ok)
		}
	}
}

// The reader indexes into a record it has measured. Whatever is done to a
// record, the measuring has to be what refuses it: nothing here may
// panic, and nothing may come out with a count below none.
func TestVillages_NoDamageToARecordPanics(t *testing.T) {
	for name, c := range map[string]struct {
		record []byte
		read   func(*village, []byte) error
	}{
		"INFO":     {livedVillageInfo, (*village).info},
		"DWELLERS": {livedVillageDwellers, (*village).dwellers},
		"POI":      {livedVillagePOI, (*village).claims},
	} {
		whole := c.record
		try := func(damaged []byte) {
			var v village
			if err := c.read(&v, damaged); err != nil {
				return
			}
			if f := v.facts; f.Villagers < 0 || f.Golems < 0 || f.Cats < 0 || f.Beds < 0 || f.Bells < 0 || f.JobSites < 0 {
				t.Fatalf("%s: %x read as %+v", name, damaged, f)
			}
		}
		for cut := range whole {
			try(whole[:cut])
		}
		for i := range whole {
			for _, b := range []byte{0x00, 0x01, 0x09, 0x0a, 0x7f, 0x80, 0xff} {
				damaged := slices.Clone(whole)
				damaged[i] = b
				try(damaged)
			}
		}
		var v village
		if err := c.read(&v, whole); err != nil {
			t.Errorf("%s: the record as it was written is refused: %v", name, err)
		}
	}
}

func FuzzVillageRecord(f *testing.F) {
	for _, record := range [][]byte{livedVillageInfo, livedVillageDwellers, livedVillagePOI, newVillageInfo, newVillageDwellers, newVillagePOI} {
		f.Add(record)
	}
	f.Add(nbtRecord(nbtList("Players", nbtCompound(nbtLong("ID", -9001), nbtInt("S", 7)))))
	f.Add(nbtRecord(nbtTag(tagCompound, "Raid", nbtCompound(nbtByte("GroupNum", 2), nbtByte("NumGroups", 7), nbtByte("NumRaiders", 5), nbtLong("GameTick", 3000)))))
	f.Fuzz(func(t *testing.T, record []byte) {
		for _, read := range []func(*village, []byte) error{(*village).info, (*village).dwellers, (*village).claims, (*village).players, (*village).raid} {
			var v village
			if err := read(&v, record); err != nil {
				continue
			}
			if m := v.more; len(m.standings) > maxStandings || len(m.jobSites) > maxProfessions ||
				max(len(m.dwellers[roleVillager]), len(m.dwellers[roleGolem]), len(m.dwellers[roleCat])) > maxDwellerIDs ||
				(m.raid != nil && (min(m.raid.wave, m.raid.waves, m.raid.raiders) < 0 || max(m.raid.wave, m.raid.waves, m.raid.raiders) > 255)) {
				t.Fatalf("read as %+v", m)
			}
			if c := v.facts; c.Villagers < 0 || c.Golems < 0 || c.Cats < 0 || c.Beds < 0 || c.Bells < 0 || c.JobSites < 0 ||
				max(c.Villagers, c.Golems, c.Cats, c.Beds, c.Bells, c.JobSites) > maxCount {
				t.Fatalf("read as %+v", c)
			}
			if b := v.box; v.hasBox && (b.MinX > b.MaxX || b.MinY > b.MaxY || b.MinZ > b.MaxZ ||
				b.MaxX-b.MinX > maxVillageSpan || b.MaxY-b.MinY > maxVillageSpan || b.MaxZ-b.MinZ > maxVillageSpan ||
				min(b.MinX, b.MinY, b.MinZ) < -maxCoordinate || max(b.MaxX, b.MaxY, b.MaxZ) > maxCoordinate) {
				t.Fatalf("read a box of %+v", b)
			}
		}
	})
}

func TestAVillageIsOfTheBiomeAtItsMiddle(t *testing.T) {
	for biome, want := range map[uint32]string{biomeDesert: "desert", biomeDesertHills: "desert", biomeSavanna: "savanna", biomeSnowyPlains: "snowy", biomeTaiga: "taiga", biomeSnowyTaiga: "taiga", biomePlains: "", biomeMeadow: "", 9999: ""} {
		if got := villageVariant(biome); got != want {
			t.Errorf("a village in biome %d is %q, want %q", biome, got, want)
		}
	}
	facts := &VillageFacts{}
	before := map[chunks.Dimension][]Structure{chunks.Overworld: {
		{Kind: Village, Box: Box{MinX: 100, MaxX: 140, MinZ: -60, MaxZ: -20}, Village: facts},
		{Kind: Village, Box: Box{MinX: 900, MaxX: 940, MinZ: 0, MaxZ: 40}, Variant: "stale"},
		{Kind: Village, Box: Box{MinX: -500, MaxX: -460, MinZ: 0, MaxZ: 40}},
	}}
	asked := [][2]int32{}
	got := inBiomes(before, func(_ chunks.Dimension, x, z int32) (uint32, bool) {
		asked = append(asked, [2]int32{x, z})
		switch {
		case x == 120:
			return biomeDesert, true
		case x == 920:
			return biomePlains, true
		}
		return 0, false
	})[chunks.Overworld]
	if got[0].Variant != "desert" || got[1].Variant != "" || got[2].Variant != "" {
		t.Errorf("variants %q, %q, %q; want desert, none for plains, none where the biome is not known", got[0].Variant, got[1].Variant, got[2].Variant)
	}
	if asked[0] != [2]int32{120, -40} {
		t.Errorf("asked at %v, not at the middle of the box", asked[0])
	}
	if got[0].Village != facts {
		t.Error("a village's facts are another's after its biome was set")
	}
	// The villages given may be the ones being served, and are left as they were.
	if before[chunks.Overworld][0].Variant != "" || before[chunks.Overworld][1].Variant != "stale" {
		t.Error("the villages given were written to")
	}
	if same := inBiomes(before, nil); len(same[chunks.Overworld]) != 3 || same[chunks.Overworld][1].Variant != "stale" {
		t.Error("with no biomes to ask, the villages are not as they were")
	}
}
