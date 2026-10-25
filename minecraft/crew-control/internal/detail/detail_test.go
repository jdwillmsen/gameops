package detail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/df-mc/goleveldb/leveldb"
	"github.com/df-mc/goleveldb/leveldb/opt"
	"github.com/sandertv/gophertunnel/minecraft/nbt"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
)

type gate struct{ signedIn bool }

func (g gate) Operator(*http.Request) (auth.Operator, bool) {
	return auth.Operator{ID: "owner", Name: "Owner"}, g.signedIn
}

// fixtureRecord is a unit's record with a chest that is not empty; every
// number in it is made up.
func fixtureRecord() playerdata.Record {
	level, progress := 12, 0.25
	air := 280
	return playerdata.Record{
		Health:   &playerdata.Gauge{Current: 17, Max: 20},
		Hunger:   &playerdata.Gauge{Current: 14, Max: 20},
		Air:      &air,
		Level:    &level,
		Progress: &progress,
		GameMode: "survival", Dimension: "nether",
		Armor: []playerdata.Item{{Slot: 0, Name: "iron_helmet", Count: 1,
			Enchants: []playerdata.Enchantment{{ID: 0, Name: "Protection", Level: 4}}}},
		Inventory: []playerdata.Item{
			{Slot: 0, Name: "cobblestone", Count: 64},
			{Slot: 9, Name: "bread", Count: 3},
		},
		Mainhand: &playerdata.Item{Slot: 0, Name: "diamond_sword", Count: 1, Custom: "Sting"},
		EnderChest: []playerdata.Item{
			{Slot: 4, Name: "chest", Count: 2},
			{Slot: 26, Name: "diamond", Count: 12, Enchants: []playerdata.Enchantment{{ID: 16, Name: "Sharpness", Level: 5}}},
		},
	}
}

// fakeSource hands out one record, held at a fixed time.
type fakeSource struct {
	record  playerdata.Record
	at      time.Time
	found   bool
	scanned bool
	asked   []string
}

func (f *fakeSource) Lookup(id string) (playerdata.Record, time.Time, bool) {
	f.asked = append(f.asked, id)
	if !f.found {
		// The real store has no save time for a record it does not hold.
		return playerdata.Record{}, time.Time{}, false
	}
	return f.record, f.at, f.found
}

func (f *fakeSource) Scanned() (time.Time, bool) { return f.at, f.scanned }

func liveState(online bool, at time.Time) func(string) (telemetry.Live, bool) {
	lv := telemetry.Live{ID: "crew-01", Online: online, At: at}
	if online {
		h, hunger := float32(19), float32(18)
		lv.Health, lv.Hunger = &h, &hunger
		lv.Inventory = []telemetry.LiveItem{
			{Slot: 0, Name: "cobblestone", Count: 32},
			{Slot: 9, Name: "bread", Count: 2},
		}
		lv.Effects = []telemetry.LiveEffect{{ID: 1, Name: "speed", Amplifier: 1, Duration: -1}}
	}
	return func(string) (telemetry.Live, bool) { return lv, true }
}

func serve(t *testing.T, units roster.Roster, live func(string) (telemetry.Live, bool), saves Source, g Gate) http.Handler {
	t.Helper()
	return Handler(units, live, saves, g)
}

// get asks the handler for a unit's detail, the way the dispatcher in the
// command hands it a routed request.
func get(t *testing.T, h http.Handler, path, unit string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("id", unit)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func detailBody(t *testing.T, h http.Handler) (int, map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	rec := get(t, h, "/units/crew-01/detail", "crew-01")
	var v map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("body is not JSON: %q", rec.Body.String())
		}
	}
	return rec.Code, v, rec
}

func rosterWith(t *testing.T, player string) roster.Roster {
	t.Helper()
	raw := `[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"`
	if player != "" {
		raw += `,"player":"` + player + `"`
	}
	units, err := roster.Parse(raw + "}]")
	if err != nil {
		t.Fatal(err)
	}
	return units
}

func TestDetailIsForOperatorsOnly(t *testing.T) {
	units := rosterWith(t, "-12")
	h := serve(t, units, liveState(false, time.Time{}), nil, gate{signedIn: false})
	code, v, _ := detailBody(t, h)
	if code != http.StatusUnauthorized || v["error"] != "signed_out" {
		t.Fatalf("unsigned: status %d body %v, want 401 signed_out", code, v)
	}

	// Any other method and any unclean spelling is a 404, as it is under
	// the rest of /units/.
	h = serve(t, units, liveState(false, time.Time{}), nil, gate{signedIn: true})
	post := httptest.NewRequest(http.MethodPost, "/units/crew-01/detail", nil)
	post.SetPathValue("id", "crew-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST: %d, want 404", rec.Code)
	}
	rec = get(t, h, "/units/crew-01/./detail", "crew-01")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unclean path: %d, want 404 without a redirect", rec.Code)
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("unclean path was redirected to %q", rec.Header().Get("Location"))
	}
	rec = get(t, h, "/units/crew-99/detail", "crew-99")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown unit: %d, want 404", rec.Code)
	}
}

// A max-age here would let the browser repaint a signed-in answer after
// sign-out, so every 200 shape must forbid storing it, not only one.
func TestEveryDetailAnswerForbidsStoring(t *testing.T) {
	saved := &fakeSource{record: fixtureRecord(), at: time.Now(), found: true, scanned: true}
	for name, h := range map[string]http.Handler{
		"live with a save": serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), saved, gate{signedIn: true}),
		"offline":          serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saved, gate{signedIn: true}),
		"no save source":   serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), nil, gate{signedIn: true}),
	} {
		code, _, rec := detailBody(t, h)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", name, code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: cache-control = %q, want no-store", name, cc)
		}
	}
}

func TestDetailWithoutASourceSaysTheSaveIsOff(t *testing.T) {
	h := serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), nil, gate{signedIn: true})
	code, v, _ := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if v["save"].(map[string]any)["state"] != stateOff {
		t.Errorf("save = %v, want off", v["save"])
	}
	if v["live"].(map[string]any)["state"] != stateKnown {
		t.Errorf("live = %v, want known", v["live"])
	}
	if v["enderChest"] != nil {
		t.Errorf("ender chest = %v, want null", v["enderChest"])
	}
	if effects, ok := v["effects"].([]any); !ok || len(effects) != 1 {
		t.Errorf("effects = %v, want the live one", v["effects"])
	}
}

func TestDetailReadsTheSaveWhenTheUnitIsOffline(t *testing.T) {
	saves := &fakeSource{record: fixtureRecord(), at: time.Now(), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saves, gate{signedIn: true})
	code, v, _ := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := v["health"].(map[string]any)["current"]; got != float64(17) {
		t.Errorf("health = %v, want the saved 17", got)
	}
	inv := v["inventory"].([]any)
	if len(inv) != 2 || inv[1].(map[string]any)["name"] != "bread" {
		t.Errorf("inventory = %v, want the saved one", inv)
	}
	if v["mainhand"].(map[string]any)["customName"] != "Sting" {
		t.Errorf("mainhand = %v", v["mainhand"])
	}
	chest := v["enderChest"].([]any)
	if len(chest) != 27 {
		t.Fatalf("ender chest has %d slots, want 27", len(chest))
	}
	if chest[0] != nil || chest[1] != nil {
		t.Errorf("empty slots are %v and %v, want nulls", chest[0], chest[1])
	}
	diamond := chest[26].(map[string]any)
	if diamond["name"] != "diamond" || diamond["count"] != float64(12) {
		t.Errorf("slot 26 = %v", diamond)
	}
	if enchants := diamond["enchantments"].([]any); len(enchants) != 1 || enchants[0].(map[string]any)["name"] != "Sharpness" {
		t.Errorf("slot 26 enchants = %v", diamond["enchantments"])
	}
	xp := v["xp"].(map[string]any)
	if xp["level"] != float64(12) || xp["progress"] != 0.25 {
		t.Errorf("xp = %v", xp)
	}
}

func TestDetailPrefersTheLivePollWhereItIsFresher(t *testing.T) {
	saves := &fakeSource{record: fixtureRecord(), at: time.Now().Add(-time.Minute), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), saves, gate{signedIn: true})
	_, v, rec := detailBody(t, h)

	if got := v["health"].(map[string]any)["current"]; got != float64(19) {
		t.Errorf("health = %v, want the live 19", got)
	}
	if got := v["health"].(map[string]any)["max"]; got != float64(20) {
		t.Errorf("the live gauge lost its ceiling: %v", v["health"])
	}
	inv := v["inventory"].([]any)
	if len(inv) != 2 {
		t.Fatalf("inventory = %v", inv)
	}
	cobble := inv[0].(map[string]any)
	if cobble["count"] != float64(32) {
		t.Errorf("live count lost: %v", cobble)
	}
	if effects, ok := v["effects"].([]any); !ok || len(effects) != 1 {
		t.Errorf("effects = %v, want the live one", v["effects"])
	}
	// Armour, experience and the chest stay the save's: the poll knows
	// none of them.
	if v["xp"].(map[string]any)["level"] != float64(12) {
		t.Errorf("xp = %v", v["xp"])
	}
	if len(v["enderChest"].([]any)) != 27 {
		t.Errorf("ender chest = %v", v["enderChest"])
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache-control = %q", cc)
	}
}

func TestDetailCarriesWhatTheSaveKnowsOfALiveStack(t *testing.T) {
	rec := fixtureRecord()
	rec.Inventory[0] = playerdata.Item{Slot: 0, Name: "cobblestone", Count: 64,
		Custom: "Rubble", Enchants: []playerdata.Enchantment{{ID: 16, Name: "Sharpness", Level: 5}}}
	saves := &fakeSource{record: rec, at: time.Now().Add(-time.Minute), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), saves, gate{signedIn: true})
	_, v, _ := detailBody(t, h)
	live := v["inventory"].([]any)[0].(map[string]any)
	if live["customName"] != "Rubble" || live["count"] != float64(32) {
		t.Errorf("merged stack = %v, want the save's name on the live count", live)
	}
}

func TestDetailOfAUnitTheSaveDoesNotKnow(t *testing.T) {
	saves := &fakeSource{at: time.Now(), scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saves, gate{signedIn: true})
	code, v, _ := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if v["save"].(map[string]any)["state"] != stateNone {
		t.Errorf("save = %v, want none", v["save"])
	}
	if v["online"] != false {
		t.Errorf("online = %v", v["online"])
	}
}

func TestDetailOfAUnitNothingNamesAPlayerForHasNoRecordToFind(t *testing.T) {
	saves := &fakeSource{record: fixtureRecord(), at: time.Now(), found: true, scanned: true}
	h := serve(t, rosterWith(t, ""), liveState(false, time.Time{}), saves, gate{signedIn: true})
	_, v, _ := detailBody(t, h)
	if v["save"].(map[string]any)["state"] != stateNone {
		t.Errorf("save = %v, want none for a unit with no id to look up", v["save"])
	}
	if len(saves.asked) != 0 {
		t.Errorf("the save was asked for %v with no id to ask for", saves.asked)
	}
	if v["enderChest"] != nil {
		t.Errorf("ender chest = %v, want null", v["enderChest"])
	}
}

func TestDetailFindsTheRecordByTheIdTheUnitReports(t *testing.T) {
	reported := func(string) (telemetry.Live, bool) {
		return telemetry.Live{ID: "crew-01", Online: true, At: time.Now(), PlayerID: "-77"}, true
	}
	saves := &fakeSource{record: fixtureRecord(), at: time.Now(), found: true, scanned: true}
	h := serve(t, rosterWith(t, ""), reported, saves, gate{signedIn: true})
	if _, v, _ := detailBody(t, h); v["save"].(map[string]any)["state"] != stateKnown {
		t.Errorf("save = %v, want known", v["save"])
	}
	if len(saves.asked) != 1 || saves.asked[0] != "-77" {
		t.Errorf("asked for %v, want the reported id", saves.asked)
	}

	// The roster's word beats the unit's.
	saves.asked = nil
	h = serve(t, rosterWith(t, "-12"), reported, saves, gate{signedIn: true})
	detailBody(t, h)
	if len(saves.asked) != 1 || saves.asked[0] != "-12" {
		t.Errorf("asked for %v, want the roster's id", saves.asked)
	}
}

// The poll sees now; a record was written whenever the server last saved,
// and the save being read after the poll says nothing of that.
func TestDetailTakesTheLiveInventoryEvenWhenTheSaveWasReadLater(t *testing.T) {
	saves := &fakeSource{record: fixtureRecord(), at: time.Now().Add(time.Minute), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(true, time.Now()), saves, gate{signedIn: true})
	_, v, _ := detailBody(t, h)
	if got := v["inventory"].([]any)[0].(map[string]any)["count"]; got != float64(32) {
		t.Errorf("count = %v, want the live 32", got)
	}
	if got := v["health"].(map[string]any)["current"]; got != float64(19) {
		t.Errorf("health = %v, want the live 19", got)
	}
}

// A record a save made by hand can grow past the cap only through what
// boxes carry, so an answer over the cap drops that and keeps the rest.
func TestDetailStaysUnderTheCap(t *testing.T) {
	rec := fixtureRecord()
	var heavy []playerdata.Item
	for i := range 36 {
		heavy = append(heavy, playerdata.Item{Slot: i, Name: "shulker_box", Count: 1, Contents: make([]playerdata.Item, 64)})
	}
	rec.Inventory = heavy
	saves := &fakeSource{record: rec, at: time.Now().Add(-time.Minute), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saves, gate{signedIn: true})
	code, _, rec2 := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if rec2.Body.Len() > maxBody {
		t.Errorf("answer is %d bytes, over the cap", rec2.Body.Len())
	}
	var v map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	for _, it := range v["inventory"].([]any) {
		if contents := it.(map[string]any)["contents"]; contents != nil {
			t.Fatalf("an answer over the cap kept what a box carries: %v", it)
		}
	}
}

func TestOverCapAnswerLeavesTheCachedRecordIntact(t *testing.T) {
	rec := fixtureRecord()
	var heavy []playerdata.Item
	for i := range 36 {
		heavy = append(heavy, playerdata.Item{Slot: i, Name: "shulker_box", Count: 1, Contents: make([]playerdata.Item, 64)})
	}
	rec.Inventory = heavy
	saves := &fakeSource{record: rec, at: time.Now().Add(-time.Minute), found: true, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saves, gate{signedIn: true})
	for range 2 {
		if code, _, _ := detailBody(t, h); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}
	for i, it := range saves.record.Inventory {
		if len(it.Contents) != 64 {
			t.Fatalf("slot %d lost what the box carries in the cached record: %d stacks left", i, len(it.Contents))
		}
	}
}

func TestAMissedLookupReportsTheScanTimeNotAZeroTime(t *testing.T) {
	scan := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	saves := &fakeSource{at: scan, found: false, scanned: true}
	h := serve(t, rosterWith(t, "-12"), liveState(false, time.Time{}), saves, gate{signedIn: true})
	code, got, _ := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	save, _ := got["save"].(map[string]any)
	asOf, _ := save["asOf"].(string)
	at, err := time.Parse(time.RFC3339, asOf)
	if save["state"] != stateNone || err != nil || !at.Equal(scan) {
		t.Fatalf("save = %v, want state none as of the scan %v", save, scan)
	}
}

// world writes a LevelDB holding one player record with a non-empty ender
// chest, closed and without a LOCK file, the way a world sits on disk for
// a reader.
func world(t *testing.T, id int64, chest []any) string {
	t.Helper()
	m := map[string]any{
		"UniqueID":            id,
		"EnderChestInventory": chest,
	}
	b, err := nbt.MarshalEncoding(m, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Put([]byte("player_server_aaaa"), b, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "LOCK")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSaveReadsTheUnitsRecordOutOfTheWorld(t *testing.T) {
	dir := world(t, -12, []any{
		map[string]any{"Name": "minecraft:diamond", "Count": uint8(12), "Slot": uint8(26)},
		map[string]any{"Name": "minecraft:chest", "Count": uint8(2), "Slot": uint8(4)},
	})
	s := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Hour, Log: logging.New("error")}

	record, at, ok := s.Lookup("-12")
	if !ok {
		t.Fatal("the record was not found")
	}
	if _, scanned := s.Scanned(); !scanned {
		t.Error("the world read left no trace of a scan")
	}
	if len(record.EnderChest) != 2 || record.EnderChest[0].Slot != 26 || record.EnderChest[0].Count != 12 {
		t.Errorf("ender chest = %+v", record.EnderChest)
	}

	// The next read waits out Every, so the held time does not move.
	if _, again, ok := s.Lookup("-12"); !ok || !again.Equal(at) {
		t.Errorf("a second lookup read the world again: %v was %v", again, at)
	}

	// Both readings of the id find the record, as the store's own lookup
	// promises.
	unsigned := "18446744073709551604" // -12 counted back from the top
	if _, _, ok := s.Lookup(unsigned); !ok {
		t.Errorf("the unsigned reading %s found nothing", unsigned)
	}
}

func TestSaveKeepsTheLastGoodRecordsWhenTheWorldCannotBeRead(t *testing.T) {
	dir := world(t, -12, []any{map[string]any{"Name": "minecraft:chest", "Count": uint8(1), "Slot": uint8(0)}})
	s := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Hour, Log: logging.New("error")}
	if _, _, ok := s.Lookup("-12"); !ok {
		t.Fatal("the record was not read the first time")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// Every waits out, so the held records are served without a read.
	if _, _, ok := s.Lookup("-12"); !ok {
		t.Error("the held record was lost without a read")
	}
	forced := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Nanosecond, Log: logging.New("error")}
	if _, _, ok := forced.Lookup("-12"); ok {
		t.Error("a world that is not there was read")
	}
	if _, scanned := forced.Scanned(); scanned {
		t.Error("a failed first read counted as a scan")
	}
}

// The read must not write where the world lies, not even a LOCK file.
func TestSaveLeavesTheWorldAsItFoundIt(t *testing.T) {
	dir := world(t, -12, []any{map[string]any{"Name": "minecraft:chest", "Count": uint8(1), "Slot": uint8(0)}})
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Hour, Log: logging.New("error")}
	if _, _, ok := s.Lookup("-12"); !ok {
		t.Fatal("the record was not read")
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("the world gained files: %v, was %v", after, before)
	}
	for i, e := range after {
		if e.Name() != before[i].Name() {
			t.Errorf("the world changed: %v, was %v", after, before)
			break
		}
	}
}

func TestReadRejectsACancelledContext(t *testing.T) {
	dir := world(t, -12, nil)
	db, err := leveldb.OpenFile(dir, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := playerdata.Read(ctx, db); err == nil {
		t.Error("a cancelled read succeeded")
	}
}

// A failed read waits out Every like a good one, so an unreadable world is
// not opened afresh by every request.
func TestSaveDoesNotRetryAFailedReadBeforeEvery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	s := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Hour, Log: logging.New("error")}
	if _, _, ok := s.Lookup("-12"); ok {
		t.Fatal("a world that is not there was read")
	}
	built := world(t, -12, []any{map[string]any{"Name": "minecraft:chest", "Count": uint8(1), "Slot": uint8(0)}})
	if err := os.Rename(built, dir); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Lookup("-12"); ok {
		t.Error("the world was read again straight after a failure")
	}
	s.mu.Lock()
	s.lastRead = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	if _, _, ok := s.Lookup("-12"); !ok {
		t.Error("the world was not read once Every had passed")
	}
}

// The whole route over a real world: a record whose ender chest holds
// enchanted and named stacks is read out of LevelDB by the unit's own
// reported id, and the 27 slots come back slot for slot.
func TestDetailAnswersTheEnderChestSlotForSlotFromTheWorld(t *testing.T) {
	ench := func(id, lvl int16) map[string]any { return map[string]any{"id": id, "lvl": lvl} }
	chest := []any{
		map[string]any{"Name": "minecraft:diamond_sword", "Count": uint8(1), "Slot": uint8(0),
			"tag": map[string]any{"Damage": int32(12), "display": map[string]any{"Name": "Sting"},
				"ench": []any{ench(9, 5), ench(12, 2)}}},
		map[string]any{"Name": "minecraft:cobblestone", "Count": uint8(64), "Slot": uint8(13)},
		map[string]any{"Name": "minecraft:enchanted_book", "Count": uint8(1), "Slot": uint8(26),
			"tag": map[string]any{"ench": []any{ench(0, 4)}}},
	}
	dir := world(t, -12, chest)
	saves := &Save{WorldDir: dir, WorkDir: t.TempDir(), Every: time.Hour, Log: logging.New("error")}
	reported := func(string) (telemetry.Live, bool) {
		return telemetry.Live{ID: "crew-01", Online: true, At: time.Now(), PlayerID: "-12"}, true
	}
	h := serve(t, rosterWith(t, ""), reported, saves, gate{signedIn: true})
	code, v, rec := detailBody(t, h)
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, rec.Body)
	}
	got := v["enderChest"].([]any)
	if len(got) != 27 {
		t.Fatalf("%d slots, want 27", len(got))
	}
	full := map[int]string{0: "diamond_sword", 13: "cobblestone", 26: "enchanted_book"}
	for slot, it := range got {
		want, held := full[slot]
		if !held {
			if it != nil {
				t.Errorf("slot %d = %v, want empty", slot, it)
			}
			continue
		}
		m, _ := it.(map[string]any)
		if m == nil || m["name"] != want || m["slot"] != float64(slot) {
			t.Errorf("slot %d = %v, want %s", slot, it, want)
		}
	}
	sword := got[0].(map[string]any)
	if sword["customName"] != "Sting" || sword["damage"] != float64(12) {
		t.Errorf("sword = %v", sword)
	}
	if es := sword["enchantments"].([]any); len(es) != 2 || es[0].(map[string]any)["level"] != float64(5) {
		t.Errorf("sword enchantments = %v", sword["enchantments"])
	}
	if got[13].(map[string]any)["count"] != float64(64) {
		t.Errorf("cobblestone = %v", got[13])
	}
	if rec.Body.Len() > maxBody {
		t.Errorf("answer is %d bytes", rec.Body.Len())
	}
}
