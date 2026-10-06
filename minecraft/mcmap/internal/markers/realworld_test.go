package markers

import (
	"context"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to check what the scan reads after a
// game update, and to measure it:
//
//	MCMAP_REAL_WORLD=/path/to/FWB/db go test -run RealWorld -v ./minecraft/mcmap/internal/markers/
//
// Named mobs are printed with their names; nothing else a player wrote is.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	db, done, err := chunks.OpenView(world, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	started := time.Now()
	w, stats, err := Scan(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("took %s: %+v", time.Since(started).Round(time.Millisecond), stats)
	for _, d := range chunks.Dimensions {
		l := w[d]
		beds, containers := map[string]int{}, map[string]int{}
		for _, m := range l.Beds {
			beds[m.Colour]++
		}
		for _, m := range l.Containers {
			kind := m.Kind
			if m.Trapped {
				kind = "trapped " + kind
			}
			if m.Colour != "" {
				kind = m.Colour + " " + kind
			}
			containers[kind]++
		}
		t.Logf("%s: %d beds, %d containers, %d named mobs", d.Name(), len(l.Beds), len(l.Containers), len(l.Mobs))
		for _, colour := range slices.Sorted(maps.Keys(beds)) {
			t.Logf("  bed %-12q %d", colour, beds[colour])
		}
		for _, kind := range slices.Sorted(maps.Keys(containers)) {
			t.Logf("  container %-28s %d", kind, containers[kind])
		}
		for _, m := range l.Mobs {
			t.Logf("  mob %-16s %-20q baby=%v", m.Kind, m.Name, m.Baby)
		}
	}
}
