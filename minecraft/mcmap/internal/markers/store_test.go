package markers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

type decoded struct {
	At         *time.Time `json:"at"`
	Beds       []Marker   `json:"beds"`
	Containers []Marker   `json:"containers"`
	Mobs       []Marker   `json:"mobs"`
	More       More       `json:"more"`
}

func read(t *testing.T, s *Store, dimension string) (decoded, string) {
	t.Helper()
	body, etag, ok := s.Dimension(dimension)
	if !ok {
		t.Fatalf("no %s", dimension)
	}
	var d decoded
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("%v in %s", err, body)
	}
	return d, etag
}

func TestStore_AnswersWithEmptyListsBeforeTheFirstScan(t *testing.T) {
	s := NewStore()
	body, _, ok := s.Dimension("nether")
	if !ok || string(body) != `{"beds":[],"containers":[],"mobs":[],"more":{"beds":0,"containers":0,"mobs":0}}` {
		t.Errorf("nether = %s, %v", body, ok)
	}
	if _, _, ok := s.Dimension("aether"); ok {
		t.Error("an unknown dimension has markers")
	}
}

// A day of history is kept with the markers of its moment, and the empty
// answer from before the first scan is not that: it would be kept for good
// as a day on which the world held nothing.
func TestStore_HasNothingToKeepUntilAScanHasReadTheWorld(t *testing.T) {
	s := NewStore()
	if body, ok := s.Scanned("overworld"); ok {
		t.Fatalf("markers to keep before any scan: %s", body)
	}
	s.Set(time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC), World{chunks.Overworld: {Beds: []Marker{{X: 1, Y: 2, Z: 3}}}})
	served, _, _ := s.Dimension("overworld")
	if body, ok := s.Scanned("overworld"); !ok || string(body) != string(served) {
		t.Errorf("markers to keep = %s, %v; want what is served, %s", body, ok, served)
	}
	if body, ok := s.Scanned("end"); !ok || !strings.Contains(string(body), `"beds":[]`) {
		t.Errorf("a dimension scanned and found empty = %s, %v; want its empty answer kept", body, ok)
	}
	if _, ok := s.Scanned("aether"); ok {
		t.Error("an unknown dimension has markers to keep")
	}
}

func TestStore_ServesEachDimensionItsOwnMarkers(t *testing.T) {
	s := NewStore()
	_, before := read(t, s, "overworld")
	at := time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC)
	s.Set(at, World{
		chunks.Overworld: {Beds: []Marker{{X: 1, Y: 2, Z: 3}}, More: More{Mobs: 4}},
		chunks.Nether:    {Containers: []Marker{{X: -5, Y: 6, Z: 7, Kind: "chest", Name: "Loot"}}},
	})
	ow, after := read(t, s, "overworld")
	if len(ow.Beds) != 1 || ow.Beds[0] != (Marker{X: 1, Y: 2, Z: 3}) || len(ow.Containers) != 0 || ow.More.Mobs != 4 || !ow.At.Equal(at) {
		t.Errorf("overworld = %+v", ow)
	}
	if nether, _ := read(t, s, "nether"); len(nether.Containers) != 1 || nether.Containers[0].Name != "Loot" || len(nether.Beds) != 0 {
		t.Errorf("nether = %+v", nether)
	}
	if end, _ := read(t, s, "end"); end.At == nil || len(end.Beds) != 0 {
		t.Errorf("end = %+v", end)
	}
	if before == after || !strings.HasPrefix(after, `"`) {
		t.Errorf("the tag did not change with the markers: %s then %s", before, after)
	}
}

// Every limit reached at once, with names that escape to six bytes a
// character: the count limits alone would let this through at 5 MB.
func TestStore_CutsAnAnswerThatWouldBeTooLarge(t *testing.T) {
	name := strings.Repeat("<", MaxName)
	full := func(n int) []Marker {
		m := make([]Marker, n)
		for i := range m {
			m[i] = Marker{X: -30_000_000, Y: -30_000_000, Z: -30_000_000, Kind: strings.Repeat("k", MaxName), Name: name}
		}
		return m
	}
	s := NewStore()
	s.Set(time.Now(), World{chunks.Overworld: {Beds: full(MaxBeds), Containers: full(MaxContainers), Mobs: full(MaxMobs), More: More{Beds: 1}}})
	body, _, _ := s.Dimension("overworld")
	if len(body) > MaxBody {
		t.Fatalf("answer is %d bytes, over the %d allowed", len(body), MaxBody)
	}
	got, _ := read(t, s, "overworld")
	if len(got.Beds) == 0 || len(got.Beds)+got.More.Beds != MaxBeds+1 || len(got.Containers)+got.More.Containers != MaxContainers || len(got.Mobs)+got.More.Mobs != MaxMobs {
		t.Errorf("kept %d beds (+%d), %d containers (+%d), %d mobs (+%d): what was cut is not all counted",
			len(got.Beds), got.More.Beds, len(got.Containers), got.More.Containers, len(got.Mobs), got.More.Mobs)
	}
}

func TestExtractor_FillsTheStoreAndLeavesTheWorldAlone(t *testing.T) {
	db := world(t,
		blockEntities(t, 0, 0, 0, bedAt(1, 64, 1, 0), chestAt(2, 64, 2, nil)),
		actor(t, 9, map[string]any{"identifier": "minecraft:cat", "CustomName": "Tango", "Pos": []any{float32(1), float32(2), float32(3)}}),
		digp(0, 0, 0, 9),
	)
	e := &Extractor{WorkDir: t.TempDir(), Store: NewStore(), Timeout: time.Minute}
	at := time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC)
	stats, err := e.Extract(context.Background(), db, at)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Beds != 1 || stats.Containers != 1 || stats.Mobs != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if got, _ := read(t, e.Store, "overworld"); len(got.Beds) != 1 || len(got.Containers) != 1 || len(got.Mobs) != 1 || !got.At.Equal(at) {
		t.Errorf("overworld = %+v", got)
	}
}

// A scan that fails or runs out of time must not empty the map of the
// markers the last good one found.
func TestExtractor_KeepsTheLastMarkersWhenAScanFails(t *testing.T) {
	db := world(t, blockEntities(t, 0, 0, 0, bedAt(1, 64, 1, 0)))
	e := &Extractor{WorkDir: t.TempDir(), Store: NewStore()}
	if _, err := e.Extract(context.Background(), db, time.Now()); err != nil {
		t.Fatal(err)
	}
	e.Timeout = time.Nanosecond
	if _, err := e.Extract(context.Background(), db, time.Now()); err == nil {
		t.Error("a scan with no time to run reported success")
	}
	if _, err := e.Extract(context.Background(), t.TempDir(), time.Now()); err == nil {
		t.Error("a scan of a directory with no world reported success")
	}
	if got, _ := read(t, e.Store, "overworld"); len(got.Beds) != 1 {
		t.Errorf("beds = %v, want the one from the scan that worked", got.Beds)
	}
}

func TestStore_ListsEachNamedMobsTypeOnce(t *testing.T) {
	s := NewStore()
	if kinds := s.MobKinds(); len(kinds) != 0 {
		t.Errorf("mob kinds before any scan = %v", kinds)
	}
	s.Set(time.Now(), World{
		chunks.Overworld: {Mobs: []Marker{{Kind: "sheep", Name: "jeb_"}, {Kind: "sheep", Name: "Dinnerbone"}, {Kind: "cat", Name: "Tango"}}},
		chunks.End:       {Mobs: []Marker{{Kind: "happy_ghast", Name: "Casper"}}},
	})
	if got := strings.Join(s.MobKinds(), ","); got != "cat,happy_ghast,sheep" {
		t.Errorf("mob kinds = %s", got)
	}
}

func TestWithSurfaceKeepsEverythingAMarkerHasAndAddsTheSurfaceWhereKnown(t *testing.T) {
	s := NewStore()
	at := time.Date(2026, 10, 5, 4, 1, 1, 0, time.UTC)
	s.Set(at, World{chunks.Overworld: {
		Beds:       []Marker{{X: 3, Y: 64, Z: 5, Colour: "red"}},
		Containers: []Marker{{X: -9, Y: -20, Z: 9, Kind: "chest", Trapped: true, Name: "Ore"}},
		More:       More{Beds: 2},
	}})
	body, _, _ := s.Dimension("overworld")
	out, err := WithSurface(body, func(x, z int32) (int32, bool) { return 70, x > 0 })
	if err != nil {
		t.Fatal(err)
	}
	var d decoded
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	if d.At == nil || !d.At.Equal(at) || d.More.Beds != 2 || d.Mobs == nil {
		t.Errorf("the rest of the answer changed: %s", out)
	}
	bed, chest := d.Beds[0], d.Containers[0]
	if bed.Surface == nil || *bed.Surface != 70 || bed.Y != 64 || bed.Colour != "red" {
		t.Errorf("bed = %+v", bed)
	}
	if chest.Surface != nil || chest.Y != -20 || !chest.Trapped || chest.Name != "Ore" {
		t.Errorf("a marker where nothing is known = %+v", chest)
	}

	// Empty lists stay lists.
	out, err = WithSurface([]byte(`{"beds":[]}`), func(int32, int32) (int32, bool) { return 0, false })
	if err != nil || !strings.Contains(string(out), `"containers":[]`) || !strings.Contains(string(out), `"mobs":[]`) {
		t.Errorf("%s, %v", out, err)
	}
	if _, err := WithSurface([]byte(`nope`), nil); err == nil {
		t.Error("a body that is not markers was written out")
	}
}
