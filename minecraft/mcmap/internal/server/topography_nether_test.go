package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/layers"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

type floorSource struct{ held *layers.Floors }

func (f *floorSource) Floors() *layers.Floors { return f.held }

// netherFloors is a nether whose floor rises to the east, a block in four,
// at the height each level is given: a different land for each level.
func netherFloors(t *testing.T, base map[int]int16) *layers.Floors {
	t.Helper()
	maps := map[int]*heights.Map{}
	at := time.Unix(1_700_000_000, 0)
	for cut, y0 := range base {
		b := heights.NewBuilder(at, -1)
		for cz := range int32(2) {
			for cx := range int32(2) {
				var cols [heights.Columns]int16
				for i := range cols {
					cols[i] = y0 + int16((i%16+int(cx)*16)/4) + 1
				}
				if err := b.AddColumns(chunks.Nether, cx, cz, &cols); err != nil {
					t.Fatal(err)
				}
			}
		}
		maps[cut] = b.Map()
	}
	return layers.NewFloors(at, -1, maps)
}

func withNetherFloors(t *testing.T) (*Server, *floorSource) {
	t.Helper()
	s := withLand(t)
	s.NetherTop = 100
	s.NetherLevels = []render.NetherLevel{{TopY: 66, Label: "Mid"}, {TopY: 34, Label: "Low"}}
	f := &floorSource{held: netherFloors(t, map[int]int16{100: 70, 66: 40, 34: 32})}
	s.Floors = f
	return s, f
}

func TestTopographyOffersTheNetherWhereItsFloorsAreRead(t *testing.T) {
	s, f := withNetherFloors(t)
	got := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension=nether"))
	if !got.Offered || !got.Ready || got.Version == "" || len(got.Bands) < 4 {
		t.Errorf("nether with floors: %+v", got)
	}
	for _, level := range []string{"66", "34", "default"} {
		if l := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension=nether&level="+level)); !l.Ready {
			t.Errorf("level %s: %+v", level, l)
		}
	}
	// Before the first reading the page is told it is coming.
	f.held = nil
	if before := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension=nether")); !before.Offered || before.Ready || before.Version != "" {
		t.Errorf("before the first reading: %+v", before)
	}
	// And the other dimensions are as they were.
	if ow := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension=overworld")); !ow.Offered || !ow.Ready {
		t.Errorf("overworld: %+v", ow)
	}
	if rec := fetch(t, s, "/api/topography?dimension=nether&level=50"); rec.Code != http.StatusBadRequest {
		t.Errorf("a level that is not configured = %d", rec.Code)
	}
}

func TestTopographyOfTheNetherIsNotOfferedWithNoFloorsAtAll(t *testing.T) {
	s := withLand(t)
	if got := decodeBody[topographyListing](t, fetch(t, s, "/api/topography?dimension=nether")); got.Offered || got.Ready {
		t.Errorf("%+v", got)
	}
	if rec := fetch(t, s, "/api/topography/tiles/nether/0/0/0.png?styles=tint"); rec.Code != http.StatusNotFound {
		t.Errorf("a nether tile with no floors = %d", rec.Code)
	}
}

func TestTopographyTilesOfTheNetherFollowTheLevelAsked(t *testing.T) {
	s, f := withNetherFloors(t)
	tileOf := func(level string) (string, string) {
		path := "/api/topography/tiles/nether/0/0/0.png?styles=tint,hillshade"
		if level != "" {
			path += "&level=" + level
		}
		rec := fetch(t, s, path)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("level %q: %d %s", level, rec.Code, rec.Body)
		}
		return rec.Body.String(), rec.Header().Get("ETag")
	}
	top, topTag := tileOf("")
	mid, midTag := tileOf("66")
	low, lowTag := tileOf("34")
	if top == mid || mid == low || top == low {
		t.Error("two levels were drawn alike, from heights as different as these")
	}
	if topTag == midTag || midTag == lowTag {
		t.Errorf("etags %s %s %s do not tell the levels apart", topTag, midTag, lowTag)
	}
	// The default level is the first one, named or not.
	if def, _ := tileOf("default"); def != top {
		t.Error("the default level is not the nether's top cut")
	}
	for _, bad := range []string{"50", "abc", "-1", "100000"} {
		if rec := fetch(t, s, "/api/topography/tiles/nether/0/0/0.png?styles=tint&level="+bad); rec.Code != http.StatusBadRequest {
			t.Errorf("level %s = %d, want 400", bad, rec.Code)
		}
	}
	// A level that was configured and has not been read is not a tile.
	f.held = layers.NewFloors(time.Unix(1_700_000_000, 0), -1, map[int]*heights.Map{100: f.held.Level(100)})
	if rec := fetch(t, s, "/api/topography/tiles/nether/0/0/0.png?styles=tint&level=66"); rec.Code != http.StatusNotFound {
		t.Errorf("a level not read = %d", rec.Code)
	}
	// A level has no say over a dimension that has no levels.
	if rec := fetch(t, s, "/api/topography/tiles/overworld/0/0/0.png?styles=tint&level=66"); rec.Code != http.StatusOK || strings.Contains(rec.Header().Get("ETag"), "66") {
		t.Errorf("the overworld given a level: %d %s", rec.Code, rec.Header().Get("ETag"))
	}
}
