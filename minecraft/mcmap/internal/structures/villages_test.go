package structures

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// The records of two villages, as game version 1.26.52 wrote them in the FWB
// world; the PLAYERS records are left behind. The first is a generated
// village down to its last villager, with a golem, three cats, one claimed
// bed and its bell. The second is one the game has made a record for and
// never run.
const (
	realVillageInfo     = "0a0000040600424454696d654a36511f00000000040600474454696d652adc521f00000000010b00496e697469616c697a6564010405004d5469636bce3e471f0000000004060050445469636bbf10511f0000000003030052583000000000030300525831010000000303005259300000000003030052593101000000030300525a3000000000030300525a31010000000404005469636bb70e511f0000000001070056657273696f6e0103020058307efcffff0302005831befcffff03020059303700000003020059314f0000000302005a30ed0400000302005a313305000000"
	realVillageDwellers = "0a00000908004477656c6c6572730a040000000906006163746f72730a0100000004020049444d510000d0ffffff0402005453840e511f00000000090e006c6173745f73617665645f706f730303000000affcffff410000000e05000000000906006163746f72730a01000000040200494439510000d0ffffff0402005453a90e511f00000000090e006c6173745f73617665645f706f730303000000b9fcffff40000000fb04000000000906006163746f72730000000000000906006163746f72730a0300000004020049443d510000d0ffffff0402005453b70e511f00000000090e006c6173745f73617665645f706f73030300000097fcffff46000000230500000004020049443a510000d0ffffff04020054538d0e511f00000000090e006c6173745f73617665645f706f730303000000a8fcffff43000000e50400000004020049443e510000d0ffffff0402005453940e511f00000000090e006c6173745f73617665645f706f73030300000092fcffff4700000001050000000000"
	realVillagePOI      = "0a0000090300504f490a01000000040a0056696c6c6167657249444d510000d0ffffff090900696e7374616e6365730a0300000004080043617061636974790100000000000000080900496e69744576656e7400000804004e616d65080076696c6c61676572040a004f776e6572436f756e7401000000000000000506005261646975730000403f010400536b697000080a00536f756e644576656e740900756e646566696e6564030400547970650000000001070055736541414242010406005765696768740100000000000000030100589efcffff03010059430000000301005a130500000004080043617061636974791400000000000000080900496e69744576656e7400000804004e616d65080076696c6c61676572040a004f776e6572436f756e7401000000000000000506005261646975730000e040010400536b697000080a00536f756e644576656e740900756e646566696e656403040054797065010000000107005573654141424200040600576569676874010000000000000003010058a5fcffff03010059470000000301005aed04000000010400536b697001000000"

	realNewVillageInfo     = "0a0000040600424454696d650000000000000000040600474454696d650000000000000000010b00496e697469616c697a6564000405004d5469636b000000000000000004060050445469636b000000000000000003030052583000000000030300525831010000000303005259300000000003030052593101000000030300525a3000000000030300525a31010000000404005469636b000000000000000001070056657273696f6e010302005830b2efffff0302005831f2efffff03020059303700000003020059314f0000000302005a30990d00000302005a31d90d000000"
	realNewVillageDwellers = "0a00000908004477656c6c6572730a040000000906006163746f72730000000000000906006163746f72730000000000000906006163746f72730000000000000906006163746f727300000000000000"
	realNewVillagePOI      = "0a0000090300504f49000000000000"
)

var (
	realVillageBox    = Box{-898, 55, 1261, -834, 79, 1331}
	realNewVillageBox = Box{-4174, 55, 3481, -4110, 79, 3545}
)

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

func TestVillages_RealRecords(t *testing.T) {
	w := newWorld(t).
		village("Overworld", testVillageID, unhex(t, realVillageInfo), unhex(t, realVillageDwellers), unhex(t, realVillagePOI)).
		village("Overworld", "238407a1-d860-4020-80a6-578192adfcbb", unhex(t, realNewVillageInfo), unhex(t, realNewVillageDwellers), unhex(t, realNewVillagePOI)).
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
	if lived.Box != realVillageBox || lived.Areas != 0 {
		t.Errorf("box = %+v (areas %d), want %+v", lived.Box, lived.Areas, realVillageBox)
	}
	if want := (VillageFacts{Counted: true, Villagers: 1, Golems: 1, Cats: 3, Beds: 1, Bells: 1}); *lived.Village != want {
		t.Errorf("facts = %+v, want %+v", *lived.Village, want)
	}
	// One the game has not run is still where a village is, and says
	// nothing has been counted rather than that nothing is there.
	if fresh.Box != realNewVillageBox || *fresh.Village != (VillageFacts{}) {
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
		village("Overworld", "0002", infoRecord(Box{200, 60, 200, 264, 84, 264}, 0), dwellersRecord(0, 0, 0, 0), poiRecord())
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
// what bounds the work. A real record is five deep, and is read by the
// tests of real records; this is where the bound falls.
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
// real record, the measuring has to be what refuses it: nothing here may
// panic, and nothing may come out with a count below none.
func TestVillages_NoDamageToARealRecordPanics(t *testing.T) {
	for name, c := range map[string]struct {
		record string
		read   func(*village, []byte) error
	}{
		"INFO":     {realVillageInfo, (*village).info},
		"DWELLERS": {realVillageDwellers, (*village).dwellers},
		"POI":      {realVillagePOI, (*village).claims},
	} {
		whole := unhex(t, c.record)
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
	for _, record := range []string{realVillageInfo, realVillageDwellers, realVillagePOI, realNewVillageInfo, realNewVillageDwellers, realNewVillagePOI} {
		b, err := hex.DecodeString(record)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, record []byte) {
		for _, read := range []func(*village, []byte) error{(*village).info, (*village).dwellers, (*village).claims} {
			var v village
			if err := read(&v, record); err != nil {
				continue
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
