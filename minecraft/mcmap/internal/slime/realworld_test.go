package slime

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

// Run against a copy of a real world to set the function beside the
// slimes the world itself holds:
//
//	MCMAP_REAL_WORLD=/path/to/FWB go test -run RealWorld -v ./minecraft/mcmap/internal/slime/
//
// A slime below y 40 spawned in a slime chunk, unless a trial chamber's
// spawner made it; chambers lie between y -41 and -19, so slimes there say
// nothing. Slimes move, so a few will have left the chunk they spawned in,
// but one chunk in ten is a slime chunk and far more than that must hold.
func TestRealWorld(t *testing.T) {
	world := os.Getenv("MCMAP_REAL_WORLD")
	if world == "" {
		t.Skip("MCMAP_REAL_WORLD names no world copy")
	}
	db, done, err := chunks.OpenView(filepath.Join(world, "db"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	// An actor's position: a list named Pos of three floats.
	position := []byte("\x09\x03\x00Pos\x05\x03\x00\x00\x00")
	prefix := []byte("actorprefix")
	it := db.NewIterator(nil, nil)
	defer it.Release()
	found, inside := map[[2]int32]bool{}, 0
	for ok := it.Seek(prefix); ok && bytes.HasPrefix(it.Key(), prefix); ok = it.Next() {
		v := it.Value()
		at := bytes.Index(v, position)
		if at < 0 || len(v) < at+len(position)+12 || !bytes.Contains(v, []byte("minecraft:slime")) {
			continue
		}
		p := v[at+len(position):]
		coordinate := func(i int) float64 {
			return float64(math.Float32frombits(binary.LittleEndian.Uint32(p[i*4:])))
		}
		x, y, z := coordinate(0), coordinate(1), coordinate(2)
		if y >= 40 || (y >= -41 && y <= -19) {
			continue
		}
		c := [2]int32{int32(math.Floor(x)) >> 4, int32(math.Floor(z)) >> 4}
		if !found[c] {
			found[c] = true
			if Chunk(c[0], c[1]) {
				inside++
			}
			t.Logf("slime at y %.0f in chunk %d, %d: slime chunk %v", y, c[0], c[1], Chunk(c[0], c[1]))
		}
	}
	if err := it.Error(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d of the %d chunks holding such a slime are slime chunks", inside, len(found))
	if len(found) >= 5 && inside*2 < len(found) {
		t.Errorf("only %d of %d: the function does not describe this world", inside, len(found))
	}
}
