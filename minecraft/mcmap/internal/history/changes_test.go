package history

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

var (
	east  = render.Tile{Zoom: 0, X: 38, Y: -4}
	south = render.Tile{Zoom: 0, X: 37, Y: -3}
	north = render.Tile{Zoom: 0, X: 37, Y: -5}
)

func changesOn(t *testing.T, s *Store, day, dimension string) Changes {
	t.Helper()
	c, ok := s.Changes(day, dimension)
	if !ok {
		t.Fatalf("no changes for %s of %s", dimension, day)
	}
	return c
}

func TestTheFirstDayHeldHasNoDayToDifferFrom(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)

	c := changesOn(t, s, "2026-10-12", "overworld")
	if c.Day != "2026-10-12" || c.Before != "" || c.Zoom != 0 || c.Tiles == nil || len(c.Tiles) != 0 || c.More != 0 {
		t.Fatalf("changes = %+v, want the day alone with an empty list", c)
	}
	if c.Settled {
		t.Error("the first day's answer is called settled, and any earlier day put in changes it")
	}
}

func TestChangesNameTheTilesDrawnDifferentlyAddedAndGone(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	w.draw("overworld", east, "a field")
	w.draw("overworld", south, "a pond")
	w.draw("overworld", far, "hills")
	w.draw("nether", home, "lava")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1)

	w.draw("overworld", home, "a castle")
	w.erase("overworld", south)
	w.draw("overworld", north, "a road")
	w.draw("overworld", far, "hills and a castle")
	capture(t, s, day1.Add(24*time.Hour))
	// Worked out from what each day's manifest says, with no tile to read.
	if err := os.RemoveAll(filepath.Join(dir, blobsDir)); err != nil {
		t.Fatal(err)
	}

	c := changesOn(t, s, "2026-10-13", "overworld")
	if c.Before != "2026-10-12" || c.Zoom != 0 || c.More != 0 {
		t.Fatalf("changes = %+v", c)
	}
	// By row and then along it; the coarser tile that changed with them is
	// not one of them.
	if want := [][2]int{{37, -5}, {37, -4}, {37, -3}}; !reflect.DeepEqual(c.Tiles, want) {
		t.Errorf("tiles = %v, want %v", c.Tiles, want)
	}
	if c.Of != 4 {
		t.Errorf("of = %d, want the four addresses the two days hold between them", c.Of)
	}
	if n := changesOn(t, s, "2026-10-13", "nether"); len(n.Tiles) != 0 || n.Of != 1 {
		t.Errorf("a dimension nothing changed in = %+v", n)
	}
}

func TestChangesAreOfDaysAndDimensionsHeld(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)
	for _, at := range [][2]string{{"2026-10-11", "overworld"}, {"", "overworld"}, {"../days/2026-10-12", "overworld"}, {"2026-10-12.json.gz", "overworld"}, {"2026-10-12", "moon"}, {"2026-10-12", "../overworld"}} {
		if c, ok := s.Changes(at[0], at[1]); ok {
			t.Errorf("changes of %q on %q = %+v", at[1], at[0], c)
		}
	}
}

// A backfill puts days in before days already held, so the day before is
// whichever is held when it is asked, and an answer stands for good only
// where no day could come between the two.
func TestADayPutInBetweenIsWhatALaterDayIsThenComparedWith(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	s := open(t, t.TempDir(), w)
	capture(t, s, day1)
	w.draw("overworld", home, "a castle")
	capture(t, s, day1.Add(72*time.Hour))

	c := changesOn(t, s, "2026-10-15", "overworld")
	if c.Before != "2026-10-12" || len(c.Tiles) != 1 || c.Settled {
		t.Fatalf("across a gap = %+v, want one tile against the 12th and not settled", c)
	}

	// The world as an archive of the 14th held it: the castle already built.
	capture(t, s, day1.Add(48*time.Hour))
	c = changesOn(t, s, "2026-10-15", "overworld")
	if c.Before != "2026-10-14" || len(c.Tiles) != 0 || !c.Settled {
		t.Fatalf("after the 14th was put in = %+v, want nothing changed against it, settled", c)
	}
	if c := changesOn(t, s, "2026-10-14", "overworld"); c.Before != "2026-10-12" || len(c.Tiles) != 1 || c.Settled {
		t.Errorf("the day put in = %+v, want one tile against the 12th and not settled", c)
	}
}

func TestChangesStopAtALimitAndSayHowManyWereLeftOut(t *testing.T) {
	// Two days written as their manifests alone: what changed is never read
	// from a tile, and a capture of this many would flush each one.
	dir := t.TempDir()
	open(t, dir, newWorld(t))
	blob := func(of string) string {
		sum := sha256.Sum256([]byte(of))
		return hex.EncodeToString(sum[:])
	}
	first := dimension{Tiles: map[string]string{"0/37/-4": blob("a hut")}}
	second := dimension{Tiles: map[string]string{"0/37/-4": blob("a hut")}}
	for x := range maxChanges + 3 {
		second.Tiles[fmt.Sprintf("0/%d/9", x)] = blob(fmt.Sprintf("field %d", x))
	}
	for day, dim := range map[string]dimension{"2026-10-12": first, "2026-10-13": second} {
		if err := writeManifest(filepath.Join(dir, daysDir), manifest{Version: manifestVersion, Day: day, TakenAt: day1, Dimensions: map[string]dimension{"overworld": dim}}); err != nil {
			t.Fatal(err)
		}
	}
	s := open(t, dir, newWorld(t))

	c := changesOn(t, s, "2026-10-13", "overworld")
	if len(c.Tiles) != maxChanges || c.More != 3 || c.Of != maxChanges+4 {
		t.Fatalf("%d tiles, %d more, of %d; want %d, 3, %d", len(c.Tiles), c.More, c.Of, maxChanges, maxChanges+4)
	}
	if c.Tiles[0] != [2]int{0, 9} || c.Tiles[maxChanges-1] != [2]int{maxChanges - 1, 9} {
		t.Errorf("the tiles kept run from %v to %v, want the first of the row", c.Tiles[0], c.Tiles[maxChanges-1])
	}
}

func TestAnEarlierDayWithoutTheDimensionIsOneEveryTileDiffersFrom(t *testing.T) {
	w := newWorld(t)
	w.draw("overworld", home, "a hut")
	dir := t.TempDir()
	s := open(t, dir, w)
	capture(t, s, day1.Add(24*time.Hour))
	if err := writeManifest(filepath.Join(dir, daysDir), manifest{Version: manifestVersion, Day: "2026-10-12", TakenAt: day1, Dimensions: map[string]dimension{}}); err != nil {
		t.Fatal(err)
	}
	s = open(t, dir, w)

	if c := changesOn(t, s, "2026-10-13", "overworld"); c.Before != "2026-10-12" || len(c.Tiles) != 1 || c.Of != 1 {
		t.Errorf("changes = %+v, want the day's one tile", c)
	}
	if c, ok := s.Changes("2026-10-12", "overworld"); ok {
		t.Errorf("a day that held nothing of the dimension answered %+v", c)
	}
}
